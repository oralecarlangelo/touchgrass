package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/oralecarlangelo/touchgrass/internal/model"
)

// SDKLogFilter bounds an SDK log search. Nil Since/Until select open
// ends; empty Levels selects all levels; empty TraceID selects all
// traces; Query matches message substrings, case-insensitively.
type SDKLogFilter struct {
	ServiceID string
	Query     string
	Levels    []string
	TraceID   string
	Since     *time.Time
	Until     *time.Time
	Limit     int
}

// SDKLogStore persists structured SDK log rows.
type SDKLogStore struct {
	db *DB
}

// NewSDKLogStore builds an SDKLogStore on db.
func NewSDKLogStore(db *DB) *SDKLogStore {
	return &SDKLogStore{db: db}
}

// InsertBatch stores rows in one transaction and reports the count.
func (s *SDKLogStore) InsertBatch(ctx context.Context, logs []model.SDKLog) (int64, error) {
	if len(logs) == 0 {
		return 0, nil
	}

	tx, err := s.db.sql.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("starting sdk log batch: %w", err)
	}

	defer func() {
		_ = tx.Rollback()
	}()

	stmt, err := tx.PrepareContext(ctx,
		`INSERT INTO sdk_logs (service_id, ts, level, severity, message, attributes, trace_id, span_id, release, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
	)
	if err != nil {
		return 0, fmt.Errorf("preparing sdk log insert: %w", err)
	}

	defer func() {
		_ = stmt.Close()
	}()

	now := time.Now()

	for _, log := range logs {
		ts := log.Ts
		if ts.IsZero() {
			ts = now
		}

		created := log.CreatedAt
		if created.IsZero() {
			created = now
		}

		attributes, err := encodeSDKAttributes(log.Attributes)
		if err != nil {
			return 0, err
		}

		if _, err := stmt.ExecContext(ctx,
			log.ServiceID, formatFixedTime(ts), log.Level, log.Severity,
			log.Message, attributes, log.TraceID, log.SpanID,
			log.Release, formatTime(created),
		); err != nil {
			return 0, fmt.Errorf("inserting sdk log: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("committing sdk log batch: %w", err)
	}

	return int64(len(logs)), nil
}

// Search returns matching rows newest first, bounded by limit.
func (s *SDKLogStore) Search(ctx context.Context, filter SDKLogFilter) ([]model.SDKLog, error) {
	since, until := searchBounds(LogFilter{Since: filter.Since, Until: filter.Until})
	limit := filter.Limit

	if limit <= 0 {
		limit = defaultSearchLimit
	}

	extra, extraArgs := sdkLogFilterClauses(filter)

	return queryRows(ctx, s.db.sql, rowQuery[model.SDKLog]{
		what: "sdk logs",
		query: `SELECT id, service_id, ts, level, severity, message, attributes, trace_id, span_id, release, created_at
		 FROM sdk_logs
		 WHERE service_id = ? AND ts >= ? AND ts <= ?` + extra + `
		 ORDER BY id DESC LIMIT ?`,
		args: append([]any{filter.ServiceID, since, until}, append(extraArgs, limit)...),
		scan: scanSDKLog,
	})
}

// TrimBefore deletes rows emitted before cutoff and reports the count.
func (s *SDKLogStore) TrimBefore(ctx context.Context, cutoff time.Time) (int64, error) {
	res, err := s.db.sql.ExecContext(ctx,
		"DELETE FROM sdk_logs WHERE ts < ?", formatFixedTime(cutoff))
	if err != nil {
		return 0, fmt.Errorf("trimming sdk logs: %w", err)
	}

	trimmed, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("counting trimmed sdk logs: %w", err)
	}

	return trimmed, nil
}

// sdkLogFilterClauses renders optional trace/level/message predicates.
// Fragments are constant; values ride as args, never interpolated.
func sdkLogFilterClauses(filter SDKLogFilter) (string, []any) {
	clauses := ""
	args := []any{}

	if filter.TraceID != "" {
		clauses += " AND trace_id = ?"

		args = append(args, filter.TraceID)
	}

	if len(filter.Levels) > 0 {
		placeholders := make([]string, 0, len(filter.Levels))

		for _, level := range filter.Levels {
			placeholders = append(placeholders, "?")
			args = append(args, level)
		}

		clauses += " AND level IN (" + strings.Join(placeholders, ", ") + ")"
	}

	if strings.TrimSpace(filter.Query) != "" {
		clauses += " AND message LIKE ? ESCAPE '\\'"

		args = append(args, "%"+escapeLike(filter.Query)+"%")
	}

	return clauses, args
}

// escapeLike quotes LIKE metacharacters so queries match literally.
func escapeLike(query string) string {
	query = strings.ReplaceAll(query, "\\", "\\\\")
	query = strings.ReplaceAll(query, "%", "\\%")
	query = strings.ReplaceAll(query, "_", "\\_")

	return query
}

// emptyAttributes is the stored form when a row carries no attributes.
const emptyAttributes = "{}"

// encodeSDKAttributes renders attributes JSON, defaulting empty to {}.
func encodeSDKAttributes(attributes map[string]model.SDKLogAttribute) (string, error) {
	if len(attributes) == 0 {
		return emptyAttributes, nil
	}

	raw, err := json.Marshal(attributes)
	if err != nil {
		return "", fmt.Errorf("encoding sdk log attributes: %w", err)
	}

	return string(raw), nil
}

// scanSDKLog maps one sdk_logs row.
func scanSDKLog(row scanner) (model.SDKLog, error) {
	var (
		log        model.SDKLog
		ts         string
		attributes string
		createdAt  string
	)

	if err := row.Scan(
		&log.ID,
		&log.ServiceID,
		&ts,
		&log.Level,
		&log.Severity,
		&log.Message,
		&attributes,
		&log.TraceID,
		&log.SpanID,
		&log.Release,
		&createdAt,
	); err != nil {
		return model.SDKLog{}, fmt.Errorf("scanning sdk log: %w", err)
	}

	stamp, err := parseTime(ts, "ts")
	if err != nil {
		return model.SDKLog{}, err
	}

	log.Ts = stamp

	decoded := map[string]model.SDKLogAttribute{}

	if err := json.Unmarshal([]byte(attributes), &decoded); err != nil {
		return model.SDKLog{}, fmt.Errorf("decoding sdk log attributes: %w", err)
	}

	log.Attributes = decoded

	created, err := parseTime(createdAt, "created_at")
	if err != nil {
		return model.SDKLog{}, err
	}

	log.CreatedAt = created

	return log, nil
}
