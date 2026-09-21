package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/oralecarlangelo/touchgrass/internal/model"
)

// ErrRuleNotFound reports an unknown alert rule id.
var ErrRuleNotFound = errors.New("store: alert rule not found")

// RuleStore persists threshold alert rules.
type RuleStore struct {
	db *DB
}

// NewRuleStore builds a RuleStore on db.
func NewRuleStore(db *DB) *RuleStore {
	return &RuleStore{db: db}
}

// AllEnabled returns every enabled rule.
func (s *RuleStore) AllEnabled(ctx context.Context) ([]model.AlertRule, error) {
	return s.query(ctx, "SELECT id, service_id, metric, threshold, duration_secs, enabled, created_at "+
		"FROM alert_rules WHERE enabled = 1 ORDER BY id")
}

// ListByService returns every rule for one service.
func (s *RuleStore) ListByService(ctx context.Context, serviceID string) ([]model.AlertRule, error) {
	rows, err := s.db.sql.QueryContext(ctx,
		`SELECT id, service_id, metric, threshold, duration_secs, enabled, created_at
		 FROM alert_rules WHERE service_id = ? ORDER BY id`, serviceID)
	if err != nil {
		return nil, fmt.Errorf("querying alert rules: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()

	return collectRules(rows)
}

// Create inserts a rule and returns it with id and timestamp.
func (s *RuleStore) Create(ctx context.Context, create model.RuleCreate) (model.AlertRule, error) {
	now := time.Now()

	res, err := s.db.sql.ExecContext(ctx,
		`INSERT INTO alert_rules (service_id, metric, threshold, duration_secs, enabled, created_at)
		 VALUES (?, ?, ?, ?, 1, ?)`,
		create.ServiceID, create.Metric, create.Threshold, create.DurationSecs, formatTime(now),
	)
	if err != nil {
		return model.AlertRule{}, fmt.Errorf("inserting alert rule: %w", err)
	}

	id, err := res.LastInsertId()
	if err != nil {
		return model.AlertRule{}, fmt.Errorf("reading alert rule id: %w", err)
	}

	return model.AlertRule{
		ID:           id,
		ServiceID:    create.ServiceID,
		Metric:       create.Metric,
		Threshold:    create.Threshold,
		DurationSecs: create.DurationSecs,
		Enabled:      true,
		CreatedAt:    now,
	}, nil
}

// Delete removes a rule.
func (s *RuleStore) Delete(ctx context.Context, id int64) error {
	res, err := s.db.sql.ExecContext(ctx, "DELETE FROM alert_rules WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("deleting alert rule: %w", err)
	}

	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("counting deleted alert rules: %w", err)
	}

	if affected == 0 {
		return fmt.Errorf("%w: %d", ErrRuleNotFound, id)
	}

	return nil
}

// query runs a parameterless rule select.
func (s *RuleStore) query(ctx context.Context, query string) ([]model.AlertRule, error) {
	rows, err := s.db.sql.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("querying alert rules: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()

	return collectRules(rows)
}

// collectRules scans rule rows.
func collectRules(rows *sql.Rows) ([]model.AlertRule, error) {
	rules := []model.AlertRule{}

	for rows.Next() {
		var (
			rule      model.AlertRule
			createdAt string
		)

		if err := rows.Scan(
			&rule.ID, &rule.ServiceID, &rule.Metric, &rule.Threshold,
			&rule.DurationSecs, &rule.Enabled, &createdAt,
		); err != nil {
			return nil, fmt.Errorf("scanning alert rule: %w", err)
		}

		parsedAt, err := parseTime(createdAt, "created_at")
		if err != nil {
			return nil, err
		}

		rule.CreatedAt = parsedAt
		rules = append(rules, rule)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating alert rules: %w", err)
	}

	return rules, nil
}
