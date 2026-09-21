package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/oralecarlangelo/touchgrass/internal/model"
)

// DeployStore persists deploy history entries.
type DeployStore struct {
	db *DB
}

// NewDeployStore builds a DeployStore on db.
func NewDeployStore(db *DB) *DeployStore {
	return &DeployStore{db: db}
}

// Record inserts one history entry and returns its id.
func (s *DeployStore) Record(ctx context.Context, deploy model.Deploy) (int64, error) {
	if deploy.CreatedAt.IsZero() {
		deploy.CreatedAt = time.Now()
	}

	res, err := s.db.sql.ExecContext(ctx,
		`INSERT INTO deploys (service_id, sha, actor, type, outcome, started_at,
		  finished_at, duration_secs, downtime_secs, notes, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		deploy.ServiceID, deploy.SHA, deploy.Actor, deploy.Type, deploy.Outcome,
		formatNullTime(deploy.StartedAt), formatNullTime(deploy.FinishedAt),
		deploy.DurationSecs, deploy.DowntimeSecs, deploy.Notes, formatTime(deploy.CreatedAt),
	)
	if err != nil {
		return 0, fmt.Errorf("recording deploy: %w", err)
	}

	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("reading deploy id: %w", err)
	}

	return id, nil
}

// ListByService returns history newest first, bounded by limit.
func (s *DeployStore) ListByService(
	ctx context.Context,
	serviceID string,
	limit int,
) ([]model.Deploy, error) {
	rows, err := s.db.sql.QueryContext(ctx,
		`SELECT id, service_id, sha, actor, type, outcome, started_at,
		  finished_at, duration_secs, downtime_secs, notes, created_at
		 FROM deploys WHERE service_id = ? ORDER BY id DESC LIMIT ?`, serviceID, limit)
	if err != nil {
		return nil, fmt.Errorf("querying deploys: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()

	deploys := []model.Deploy{}

	for rows.Next() {
		var (
			deploy     model.Deploy
			startedAt  sql.NullString
			finishedAt sql.NullString
			createdAt  string
		)

		if err := rows.Scan(
			&deploy.ID, &deploy.ServiceID, &deploy.SHA, &deploy.Actor,
			&deploy.Type, &deploy.Outcome, &startedAt, &finishedAt,
			&deploy.DurationSecs, &deploy.DowntimeSecs, &deploy.Notes, &createdAt,
		); err != nil {
			return nil, fmt.Errorf("scanning deploy: %w", err)
		}

		started, err := parseNullTime(startedAt, "started_at")
		if err != nil {
			return nil, err
		}

		deploy.StartedAt = started

		finished, err := parseNullTime(finishedAt, "finished_at")
		if err != nil {
			return nil, err
		}

		deploy.FinishedAt = finished

		created, err := parseTime(createdAt, "created_at")
		if err != nil {
			return nil, err
		}

		deploy.CreatedAt = created

		deploys = append(deploys, deploy)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating deploys: %w", err)
	}

	return deploys, nil
}

// InsertProbe records one public-URL health sample and returns its id.
func (s *DeployStore) InsertProbe(ctx context.Context, probe model.DeployProbe) (int64, error) {
	if probe.SampledAt.IsZero() {
		probe.SampledAt = time.Now()
	}

	ok := 0
	if probe.OK {
		ok = 1
	}

	res, err := s.db.sql.ExecContext(ctx,
		`INSERT INTO deploy_probes (service_id, ok, status_code, latency_ms, sampled_at)
		 VALUES (?, ?, ?, ?, ?)`,
		probe.ServiceID, ok, probe.StatusCode, probe.LatencyMs, formatTime(probe.SampledAt),
	)
	if err != nil {
		return 0, fmt.Errorf("inserting deploy probe: %w", err)
	}

	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("reading deploy probe id: %w", err)
	}

	return id, nil
}

// FailedProbeCount counts failed samples for a service in [since, until).
func (s *DeployStore) FailedProbeCount(
	ctx context.Context,
	serviceID string,
	since, until time.Time,
) (int, error) {
	var count int

	err := s.db.sql.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM deploy_probes
		 WHERE service_id = ? AND ok = 0 AND sampled_at >= ? AND sampled_at < ?`,
		serviceID, formatTime(since), formatTime(until),
	).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("counting failed deploy probes: %w", err)
	}

	return count, nil
}

// TrimProbes deletes samples taken before cutoff and reports the count.
func (s *DeployStore) TrimProbes(ctx context.Context, cutoff time.Time) (int64, error) {
	res, err := s.db.sql.ExecContext(ctx, "DELETE FROM deploy_probes WHERE sampled_at < ?", formatTime(cutoff))
	if err != nil {
		return 0, fmt.Errorf("trimming deploy probes: %w", err)
	}

	trimmed, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("counting trimmed deploy probes: %w", err)
	}

	return trimmed, nil
}

// TrimBefore deletes entries created before cutoff and reports the count.
func (s *DeployStore) TrimBefore(ctx context.Context, cutoff time.Time) (int64, error) {
	res, err := s.db.sql.ExecContext(ctx, "DELETE FROM deploys WHERE created_at < ?", formatTime(cutoff))
	if err != nil {
		return 0, fmt.Errorf("trimming deploys: %w", err)
	}

	trimmed, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("counting trimmed deploys: %w", err)
	}

	return trimmed, nil
}
