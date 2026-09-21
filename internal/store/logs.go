package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/oralecarlangelo/touchgrass/internal/model"
)

// ErrLogLineNotFound reports an unknown log line id.
var ErrLogLineNotFound = errors.New("store: log line not found")

// LogFilter bounds a log search. Nil Since/Until select open ends.
type LogFilter struct {
	ServiceID string
	Query     string
	Since     *time.Time
	Until     *time.Time
	Limit     int
}

// LogStore persists container log lines with full-text search.
type LogStore struct {
	db *DB
}

// NewLogStore builds a LogStore on db.
func NewLogStore(db *DB) *LogStore {
	return &LogStore{db: db}
}

// InsertBatch stores lines in one transaction and reports the count.
func (s *LogStore) InsertBatch(ctx context.Context, lines []model.LogLine) (int64, error) {
	if len(lines) == 0 {
		return 0, nil
	}

	tx, err := s.db.sql.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("starting log batch: %w", err)
	}

	defer func() {
		_ = tx.Rollback()
	}()

	stmt, err := tx.PrepareContext(ctx,
		`INSERT INTO log_lines (service_id, container, stream, line, ts, created_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
	)
	if err != nil {
		return 0, fmt.Errorf("preparing log insert: %w", err)
	}

	defer func() {
		_ = stmt.Close()
	}()

	now := time.Now()

	for _, line := range lines {
		ts := line.Ts
		if ts.IsZero() {
			ts = now
		}

		created := line.CreatedAt
		if created.IsZero() {
			created = now
		}

		if _, err := stmt.ExecContext(ctx,
			line.ServiceID, line.Container, line.Stream, line.Line,
			formatFixedTime(ts), formatTime(created),
		); err != nil {
			return 0, fmt.Errorf("inserting log line: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("committing log batch: %w", err)
	}

	return int64(len(lines)), nil
}

// Search returns matching lines newest first, bounded by limit.
func (s *LogStore) Search(ctx context.Context, filter LogFilter) ([]model.LogLine, error) {
	since, until := searchBounds(filter)
	limit := filter.Limit

	if limit <= 0 {
		limit = defaultSearchLimit
	}

	if match, ok := sanitizeFTS(filter.Query); ok {
		return queryRows(ctx, s.db.sql, rowQuery[model.LogLine]{
			what: "log lines",
			query: `SELECT l.id, l.service_id, l.container, l.stream, l.line, l.ts, l.created_at
			 FROM log_lines l JOIN log_lines_fts f ON f.rowid = l.id
			 WHERE log_lines_fts MATCH ? AND l.service_id = ? AND l.ts >= ? AND l.ts <= ?
			 ORDER BY l.id DESC LIMIT ?`,
			args: []any{match, filter.ServiceID, since, until, limit},
			scan: scanLogLine,
		})
	}

	return queryRows(ctx, s.db.sql, rowQuery[model.LogLine]{
		what: "log lines",
		query: `SELECT id, service_id, container, stream, line, ts, created_at
		 FROM log_lines
		 WHERE service_id = ? AND ts >= ? AND ts <= ?
		 ORDER BY id DESC LIMIT ?`,
		args: []any{filter.ServiceID, since, until, limit},
		scan: scanLogLine,
	})
}

// Get returns one log line.
func (s *LogStore) Get(ctx context.Context, id int64) (model.LogLine, error) {
	row := s.db.sql.QueryRowContext(ctx,
		`SELECT id, service_id, container, stream, line, ts, created_at
		 FROM log_lines WHERE id = ?`,
		id,
	)

	line, err := scanLogLine(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return model.LogLine{}, fmt.Errorf("%w: %d", ErrLogLineNotFound, id)
		}

		return model.LogLine{}, err
	}

	return line, nil
}

// Context returns a line with up to before older and after newer
// same-service lines; Before and After run oldest first.
func (s *LogStore) Context(
	ctx context.Context,
	id int64,
	before, after int,
) (model.LogContext, error) {
	anchor, err := s.Get(ctx, id)
	if err != nil {
		return model.LogContext{}, err
	}

	older, err := queryRows(ctx, s.db.sql, rowQuery[model.LogLine]{
		what: "log context",
		query: `SELECT id, service_id, container, stream, line, ts, created_at
		 FROM log_lines
		 WHERE service_id = ? AND id < ? ORDER BY id DESC LIMIT ?`,
		args: []any{anchor.ServiceID, id, before},
		scan: scanLogLine,
	})
	if err != nil {
		return model.LogContext{}, err
	}

	newer, err := queryRows(ctx, s.db.sql, rowQuery[model.LogLine]{
		what: "log context",
		query: `SELECT id, service_id, container, stream, line, ts, created_at
		 FROM log_lines
		 WHERE service_id = ? AND id > ? ORDER BY id ASC LIMIT ?`,
		args: []any{anchor.ServiceID, id, after},
		scan: scanLogLine,
	})
	if err != nil {
		return model.LogContext{}, err
	}

	for i, j := 0, len(older)-1; i < j; i, j = i+1, j-1 {
		older[i], older[j] = older[j], older[i]
	}

	return model.LogContext{Anchor: anchor, Before: older, After: newer}, nil
}

// CountByService counts a service's lines.
func (s *LogStore) CountByService(ctx context.Context, serviceID string) (int64, error) {
	var count int64

	if err := s.db.sql.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM log_lines WHERE service_id = ?", serviceID,
	).Scan(&count); err != nil {
		return 0, fmt.Errorf("counting log lines: %w", err)
	}

	return count, nil
}

// TrimBeyondCap deletes a service's oldest rows past maximum and reports
// the trimmed count.
func (s *LogStore) TrimBeyondCap(
	ctx context.Context,
	serviceID string,
	maximum int,
) (int64, error) {
	res, err := s.db.sql.ExecContext(ctx,
		`DELETE FROM log_lines WHERE service_id = ? AND id NOT IN (
		   SELECT id FROM log_lines WHERE service_id = ? ORDER BY id DESC LIMIT ?
		 )`,
		serviceID, serviceID, maximum,
	)
	if err != nil {
		return 0, fmt.Errorf("trimming log lines beyond cap: %w", err)
	}

	trimmed, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("counting trimmed log lines: %w", err)
	}

	return trimmed, nil
}

// TrimBefore deletes lines emitted before cutoff and reports the count.
func (s *LogStore) TrimBefore(ctx context.Context, cutoff time.Time) (int64, error) {
	res, err := s.db.sql.ExecContext(ctx,
		"DELETE FROM log_lines WHERE ts < ?", formatFixedTime(cutoff))
	if err != nil {
		return 0, fmt.Errorf("trimming log lines: %w", err)
	}

	trimmed, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("counting trimmed log lines: %w", err)
	}

	return trimmed, nil
}

// defaultSearchLimit bounds unpaginated searches.
const defaultSearchLimit = 100

// searchBounds renders fixed-precision inclusive ts bounds.
func searchBounds(filter LogFilter) (string, string) {
	since := "0001-01-01T00:00:00.000000000Z"
	until := "9999-12-31T23:59:59.999999999Z"

	if filter.Since != nil {
		since = formatFixedTime(*filter.Since)
	}

	if filter.Until != nil {
		until = formatFixedTime(*filter.Until)
	}

	return since, until
}

// sanitizeFTS quotes whitespace-separated terms into an AND match.
// It reports false when the query holds no terms.
func sanitizeFTS(query string) (string, bool) {
	fields := strings.Fields(query)
	if len(fields) == 0 {
		return "", false
	}

	terms := make([]string, 0, len(fields))

	for _, field := range fields {
		terms = append(terms, `"`+strings.ReplaceAll(field, `"`, `""`)+`"`)
	}

	return strings.Join(terms, " AND "), true
}

// scanLogLine maps one log_lines row.
func scanLogLine(row scanner) (model.LogLine, error) {
	var (
		line      model.LogLine
		ts        string
		createdAt string
	)

	if err := row.Scan(
		&line.ID,
		&line.ServiceID,
		&line.Container,
		&line.Stream,
		&line.Line,
		&ts,
		&createdAt,
	); err != nil {
		return model.LogLine{}, fmt.Errorf("scanning log line: %w", err)
	}

	stamp, err := parseTime(ts, "ts")
	if err != nil {
		return model.LogLine{}, err
	}

	line.Ts = stamp

	created, err := parseTime(createdAt, "created_at")
	if err != nil {
		return model.LogLine{}, err
	}

	line.CreatedAt = created

	return line, nil
}
