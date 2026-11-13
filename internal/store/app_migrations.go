package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/oralecarlangelo/touchgrass/internal/model"
)

// Errors reported by the application-migration stores.
var (
	// ErrMigrationConfigNotFound reports a service with no migration setup.
	ErrMigrationConfigNotFound = errors.New("store: migration config not found")
	// ErrMigrationRunNotFound reports an unknown run id for a service.
	ErrMigrationRunNotFound = errors.New("store: migration run not found")
)

// maxMigrationRuns caps the run history kept per service.
const maxMigrationRuns = 200

// MigrationConfigRow is a stored migration setup with its sealed
// database URL. Only the service layer opens the envelope.
type MigrationConfigRow struct {
	model.MigrationConfig

	URLCiphertext []byte
	URLNonce      []byte
	URLKeyID      string
}

// MigrationConfigStore persists the migration setup of each service.
type MigrationConfigStore struct {
	db *DB
}

// NewMigrationConfigStore builds a MigrationConfigStore on db.
func NewMigrationConfigStore(db *DB) *MigrationConfigStore {
	return &MigrationConfigStore{db: db}
}

// Get returns a service's setup or ErrMigrationConfigNotFound.
func (s *MigrationConfigStore) Get(ctx context.Context, serviceID string) (MigrationConfigRow, error) {
	var (
		row       MigrationConfigRow
		command   string
		updatedAt string
	)

	err := s.db.sql.QueryRowContext(ctx,
		`SELECT service_id, tool, url_ciphertext, url_nonce, url_key_id, database_label,
		        container, workdir, migrations_dir, command_json, updated_by, updated_at
		   FROM service_migrations WHERE service_id = ?`, serviceID,
	).Scan(
		&row.ServiceID, &row.Tool, &row.URLCiphertext, &row.URLNonce, &row.URLKeyID, &row.Database,
		&row.Container, &row.Workdir, &row.MigrationsDir, &command, &row.UpdatedBy, &updatedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return MigrationConfigRow{}, fmt.Errorf("%w: %q", ErrMigrationConfigNotFound, serviceID)
		}

		return MigrationConfigRow{}, fmt.Errorf("scanning migration config: %w", err)
	}

	row.Command = []string{}

	if err := json.Unmarshal([]byte(command), &row.Command); err != nil {
		return MigrationConfigRow{}, fmt.Errorf("decoding migration command: %w", err)
	}

	updated, err := parseTime(updatedAt, "updated_at")
	if err != nil {
		return MigrationConfigRow{}, err
	}

	row.UpdatedAt = updated

	return row, nil
}

// Put creates or replaces a service's setup.
func (s *MigrationConfigStore) Put(ctx context.Context, row MigrationConfigRow) error {
	command := row.Command
	if command == nil {
		command = []string{}
	}

	encoded, err := json.Marshal(command)
	if err != nil {
		return fmt.Errorf("encoding migration command: %w", err)
	}

	_, err = s.db.sql.ExecContext(ctx,
		`INSERT INTO service_migrations (
		   service_id, tool, url_ciphertext, url_nonce, url_key_id, database_label,
		   container, workdir, migrations_dir, command_json, updated_by, updated_at
		 ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT (service_id) DO UPDATE SET
		   tool = excluded.tool,
		   url_ciphertext = excluded.url_ciphertext,
		   url_nonce = excluded.url_nonce,
		   url_key_id = excluded.url_key_id,
		   database_label = excluded.database_label,
		   container = excluded.container,
		   workdir = excluded.workdir,
		   migrations_dir = excluded.migrations_dir,
		   command_json = excluded.command_json,
		   updated_by = excluded.updated_by,
		   updated_at = excluded.updated_at`,
		row.ServiceID, row.Tool, row.URLCiphertext, row.URLNonce, row.URLKeyID, row.Database,
		row.Container, row.Workdir, row.MigrationsDir, string(encoded), row.UpdatedBy,
		formatTime(row.UpdatedAt),
	)
	if err != nil {
		return fmt.Errorf("saving migration config: %w", err)
	}

	return nil
}

// Delete removes a service's setup and reports whether one existed.
// Run history is kept: it is a record of what was done.
func (s *MigrationConfigStore) Delete(ctx context.Context, serviceID string) (bool, error) {
	res, err := s.db.sql.ExecContext(ctx,
		`DELETE FROM service_migrations WHERE service_id = ?`, serviceID)
	if err != nil {
		return false, fmt.Errorf("deleting migration config: %w", err)
	}

	affected, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("counting deleted migration configs: %w", err)
	}

	return affected > 0, nil
}

// MigrationRunStore persists the commands touchgrass ran.
type MigrationRunStore struct {
	db *DB
}

// NewMigrationRunStore builds a MigrationRunStore on db.
func NewMigrationRunStore(db *DB) *MigrationRunStore {
	return &MigrationRunStore{db: db}
}

// MigrationRunResult is the terminal outcome of a run.
type MigrationRunResult struct {
	State      string
	ExitCode   *int
	Output     string
	Truncated  bool
	FinishedAt time.Time
}

// Create inserts a running run and trims the service's history to the
// newest maxMigrationRuns rows.
func (s *MigrationRunStore) Create(ctx context.Context, run model.MigrationRun) (int64, error) {
	if _, err := s.db.sql.ExecContext(ctx,
		`DELETE FROM migration_runs
		  WHERE service_id = ?
		    AND id NOT IN (
		      SELECT id FROM migration_runs WHERE service_id = ? ORDER BY id DESC LIMIT ?
		    )`,
		run.ServiceID, run.ServiceID, maxMigrationRuns-1,
	); err != nil {
		return 0, fmt.Errorf("trimming migration runs: %w", err)
	}

	res, err := s.db.sql.ExecContext(ctx,
		`INSERT INTO migration_runs (
		   service_id, kind, tool, detail, actor, state, container, image, started_at
		 ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		run.ServiceID, run.Kind, run.Tool, run.Detail, run.Actor, model.MigrationRunRunning,
		run.Container, run.Image, formatTime(run.StartedAt),
	)
	if err != nil {
		return 0, fmt.Errorf("creating migration run: %w", err)
	}

	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("reading migration run id: %w", err)
	}

	return id, nil
}

// Finish stamps a run terminal.
func (s *MigrationRunStore) Finish(ctx context.Context, id int64, result MigrationRunResult) error {
	var exitCode any
	if result.ExitCode != nil {
		exitCode = *result.ExitCode
	}

	res, err := s.db.sql.ExecContext(ctx,
		`UPDATE migration_runs
		    SET state = ?, exit_code = ?, output = ?, output_truncated = ?, finished_at = ?
		  WHERE id = ?`,
		result.State, exitCode, result.Output, result.Truncated, formatTime(result.FinishedAt), id,
	)
	if err != nil {
		return fmt.Errorf("finishing migration run: %w", err)
	}

	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("reading migration run update: %w", err)
	}

	if affected == 0 {
		return fmt.Errorf("%w: %d", ErrMigrationRunNotFound, id)
	}

	return nil
}

// FailRunning marks every running run failed (restart recovery) and
// reports how many rows changed.
func (s *MigrationRunStore) FailRunning(ctx context.Context, output string) (int64, error) {
	res, err := s.db.sql.ExecContext(ctx,
		`UPDATE migration_runs SET state = ?, output = ?, finished_at = ? WHERE state = ?`,
		model.MigrationRunFailed, output, formatTime(time.Now()), model.MigrationRunRunning,
	)
	if err != nil {
		return 0, fmt.Errorf("failing running migration runs: %w", err)
	}

	affected, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("reading migration run update: %w", err)
	}

	return affected, nil
}

// ListRunning returns every run still marked running, without output.
func (s *MigrationRunStore) ListRunning(ctx context.Context) ([]model.MigrationRun, error) {
	rows, err := s.db.sql.QueryContext(ctx,
		`SELECT `+migrationRunColumns+`, ''
		   FROM migration_runs WHERE state = ? ORDER BY id`, model.MigrationRunRunning)
	if err != nil {
		return nil, fmt.Errorf("listing running migration runs: %w", err)
	}

	return collectMigrationRuns(rows)
}

// Running reports whether the service has a run in progress.
func (s *MigrationRunStore) Running(ctx context.Context, serviceID string) (bool, error) {
	var count int

	if err := s.db.sql.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM migration_runs WHERE service_id = ? AND state = ?`,
		serviceID, model.MigrationRunRunning,
	).Scan(&count); err != nil {
		return false, fmt.Errorf("counting running migration runs: %w", err)
	}

	return count > 0, nil
}

// migrationRunColumns is the list every run read selects. Output is
// replaced by ” in list reads.
const migrationRunColumns = `id, service_id, kind, tool, detail, actor, state, exit_code,
	container, image, started_at, finished_at, output_truncated`

// Get returns one run of a service, with its output. An id that
// belongs to another service is not found.
func (s *MigrationRunStore) Get(ctx context.Context, serviceID string, id int64) (model.MigrationRun, error) {
	row := s.db.sql.QueryRowContext(ctx,
		`SELECT `+migrationRunColumns+`, output
		   FROM migration_runs WHERE service_id = ? AND id = ?`, serviceID, id)

	run, err := scanMigrationRun(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return model.MigrationRun{}, fmt.Errorf("%w: %d", ErrMigrationRunNotFound, id)
		}

		return model.MigrationRun{}, err
	}

	return run, nil
}

// List returns a service's newest runs, without output.
func (s *MigrationRunStore) List(ctx context.Context, serviceID string, limit int) ([]model.MigrationRun, error) {
	rows, err := s.db.sql.QueryContext(ctx,
		`SELECT `+migrationRunColumns+`, ''
		   FROM migration_runs WHERE service_id = ? ORDER BY id DESC LIMIT ?`, serviceID, limit)
	if err != nil {
		return nil, fmt.Errorf("listing migration runs: %w", err)
	}

	return collectMigrationRuns(rows)
}

// collectMigrationRuns drains and closes a run query.
func collectMigrationRuns(rows *sql.Rows) ([]model.MigrationRun, error) {
	defer func() {
		_ = rows.Close()
	}()

	runs := []model.MigrationRun{}

	for rows.Next() {
		run, err := scanMigrationRun(rows)
		if err != nil {
			return nil, err
		}

		runs = append(runs, run)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating migration runs: %w", err)
	}

	return runs, nil
}

// scanMigrationRun maps one run row.
func scanMigrationRun(row scanner) (model.MigrationRun, error) {
	var (
		run        model.MigrationRun
		exitCode   sql.NullInt64
		startedAt  string
		finishedAt sql.NullString
	)

	if err := row.Scan(
		&run.ID, &run.ServiceID, &run.Kind, &run.Tool, &run.Detail, &run.Actor, &run.State, &exitCode,
		&run.Container, &run.Image, &startedAt, &finishedAt, &run.Truncated, &run.Output,
	); err != nil {
		return model.MigrationRun{}, fmt.Errorf("scanning migration run: %w", err)
	}

	if exitCode.Valid {
		code := int(exitCode.Int64)
		run.ExitCode = &code
	}

	started, err := parseTime(startedAt, "started_at")
	if err != nil {
		return model.MigrationRun{}, err
	}

	run.StartedAt = started

	finished, err := parseNullTime(finishedAt, "finished_at")
	if err != nil {
		return model.MigrationRun{}, err
	}

	run.FinishedAt = finished

	return run, nil
}
