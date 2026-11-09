package store

import (
	"context"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

// testServiceAPI is the seeded blue-green service id.
const testServiceAPI = "tn-api"

// openTestDB returns a migrated in-memory database.
func openTestDB(t *testing.T) *DB {
	t.Helper()

	db, err := Open(":memory:")
	if err != nil {
		t.Fatalf("Open() error = %v, want nil", err)
	}

	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("Close() error = %v, want nil", err)
		}
	})

	if _, err := db.MigrateUp(context.Background()); err != nil {
		t.Fatalf("MigrateUp() error = %v, want nil", err)
	}

	return db
}

func TestOpen(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		path    string
		wantErr bool
	}{
		{
			name:    "memory database",
			path:    ":memory:",
			wantErr: false,
		},
		{
			name:    "file in missing directory",
			path:    "/nonexistent-dir-xyz/touchgrass.db",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			db, err := Open(tt.path)

			if tt.wantErr {
				if err == nil {
					t.Fatal("Open() error = nil, want error")
				}

				return
			}

			if err != nil {
				t.Fatalf("Open() error = %v, want nil", err)
			}

			if err := db.Close(); err != nil {
				t.Errorf("Close() error = %v, want nil", err)
			}
		})
	}
}

func TestOpenKeepsReadsAvailableWhileOneConnectionIsBusy(t *testing.T) {
	t.Parallel()

	db, err := Open(filepath.Join(t.TempDir(), "touchgrass.db"))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = db.Close() })

	held, err := db.sql.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = held.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()

	var value int
	if err := db.sql.QueryRowContext(ctx, "SELECT 1").Scan(&value); err != nil {
		t.Fatalf("read while maintenance connection is busy: %v", err)
	}

	if value != 1 {
		t.Fatalf("SELECT 1 = %d, want 1", value)
	}
}

func TestMigrateUp(t *testing.T) {
	t.Parallel()

	db, err := Open(":memory:")
	if err != nil {
		t.Fatalf("Open() error = %v, want nil", err)
	}

	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("Close() error = %v, want nil", err)
		}
	})

	ctx := context.Background()

	applied, err := db.MigrateUp(ctx)
	if err != nil {
		t.Fatalf("MigrateUp() error = %v, want nil", err)
	}

	want := []string{"0001", "0002", "0003", "0004", "0005", "0006", "0007", "0008", "0009", "0010", "0011", "0012", "0013", "0014", "0015", "0016", "0017", "0018", "0019", "0020", "0021"}

	if !slices.Equal(applied, want) {
		t.Fatalf("MigrateUp() applied = %v, want %v", applied, want)
	}

	again, err := db.MigrateUp(ctx)
	if err != nil {
		t.Fatalf("second MigrateUp() error = %v, want nil", err)
	}

	if len(again) != 0 {
		t.Errorf("second MigrateUp() applied = %v, want []", again)
	}
}

func TestMigrationStatus(t *testing.T) {
	t.Parallel()

	db := openTestDB(t)

	applied, pending, err := db.MigrationStatus(context.Background())
	if err != nil {
		t.Fatalf("MigrationStatus() error = %v, want nil", err)
	}

	if len(applied) != 21 || len(pending) != 0 {
		t.Errorf("MigrationStatus() = (%v, %v), want (21 applied, 0 pending)", applied, pending)
	}
}
