package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/oralecarlangelo/touchgrass/internal/model"
)

// ErrIssueRuleNotFound reports an unknown issue rule id.
var ErrIssueRuleNotFound = errors.New("store: issue rule not found")

// IssueRuleStore persists issue alert rules.
type IssueRuleStore struct {
	db *DB
}

// NewIssueRuleStore builds an IssueRuleStore on db.
func NewIssueRuleStore(db *DB) *IssueRuleStore {
	return &IssueRuleStore{db: db}
}

// Create stores one rule and returns it with its id.
func (s *IssueRuleStore) Create(ctx context.Context, rule model.IssueRule) (model.IssueRule, error) {
	if rule.CreatedAt.IsZero() {
		rule.CreatedAt = time.Now()
	}

	enabled := 0
	if rule.Enabled {
		enabled = 1
	}

	res, err := s.db.sql.ExecContext(ctx,
		`INSERT INTO issue_rules (service_id, kind, threshold, window_secs, enabled, created_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		rule.ServiceID, rule.Kind, rule.Threshold, rule.WindowSecs, enabled, formatTime(rule.CreatedAt),
	)
	if err != nil {
		return model.IssueRule{}, fmt.Errorf("creating issue rule: %w", err)
	}

	id, err := res.LastInsertId()
	if err != nil {
		return model.IssueRule{}, fmt.Errorf("reading issue rule id: %w", err)
	}

	rule.ID = id

	return rule, nil
}

// ListByService returns a service's rules oldest first.
func (s *IssueRuleStore) ListByService(ctx context.Context, serviceID string) ([]model.IssueRule, error) {
	return s.list(ctx, "SELECT id, service_id, kind, threshold, window_secs, enabled, created_at FROM issue_rules WHERE service_id = ? ORDER BY id", serviceID)
}

// ListEnabled returns every enabled rule for the evaluator.
func (s *IssueRuleStore) ListEnabled(ctx context.Context) ([]model.IssueRule, error) {
	return s.list(ctx, "SELECT id, service_id, kind, threshold, window_secs, enabled, created_at FROM issue_rules WHERE enabled = 1 ORDER BY id")
}

// Delete removes one rule.
func (s *IssueRuleStore) Delete(ctx context.Context, id int64) error {
	res, err := s.db.sql.ExecContext(ctx, "DELETE FROM issue_rules WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("deleting issue rule: %w", err)
	}

	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("counting deleted issue rules: %w", err)
	}

	if affected == 0 {
		return fmt.Errorf("%w: %d", ErrIssueRuleNotFound, id)
	}

	return nil
}

// list runs a rule query with args.
func (s *IssueRuleStore) list(ctx context.Context, query string, args ...any) ([]model.IssueRule, error) {
	rows, err := s.db.sql.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("querying issue rules: %w", err)
	}

	defer func() {
		_ = rows.Close()
	}()

	rules := []model.IssueRule{}

	for rows.Next() {
		var (
			rule      model.IssueRule
			enabled   int
			createdAt string
		)

		if err := rows.Scan(
			&rule.ID,
			&rule.ServiceID,
			&rule.Kind,
			&rule.Threshold,
			&rule.WindowSecs,
			&enabled,
			&createdAt,
		); err != nil {
			return nil, fmt.Errorf("scanning issue rule: %w", err)
		}

		rule.Enabled = enabled != 0

		created, err := parseTime(createdAt, "created_at")
		if err != nil {
			return nil, err
		}

		rule.CreatedAt = created
		rules = append(rules, rule)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating issue rules: %w", err)
	}

	return rules, nil
}
