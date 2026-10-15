package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/oralecarlangelo/touchgrass/internal/model"
)

func TestCursorStoreRoundTrip(t *testing.T) {
	t.Parallel()

	db := openTestDB(t)
	store := NewCursorStore(db)
	ctx := context.Background()
	now := time.Date(2026, 10, 16, 1, 2, 3, 0, time.UTC)

	first := model.CollectorCursor{
		Source: "nginx", Identity: "inode-1", Offset: 24,
		LastRecordedAt: now.Add(-time.Minute), LastSuccessAt: now,
		LastError: "parse", DroppedRecords: 2,
	}
	if err := store.Upsert(ctx, first); err != nil {
		t.Fatalf("Upsert(first) error = %v", err)
	}

	first.Offset = 48
	first.LastError = ""
	first.DroppedRecords = 3
	if err := store.Upsert(ctx, first); err != nil {
		t.Fatalf("Upsert(replacement) error = %v", err)
	}

	got, err := store.Get(ctx, "nginx")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got.Source != first.Source || got.Identity != first.Identity || got.Offset != 48 || got.LastError != "" || got.DroppedRecords != 3 {
		t.Fatalf("Get() = %+v, want replacement %+v", got, first)
	}

	var count int
	if err := db.sql.QueryRowContext(ctx, "SELECT COUNT(*) FROM collector_cursors WHERE source = ?", first.Source).Scan(&count); err != nil {
		t.Fatalf("counting cursor rows: %v", err)
	}
	if count != 1 {
		t.Fatalf("cursor rows = %d, want 1", count)
	}
}

func TestMaintenanceDeletesInBatches(t *testing.T) {
	t.Parallel()

	db := openTestDB(t)
	ctx := context.Background()
	stale := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 7; i++ {
		if _, err := db.sql.ExecContext(ctx, `INSERT INTO log_lines(service_id, container, stream, line, ts, created_at)
			VALUES (?, 'api', 'stdout', 'stale', ?, ?)`, testServiceAPI, stale.Format(time.RFC3339Nano), stale.Format(time.RFC3339Nano)); err != nil {
			t.Fatalf("seeding log %d: %v", i, err)
		}
	}

	result, err := NewMaintenanceStore(db).Run(ctx, model.MaintenancePolicy{
		Now: stale.Add(48 * time.Hour), RetentionLogs: time.Hour,
		BatchSize: 3, MaxBytes: 1 << 30,
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.DeletedRows != 3 {
		t.Fatalf("DeletedRows = %d, want 3", result.DeletedRows)
	}

	var remaining int
	if err := db.sql.QueryRowContext(ctx, "SELECT COUNT(*) FROM log_lines").Scan(&remaining); err != nil {
		t.Fatalf("counting remaining logs: %v", err)
	}
	if remaining != 4 {
		t.Fatalf("remaining logs = %d, want 4", remaining)
	}
}

func TestMaintenanceCheckpointsWAL(t *testing.T) {
	t.Parallel()

	db := openFileTestDB(t)
	result, err := NewMaintenanceStore(db).Run(context.Background(), model.MaintenancePolicy{
		Now: time.Now().UTC(), BatchSize: 10, MaxBytes: 1 << 30,
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !result.WALCheckpointAttempted {
		t.Fatal("WALCheckpointAttempted = false, want true")
	}
	if result.WALBusy < 0 || result.WALLogFrames < 0 || result.WALCheckpointedFrames < 0 {
		t.Fatalf("invalid WAL checkpoint result: %+v", result)
	}
}

func TestMaintenanceReportsOverBudget(t *testing.T) {
	t.Parallel()

	db := openFileTestDB(t)
	result, err := NewMaintenanceStore(db).Run(context.Background(), model.MaintenancePolicy{
		Now: time.Now().UTC(), BatchSize: 2, MaxBytes: 1,
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !result.OverBudget || result.DatabaseBytes <= result.MaxBytes {
		t.Fatalf("Run() = %+v, want over-budget report", result)
	}
	if result.DeletedRows > 2 {
		t.Fatalf("DeletedRows = %d, want at most one bounded batch", result.DeletedRows)
	}
}

func TestMaintenanceEnforcesClassCeilingInBatch(t *testing.T) {
	t.Parallel()

	db := openTestDB(t)
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		if _, err := db.sql.ExecContext(ctx, `INSERT INTO log_lines(service_id, container, stream, line, ts)
			VALUES (?, 'api', 'stdout', 'oversized log row', ?)`, testServiceAPI, formatFixedTime(time.Now().Add(time.Duration(i)*time.Second))); err != nil {
			t.Fatalf("seeding log %d: %v", i, err)
		}
	}

	result, err := NewMaintenanceStore(db).Run(ctx, model.MaintenancePolicy{
		Now: time.Now().UTC(), BatchSize: 2, MaxBytes: 1 << 30, LogMaxBytes: 1,
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.DeletedRows != 2 {
		t.Fatalf("DeletedRows = %d, want bounded class cleanup of 2", result.DeletedRows)
	}
	if len(result.OverBudgetClasses) != 1 || result.OverBudgetClasses[0] != "logs" {
		t.Fatalf("OverBudgetClasses = %v, want logs until later bounded passes finish", result.OverBudgetClasses)
	}
}

func TestSQLiteBackupIsConsistent(t *testing.T) {
	t.Parallel()

	db := openFileTestDB(t)
	destination := filepath.Join(t.TempDir(), "snapshot.db")
	if err := db.Backup(context.Background(), destination); err != nil {
		t.Fatalf("Backup() error = %v", err)
	}

	backup, err := sql.Open("sqlite", "file:"+destination+"?mode=ro")
	if err != nil {
		t.Fatalf("opening backup: %v", err)
	}
	defer func() { _ = backup.Close() }()

	var integrity string
	if err := backup.QueryRow("PRAGMA integrity_check").Scan(&integrity); err != nil {
		t.Fatalf("integrity_check error = %v", err)
	}
	if integrity != "ok" {
		t.Fatalf("integrity_check = %q, want ok", integrity)
	}

	var migrations int
	if err := backup.QueryRow("SELECT COUNT(*) FROM schema_migrations").Scan(&migrations); err != nil {
		t.Fatalf("counting backup migrations: %v", err)
	}
	if migrations == 0 {
		t.Fatal("backup has no schema migrations")
	}
}

func openFileTestDB(t *testing.T) *DB {
	t.Helper()

	db, err := Open(filepath.Join(t.TempDir(), "touchgrass.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.MigrateUp(context.Background()); err != nil {
		t.Fatalf("MigrateUp() error = %v", err)
	}

	return db
}
