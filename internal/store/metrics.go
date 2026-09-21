package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/oralecarlangelo/touchgrass/internal/model"
)

// MetricStore persists container metric samples.
type MetricStore struct {
	db *DB
}

// NewMetricStore builds a MetricStore on db.
func NewMetricStore(db *DB) *MetricStore {
	return &MetricStore{db: db}
}

// Insert records one sample.
func (s *MetricStore) Insert(ctx context.Context, metric model.Metric) error {
	if metric.SampledAt.IsZero() {
		metric.SampledAt = time.Now()
	}

	_, err := s.db.sql.ExecContext(ctx,
		`INSERT INTO metrics (service_id, container_name, sampled_at, cpu_percent,
		  mem_bytes, mem_limit, disk_bytes, restarts, uptime_secs)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		metric.ServiceID, metric.ContainerName, formatTime(metric.SampledAt),
		metric.CPUPercent, metric.MemBytes, metric.MemLimit,
		metric.DiskBytes, metric.Restarts, metric.UptimeSecs,
	)
	if err != nil {
		return fmt.Errorf("inserting metric: %w", err)
	}

	return nil
}

// ListByService returns samples chronologically, newest bound by limit.
// A zero since disables the lower bound.
func (s *MetricStore) ListByService(
	ctx context.Context,
	serviceID string,
	since time.Time,
	limit int,
) ([]model.Metric, error) {
	query := `SELECT id, service_id, container_name, sampled_at, cpu_percent,
		mem_bytes, mem_limit, disk_bytes, restarts, uptime_secs
		FROM metrics WHERE service_id = ?`
	args := []any{serviceID}

	if !since.IsZero() {
		query += " AND sampled_at >= ?"

		args = append(args, formatTime(since))
	}

	query += " ORDER BY sampled_at ASC, id ASC LIMIT ?"

	args = append(args, limit)

	rows, err := s.db.sql.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("querying metrics: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()

	return collectMetrics(rows)
}

// LatestByService returns the newest sample per container for alerting.
func (s *MetricStore) LatestByService(ctx context.Context, serviceID string) ([]model.Metric, error) {
	rows, err := s.db.sql.QueryContext(ctx,
		`SELECT id, service_id, container_name, sampled_at, cpu_percent,
		  mem_bytes, mem_limit, disk_bytes, restarts, uptime_secs
		 FROM metrics WHERE id IN (
		   SELECT MAX(id) FROM metrics WHERE service_id = ? GROUP BY container_name
		 )`, serviceID)
	if err != nil {
		return nil, fmt.Errorf("querying latest metrics: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()

	return collectMetrics(rows)
}

// collectMetrics scans metric rows.
func collectMetrics(rows *sql.Rows) ([]model.Metric, error) {
	metrics := []model.Metric{}

	for rows.Next() {
		var (
			metric    model.Metric
			sampledAt string
		)

		if err := rows.Scan(
			&metric.ID, &metric.ServiceID, &metric.ContainerName, &sampledAt,
			&metric.CPUPercent, &metric.MemBytes, &metric.MemLimit,
			&metric.DiskBytes, &metric.Restarts, &metric.UptimeSecs,
		); err != nil {
			return nil, fmt.Errorf("scanning metric: %w", err)
		}

		parsedAt, err := parseTime(sampledAt, "sampled_at")
		if err != nil {
			return nil, err
		}

		metric.SampledAt = parsedAt
		metrics = append(metrics, metric)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating metrics: %w", err)
	}

	return metrics, nil
}

// TrimBefore deletes samples older than cutoff and reports the count.
func (s *MetricStore) TrimBefore(ctx context.Context, cutoff time.Time) (int64, error) {
	res, err := s.db.sql.ExecContext(ctx, "DELETE FROM metrics WHERE sampled_at < ?", formatTime(cutoff))
	if err != nil {
		return 0, fmt.Errorf("trimming metrics: %w", err)
	}

	trimmed, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("counting trimmed metrics: %w", err)
	}

	return trimmed, nil
}
