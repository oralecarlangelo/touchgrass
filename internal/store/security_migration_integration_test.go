//go:build integration

package store

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/oralecarlangelo/touchgrass/internal/model"
)

func TestContainerSecurityMigrationRehearsalOnBackupCopy(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	directory := t.TempDir()
	preMigrationPath := filepath.Join(directory, "pre-0019.db")
	untouchedBackupPath := filepath.Join(directory, "pre-0019.backup.db")
	migrationCopyPath := filepath.Join(directory, "migration-copy.db")

	preMigration, err := Open(preMigrationPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := preMigration.sql.ExecContext(ctx, schemaTable); err != nil {
		t.Fatal(err)
	}
	files, err := migrationList()
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		if file.version == "0019" {
			break
		}
		if err := preMigration.apply(ctx, file); err != nil {
			t.Fatalf("apply(%s): %v", file.version, err)
		}
	}
	seedGenericMigrationEvidence(t, preMigration, ctx)
	if err := preMigration.Backup(ctx, untouchedBackupPath); err != nil {
		t.Fatal(err)
	}
	if err := preMigration.Close(); err != nil {
		t.Fatal(err)
	}

	backupBytes, err := os.ReadFile(untouchedBackupPath) //nolint:gosec // path is constructed inside t.TempDir.
	if err != nil {
		t.Fatal(err)
	}
	rollbackChecksum := sha256.Sum256(backupBytes)
	if err := os.WriteFile(migrationCopyPath, backupBytes, 0o600); err != nil { //nolint:gosec // path is constructed inside t.TempDir.
		t.Fatal(err)
	}

	migrated, err := Open(migrationCopyPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = migrated.Close() })
	applied, err := migrated.MigrateUp(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(applied) != "[0019 0020]" {
		t.Fatalf("applied migrations = %v, want [0019 0020]", applied)
	}
	assertMigrationEvidence(t, migrated, ctx)
	activation, err := NewSecurityStore(migrated).Activation(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if activation.ActivatedAt != nil {
		t.Fatalf("shadow-mode activation = %+v, want disabled after migration", activation)
	}

	untouchedAfter, err := os.ReadFile(untouchedBackupPath) //nolint:gosec // path is constructed inside t.TempDir.
	if err != nil {
		t.Fatal(err)
	}
	if sha256.Sum256(untouchedAfter) != rollbackChecksum {
		t.Fatal("rollback backup changed while migrating its copy")
	}
	rollback, err := Open(untouchedBackupPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rollback.Close() }()
	_, pending, err := rollback.MigrationStatus(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(pending) != "[0019 0020]" {
		t.Fatalf("rollback backup pending = %v, want untouched pre-0019 state", pending)
	}
	t.Log("migration rehearsal PASS: online backup untouched; copy applied 0019/0020 in shadow mode; fleet, performance, and deploy evidence preserved")
}

func seedGenericMigrationEvidence(t *testing.T, db *DB, ctx context.Context) {
	t.Helper()
	now := formatTime(time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC))
	if _, err := db.sql.ExecContext(ctx, `INSERT INTO services
		(id, name, strategy, compose_project, compose_dir, config)
		VALUES ('migration-fixture', 'Generic migration fixture', 'recreate', 'generic', '/srv/generic', '{}')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.sql.ExecContext(ctx, `INSERT INTO container_samples
		(sampled_at, container_name, project, managed, service_id, state, cpu_percent, mem_bytes, mem_limit, restarts)
		VALUES (?, 'generic-web-1', 'generic', 1, 'migration-fixture', 'running', 3, 4096, 8192, 0)`, now); err != nil {
		t.Fatal(err)
	}
	if _, err := db.sql.ExecContext(ctx, `INSERT INTO deploys
		(service_id, sha, actor, type, outcome, started_at, finished_at, duration_secs, downtime_secs, notes)
		VALUES ('migration-fixture', 'fixture-sha', 'migration-test', 'deploy', 'success', ?, ?, 1, 0, 'generic fixture')`, now, now); err != nil {
		t.Fatal(err)
	}
	if _, err := db.sql.ExecContext(ctx, `INSERT INTO http_metric_buckets
		(bucket_start, service_id, environment, method, route_family, upstream, status_class, request_count, updated_at)
		VALUES (?, 'migration-fixture', 'test', 'GET', 'health', 'generic', 2, 1, ?)`, now, now); err != nil {
		t.Fatal(err)
	}
}

func assertMigrationEvidence(t *testing.T, db *DB, ctx context.Context) {
	t.Helper()
	checks := []struct {
		query string
		want  int
	}{
		{query: "SELECT COUNT(*) FROM container_samples WHERE service_id = 'migration-fixture' AND container_id IS NULL", want: 1},
		{query: "SELECT COUNT(*) FROM deploys WHERE service_id = 'migration-fixture' AND sha = 'fixture-sha'", want: 1},
		{query: "SELECT COUNT(*) FROM http_metric_buckets WHERE service_id = 'migration-fixture' AND request_count = 1", want: 1},
	}
	for _, check := range checks {
		var count int
		if err := db.sql.QueryRowContext(ctx, check.query).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != check.want {
			t.Fatalf("%s = %d, want %d", check.query, count, check.want)
		}
	}
	summary, err := NewContainerStore(db).Summary(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if summary != (model.InventorySummary{}) {
		t.Fatalf("new inventory shadow state = %+v, want empty before first reconciliation", summary)
	}
}
