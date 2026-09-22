package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/oralecarlangelo/touchgrass/internal/model"
)

// FleetStore persists fleet samples: host rows plus one row per
// container per interval.
type FleetStore struct {
	db *DB
}

// NewFleetStore builds a FleetStore on db.
func NewFleetStore(db *DB) *FleetStore {
	return &FleetStore{db: db}
}

// InsertHost records one host sample.
func (s *FleetStore) InsertHost(ctx context.Context, sample model.HostSample) error {
	if sample.SampledAt.IsZero() {
		sample.SampledAt = time.Now()
	}

	_, err := s.db.sql.ExecContext(ctx,
		`INSERT INTO host_samples (sampled_at, cpu_percent, mem_used,
		  mem_total, disk_used, disk_total, load1)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		formatTime(sample.SampledAt), nullFloat(sample.CPUPercent),
		nullInt(sample.MemUsed), nullInt(sample.MemTotal),
		nullInt(sample.DiskUsed), nullInt(sample.DiskTotal),
		nullFloat(sample.Load1),
	)
	if err != nil {
		return fmt.Errorf("inserting host sample: %w", err)
	}

	return nil
}

// InsertContainer records one container sample.
func (s *FleetStore) InsertContainer(ctx context.Context, sample model.ContainerSample) error {
	if sample.SampledAt.IsZero() {
		sample.SampledAt = time.Now()
	}

	managed := 0

	if sample.Managed {
		managed = 1
	}

	_, err := s.db.sql.ExecContext(ctx,
		`INSERT INTO container_samples (sampled_at, container_name, project,
		  managed, service_id, state, cpu_percent, mem_bytes, mem_limit,
		  restarts)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		formatTime(sample.SampledAt), sample.ContainerName, sample.Project,
		managed, sample.ServiceID, sample.State, sample.CPUPercent,
		sample.MemBytes, sample.MemLimit, sample.Restarts,
	)
	if err != nil {
		return fmt.Errorf("inserting container sample: %w", err)
	}

	return nil
}

// LatestContainers returns the newest sample per container, hottest CPU
// first with a name tiebreak so the fleet order is deterministic.
func (s *FleetStore) LatestContainers(ctx context.Context) ([]model.FleetContainer, error) {
	rows, err := s.db.sql.QueryContext(ctx,
		`SELECT container_name, project, managed, service_id, state,
		  cpu_percent, mem_bytes, mem_limit, restarts, sampled_at
		 FROM container_samples WHERE id IN (
		   SELECT MAX(id) FROM container_samples GROUP BY container_name
		 ) ORDER BY cpu_percent DESC, container_name ASC`)
	if err != nil {
		return nil, fmt.Errorf("querying latest containers: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()

	containers := []model.FleetContainer{}

	for rows.Next() {
		var (
			container model.FleetContainer
			managed   int64
			sampledAt string
		)

		if err := rows.Scan(
			&container.Name, &container.Project, &managed,
			&container.ServiceID, &container.State, &container.CPUPercent,
			&container.MemBytes, &container.MemLimit, &container.Restarts,
			&sampledAt,
		); err != nil {
			return nil, fmt.Errorf("scanning fleet container: %w", err)
		}

		parsedAt, err := parseTime(sampledAt, "sampled_at")
		if err != nil {
			return nil, err
		}

		container.Managed = managed != 0
		container.SampledAt = parsedAt
		containers = append(containers, container)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating fleet containers: %w", err)
	}

	return containers, nil
}

// hostHistoryColumns allowlists the history metric to its column. The
// query interpolates the column, so unknown metrics must error here.
var hostHistoryColumns = map[string]string{
	model.MetricCPU:  "cpu_percent",
	model.MetricMem:  "mem_used",
	model.MetricLoad: "load1",
}

// History bucket-averages one host metric in SQL over the window,
// oldest bucket first. Buckets whose rows are all null are skipped.
func (s *FleetStore) History(
	ctx context.Context,
	metric string,
	since time.Time,
	bucketSecs int64,
) ([]model.HistoryPoint, error) {
	column, ok := hostHistoryColumns[metric]
	if !ok {
		return nil, fmt.Errorf("unknown history metric %q", metric)
	}

	if bucketSecs < 1 {
		bucketSecs = 1
	}

	//nolint:gosec // column comes from the fixed hostHistoryColumns allowlist, never user input.
	rows, err := s.db.sql.QueryContext(ctx,
		`SELECT CAST(strftime('%s', sampled_at) / ? AS INTEGER) * ?,
		  AVG(`+column+`)
		 FROM host_samples WHERE sampled_at >= ?
		 GROUP BY 1 ORDER BY 1 ASC`,
		bucketSecs, bucketSecs, formatTime(since))
	if err != nil {
		return nil, fmt.Errorf("querying host history: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()

	points := []model.HistoryPoint{}

	for rows.Next() {
		var (
			bucket  int64
			average sql.NullFloat64
		)

		if err := rows.Scan(&bucket, &average); err != nil {
			return nil, fmt.Errorf("scanning history point: %w", err)
		}

		if !average.Valid {
			continue
		}

		points = append(points, model.HistoryPoint{
			TS:    time.Unix(bucket, 0).UTC(),
			Value: average.Float64,
		})
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating history: %w", err)
	}

	return points, nil
}

// TrimHostsBefore deletes host samples older than cutoff.
func (s *FleetStore) TrimHostsBefore(ctx context.Context, cutoff time.Time) (int64, error) {
	res, err := s.db.sql.ExecContext(ctx, "DELETE FROM host_samples WHERE sampled_at < ?", formatTime(cutoff))
	if err != nil {
		return 0, fmt.Errorf("trimming host samples: %w", err)
	}

	trimmed, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("counting trimmed host samples: %w", err)
	}

	return trimmed, nil
}

// TrimContainersBefore deletes container samples older than cutoff.
func (s *FleetStore) TrimContainersBefore(ctx context.Context, cutoff time.Time) (int64, error) {
	res, err := s.db.sql.ExecContext(
		ctx,
		"DELETE FROM container_samples WHERE sampled_at < ?",
		formatTime(cutoff),
	)
	if err != nil {
		return 0, fmt.Errorf("trimming container samples: %w", err)
	}

	trimmed, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("counting trimmed container samples: %w", err)
	}

	return trimmed, nil
}

// nullFloat renders an optional float for storage.
func nullFloat(value *float64) any {
	if value == nil {
		return nil
	}

	return *value
}

// nullInt renders an optional int for storage.
func nullInt(value *int64) any {
	if value == nil {
		return nil
	}

	return *value
}
