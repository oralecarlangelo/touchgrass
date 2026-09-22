package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/oralecarlangelo/touchgrass/internal/model"
)

// ErrDBJobNotFound reports an unknown database job id.
var ErrDBJobNotFound = errors.New("store: database job not found")

// maxDBJobs caps the db_jobs history; older rows trim on insert.
const maxDBJobs = 50

// DBJobStore persists backup/restore job history.
type DBJobStore struct {
	db *DB
}

// NewDBJobStore builds a DBJobStore on db.
func NewDBJobStore(db *DB) *DBJobStore {
	return &DBJobStore{db: db}
}

// Create inserts one job and trims history to the newest maxDBJobs rows.
// The trim runs first so a trim failure never orphans a created row.
func (s *DBJobStore) Create(ctx context.Context, job model.DBJob) (int64, error) {
	if job.StartedAt.IsZero() {
		job.StartedAt = time.Now()
	}

	if err := s.trim(ctx, maxDBJobs-1); err != nil {
		return 0, err
	}

	res, err := s.db.sql.ExecContext(ctx,
		`INSERT INTO db_jobs (kind, target, status, detail, started_at, finished_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		job.Kind, job.Target, job.Status, job.Detail,
		formatTime(job.StartedAt), formatNullTime(job.FinishedAt),
	)
	if err != nil {
		return 0, fmt.Errorf("creating database job: %w", err)
	}

	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("reading database job id: %w", err)
	}

	return id, nil
}

// Get returns one job by id.
func (s *DBJobStore) Get(ctx context.Context, id int64) (model.DBJob, error) {
	var (
		job        model.DBJob
		startedAt  string
		finishedAt sql.NullString
	)

	if err := s.db.sql.QueryRowContext(ctx,
		`SELECT id, kind, target, status, detail, started_at, finished_at
		 FROM db_jobs WHERE id = ?`, id,
	).Scan(
		&job.ID, &job.Kind, &job.Target, &job.Status,
		&job.Detail, &startedAt, &finishedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return model.DBJob{}, fmt.Errorf("%w: %d", ErrDBJobNotFound, id)
		}

		return model.DBJob{}, fmt.Errorf("scanning database job: %w", err)
	}

	started, err := parseTime(startedAt, "started_at")
	if err != nil {
		return model.DBJob{}, err
	}

	job.StartedAt = started

	finished, err := parseNullTime(finishedAt, "finished_at")
	if err != nil {
		return model.DBJob{}, err
	}

	job.FinishedAt = finished

	return job, nil
}

// Finish stamps a job terminal with status, detail, and finish time.
func (s *DBJobStore) Finish(
	ctx context.Context,
	id int64,
	status, detail string,
	finishedAt time.Time,
) error {
	res, err := s.db.sql.ExecContext(ctx,
		`UPDATE db_jobs SET status = ?, detail = ?, finished_at = ? WHERE id = ?`,
		status, detail, formatTime(finishedAt), id,
	)
	if err != nil {
		return fmt.Errorf("finishing database job: %w", err)
	}

	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("reading database job update: %w", err)
	}

	if affected == 0 {
		return fmt.Errorf("%w: %d", ErrDBJobNotFound, id)
	}

	return nil
}

// FailRunning marks every running job failed (restart recovery) and
// reports how many rows changed.
func (s *DBJobStore) FailRunning(ctx context.Context, detail string) (int64, error) {
	res, err := s.db.sql.ExecContext(ctx,
		`UPDATE db_jobs SET status = ?, detail = ?, finished_at = ? WHERE status = ?`,
		model.DBJobFailed, detail, formatTime(time.Now()), model.DBJobRunning,
	)
	if err != nil {
		return 0, fmt.Errorf("failing running database jobs: %w", err)
	}

	affected, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("reading database job update: %w", err)
	}

	return affected, nil
}

// List returns jobs newest first, bounded by limit.
func (s *DBJobStore) List(ctx context.Context, limit int) ([]model.DBJob, error) {
	rows, err := s.db.sql.QueryContext(ctx,
		`SELECT id, kind, target, status, detail, started_at, finished_at
		 FROM db_jobs ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("querying database jobs: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()

	jobs := []model.DBJob{}

	for rows.Next() {
		var (
			job        model.DBJob
			startedAt  string
			finishedAt sql.NullString
		)

		if err := rows.Scan(
			&job.ID, &job.Kind, &job.Target, &job.Status,
			&job.Detail, &startedAt, &finishedAt,
		); err != nil {
			return nil, fmt.Errorf("scanning database job: %w", err)
		}

		started, err := parseTime(startedAt, "started_at")
		if err != nil {
			return nil, err
		}

		job.StartedAt = started

		finished, err := parseNullTime(finishedAt, "finished_at")
		if err != nil {
			return nil, err
		}

		job.FinishedAt = finished
		jobs = append(jobs, job)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating database jobs: %w", err)
	}

	return jobs, nil
}

// trim deletes jobs older than the newest keep rows.
func (s *DBJobStore) trim(ctx context.Context, keep int) error {
	if _, err := s.db.sql.ExecContext(ctx,
		`DELETE FROM db_jobs WHERE id NOT IN
		 (SELECT id FROM db_jobs ORDER BY id DESC LIMIT ?)`, keep,
	); err != nil {
		return fmt.Errorf("trimming database jobs: %w", err)
	}

	return nil
}

// SQLiteHealth reports the embedded database version and the first
// PRAGMA integrity_check verdict ("ok" when healthy).
func (db *DB) SQLiteHealth(ctx context.Context) (model.SQLiteHealth, error) {
	var health model.SQLiteHealth

	if err := db.sql.QueryRowContext(ctx, "SELECT sqlite_version()").Scan(&health.Version); err != nil {
		return model.SQLiteHealth{}, fmt.Errorf("querying sqlite version: %w", err)
	}

	if err := db.sql.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&health.Integrity); err != nil {
		return model.SQLiteHealth{}, fmt.Errorf("checking sqlite integrity: %w", err)
	}

	return health, nil
}
