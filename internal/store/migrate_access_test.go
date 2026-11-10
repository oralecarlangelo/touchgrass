package store

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/oralecarlangelo/touchgrass/internal/model"
	"github.com/oralecarlangelo/touchgrass/internal/testfixture"
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

// A v0.4.0 database stops at 0021: users has no must_change_password
// and no scope. Migrating it must keep every account as it was.
func TestMigrateUserAccessFromV040(t *testing.T) {
	t.Parallel()

	db := openMigratedThrough(t, "0021")
	ctx := context.Background()

	execAll(t, db,
		`INSERT INTO users (username, password_hash, role, created_at)
		 VALUES ('admin', 'hash-admin', 'admin', '2026-01-02T03:04:05.000Z')`,
		`INSERT INTO users (username, password_hash, role, disabled_at, created_at)
		 VALUES ('reader', 'hash-reader', 'viewer', '2026-02-03T04:05:06.000Z', '2026-01-03T03:04:05.000Z')`,
		`INSERT INTO users (username, password_hash, role, created_at)
		 VALUES ('leaver', 'hash-leaver', 'viewer', '2026-01-04T03:04:05.000Z')`,
		`DELETE FROM users WHERE username = 'leaver'`,
	)

	applied, err := db.MigrateUp(ctx)
	if err != nil {
		t.Fatalf("MigrateUp() error = %v, want nil", err)
	}

	if len(applied) != 2 || applied[0] != "0022" || applied[1] != "0023" {
		t.Fatalf("MigrateUp() applied = %v, want [0022 0023]", applied)
	}

	users := NewUserStore(db)

	listed, err := users.List(ctx, 10)
	if err != nil {
		t.Fatalf("List() error = %v, want nil", err)
	}

	if len(listed) != 2 {
		t.Fatalf("List() = %+v, want the two surviving accounts", listed)
	}

	admin, reader := listed[0], listed[1]

	if admin.ID != 1 || admin.Username != "admin" || admin.Role != model.RoleAdmin ||
		admin.PasswordHash != "hash-admin" || admin.Disabled() {
		t.Errorf("admin = %+v, want the same enabled admin", admin)
	}

	if reader.ID != 2 || reader.Role != model.RoleViewer || !reader.Disabled() ||
		reader.PasswordHash != "hash-reader" {
		t.Errorf("reader = %+v, want the same disabled viewer", reader)
	}

	for _, user := range listed {
		if !user.AllServices || len(user.Services) != 0 || user.MustChangePassword {
			t.Errorf("%s = %+v, want all services, no grants, no forced change", user.Username, user)
		}
	}

	if got := admin.CreatedAt.UTC().Format("2006-01-02T15:04:05"); got != "2026-01-02T03:04:05" {
		t.Errorf("admin created_at = %s, want it carried over", got)
	}

	// Id 3 belonged to the deleted account and must not come back.
	created, err := users.Create(ctx, model.User{
		Username: "newcomer", PasswordHash: "hash", Role: model.RoleViewer, AllServices: true,
	}, testGranter)
	if err != nil {
		t.Fatalf("Create() after migrating error = %v, want nil", err)
	}

	if created.ID != 4 {
		t.Errorf("first id after migrating = %d, want 4 (a deleted user's id is never reused)", created.ID)
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
		 VALUES ('admin', 'hash-admin', 'admin', 1)`,
		`INSERT INTO users (username, password_hash, role, must_change_password)
		 VALUES ('reader', 'hash-reader', 'viewer', 0)`,
	)

	if _, err := db.MigrateUp(ctx); err != nil {
		t.Fatalf("MigrateUp() error = %v, want nil", err)
	}

	users := NewUserStore(db)

	admin, err := users.ByUsername(ctx, "admin")
	if err != nil {
		t.Fatalf("ByUsername(admin) error = %v, want nil", err)
	}

	if !admin.MustChangePassword || !admin.AllServices {
		t.Errorf("admin = %+v, want the pending one-time password and all services", admin)
	}

	reader, err := users.ByUsername(ctx, "reader")
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

	db := openTestDB(t)
	seedGenericServices(t, db)

	ctx := context.Background()

	if _, err := db.sql.ExecContext(ctx,
		`INSERT INTO user_services (user_id, service_id, granted_by) VALUES (999, ?, 'root')`,
		testfixture.GenericGranted,
	); err == nil {
		t.Error("grant for an unknown user error = nil, want a foreign key failure")
	}

	created, err := NewUserStore(db).Create(ctx, model.User{
		Username: "dev", PasswordHash: "hash", Role: model.RoleViewer,
		Services: []string{testfixture.GenericGranted},
	}, testGranter)
	if err != nil {
		t.Fatalf("Create() error = %v, want nil", err)
	}

	// A service that still has a grant cannot be removed behind the
	// store's back; ServiceStore.Delete clears the grant first.
	if _, err := db.sql.ExecContext(ctx, `DELETE FROM services WHERE id = ?`, testfixture.GenericGranted); err == nil {
		t.Error("raw delete of a granted service error = nil, want a foreign key failure")
	}

	if got := countGrants(t, db, "user_id", created.ID); got != 1 {
		t.Errorf("grants = %d, want 1", got)
	}
}
