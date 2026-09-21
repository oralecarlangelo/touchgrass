package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/oralecarlangelo/touchgrass/internal/model"
)

// AuditStore persists append-only audit entries. It deliberately exposes
// insert/list only: audit rows are never updated, deleted, or trimmed.
type AuditStore struct {
	db *DB
}

// NewAuditStore builds an AuditStore on db.
func NewAuditStore(db *DB) *AuditStore {
	return &AuditStore{db: db}
}

// Insert records one audit entry and returns its id.
func (s *AuditStore) Insert(ctx context.Context, audit model.Audit) (int64, error) {
	if audit.CreatedAt.IsZero() {
		audit.CreatedAt = time.Now()
	}

	res, err := s.db.sql.ExecContext(ctx,
		`INSERT INTO audit (service_id, actor, action, result, detail, created_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		nullService(audit.ServiceID), audit.Actor, audit.Action,
		audit.Result, audit.Detail, formatTime(audit.CreatedAt),
	)
	if err != nil {
		return 0, fmt.Errorf("inserting audit entry: %w", err)
	}

	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("reading audit id: %w", err)
	}

	return id, nil
}

// List returns audit entries newest first, bounded by limit. An empty
// serviceID selects every service; otherwise it selects one service.
func (s *AuditStore) List(
	ctx context.Context,
	serviceID string,
	limit int,
) ([]model.Audit, error) {
	query := `SELECT id, service_id, actor, action, result, detail, created_at
	 FROM audit ORDER BY id DESC LIMIT ?`
	args := []any{limit}

	if serviceID != "" {
		query = `SELECT id, service_id, actor, action, result, detail, created_at
		 FROM audit WHERE service_id = ? ORDER BY id DESC LIMIT ?`
		args = []any{serviceID, limit}
	}

	rows, err := s.db.sql.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("querying audit: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()

	entries := []model.Audit{}

	for rows.Next() {
		var (
			entry     model.Audit
			serviceID sql.NullString
			createdAt string
		)

		if err := rows.Scan(
			&entry.ID, &serviceID, &entry.Actor, &entry.Action,
			&entry.Result, &entry.Detail, &createdAt,
		); err != nil {
			return nil, fmt.Errorf("scanning audit entry: %w", err)
		}

		if serviceID.Valid {
			service := serviceID.String
			entry.ServiceID = &service
		}

		created, err := parseTime(createdAt, "created_at")
		if err != nil {
			return nil, err
		}

		entry.CreatedAt = created
		entries = append(entries, entry)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating audit: %w", err)
	}

	return entries, nil
}

// nullService renders an optional service id for storage.
func nullService(serviceID *string) any {
	if serviceID == nil {
		return nil
	}

	return *serviceID
}
