package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/oralecarlangelo/touchgrass/internal/model"
)

// rowExecer covers *sql.DB and *sql.Tx for inserts.
type rowExecer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// OccurrenceStore persists raw SDK error reports.
type OccurrenceStore struct {
	db *DB
}

// NewOccurrenceStore builds an OccurrenceStore on db.
func NewOccurrenceStore(db *DB) *OccurrenceStore {
	return &OccurrenceStore{db: db}
}

// Insert stores one occurrence and returns it with its id.
func (s *OccurrenceStore) Insert(ctx context.Context, occurrence model.Occurrence) (model.Occurrence, error) {
	if occurrence.CreatedAt.IsZero() {
		occurrence.CreatedAt = time.Now()
	}

	id, err := insertOccurrenceRow(ctx, s.db.sql, occurrence)
	if err != nil {
		return model.Occurrence{}, err
	}

	occurrence.ID = id

	return occurrence, nil
}

// insertOccurrenceRow inserts one row on db or a transaction.
func insertOccurrenceRow(
	ctx context.Context,
	exec rowExecer,
	occurrence model.Occurrence,
) (int64, error) {
	stack, err := json.Marshal(nonNilFrames(occurrence.Stack))
	if err != nil {
		return 0, fmt.Errorf("encoding stack: %w", err)
	}

	crumbs, err := json.Marshal(nonNilCrumbs(occurrence.Breadcrumbs))
	if err != nil {
		return 0, fmt.Errorf("encoding breadcrumbs: %w", err)
	}

	res, err := exec.ExecContext(ctx,
		`INSERT INTO occurrences (service_id, type, message, stack, breadcrumbs, release, issue_id, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		occurrence.ServiceID, occurrence.Type, occurrence.Message,
		string(stack), string(crumbs), occurrence.Release, occurrence.IssueID,
		formatTime(occurrence.CreatedAt),
	)
	if err != nil {
		return 0, fmt.Errorf("inserting occurrence: %w", err)
	}

	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("reading occurrence id: %w", err)
	}

	return id, nil
}

// CountByService counts occurrences for one service.
func (s *OccurrenceStore) CountByService(ctx context.Context, serviceID string) (int64, error) {
	var count int64

	if err := s.db.sql.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM occurrences WHERE service_id = ?", serviceID,
	).Scan(&count); err != nil {
		return 0, fmt.Errorf("counting occurrences: %w", err)
	}

	return count, nil
}

// TrimBeyondCap deletes a service's oldest rows past maximum, newest kept,
// and reports the trimmed count.
func (s *OccurrenceStore) TrimBeyondCap(ctx context.Context, serviceID string, maximum int) (int64, error) {
	res, err := s.db.sql.ExecContext(ctx,
		`DELETE FROM occurrences WHERE service_id = ? AND id NOT IN (
		   SELECT id FROM occurrences WHERE service_id = ? ORDER BY id DESC LIMIT ?
		 )`,
		serviceID, serviceID, maximum,
	)
	if err != nil {
		return 0, fmt.Errorf("trimming occurrences beyond cap: %w", err)
	}

	trimmed, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("counting trimmed occurrences: %w", err)
	}

	return trimmed, nil
}

// TrimBefore deletes occurrences created before cutoff and reports the count.
func (s *OccurrenceStore) TrimBefore(ctx context.Context, cutoff time.Time) (int64, error) {
	res, err := s.db.sql.ExecContext(ctx,
		"DELETE FROM occurrences WHERE created_at < ?", formatTime(cutoff))
	if err != nil {
		return 0, fmt.Errorf("trimming occurrences: %w", err)
	}

	trimmed, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("counting trimmed occurrences: %w", err)
	}

	return trimmed, nil
}

// ListByService returns occurrences newest first, bounded by limit.
func (s *OccurrenceStore) ListByService(
	ctx context.Context,
	serviceID string,
	limit int,
) ([]model.Occurrence, error) {
	return queryRows(ctx, s.db.sql, rowQuery[model.Occurrence]{
		what: "occurrences",
		query: `SELECT id, service_id, type, message, stack, breadcrumbs, release, issue_id, created_at
		 FROM occurrences WHERE service_id = ? ORDER BY id DESC LIMIT ?`,
		args: []any{serviceID, limit},
		scan: scanOccurrence,
	})
}

// ListByIssue returns an issue's occurrences newest first, bounded by limit.
func (s *OccurrenceStore) ListByIssue(
	ctx context.Context,
	issueID int64,
	limit int,
) ([]model.Occurrence, error) {
	return queryRows(ctx, s.db.sql, rowQuery[model.Occurrence]{
		what: "issue occurrences",
		query: `SELECT id, service_id, type, message, stack, breadcrumbs, release, issue_id, created_at
		 FROM occurrences WHERE issue_id = ? ORDER BY id DESC LIMIT ?`,
		args: []any{issueID, limit},
		scan: scanOccurrence,
	})
}

// CountByIssueSince counts an issue's occurrences at or after since.
func (s *OccurrenceStore) CountByIssueSince(
	ctx context.Context,
	issueID int64,
	since time.Time,
) (int64, error) {
	var count int64

	if err := s.db.sql.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM occurrences WHERE issue_id = ? AND created_at >= ?",
		issueID, formatTime(since),
	).Scan(&count); err != nil {
		return 0, fmt.Errorf("counting issue occurrences: %w", err)
	}

	return count, nil
}

// scanOccurrence maps one occurrences row.
func scanOccurrence(row scanner) (model.Occurrence, error) {
	var (
		occurrence model.Occurrence
		stack      string
		crumbs     string
		issueID    sql.NullInt64
		createdAt  string
	)

	if err := row.Scan(
		&occurrence.ID,
		&occurrence.ServiceID,
		&occurrence.Type,
		&occurrence.Message,
		&stack,
		&crumbs,
		&occurrence.Release,
		&issueID,
		&createdAt,
	); err != nil {
		return model.Occurrence{}, fmt.Errorf("scanning occurrence: %w", err)
	}

	if issueID.Valid {
		occurrence.IssueID = &issueID.Int64
	}

	if err := json.Unmarshal([]byte(stack), &occurrence.Stack); err != nil {
		return model.Occurrence{}, fmt.Errorf("decoding stack: %w", err)
	}

	if err := json.Unmarshal([]byte(crumbs), &occurrence.Breadcrumbs); err != nil {
		return model.Occurrence{}, fmt.Errorf("decoding breadcrumbs: %w", err)
	}

	occurrence.Stack = nonNilFrames(occurrence.Stack)
	occurrence.Breadcrumbs = nonNilCrumbs(occurrence.Breadcrumbs)

	created, err := parseTime(createdAt, "created_at")
	if err != nil {
		return model.Occurrence{}, err
	}

	occurrence.CreatedAt = created

	return occurrence, nil
}

// nonNilFrames normalizes a nil frame slice for storage and JSON.
func nonNilFrames(frames []model.StackFrame) []model.StackFrame {
	if frames == nil {
		return []model.StackFrame{}
	}

	return frames
}

// nonNilCrumbs normalizes a nil breadcrumb slice for storage and JSON.
func nonNilCrumbs(crumbs []model.Breadcrumb) []model.Breadcrumb {
	if crumbs == nil {
		return []model.Breadcrumb{}
	}

	return crumbs
}
