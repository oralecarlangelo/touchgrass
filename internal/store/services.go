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
