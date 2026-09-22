package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/oralecarlangelo/touchgrass/internal/model"
)

// ErrServiceNotFound reports an unknown service id.
var ErrServiceNotFound = errors.New("store: service not found")

// ServiceStore persists managed service definitions.
type ServiceStore struct {
	db *DB
}

// NewServiceStore builds a ServiceStore on db.
func NewServiceStore(db *DB) *ServiceStore {
	return &ServiceStore{db: db}
}

// All returns every service ordered by id.
func (s *ServiceStore) All(ctx context.Context) ([]model.Service, error) {
	rows, err := s.db.sql.QueryContext(
		ctx,
		"SELECT id, name, strategy, compose_project, compose_dir, config FROM services ORDER BY id",
	)
	if err != nil {
		return nil, fmt.Errorf("querying services: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()

	services := []model.Service{}

	for rows.Next() {
		service, err := scanService(rows)
		if err != nil {
			return nil, err
		}

		services = append(services, service)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating services: %w", err)
	}

	return services, nil
}

// Get returns the service with id.
func (s *ServiceStore) Get(ctx context.Context, id string) (model.Service, error) {
	row := s.db.sql.QueryRowContext(
		ctx,
		"SELECT id, name, strategy, compose_project, compose_dir, config FROM services WHERE id = ?",
		id,
	)

	service, err := scanService(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return model.Service{}, fmt.Errorf("%w: %q", ErrServiceNotFound, id)
		}

		return model.Service{}, err
	}

	return service, nil
}

// Create inserts a new service definition. Duplicate ids fail.
func (s *ServiceStore) Create(ctx context.Context, service model.Service) error {
	if _, err := s.db.sql.ExecContext(
		ctx,
		"INSERT INTO services (id, name, strategy, compose_project, compose_dir, config) VALUES (?, ?, ?, ?, ?, ?)",
		service.ID,
		service.Name,
		string(service.Strategy),
		service.ComposeProject,
		service.ComposeDir,
		string(service.Config),
	); err != nil {
		return fmt.Errorf("creating service: %w", err)
	}

	return nil
}

// UpdateConfig replaces the strategy config for id.
func (s *ServiceStore) UpdateConfig(ctx context.Context, id string, config json.RawMessage) error {
	res, err := s.db.sql.ExecContext(ctx, "UPDATE services SET config = ? WHERE id = ?", string(config), id)
	if err != nil {
		return fmt.Errorf("updating service config: %w", err)
	}

	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("counting updated services: %w", err)
	}

	if affected == 0 {
		return fmt.Errorf("%w: %q", ErrServiceNotFound, id)
	}

	return nil
}

// serviceChildren lists every table with an enforced foreign key to
// services(id). Delete clears them before the parent row.
var serviceChildren = []string{
	"alert_rules",
	"api_keys",
	"audit",
	"deploy_probes",
	"deploys",
	"issue_rules",
	"issues",
	"log_lines",
	"metrics",
	"notifications",
	"occurrences",
	"sdk_logs",
}

// Delete removes the service definition and all of its touchgrass-side
// rows (rules, keys, deploys, metrics, logs, issues, audit history) in
// one transaction. Running containers are untouched. Unknown ids fail
// with ErrServiceNotFound.
func (s *ServiceStore) Delete(ctx context.Context, id string) error {
	tx, err := s.db.sql.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("starting service delete transaction: %w", err)
	}

	defer func() {
		_ = tx.Rollback()
	}()

	for _, table := range serviceChildren {
		//nolint:gosec // table names come from a fixed internal allowlist; id stays parameterized.
		if _, err := tx.ExecContext(ctx, "DELETE FROM "+table+" WHERE service_id = ?", id); err != nil {
			return fmt.Errorf("deleting service %s: %w", table, err)
		}
	}

	res, err := tx.ExecContext(ctx, "DELETE FROM services WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("deleting service: %w", err)
	}

	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("counting deleted services: %w", err)
	}

	if affected == 0 {
		return fmt.Errorf("%w: %q", ErrServiceNotFound, id)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing service delete: %w", err)
	}

	return nil
}

// scanner covers *sql.Row and *sql.Rows for scanService.
type scanner interface {
	Scan(dest ...any) error
}

// scanService maps one service row.
func scanService(row scanner) (model.Service, error) {
	var (
		service   model.Service
		configRaw string
	)

	if err := row.Scan(
		&service.ID,
		&service.Name,
		&service.Strategy,
		&service.ComposeProject,
		&service.ComposeDir,
		&configRaw,
	); err != nil {
		return model.Service{}, fmt.Errorf("scanning service: %w", err)
	}

	service.Config = json.RawMessage(configRaw)

	return service, nil
}
