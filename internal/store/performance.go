package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/oralecarlangelo/touchgrass/internal/model"
)

// PerformanceStore persists bounded aggregate buckets.
type PerformanceStore struct {
	db *DB
}

// NewPerformanceStore builds a PerformanceStore on db.
func NewPerformanceStore(db *DB) *PerformanceStore {
	return &PerformanceStore{db: db}
}

// UpsertHTTP additively merges one partial minute bucket. The opaque ingest key
// plus bounded bucket dimensions makes a retry idempotent.
func (s *PerformanceStore) UpsertHTTP(ctx context.Context, bucket model.HTTPMetricBucket) error {
	if err := validateHTTPBucket(bucket); err != nil {
		return err
	}
	bucket.BucketStart = bucket.BucketStart.UTC().Truncate(time.Minute)
	bucketKey := httpBucketKey(bucket)

	tx, err := s.db.sql.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("starting HTTP bucket upsert: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	result, err := tx.ExecContext(ctx, `INSERT INTO performance_ingest_batches
		(source, ingest_key, bucket_key, bucket_start, created_at)
		VALUES ('nginx', ?, ?, ?, ?)
		ON CONFLICT(source, ingest_key, bucket_key) DO NOTHING`,
		bucket.IngestKey, bucketKey, formatFixedTime(bucket.BucketStart), formatTime(time.Now().UTC()))
	if err != nil {
		return fmt.Errorf("recording HTTP ingest batch: %w", err)
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("checking HTTP ingest batch: %w", err)
	}
	if inserted == 0 {
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("committing duplicate HTTP bucket: %w", err)
		}
		return nil
	}

	counts := bucket.Histogram.Counts
	_, err = tx.ExecContext(ctx, `INSERT INTO http_metric_buckets (
		bucket_start, service_id, environment, method, route_family, upstream, status_class,
		request_count, error_count, duration_total_ms, upstream_connect_total_ms,
		upstream_header_total_ms, upstream_response_total_ms, bytes_sent, request_bytes,
		h_10, h_25, h_50, h_100, h_250, h_500, h_1000, h_2500, h_5000, h_inf, updated_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(bucket_start, service_id, environment, method, route_family, upstream, status_class)
	DO UPDATE SET
		request_count=request_count+excluded.request_count,
		error_count=error_count+excluded.error_count,
		duration_total_ms=duration_total_ms+excluded.duration_total_ms,
		upstream_connect_total_ms=upstream_connect_total_ms+excluded.upstream_connect_total_ms,
		upstream_header_total_ms=upstream_header_total_ms+excluded.upstream_header_total_ms,
		upstream_response_total_ms=upstream_response_total_ms+excluded.upstream_response_total_ms,
		bytes_sent=bytes_sent+excluded.bytes_sent,
		request_bytes=request_bytes+excluded.request_bytes,
		h_10=h_10+excluded.h_10, h_25=h_25+excluded.h_25, h_50=h_50+excluded.h_50,
		h_100=h_100+excluded.h_100, h_250=h_250+excluded.h_250, h_500=h_500+excluded.h_500,
		h_1000=h_1000+excluded.h_1000, h_2500=h_2500+excluded.h_2500,
		h_5000=h_5000+excluded.h_5000, h_inf=h_inf+excluded.h_inf,
		updated_at=excluded.updated_at`,
		formatFixedTime(bucket.BucketStart), bucket.ServiceID, bucket.Environment, bucket.Method,
		bucket.RouteFamily, bucket.Upstream, bucket.StatusClass, bucket.RequestCount, bucket.ErrorCount,
		bucket.DurationTotalMS, bucket.UpstreamConnectTotalMS, bucket.UpstreamHeaderTotalMS,
		bucket.UpstreamResponseTotalMS, bucket.BytesSent, bucket.RequestBytes,
		counts[0], counts[1], counts[2], counts[3], counts[4], counts[5], counts[6], counts[7], counts[8], counts[9],
		formatTime(time.Now().UTC()),
	)
	if err != nil {
		return fmt.Errorf("upserting HTTP bucket: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing HTTP bucket: %w", err)
	}
	return nil
}

// ListHTTP returns minute buckets ordered chronologically.
func (s *PerformanceStore) ListHTTP(ctx context.Context, query model.PerformanceRange) ([]model.HTTPMetricBucket, error) {
	return s.listHTTP(ctx, "http_metric_buckets", "", query)
}

// ListHTTPHourly returns hourly HTTP rollups ordered chronologically.
func (s *PerformanceStore) ListHTTPHourly(ctx context.Context, query model.PerformanceRange) ([]model.HTTPMetricBucket, error) {
	return s.listHTTP(ctx, "performance_hourly_rollups", "source = 'http' AND ", query)
}

func (s *PerformanceStore) listHTTP(ctx context.Context, table, prefix string, query model.PerformanceRange) ([]model.HTTPMetricBucket, error) {
	limit := query.Limit
	if limit <= 0 || limit > 10000 {
		limit = 1000
	}
	statement := fmt.Sprintf(`SELECT bucket_start, service_id, environment, method, route_family,
		upstream, status_class, request_count, error_count, duration_total_ms,
		upstream_connect_total_ms, upstream_header_total_ms, upstream_response_total_ms,
		bytes_sent, request_bytes, h_10, h_25, h_50, h_100, h_250, h_500, h_1000,
		h_2500, h_5000, h_inf FROM %s WHERE %sbucket_start >= ? AND bucket_start < ?`, table, prefix)
	args := []any{formatFixedTime(query.Start.UTC()), formatFixedTime(query.End.UTC())}
	if query.ServiceID != "" {
		statement += " AND service_id = ?"
		args = append(args, query.ServiceID)
	}
	statement += " ORDER BY bucket_start, service_id, method, route_family, upstream, status_class LIMIT ?"
	args = append(args, limit)

	rows, err := s.db.sql.QueryContext(ctx, statement, args...)
	if err != nil {
		return nil, fmt.Errorf("listing HTTP buckets: %w", err)
	}
	defer func() { _ = rows.Close() }()

	buckets := []model.HTTPMetricBucket{}
	for rows.Next() {
		bucket, err := scanHTTPBucket(rows)
		if err != nil {
			return nil, err
		}
		buckets = append(buckets, bucket)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating HTTP buckets: %w", err)
	}
	return buckets, nil
}

// RollupHTTP replaces the requested complete-hour range from minute buckets.
func (s *PerformanceStore) RollupHTTP(ctx context.Context, start, end time.Time) error {
	start = start.UTC().Truncate(time.Hour)
	end = end.UTC().Truncate(time.Hour)
	if !end.After(start) {
		return errors.New("rolling up HTTP buckets: end must be after start")
	}

	tx, err := s.db.sql.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("starting HTTP rollup: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `DELETE FROM performance_hourly_rollups
		WHERE source='http' AND bucket_start >= ? AND bucket_start < ?`, formatFixedTime(start), formatFixedTime(end)); err != nil {
		return fmt.Errorf("clearing HTTP rollups: %w", err)
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO performance_hourly_rollups (
		source, bucket_start, service_id, environment, method, route_family, upstream, status_class,
		request_count, error_count, duration_total_ms, upstream_connect_total_ms,
		upstream_header_total_ms, upstream_response_total_ms, bytes_sent, request_bytes,
		h_10, h_25, h_50, h_100, h_250, h_500, h_1000, h_2500, h_5000, h_inf, updated_at
	) SELECT 'http', strftime('%Y-%m-%dT%H:00:00', bucket_start) || '.000000000Z', service_id, environment,
		method, route_family, upstream, status_class, SUM(request_count), SUM(error_count),
		SUM(duration_total_ms), SUM(upstream_connect_total_ms), SUM(upstream_header_total_ms),
		SUM(upstream_response_total_ms), SUM(bytes_sent), SUM(request_bytes),
		SUM(h_10), SUM(h_25), SUM(h_50), SUM(h_100), SUM(h_250), SUM(h_500),
		SUM(h_1000), SUM(h_2500), SUM(h_5000), SUM(h_inf), ?
		FROM http_metric_buckets WHERE bucket_start >= ? AND bucket_start < ?
		GROUP BY strftime('%Y-%m-%dT%H:00:00', bucket_start), service_id, environment,
		method, route_family, upstream, status_class`, formatTime(time.Now().UTC()), formatFixedTime(start), formatFixedTime(end))
	if err != nil {
		return fmt.Errorf("writing HTTP rollups: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing HTTP rollups: %w", err)
	}
	return nil
}

// TrimHTTPBefore deletes at most limit minute buckets and their batch markers.
func (s *PerformanceStore) TrimHTTPBefore(ctx context.Context, cutoff time.Time, limit int) (int64, error) {
	if limit <= 0 || limit > model.MaximumMaintenanceBatch {
		limit = model.MaximumMaintenanceBatch
	}
	result, err := s.db.sql.ExecContext(ctx, `DELETE FROM http_metric_buckets WHERE id IN (
		SELECT id FROM http_metric_buckets WHERE bucket_start < ? ORDER BY bucket_start, id LIMIT ?
	)`, formatTime(cutoff.UTC()), limit)
	if err != nil {
		return 0, fmt.Errorf("trimming HTTP buckets: %w", err)
	}
	deleted, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("counting trimmed HTTP buckets: %w", err)
	}
	_, _ = s.db.sql.ExecContext(ctx, `DELETE FROM performance_ingest_batches WHERE bucket_start < ?`, formatTime(cutoff.UTC()))
	return deleted, nil
}

func validateHTTPBucket(bucket model.HTTPMetricBucket) error {
	if bucket.BucketStart.IsZero() || bucket.ServiceID == "" || bucket.Environment == "" ||
		bucket.Method == "" || bucket.RouteFamily == "" || bucket.Upstream == "" ||
		bucket.StatusClass < 1 || bucket.StatusClass > 5 || bucket.IngestKey == "" {
		return errors.New("upserting HTTP bucket: required bounded fields are missing")
	}
	if len(bucket.IngestKey) > 256 || bucket.RequestCount < 0 || bucket.ErrorCount < 0 {
		return errors.New("upserting HTTP bucket: invalid counts or ingest key")
	}
	return nil
}

func httpBucketKey(bucket model.HTTPMetricBucket) string {
	parts := []string{formatFixedTime(bucket.BucketStart.UTC().Truncate(time.Minute)), bucket.ServiceID,
		bucket.Environment, bucket.Method, bucket.RouteFamily, bucket.Upstream, fmt.Sprint(bucket.StatusClass)}
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x1f")))
	return hex.EncodeToString(sum[:])
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanHTTPBucket(row rowScanner) (model.HTTPMetricBucket, error) {
	var bucket model.HTTPMetricBucket
	var bucketStart string
	counts := &bucket.Histogram.Counts
	if err := row.Scan(&bucketStart, &bucket.ServiceID, &bucket.Environment, &bucket.Method,
		&bucket.RouteFamily, &bucket.Upstream, &bucket.StatusClass, &bucket.RequestCount,
		&bucket.ErrorCount, &bucket.DurationTotalMS, &bucket.UpstreamConnectTotalMS,
		&bucket.UpstreamHeaderTotalMS, &bucket.UpstreamResponseTotalMS, &bucket.BytesSent,
		&bucket.RequestBytes, &counts[0], &counts[1], &counts[2], &counts[3], &counts[4],
		&counts[5], &counts[6], &counts[7], &counts[8], &counts[9]); err != nil {
		return model.HTTPMetricBucket{}, fmt.Errorf("scanning HTTP bucket: %w", err)
	}
	parsed, err := parseTime(bucketStart, "bucket_start")
	if err != nil {
		return model.HTTPMetricBucket{}, err
	}
	bucket.BucketStart = parsed
	return bucket, nil
}

var _ rowScanner = (*sql.Row)(nil)
