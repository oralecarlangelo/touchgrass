package store

import (
	"context"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/oralecarlangelo/touchgrass/internal/testfixture"
)

// testServiceAPI is the seeded blue-green service id.
const testServiceAPI = "tn-api"

// seedDogfoodServices inserts the shared service fixture. Migrations
// no longer seed any service, so suites that need an inventory add it.
func seedDogfoodServices(t *testing.T, db *DB) {
	t.Helper()

	services := NewServiceStore(db)
	rules := NewRuleStore(db)

	for _, service := range testfixture.DogfoodServices() {
		if err := services.Create(context.Background(), service); err != nil {
			t.Fatalf("Create(%s) error = %v, want nil", service.ID, err)
		}

		if _, err := rules.Create(context.Background(), testfixture.DefaultRule(service.ID)); err != nil {
			t.Fatalf("Create(rule for %s) error = %v, want nil", service.ID, err)
		}
	}
}

// openTestDB returns a migrated in-memory database holding the shared
// service fixture.
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

	seedDogfoodServices(t, db)

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

	want := []string{"0001", "0002", "0003", "0004", "0005", "0006", "0007", "0008", "0009", "0010", "0011", "0012", "0013", "0014", "0015", "0016", "0017", "0018", "0019", "0020", "0021", "0022", "0023"}

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

	if len(applied) != 23 || len(pending) != 0 {
		t.Errorf("MigrationStatus() = (%v, %v), want (23 applied, 0 pending)", applied, pending)
	}
}

// A fresh install opens with nothing in its inventory: no migration may
// seed a service (or anything hanging off one) from another deployment.
func TestMigrateUpSeedsNoServices(t *testing.T) {
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

	if _, err := db.MigrateUp(ctx); err != nil {
		t.Fatalf("MigrateUp() error = %v, want nil", err)
	}

	services, err := NewServiceStore(db).All(ctx)
	if err != nil {
		t.Fatalf("All() error = %v, want nil", err)
	}

	if len(services) != 0 {
		t.Errorf("services after migrate = %+v, want none", services)
	}

	rules, err := NewRuleStore(db).AllEnabled(ctx)
	if err != nil {
		t.Fatalf("AllEnabled() error = %v, want nil", err)
	}

	if len(rules) != 0 {
		t.Errorf("alert rules after migrate = %+v, want none", rules)
	}
}

// A fresh install opens with nothing in its inventory: no migration may
// seed a service (or anything hanging off one) from another deployment.
func TestMigrateUpSeedsNoServices(t *testing.T) {
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

	if _, err := db.MigrateUp(ctx); err != nil {
		t.Fatalf("MigrateUp() error = %v, want nil", err)
	}

	services, err := NewServiceStore(db).All(ctx)
	if err != nil {
		t.Fatalf("All() error = %v, want nil", err)
	}

	if len(services) != 0 {
		t.Errorf("services after migrate = %+v, want none", services)
	}

	rules, err := NewRuleStore(db).AllEnabled(ctx)
	if err != nil {
		t.Fatalf("AllEnabled() error = %v, want nil", err)
	}

	if len(rules) != 0 {
		t.Errorf("alert rules after migrate = %+v, want none", rules)
	}
}
