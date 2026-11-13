package store

import (
	"context"
	"path/filepath"
	"slices"
	"testing"

	"github.com/oralecarlangelo/touchgrass/internal/model"
	"github.com/oralecarlangelo/touchgrass/internal/testfixture"
)

// Accounts an older release left behind.
const (
	testOwnerName  = "owner"
	testReaderName = "reader"
)

// openMigratedThrough opens a file database with migrations applied up
// to and including version last, as an older release left it.
func openMigratedThrough(t *testing.T, last string) *DB {
	t.Helper()

	db, err := Open(filepath.Join(t.TempDir(), "touchgrass.db"))
	if err != nil {
		t.Fatalf("Open() error = %v, want nil", err)
	}

	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("Close() error = %v, want nil", err)
		}
	})

	pending, err := db.pending(context.Background())
	if err != nil {
		t.Fatalf("pending() error = %v, want nil", err)
	}

	for _, file := range pending {
		if file.version > last {
			break
		}

		if err := db.apply(context.Background(), file); err != nil {
			t.Fatalf("apply(%s) error = %v, want nil", file.version, err)
		}
	}

	return db
}

// execAll runs statements that must succeed.
func execAll(t *testing.T, db *DB, statements ...string) {
	t.Helper()

	for _, statement := range statements {
		if _, err := db.sql.ExecContext(context.Background(), statement); err != nil {
			t.Fatalf("exec %q: %v", statement, err)
		}
	}
}

// migrateFromV040 builds a v0.4.0 database (migrations through 0021:
// users has neither must_change_password nor a scope) holding an
// admin, a disabled viewer and one deleted account, then migrates it.
func migrateFromV040(t *testing.T) *UserStore {
	t.Helper()

	db := openMigratedThrough(t, "0021")

	execAll(t, db,
		`INSERT INTO users (username, password_hash, role, created_at)
		 VALUES ('owner', 'hash-owner', 'admin', '2026-01-02T03:04:05.000Z')`,
		`INSERT INTO users (username, password_hash, role, disabled_at, created_at)
		 VALUES ('reader', 'hash-reader', 'viewer', '2026-02-03T04:05:06.000Z', '2026-01-03T03:04:05.000Z')`,
		`INSERT INTO users (username, password_hash, role, created_at)
		 VALUES ('leaver', 'hash-leaver', 'viewer', '2026-01-04T03:04:05.000Z')`,
		`DELETE FROM users WHERE username = 'leaver'`,
	)

	applied, err := db.MigrateUp(context.Background())
	if err != nil {
		t.Fatalf("MigrateUp() error = %v, want nil", err)
	}

	if !slices.Equal(applied, []string{"0022", "0023", "0024", "0025"}) {
		t.Fatalf("MigrateUp() applied = %v, want [0022 0023 0024 0025]", applied)
	}

	return NewUserStore(db)
}

func TestMigrateUserAccessKeepsEveryAccount(t *testing.T) {
	t.Parallel()

	users := migrateFromV040(t)
	ctx := context.Background()

	owner, err := users.ByUsername(ctx, testOwnerName)
	if err != nil {
		t.Fatalf("ByUsername(owner) error = %v, want nil", err)
	}

	if owner.ID != 1 || owner.Role != model.RoleAdmin || owner.PasswordHash != "hash-owner" || owner.Disabled() {
		t.Errorf("owner = %+v, want the same enabled admin with id 1", owner)
	}

	if got := owner.CreatedAt.UTC().Format("2006-01-02T15:04:05"); got != "2026-01-02T03:04:05" {
		t.Errorf("owner created_at = %s, want it carried over", got)
	}

	reader, err := users.ByUsername(ctx, testReaderName)
	if err != nil {
		t.Fatalf("ByUsername(reader) error = %v, want nil", err)
	}

	if reader.ID != 2 || reader.Role != model.RoleViewer || reader.PasswordHash != "hash-reader" || !reader.Disabled() {
		t.Errorf("reader = %+v, want the same disabled viewer with id 2", reader)
	}
}

func TestMigrateUserAccessGivesEveryoneAllServices(t *testing.T) {
	t.Parallel()

	users := migrateFromV040(t)

	listed, err := users.List(context.Background(), 10)
	if err != nil {
		t.Fatalf("List() error = %v, want nil", err)
	}

	if len(listed) != 2 {
		t.Fatalf("List() = %+v, want the two surviving accounts", listed)
	}

	for _, user := range listed {
		if !user.AllServices || len(user.Services) != 0 || user.MustChangePassword {
			t.Errorf("%s = %+v, want all services, no grants, no forced change", user.Username, user)
		}
	}
}

func TestMigrateUserAccessNeverReusesADeletedID(t *testing.T) {
	t.Parallel()

	users := migrateFromV040(t)

	// Id 3 belonged to the deleted account and must not come back.
	created := createUser(t, users, model.User{Username: "newcomer", Role: model.RoleViewer, AllServices: true})
	if created.ID != 4 {
		t.Errorf("first id after migrating = %d, want 4", created.ID)
	}
}

// A database already on 0022 carries a pending one-time password
// through the rebuild.
func TestMigrateUserAccessKeepsMustChangePassword(t *testing.T) {
	t.Parallel()

	db := openMigratedThrough(t, "0022")
	ctx := context.Background()

	execAll(t, db,
		`INSERT INTO users (username, password_hash, role, must_change_password)
		 VALUES ('owner', 'hash-owner', 'admin', 1)`,
		`INSERT INTO users (username, password_hash, role, must_change_password)
		 VALUES ('reader', 'hash-reader', 'viewer', 0)`,
	)

	if _, err := db.MigrateUp(ctx); err != nil {
		t.Fatalf("MigrateUp() error = %v, want nil", err)
	}

	users := NewUserStore(db)

	owner, err := users.ByUsername(ctx, testOwnerName)
	if err != nil {
		t.Fatalf("ByUsername(owner) error = %v, want nil", err)
	}

	if !owner.MustChangePassword || !owner.AllServices {
		t.Errorf("owner = %+v, want the pending one-time password and all services", owner)
	}

	reader, err := users.ByUsername(ctx, testReaderName)
	if err != nil {
		t.Fatalf("ByUsername(reader) error = %v, want nil", err)
	}

	if reader.MustChangePassword || !reader.AllServices {
		t.Errorf("reader = %+v, want no forced change and all services", reader)
	}
}

// The grants table enforces both of its foreign keys once it exists.
func TestMigrateUserAccessEnforcesForeignKeys(t *testing.T) {
	t.Parallel()

	db, users := openScopeDB(t)
	ctx := context.Background()

	if _, err := db.sql.ExecContext(ctx,
		`INSERT INTO user_services (user_id, service_id, granted_by) VALUES (999, ?, 'root')`,
		testfixture.GenericGranted,
	); err == nil {
		t.Error("grant for an unknown user error = nil, want a foreign key failure")
	}

	created := createUser(t, users, model.User{
		Username: testScopedName, Role: model.RoleViewer, Services: []string{testfixture.GenericGranted},
	})

	// A service that still has a grant cannot be removed behind the
	// store's back; ServiceStore.Delete clears the grant first.
	if _, err := db.sql.ExecContext(ctx, `DELETE FROM services WHERE id = ?`, testfixture.GenericGranted); err == nil {
		t.Error("raw delete of a granted service error = nil, want a foreign key failure")
	}

	if got := countGrants(t, db, false, created.ID); got != 1 {
		t.Errorf("grants = %d, want 1", got)
	}
}
