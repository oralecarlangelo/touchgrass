package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/oralecarlangelo/touchgrass/internal/model"
)

// ErrCollectorCursorNotFound reports an unknown collector source.
var ErrCollectorCursorNotFound = errors.New("store: collector cursor not found")

// CursorStore persists native-source progress and health.
type CursorStore struct {
	db *DB
}

// NewCursorStore builds a CursorStore on db.
func NewCursorStore(db *DB) *CursorStore {
	return &CursorStore{db: db}
}

// Get returns one collector cursor.
func (s *CursorStore) Get(ctx context.Context, source string) (model.CollectorCursor, error) {
	var (
		cursor                    model.CollectorCursor
		lastRecorded, lastSuccess sql.NullString
		updatedRaw                string
	)

	err := s.db.sql.QueryRowContext(ctx, `SELECT source, identity, offset, last_recorded_at,
		last_success_at, last_error, dropped_records, coverage_complete, effective_interval_secs, updated_at
		FROM collector_cursors WHERE source = ?`, source).Scan(
		&cursor.Source, &cursor.Identity, &cursor.Offset, &lastRecorded,
		&lastSuccess, &cursor.LastError, &cursor.DroppedRecords, &cursor.CoverageComplete,
		&cursor.EffectiveIntervalSecs, &updatedRaw,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return model.CollectorCursor{}, fmt.Errorf("%w: %s", ErrCollectorCursorNotFound, source)
	}
	if err != nil {
		return model.CollectorCursor{}, fmt.Errorf("getting collector cursor: %w", err)
	}

	if cursor.LastRecordedAt, err = zeroTime(lastRecorded, "last_recorded_at"); err != nil {
		return model.CollectorCursor{}, err
	}
	if cursor.LastSuccessAt, err = zeroTime(lastSuccess, "last_success_at"); err != nil {
		return model.CollectorCursor{}, err
	}
	if cursor.UpdatedAt, err = parseTime(updatedRaw, "updated_at"); err != nil {
		return model.CollectorCursor{}, err
	}

	return cursor, nil
}

// Upsert replaces one source cursor idempotently.
func (s *CursorStore) Upsert(ctx context.Context, cursor model.CollectorCursor) error {
	if cursor.Source == "" {
		return errors.New("upserting collector cursor: source is required")
	}

	_, err := s.db.sql.ExecContext(ctx, `INSERT INTO collector_cursors (
		source, identity, offset, last_recorded_at, last_success_at, last_error, dropped_records,
		coverage_complete, effective_interval_secs, updated_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(source) DO UPDATE SET
		identity = excluded.identity,
		offset = excluded.offset,
		last_recorded_at = excluded.last_recorded_at,
		last_success_at = excluded.last_success_at,
		last_error = excluded.last_error,
		dropped_records = excluded.dropped_records,
		coverage_complete = excluded.coverage_complete,
		effective_interval_secs = excluded.effective_interval_secs,
		updated_at = excluded.updated_at`,
		cursor.Source, cursor.Identity, cursor.Offset, nullableTime(cursor.LastRecordedAt),
		nullableTime(cursor.LastSuccessAt), cursor.LastError, cursor.DroppedRecords,
		boolInteger(cursor.CoverageComplete), cursor.EffectiveIntervalSecs,
		formatTime(time.Now()),
	)
	if err != nil {
		return fmt.Errorf("upserting collector cursor: %w", err)
	}

	return nil
}

func nullableTime(value time.Time) any {
	if value.IsZero() {
		return nil
	}

	return formatTime(value)
}

func zeroTime(raw sql.NullString, column string) (time.Time, error) {
	if !raw.Valid {
		return time.Time{}, nil
	}

	return parseTime(raw.String, column)
}

// MaintenanceStore performs one bounded retention and checkpoint pass.
type MaintenanceStore struct {
	db *DB
}

// NewMaintenanceStore builds a MaintenanceStore on db.
func NewMaintenanceStore(db *DB) *MaintenanceStore {
	return &MaintenanceStore{db: db}
}

type retentionTarget struct {
	table     string
	timestamp string
	retention time.Duration
	predicate string
}

type storageClass struct {
	name    string
	limit   int64
	targets []retentionTarget
}

// Run enforces age limits in one bounded batch, checks budgets, and attempts a
// passive WAL checkpoint. Audit rows are deliberately excluded.
func (s *MaintenanceStore) Run(ctx context.Context, policy model.MaintenancePolicy) (model.MaintenanceResult, error) {
	policy = normalizeMaintenancePolicy(policy)
	result := model.MaintenanceResult{
		RanAt: policy.Now, MaxBytes: policy.MaxBytes,
		DeletedByTable: map[string]int64{}, ClassBytes: map[string]int64{},
	}

	targets := []retentionTarget{
		{table: "log_lines", timestamp: "ts", retention: policy.RetentionLogs},
		{table: "sdk_logs", timestamp: "ts", retention: policy.RetentionSDKLogs},
		{table: "occurrences", timestamp: "created_at", retention: policy.RetentionErrors},
		{table: "issues", timestamp: "last_seen", retention: policy.RetentionErrors},
		{table: "metrics", timestamp: "sampled_at", retention: policy.RetentionMetrics},
		{table: "host_samples", timestamp: "sampled_at", retention: policy.RetentionMetrics},
		{table: "container_samples", timestamp: "sampled_at", retention: policy.RetentionMetrics},
		{table: "notifications", timestamp: "created_at", retention: policy.RetentionNotifications},
		{table: "deploy_probes", timestamp: "sampled_at", retention: policy.RetentionDeploys},
		{table: "deploys", timestamp: "created_at", retention: policy.RetentionDeploys},
		{table: "db_jobs", timestamp: "created_at", retention: policy.RetentionDeploys},
		{table: "http_metric_buckets", timestamp: "bucket_start", retention: policy.RetentionPerformance},
		{table: "postgres_metric_buckets", timestamp: "bucket_start", retention: policy.RetentionPerformance},
		{table: "postgres_statement_buckets", timestamp: "bucket_start", retention: policy.RetentionPerformance},
		{table: "redis_metric_buckets", timestamp: "bucket_start", retention: policy.RetentionPerformance},
		{table: "log_volume_buckets", timestamp: "bucket_start", retention: policy.RetentionPerformance},
		{table: "performance_hourly_rollups", timestamp: "bucket_start", retention: policy.RetentionPerformanceHourly},
		{table: "security_observations", timestamp: "observed_at", retention: policy.RetentionSecurityObservations},
		{table: "container_events", timestamp: "occurred_at", retention: policy.RetentionContainerEvents},
		{table: "security_findings", timestamp: "resolved_at", retention: policy.RetentionSecurityFindings, predicate: "status = 'resolved'"},
		{table: "performance_ingest_batches", timestamp: "bucket_start", retention: policy.RetentionPerformance},
	}

	remaining := policy.BatchSize
	for _, target := range targets {
		if remaining == 0 || target.retention <= 0 {
			continue
		}

		deleted, err := s.deleteBefore(ctx, target, policy.Now.Add(-target.retention), remaining)
		if err != nil {
			return result, err
		}
		if deleted > 0 {
			result.DeletedByTable[target.table] = deleted
			result.DeletedRows += deleted
			remaining -= int(deleted)
		}
	}

	classBytes, err := s.classBytes(ctx)
	if err != nil {
		return result, err
	}
	classes := []storageClass{
		{name: "logs", limit: policy.LogMaxBytes, targets: []retentionTarget{
			{table: "log_lines", timestamp: "ts"}, {table: "sdk_logs", timestamp: "ts"},
		}},
		{name: "performance", limit: policy.PerformanceMaxBytes, targets: []retentionTarget{
			{table: "metrics", timestamp: "sampled_at"}, {table: "host_samples", timestamp: "sampled_at"},
			{table: "container_samples", timestamp: "sampled_at"},
			{table: "http_metric_buckets", timestamp: "bucket_start"},
			{table: "postgres_metric_buckets", timestamp: "bucket_start"},
			{table: "postgres_statement_buckets", timestamp: "bucket_start"},
			{table: "redis_metric_buckets", timestamp: "bucket_start"},
			{table: "log_volume_buckets", timestamp: "bucket_start"},
			{table: "performance_hourly_rollups", timestamp: "bucket_start"},
			{table: "container_events", timestamp: "occurred_at"},
			{table: "performance_ingest_batches", timestamp: "bucket_start"},
		}},
		{name: "issues", limit: policy.IssueMaxBytes, targets: []retentionTarget{
			{table: "occurrences", timestamp: "created_at"}, {table: "issues", timestamp: "last_seen"},
		}},
		{name: "administrative", limit: policy.AdministrativeMaxBytes, targets: []retentionTarget{
			{table: "notifications", timestamp: "created_at"}, {table: "deploy_probes", timestamp: "sampled_at"},
			{table: "deploys", timestamp: "created_at"}, {table: "db_jobs", timestamp: "created_at"},
		}},
		{name: "security", limit: model.DefaultSecurityClassMaxBytes, targets: []retentionTarget{
			{table: "security_observations", timestamp: "observed_at"},
			{table: "container_events", timestamp: "occurred_at"},
			{table: "security_findings", timestamp: "resolved_at", predicate: "status = 'resolved'"},
		}},
	}
	for _, class := range classes {
		if classBytes[class.name] <= class.limit || remaining == 0 {
			continue
		}
		for _, target := range class.targets {
			if remaining == 0 {
				break
			}
			deleted, err := s.deleteOldest(ctx, target, remaining)
			if err != nil {
				return result, err
			}
			if deleted > 0 {
				result.DeletedByTable[target.table] += deleted
				result.DeletedRows += deleted
				remaining -= int(deleted)
			}
		}
	}
	if result.DeletedRows > 0 {
		classBytes, err = s.classBytes(ctx)
		if err != nil {
			return result, err
		}
	}
	result.ClassBytes = classBytes
	for _, class := range classes {
		if classBytes[class.name] > class.limit {
			result.OverBudgetClasses = append(result.OverBudgetClasses, class.name)
		}
	}
	slices.Sort(result.OverBudgetClasses)

	result.WALCheckpointAttempted = true
	if err := s.db.sql.QueryRowContext(ctx, "PRAGMA wal_checkpoint(PASSIVE)").Scan(
		&result.WALBusy, &result.WALLogFrames, &result.WALCheckpointedFrames,
	); err != nil {
		return result, fmt.Errorf("checkpointing sqlite WAL: %w", err)
	}

	result.DatabaseBytes, err = s.databaseBytes(ctx)
	if err != nil {
		return result, err
	}
	result.OverBudget = result.DatabaseBytes > policy.MaxBytes || len(result.OverBudgetClasses) > 0

	return result, nil
}

func normalizeMaintenancePolicy(policy model.MaintenancePolicy) model.MaintenancePolicy {
	if policy.Now.IsZero() {
		policy.Now = time.Now().UTC()
	}
	if policy.BatchSize <= 0 || policy.BatchSize > model.MaximumMaintenanceBatch {
		policy.BatchSize = model.MaximumMaintenanceBatch
	}
	if policy.MaxBytes <= 0 {
		policy.MaxBytes = model.DefaultSQLiteMaxBytes
	}
	if policy.LogMaxBytes <= 0 {
		policy.LogMaxBytes = model.DefaultLogClassMaxBytes
	}
	if policy.PerformanceMaxBytes <= 0 {
		policy.PerformanceMaxBytes = model.DefaultPerformanceClassMaxBytes
	}
	if policy.IssueMaxBytes <= 0 {
		policy.IssueMaxBytes = model.DefaultIssueClassMaxBytes
	}
	if policy.AdministrativeMaxBytes <= 0 {
		policy.AdministrativeMaxBytes = model.DefaultAdministrativeClassMaxBytes
	}

	return policy
}

func (s *MaintenanceStore) deleteBefore(
	ctx context.Context,
	target retentionTarget,
	cutoff time.Time,
	limit int,
) (int64, error) {
	predicate := ""
	if target.predicate != "" {
		predicate = " AND " + target.predicate
	}
	query := fmt.Sprintf(`DELETE FROM %s WHERE id IN (
		SELECT id FROM %s WHERE %s < ?%s ORDER BY id LIMIT ?
	)`, target.table, target.table, target.timestamp, predicate)
	res, err := s.db.sql.ExecContext(ctx, query, formatFixedTime(cutoff), limit)
	if err != nil {
		return 0, fmt.Errorf("trimming %s: %w", target.table, err)
	}

	deleted, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("counting trimmed %s: %w", target.table, err)
	}

	return deleted, nil
}

func (s *MaintenanceStore) deleteOldest(ctx context.Context, target retentionTarget, limit int) (int64, error) {
	predicate := ""
	if target.predicate != "" {
		predicate = " WHERE " + target.predicate
	}
	query := fmt.Sprintf(`DELETE FROM %s WHERE id IN (
		SELECT id FROM %s%s ORDER BY %s, id LIMIT ?
	)`, target.table, target.table, predicate, target.timestamp)
	res, err := s.db.sql.ExecContext(ctx, query, limit)
	if err != nil {
		return 0, fmt.Errorf("trimming over-budget %s: %w", target.table, err)
	}
	deleted, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("counting over-budget %s rows: %w", target.table, err)
	}

	return deleted, nil
}

func (s *MaintenanceStore) databaseBytes(ctx context.Context) (int64, error) {
	var pageCount, pageSize int64
	if err := s.db.sql.QueryRowContext(ctx, "PRAGMA page_count").Scan(&pageCount); err != nil {
		return 0, fmt.Errorf("reading sqlite page count: %w", err)
	}
	if err := s.db.sql.QueryRowContext(ctx, "PRAGMA page_size").Scan(&pageSize); err != nil {
		return 0, fmt.Errorf("reading sqlite page size: %w", err)
	}

	return pageCount * pageSize, nil
}

func (s *MaintenanceStore) classBytes(ctx context.Context) (map[string]int64, error) {
	queries := map[string]string{
		"logs": `SELECT
			COALESCE((SELECT SUM(length(line)+length(container)+length(stream)+length(service_id)+length(ts)) FROM log_lines), 0) +
			COALESCE((SELECT SUM(length(message)+length(attributes)+length(service_id)+length(ts)) FROM sdk_logs), 0)`,
		"performance": `SELECT
			(SELECT COUNT(*) * 128 FROM metrics) +
			(SELECT COUNT(*) * 96 FROM host_samples) +
			(SELECT COUNT(*) * 128 FROM container_samples) +
			(SELECT COUNT(*) * 256 FROM http_metric_buckets) +
			(SELECT COUNT(*) * 96 FROM postgres_metric_buckets) +
			(SELECT COUNT(*) * 160 FROM postgres_statement_buckets) +
			(SELECT COUNT(*) * 96 FROM redis_metric_buckets) +
			(SELECT COUNT(*) * 80 FROM log_volume_buckets) +
			(SELECT COUNT(*) * 256 FROM performance_hourly_rollups) +
			(SELECT COUNT(*) * 128 FROM container_events) +
			(SELECT COUNT(*) * 96 FROM performance_ingest_batches)`,
		"issues": `SELECT
			COALESCE((SELECT SUM(length(type)+length(message)+length(stack)+length(breadcrumbs)+length(service_id)) FROM occurrences), 0) +
			COALESCE((SELECT SUM(length(title)+length(fingerprint)+length(service_id)) FROM issues), 0)`,
		"administrative": `SELECT
			COALESCE((SELECT SUM(length(title)+length(body)+length(kind)+length(service_id)) FROM notifications), 0) +
			(SELECT COUNT(*) * 128 FROM deploys) +
			(SELECT COUNT(*) * 48 FROM deploy_probes) +
			COALESCE((SELECT SUM(length(target)+length(detail)+length(kind)+length(status)) FROM db_jobs), 0)`,
		"security": `SELECT
			COALESCE((SELECT SUM(length(container_id)+length(rule_id)+length(category)+length(severity)+length(confidence)+length(evidence_json)+length(observed_at)) FROM security_observations), 0) +
			COALESCE((SELECT SUM(length(fingerprint)+length(container_id)+length(rule_id)+length(evidence_json)+length(opened_at)+length(last_seen_at)) FROM security_findings), 0) +
			COALESCE((SELECT COUNT(*) * 192 FROM container_events), 0)`,
	}

	result := make(map[string]int64, len(queries))
	for name, query := range queries {
		var bytes int64
		if err := s.db.sql.QueryRowContext(ctx, query).Scan(&bytes); err != nil {
			return nil, fmt.Errorf("measuring %s storage class: %w", name, err)
		}
		result[name] = bytes
	}

	return result, nil
}
