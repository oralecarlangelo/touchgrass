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
	return s.InsertBatch(ctx, []model.Metric{metric})
}

// InsertBatch records one collection cycle's samples in a single transaction,
// so a cycle takes the write connection once instead of once per container.
func (s *MetricStore) InsertBatch(ctx context.Context, metrics []model.Metric) error {
	if len(metrics) == 0 {
		return nil
	}

	tx, err := s.db.sql.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("starting metric batch: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	now := time.Now()

	for _, metric := range metrics {
		if metric.SampledAt.IsZero() {
			metric.SampledAt = now
		}

		if _, err := tx.ExecContext(ctx,
			`INSERT INTO metrics (service_id, container_name, sampled_at, cpu_percent,
			  mem_bytes, mem_limit, disk_bytes, restarts, uptime_secs)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			metric.ServiceID, metric.ContainerName, formatTime(metric.SampledAt),
			metric.CPUPercent, metric.MemBytes, metric.MemLimit,
			metric.DiskBytes, metric.Restarts, metric.UptimeSecs,
		); err != nil {
			return fmt.Errorf("inserting metric: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing metric batch: %w", err)
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

	rows, err := s.db.read.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("querying metrics: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()

	return collectMetrics(rows)
}

// ListRange returns bounded samples for timeline correlation. An empty service
// ID selects every managed service.
func (s *MetricStore) ListRange(ctx context.Context, query model.PerformanceRange) ([]model.Metric, error) {
	statement := `SELECT id, service_id, container_name, sampled_at, cpu_percent,
		mem_bytes, mem_limit, disk_bytes, restarts, uptime_secs
		FROM metrics WHERE sampled_at >= ? AND sampled_at < ?`
	args := []any{formatTime(query.Start.UTC()), formatTime(query.End.UTC())}
	if query.ServiceID != "" {
		statement += " AND service_id = ?"
		args = append(args, query.ServiceID)
	}
	statement += " ORDER BY sampled_at ASC, id ASC LIMIT ?"
	limit := query.Limit
	if limit <= 0 || limit > 10000 {
		limit = 10000
	}
	args = append(args, limit)

	rows, err := s.db.read.QueryContext(ctx, statement, args...)
	if err != nil {
		return nil, fmt.Errorf("querying metric range: %w", err)
	}
	defer func() { _ = rows.Close() }()
	return collectMetrics(rows)
}

// LatestByService returns the newest sample per container for alerting.
func (s *MetricStore) LatestByService(ctx context.Context, serviceID string) ([]model.Metric, error) {
	rows, err := s.db.read.QueryContext(ctx,
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
	return s.db.trimOlderThan(ctx, ageTrim{table: tableMetrics, column: columnSampledAt}, formatTime(cutoff))
}
