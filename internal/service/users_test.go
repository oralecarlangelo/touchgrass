package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/oralecarlangelo/touchgrass/internal/model"
	"github.com/oralecarlangelo/touchgrass/internal/store"
)

// openUsers wires a Users service on a migrated memory database.
func openUsers(t *testing.T) (*Users, *store.DB) {
	t.Helper()

	db := openInventoryDB(t)
	audit := NewAudit(store.NewServiceStore(db), store.NewAuditStore(db))

	return NewUsers(store.NewUserStore(db), audit), db
}

func TestUsersCreate(t *testing.T) {
	t.Parallel()

	users, _ := openUsers(t)
	ctx := context.Background()

	created, err := users.Create(ctx, CreateUserInput{
		Username: "bob", Password: testUserPassword, Role: model.RoleViewer, AllServices: true,
	}, testAdminActor)
	if err != nil {
		t.Fatalf("Create() error = %v, want nil", err)
	}

	if created.ID == 0 || created.Username != "bob" || created.Role != model.RoleViewer {
		t.Fatalf("Create() = %+v, want bob the viewer", created)
	}

	if _, err := users.Verify(ctx, "bob", testUserPassword); err != nil {
		t.Errorf("Verify() error = %v, want nil", err)
	}
}

func TestUsersCreateRejects(t *testing.T) {
	t.Parallel()

	users, _ := openUsers(t)
	ctx := context.Background()

	if _, err := users.Create(ctx, CreateUserInput{
		Username: "amy", Password: testUserPassword, Role: model.RoleAdmin, AllServices: true,
	}, testAdminActor); err != nil {
		t.Fatalf("Create(amy) error = %v, want nil", err)
	}

	tests := []struct {
		name     string
		input    CreateUserInput
		actor    string
		wantErr  error
		wantText string
	}{
		{name: "missing actor", input: CreateUserInput{Username: "x", Password: testUserPassword, Role: model.RoleViewer, AllServices: true}, actor: "", wantErr: ErrInvalidInput, wantText: "actor is required"},
		{name: "empty username", input: CreateUserInput{Username: "", Password: testUserPassword, Role: model.RoleViewer, AllServices: true}, actor: testAdminActor, wantErr: ErrInvalidInput, wantText: "username"},
		{name: "uppercase username", input: CreateUserInput{Username: "Bob", Password: testUserPassword, Role: model.RoleViewer, AllServices: true}, actor: testAdminActor, wantErr: ErrInvalidInput, wantText: "username"},
		{name: "long username", input: CreateUserInput{Username: strings.Repeat("a", 65), Password: testUserPassword, Role: model.RoleViewer, AllServices: true}, actor: testAdminActor, wantErr: ErrInvalidInput, wantText: "username"},
		{name: "duplicate username", input: CreateUserInput{Username: "amy", Password: testUserPassword, Role: model.RoleViewer, AllServices: true}, actor: testAdminActor, wantErr: ErrConflict, wantText: "already exists"},
		{name: "unknown role", input: CreateUserInput{Username: "zed", Password: testUserPassword, Role: model.RoleUnknown}, actor: testAdminActor, wantErr: ErrInvalidInput, wantText: "role"},
		{name: "short password", input: CreateUserInput{Username: "zed", Password: "short", Role: model.RoleViewer, AllServices: true}, actor: testAdminActor, wantErr: ErrInvalidInput, wantText: "too short"},
		{name: "long password", input: CreateUserInput{Username: "zed", Password: strings.Repeat("a", 73), Role: model.RoleViewer, AllServices: true}, actor: testAdminActor, wantErr: ErrInvalidInput, wantText: "too long"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := users.Create(ctx, tt.input, tt.actor)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Create() error = %v, want %v", err, tt.wantErr)
			}

			if !strings.Contains(err.Error(), tt.wantText) {
				t.Errorf("Create() error = %q, want %q inside", err.Error(), tt.wantText)
			}
		})
	}
}

func TestUsersVerify(t *testing.T) {
	t.Parallel()

	users, _ := openUsers(t)
	ctx := context.Background()

	created, err := users.Create(ctx, CreateUserInput{
		Username: "carol", Password: testUserPassword, Role: model.RoleViewer, AllServices: true,
	}, testAdminActor)
	if err != nil {
		t.Fatalf("Create() error = %v, want nil", err)
	}

	if _, err := users.Verify(ctx, "carol", testUserPassword); err != nil {
		t.Errorf("Verify(correct) error = %v, want nil", err)
	}

	for _, tt := range []struct {
		name     string
		username string
		password string
	}{
		{name: "wrong password", username: "carol", password: "wrong-horse-22"},
		{name: "unknown user", username: "nobody", password: testUserPassword},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if _, err := users.Verify(ctx, tt.username, tt.password); !errors.Is(err, ErrUnauthorized) {
				t.Errorf("Verify() error = %v, want ErrUnauthorized", err)
			}
		})
	}

	if _, err := users.SetDisabled(ctx, created.ID, true, testAdminActor); err != nil {
		t.Fatalf("SetDisabled() error = %v, want nil", err)
	}

	if _, err := users.Verify(ctx, "carol", testUserPassword); !errors.Is(err, ErrUnauthorized) {
		t.Errorf("Verify(disabled) error = %v, want ErrUnauthorized", err)
	}

	if _, err := users.Lookup(ctx, "carol"); !errors.Is(err, ErrUnauthorized) {
		t.Errorf("Lookup(disabled) error = %v, want ErrUnauthorized", err)
	}
}

func TestUsersLastAdminGuard(t *testing.T) {
	t.Parallel()

	users, _ := openUsers(t)
	ctx := context.Background()

	admin, err := users.Create(ctx, CreateUserInput{
		Username: "solo", Password: testUserPassword, Role: model.RoleAdmin, AllServices: true,
	}, testAdminActor)
	if err != nil {
		t.Fatalf("Create() error = %v, want nil", err)
	}

	if _, err := users.SetRole(ctx, admin.ID, model.RoleViewer, testAdminActor); !errors.Is(err, ErrConflict) {
		t.Errorf("SetRole(last admin) error = %v, want ErrConflict", err)
	}

	if _, err := users.SetDisabled(ctx, admin.ID, true, testAdminActor); !errors.Is(err, ErrConflict) {
		t.Errorf("SetDisabled(last admin) error = %v, want ErrConflict", err)
	}

	if _, err := users.Delete(ctx, admin.ID, testAdminActor); !errors.Is(err, ErrConflict) {
		t.Errorf("Delete(last admin) error = %v, want ErrConflict", err)
	}

	second, err := users.Create(ctx, CreateUserInput{
		Username: "backup", Password: "correct-horse-2", Role: model.RoleAdmin, AllServices: true,
	}, testAdminActor)
	if err != nil {
		t.Fatalf("Create(backup) error = %v, want nil", err)
	}

	if _, err := users.Delete(ctx, second.ID, testAdminActor); err != nil {
		t.Errorf("Delete(second admin) error = %v, want nil", err)
	}
}

func TestUsersSetPassword(t *testing.T) {
	t.Parallel()

	users, _ := openUsers(t)
	ctx := context.Background()

	created, err := users.Create(ctx, CreateUserInput{
		Username: "dan", Password: testUserPassword, Role: model.RoleViewer, AllServices: true,
	}, testAdminActor)
	if err != nil {
		t.Fatalf("Create() error = %v, want nil", err)
	}

	if _, err := users.SetPassword(ctx, created.ID, "brand-new-horse-9", testAdminActor); err != nil {
		t.Fatalf("SetPassword() error = %v, want nil", err)
	}

	if _, err := users.Verify(ctx, "dan", "brand-new-horse-9"); err != nil {
		t.Errorf("Verify(new) error = %v, want nil", err)
	}

	if _, err := users.Verify(ctx, "dan", testUserPassword); !errors.Is(err, ErrUnauthorized) {
		t.Errorf("Verify(old) error = %v, want ErrUnauthorized", err)
	}

	if _, err := users.SetPassword(ctx, created.ID, "short", testAdminActor); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("SetPassword(short) error = %v, want ErrInvalidInput", err)
	}

	if _, err := users.SetPassword(ctx, created.ID+99, "brand-new-horse-9", testAdminActor); !errors.Is(err, store.ErrUserNotFound) {
		t.Errorf("SetPassword(unknown) error = %v, want ErrUserNotFound", err)
	}
}

func TestUsersEnsureAdmin(t *testing.T) {
	t.Parallel()

	users, _ := openUsers(t)
	ctx := context.Background()

	bootstrap, err := users.EnsureAdmin(ctx, "bootstrap-horse-1")
	if err != nil {
		t.Fatalf("EnsureAdmin() error = %v, want nil", err)
	}

	if !bootstrap.Seeded || bootstrap.Generated || bootstrap.Pending {
		t.Fatalf("EnsureAdmin() = %+v, want a configured seed on an empty table", bootstrap)
	}

	seeded, err := users.Verify(ctx, "admin", "bootstrap-horse-1")
	if err != nil {
		t.Fatalf("Verify(bootstrap) error = %v, want nil", err)
	}

	if seeded.MustChangePassword {
		t.Error("configured admin MustChangePassword = true, want false (the operator chose it)")
	}

	bootstrap, err = users.EnsureAdmin(ctx, "another-horse-22")
	if err != nil {
		t.Fatalf("EnsureAdmin() again error = %v, want nil", err)
	}

	if bootstrap != (AdminBootstrap{}) {
		t.Errorf("EnsureAdmin() again = %+v, want nothing done once an admin exists", bootstrap)
	}

	if _, err := users.Verify(ctx, "admin", "bootstrap-horse-1"); err != nil {
		t.Errorf("Verify(original) error = %v, want nil (env must not clobber)", err)
	}

	if _, err := users.EnsureAdmin(ctx, ""); err != nil {
		t.Errorf("EnsureAdmin(\"\") with an admin present error = %v, want nil (the password is optional)", err)
	}
}

// openUsersWithFile wires Users with an initial password file in a
// temp dir and returns the file path.
func openUsersWithFile(t *testing.T) (*Users, string) {
	t.Helper()

	users, _ := openUsers(t)
	path := filepath.Join(t.TempDir(), InitialPasswordFileName)

	return users.WithInitialPasswordFile(path), path
}

// readInitialPassword returns the generated password in path.
func readInitialPassword(t *testing.T, path string) string {
	t.Helper()

	raw, err := os.ReadFile(path) //nolint:gosec // the test reads a file it created under t.TempDir.
	if err != nil {
		t.Fatalf("reading initial password file: %v", err)
	}

	return strings.TrimSuffix(string(raw), "\n")
}

// seedGeneratedAdmin boots Users with no configured password and
// returns the service, the initial password file and its password.
func seedGeneratedAdmin(t *testing.T) (*Users, string, string) {
	t.Helper()

	users, path := openUsersWithFile(t)

	bootstrap, err := users.EnsureAdmin(context.Background(), "")
	if err != nil {
		t.Fatalf("EnsureAdmin(\"\") error = %v, want nil", err)
	}

	want := AdminBootstrap{Seeded: true, Generated: true, PasswordFile: path}
	if bootstrap != want {
		t.Fatalf("EnsureAdmin(\"\") = %+v, want %+v", bootstrap, want)
	}

	return users, path, readInitialPassword(t, path)
}

func TestUsersEnsureAdminGeneratesOneTimePassword(t *testing.T) {
	t.Parallel()

	users, path, password := seedGeneratedAdmin(t)

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat(initial password file) error = %v, want a file", err)
	}

	if info.Mode().Perm() != 0o600 {
		t.Errorf("initial password file mode = %v, want 0600", info.Mode().Perm())
	}

	if len(password) < 24 {
		t.Fatalf("generated password length = %d, want at least 24", len(password))
	}

	admin, err := users.Verify(context.Background(), "admin", password)
	if err != nil {
		t.Fatalf("Verify(generated) error = %v, want nil", err)
	}

	if admin.Role != model.RoleAdmin || !admin.MustChangePassword {
		t.Fatalf("generated admin = %+v, want an admin that must change its password", admin)
	}
}

// A restart before first login reports the pending admin and keeps the
// same password: the file must still open the account.
func TestUsersEnsureAdminRestartKeepsTheUnusedPassword(t *testing.T) {
	t.Parallel()

	users, path, password := seedGeneratedAdmin(t)
	ctx := context.Background()

	bootstrap, err := users.EnsureAdmin(ctx, "")
	if err != nil {
		t.Fatalf("EnsureAdmin(\"\") restart error = %v, want nil", err)
	}

	if bootstrap != (AdminBootstrap{Pending: true, PasswordFile: path}) {
		t.Errorf("EnsureAdmin(\"\") restart = %+v, want pending with the file", bootstrap)
	}

	if readInitialPassword(t, path) != password {
		t.Error("restart rewrote the initial password file, want it untouched")
	}

	if _, err := users.Verify(ctx, "admin", password); err != nil {
		t.Errorf("Verify(generated) after restart error = %v, want nil", err)
	}
}

func TestUsersFirstLoginChangeRetiresTheGeneratedPassword(t *testing.T) {
	t.Parallel()

	users, path, password := seedGeneratedAdmin(t)
	ctx := context.Background()

	if err := users.ChangeOwnPassword(ctx, "admin", password, "my-own-password-9"); err != nil {
		t.Fatalf("ChangeOwnPassword() error = %v, want nil", err)
	}

	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("Stat(initial password file) after change error = %v, want not-exist", err)
	}

	changed, err := users.Verify(ctx, "admin", "my-own-password-9")
	if err != nil || changed.MustChangePassword {
		t.Fatalf("Verify(changed) = %+v, %v, want a normal admin", changed, err)
	}

	bootstrap, err := users.EnsureAdmin(ctx, "")
	if err != nil || bootstrap != (AdminBootstrap{}) {
		t.Errorf("EnsureAdmin(\"\") after first login = %+v, %v, want nothing to do", bootstrap, err)
	}
}

func TestUsersEnsureAdminGenerationNeedsAFile(t *testing.T) {
	t.Parallel()

	users, _ := openUsers(t)

	_, err := users.EnsureAdmin(context.Background(), "")
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("EnsureAdmin(\"\") without a file error = %v, want ErrInvalidInput", err)
	}

	if _, err := users.Lookup(context.Background(), "admin"); !errors.Is(err, ErrUnauthorized) {
		t.Errorf("Lookup(admin) error = %v, want no admin seeded", err)
	}
}

func TestUsersEnsureAdminRejectsShortConfiguredPassword(t *testing.T) {
	t.Parallel()

	users, path := openUsersWithFile(t)

	if _, err := users.EnsureAdmin(context.Background(), "short"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("EnsureAdmin(short) error = %v, want ErrInvalidInput", err)
	}

	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a refused configured password left a generated file: %v", err)
	}
}

func TestUsersEnsureAdminConfiguredPasswordReplacesUnusedGenerated(t *testing.T) {
	t.Parallel()

	users, path := openUsersWithFile(t)
	ctx := context.Background()

	if _, err := users.EnsureAdmin(ctx, ""); err != nil {
		t.Fatalf("EnsureAdmin(\"\") error = %v, want nil", err)
	}

	generated := readInitialPassword(t, path)

	bootstrap, err := users.EnsureAdmin(ctx, "operator-chosen-1")
	if err != nil {
		t.Fatalf("EnsureAdmin(configured) error = %v, want nil", err)
	}

	if bootstrap != (AdminBootstrap{Seeded: true}) {
		t.Errorf("EnsureAdmin(configured) = %+v, want a configured seed", bootstrap)
	}

	admin, err := users.Verify(ctx, "admin", "operator-chosen-1")
	if err != nil || admin.MustChangePassword {
		t.Fatalf("Verify(configured) = %+v, %v, want a normal admin", admin, err)
	}

	if _, err := users.Verify(ctx, "admin", generated); !errors.Is(err, ErrUnauthorized) {
		t.Errorf("Verify(generated) error = %v, want ErrUnauthorized after the override", err)
	}

	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("Stat(initial password file) error = %v, want it removed", err)
	}
}

func TestUsersChangeOwnPassword(t *testing.T) {
	t.Parallel()

	users, _ := openUsers(t)
	ctx := context.Background()

	if _, err := users.Create(ctx, CreateUserInput{
		Username: "dev", Password: testUserPassword, Role: model.RoleViewer, AllServices: true,
	}, testAdminActor); err != nil {
		t.Fatalf("Create(dev) error = %v, want nil", err)
	}

	tests := []struct {
		name    string
		current string
		next    string
		want    error
	}{
		{name: "wrong current", current: "not-the-password", next: "a-fine-new-password", want: ErrUnauthorized},
		{name: "same as current", current: testUserPassword, next: testUserPassword, want: ErrInvalidInput},
		{name: "too short", current: testUserPassword, next: "short", want: ErrInvalidInput},
		{name: "too long", current: testUserPassword, next: strings.Repeat("x", 73), want: ErrInvalidInput},
	}

	for _, tt := range tests {
		if err := users.ChangeOwnPassword(ctx, "dev", tt.current, tt.next); !errors.Is(err, tt.want) {
			t.Errorf("ChangeOwnPassword(%s) error = %v, want %v", tt.name, err, tt.want)
		}
	}

	if _, err := users.Verify(ctx, "dev", testUserPassword); err != nil {
		t.Fatalf("Verify(original) after refusals error = %v, want the password unchanged", err)
	}

	if err := users.ChangeOwnPassword(ctx, "dev", testUserPassword, "a-fine-new-password"); err != nil {
		t.Fatalf("ChangeOwnPassword() error = %v, want nil", err)
	}

	if _, err := users.Verify(ctx, "dev", "a-fine-new-password"); err != nil {
		t.Errorf("Verify(new) error = %v, want nil", err)
	}

	if _, err := users.Verify(ctx, "dev", testUserPassword); !errors.Is(err, ErrUnauthorized) {
		t.Errorf("Verify(old) error = %v, want ErrUnauthorized", err)
	}

	if err := users.ChangeOwnPassword(ctx, "ghost", testUserPassword, "a-fine-new-password"); !errors.Is(err, ErrUnauthorized) {
		t.Errorf("ChangeOwnPassword(unknown user) error = %v, want ErrUnauthorized", err)
	}
}

func TestUsersResetWithOneTimePassword(t *testing.T) {
	t.Parallel()

	users, path, _ := seedGeneratedAdmin(t)
	ctx := context.Background()

	password, err := users.ResetWithOneTimePassword(ctx, "admin", "cli:root")
	if err != nil {
		t.Fatalf("ResetWithOneTimePassword() error = %v, want nil", err)
	}

	admin, err := users.Verify(ctx, "admin", password)
	if err != nil || !admin.MustChangePassword {
		t.Fatalf("Verify(one-time) = %+v, %v, want an admin that must change its password", admin, err)
	}

	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("Stat(initial password file) error = %v, want the superseded file removed", err)
	}

	bootstrap, err := users.EnsureAdmin(ctx, "")
	if err != nil || bootstrap != (AdminBootstrap{Pending: true}) {
		t.Errorf("EnsureAdmin(\"\") after reset = %+v, %v, want pending without a file", bootstrap, err)
	}

	if _, err := users.Verify(ctx, "admin", password); err != nil {
		t.Errorf("Verify(one-time) after restart error = %v, want the reset password kept", err)
	}
}

func TestUsersResetWithOneTimePasswordRejects(t *testing.T) {
	t.Parallel()

	users, _, _ := seedGeneratedAdmin(t)
	ctx := context.Background()

	if _, err := users.ResetWithOneTimePassword(ctx, "ghost", "cli:root"); !errors.Is(err, store.ErrUserNotFound) {
		t.Errorf("ResetWithOneTimePassword(ghost) error = %v, want ErrUserNotFound", err)
	}

	if _, err := users.ResetWithOneTimePassword(ctx, "admin", ""); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("ResetWithOneTimePassword(no actor) error = %v, want ErrInvalidInput", err)
	}
}

// An admin reset through user management hands over a temporary
// password: the account must replace it at its next login (D-13).
func TestUsersSetPasswordLeavesATemporaryPassword(t *testing.T) {
	t.Parallel()

	users, db := openUsers(t)
	ctx := context.Background()

	created, err := users.Create(ctx, CreateUserInput{
		Username: "erin", Password: testUserPassword, Role: model.RoleViewer, AllServices: true,
	}, testAdminActor)
	if err != nil {
		t.Fatalf("Create() error = %v, want nil", err)
	}

	if !created.MustChangePassword {
		t.Error("Create() left must_change_password unset, want a temporary password")
	}

	if err := users.ChangeOwnPassword(ctx, "erin", testUserPassword, "chosen-by-erin-12"); err != nil {
		t.Fatalf("ChangeOwnPassword() error = %v, want nil", err)
	}

	reset, err := users.SetPassword(ctx, created.ID, "chosen-by-an-admin-1", testAdminActor)
	if err != nil {
		t.Fatalf("SetPassword() error = %v, want nil", err)
	}

	if reset.Username != "erin" || !reset.MustChangePassword {
		t.Errorf("SetPassword() = %+v, want erin on a temporary password", reset)
	}

	verified, err := users.Verify(ctx, "erin", "chosen-by-an-admin-1")
	if err != nil || !verified.MustChangePassword {
		t.Errorf("Verify(admin reset) = %+v, %v, want must_change_password set", verified, err)
	}

	entries, err := store.NewAuditStore(db).List(ctx, "", 1)
	if err != nil {
		t.Fatalf("List() error = %v, want nil", err)
	}

	if len(entries) != 1 || entries[0].Detail != "reset password of user erin to a temporary one" {
		t.Errorf("audit = %+v, want the temporary password reset", entries)
	}
}

func TestUsersResetWithOneTimePasswordRefusesDisabled(t *testing.T) {
	t.Parallel()

	users, _ := openUsers(t)
	ctx := context.Background()

	if _, err := users.EnsureAdmin(ctx, "bootstrap-horse-1"); err != nil {
		t.Fatalf("EnsureAdmin() error = %v, want nil", err)
	}

	dev, err := users.Create(ctx, CreateUserInput{
		Username: "dev", Password: testUserPassword, Role: model.RoleViewer, AllServices: true,
	}, testAdminActor)
	if err != nil {
		t.Fatalf("Create(dev) error = %v, want nil", err)
	}

	if _, err := users.SetDisabled(ctx, dev.ID, true, testAdminActor); err != nil {
		t.Fatalf("SetDisabled(dev) error = %v, want nil", err)
	}

	if _, err := users.ResetWithOneTimePassword(ctx, "dev", "cli:root"); !errors.Is(err, ErrConflict) {
		t.Errorf("ResetWithOneTimePassword(disabled) error = %v, want ErrConflict", err)
	}
}

func TestWriteInitialPasswordReplacesAStaleFile(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), InitialPasswordFileName)

	if err := os.WriteFile(path, []byte("stale\n"), 0o644); err != nil { //nolint:gosec // deliberately loose to prove the rewrite tightens it.
		t.Fatalf("seeding stale file: %v", err)
	}

	if err := writeInitialPassword(path, "fresh-password"); err != nil {
		t.Fatalf("writeInitialPassword() error = %v, want nil", err)
	}

	if got := readInitialPassword(t, path); got != "fresh-password" {
		t.Errorf("initial password file = %q, want the fresh password", got)
	}

	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("initial password file mode = %v, %v, want 0600", info.Mode().Perm(), err)
	}

	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil || len(entries) != 1 {
		t.Errorf("directory entries = %d, %v, want only the password file (no temp left)", len(entries), err)
	}
}
