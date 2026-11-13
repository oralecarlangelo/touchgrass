package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/oralecarlangelo/touchgrass/internal/config"
	"github.com/oralecarlangelo/touchgrass/internal/dbmigrate"
	"github.com/oralecarlangelo/touchgrass/internal/docker"
	"github.com/oralecarlangelo/touchgrass/internal/secret"
	"github.com/oralecarlangelo/touchgrass/internal/service"
	"github.com/oralecarlangelo/touchgrass/internal/store"
)

// migrationsWiring bundles what the schema-migrations pillar needs.
type migrationsWiring struct {
	db       *store.DB
	cfg      config.Config
	services *store.ServiceStore
	audit    *service.Audit
	docker   *docker.Client
	logger   *slog.Logger
}

// migrationTools is the set of migration tools this build ships. A new
// tool is one more entry here; nothing else in the wiring changes.
func migrationTools() *dbmigrate.Registry {
	return dbmigrate.NewRegistry(dbmigrate.NewPrisma(), dbmigrate.NewMigrateMongo())
}

// newMigrationsService builds the schema-migrations pillar and fails
// the runs a previous process left unfinished. It shares the env key:
// without one, no database URL can be stored and the pillar reports
// every service as not configured.
func newMigrationsService(ctx context.Context, wiring migrationsWiring) (*service.Migrations, error) {
	key, err := secret.Load(wiring.cfg.EnvKeyFile, wiring.cfg.EnvKey)
	if err != nil && !errors.Is(err, secret.ErrKeyMissing) {
		return nil, fmt.Errorf("loading env key: %w", err)
	}

	migrations := service.NewMigrations(service.MigrationsConfig{
		Services: wiring.services,
		Configs:  store.NewMigrationConfigStore(wiring.db),
		Runs:     store.NewMigrationRunStore(wiring.db),
		Audit:    wiring.audit,
		Key:      key,
		Tools:    migrationTools(),
		Docker:   wiring.docker,
		Logger:   wiring.logger,
	})

	if err := migrations.Recover(ctx); err != nil {
		return nil, fmt.Errorf("recovering migration runs: %w", err)
	}

	return migrations, nil
}
