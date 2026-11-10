package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/oralecarlangelo/touchgrass/internal/model"
)

// ErrResourceNotFound reports a resource id that names no row, or a
// row that belongs to no service.
var ErrResourceNotFound = errors.New("store: resource not found")

// ownerQueries maps each resource kind to the query that finds the
// service a row belongs to. The table names are fixed here; nothing a
// caller supplies reaches the SQL text.
var ownerQueries = map[model.ResourceKind]string{
	model.ResourceIssue:        `SELECT service_id FROM issues WHERE id = ?`,
	model.ResourceLogLine:      `SELECT service_id FROM log_lines WHERE id = ?`,
	model.ResourceAlertRule:    `SELECT service_id FROM alert_rules WHERE id = ?`,
	model.ResourceIssueRule:    `SELECT service_id FROM issue_rules WHERE id = ?`,
	model.ResourceAPIKey:       `SELECT service_id FROM api_keys WHERE id = ?`,
	model.ResourceNotification: `SELECT service_id FROM notifications WHERE id = ?`,
}

// OwnerStore answers which service a resource row belongs to, so
// requests keyed by a resource id can be scoped like the rest.
type OwnerStore struct {
	db *DB
}

// NewOwnerStore builds an OwnerStore on db.
func NewOwnerStore(db *DB) *OwnerStore {
	return &OwnerStore{db: db}
}

// ServiceOf returns the id of the service that owns one resource row.
// A missing row and a row with no service both report
// ErrResourceNotFound.
func (s *OwnerStore) ServiceOf(ctx context.Context, kind model.ResourceKind, id int64) (string, error) {
	query, ok := ownerQueries[kind]
	if !ok {
		return "", fmt.Errorf("resolving owner: unsupported resource kind %d", kind)
	}

	var serviceID sql.NullString

	if err := s.db.sql.QueryRowContext(ctx, query, id).Scan(&serviceID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", ErrResourceNotFound
		}

		return "", fmt.Errorf("resolving owner: %w", err)
	}

	if !serviceID.Valid || serviceID.String == "" {
		return "", ErrResourceNotFound
	}

	return serviceID.String, nil
}
