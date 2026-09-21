package store

import (
	"context"
	"fmt"
	"io/fs"
	"regexp"

	"github.com/oralecarlangelo/touchgrass/migrations"
)

const schemaTable = `CREATE TABLE IF NOT EXISTS schema_migrations (
  version TEXT PRIMARY KEY,
  applied_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
)`

var migrationName = regexp.MustCompile(`^(\d{4})_[a-z0-9_]+\.sql$`)

// MigrateUp applies pending migrations in version order and returns the
// versions it applied (empty when already current).
func (db *DB) MigrateUp(ctx context.Context) ([]string, error) {
	pending, err := db.pending(ctx)
	if err != nil {
		return nil, err
	}

	applied := []string{}

	for _, file := range pending {
		if err := db.apply(ctx, file); err != nil {
			return applied, err
		}

		applied = append(applied, file.version)
	}

	return applied, nil
}

// MigrationStatus reports applied and pending migration versions.
func (db *DB) MigrationStatus(ctx context.Context) (applied, pending []string, err error) {
	appliedSet, err := db.appliedVersions(ctx)
	if err != nil {
		return nil, nil, err
	}

	files, err := migrationList()
	if err != nil {
		return nil, nil, err
	}

	applied = []string{}
	pending = []string{}

	for _, file := range files {
		if appliedSet[file.version] {
			applied = append(applied, file.version)
		} else {
			pending = append(pending, file.version)
		}
	}

	return applied, pending, nil
}

// migration is one versioned migration file.
type migration struct {
	version string
	name    string
}

// migrationList returns embedded migrations sorted by version.
func migrationList() ([]migration, error) {
	entries, err := fs.ReadDir(migrations.FS, ".")
	if err != nil {
		return nil, fmt.Errorf("reading migrations: %w", err)
	}

	files := []migration{}

	for _, entry := range entries {
		match := migrationName.FindStringSubmatch(entry.Name())
		if match == nil {
			return nil, fmt.Errorf("invalid migration name %q: want NNNN_description.sql", entry.Name())
		}

		files = append(files, migration{version: match[1], name: entry.Name()})
	}

	return files, nil
}

// pending returns migrations not yet recorded in schema_migrations.
func (db *DB) pending(ctx context.Context) ([]migration, error) {
	applied, err := db.appliedVersions(ctx)
	if err != nil {
		return nil, err
	}

	files, err := migrationList()
	if err != nil {
		return nil, err
	}

	pending := []migration{}

	for _, file := range files {
		if !applied[file.version] {
			pending = append(pending, file)
		}
	}

	return pending, nil
}

// appliedVersions returns the recorded migration versions.
func (db *DB) appliedVersions(ctx context.Context) (map[string]bool, error) {
	if _, err := db.sql.ExecContext(ctx, schemaTable); err != nil {
		return nil, fmt.Errorf("ensuring schema_migrations: %w", err)
	}

	rows, err := db.sql.QueryContext(ctx, "SELECT version FROM schema_migrations ORDER BY version")
	if err != nil {
		return nil, fmt.Errorf("querying schema_migrations: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()

	applied := map[string]bool{}

	for rows.Next() {
		var version string

		if err := rows.Scan(&version); err != nil {
			return nil, fmt.Errorf("scanning schema_migrations: %w", err)
		}

		applied[version] = true
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating schema_migrations: %w", err)
	}

	return applied, nil
}

// apply runs one migration and records it atomically.
func (db *DB) apply(ctx context.Context, file migration) error {
	body, err := fs.ReadFile(migrations.FS, file.name)
	if err != nil {
		return fmt.Errorf("reading migration %s: %w", file.version, err)
	}

	tx, err := db.sql.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("beginning migration %s: %w", file.version, err)
	}

	// Rollback is best-effort: after a successful commit it reports
	// sql.ErrTxDone, which must not mask the migration result.
	defer func() {
		_ = tx.Rollback()
	}()

	if _, err := tx.ExecContext(ctx, string(body)); err != nil {
		return fmt.Errorf("applying migration %s: %w", file.version, err)
	}

	if _, err := tx.ExecContext(ctx, "INSERT INTO schema_migrations (version) VALUES (?)", file.version); err != nil {
		return fmt.Errorf("recording migration %s: %w", file.version, err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing migration %s: %w", file.version, err)
	}

	return nil
}
