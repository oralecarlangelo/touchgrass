package store

import (
	"context"
	"slices"
	"testing"
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

	want := []string{"0001", "0002", "0003", "0004", "0005", "0006", "0007", "0008", "0009", "0010"}

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

	if len(applied) != 10 || len(pending) != 0 {
		t.Errorf("MigrationStatus() = (%v, %v), want (10 applied, 0 pending)", applied, pending)
	}
}
