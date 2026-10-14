package service

import (
	"context"
	"errors"
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
		Username: "bob", Password: testUserPassword, Role: model.RoleViewer,
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
		Username: "amy", Password: testUserPassword, Role: model.RoleAdmin,
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
		{name: "missing actor", input: CreateUserInput{Username: "x", Password: testUserPassword, Role: model.RoleViewer}, actor: "", wantErr: ErrInvalidInput, wantText: "actor is required"},
		{name: "empty username", input: CreateUserInput{Username: "", Password: testUserPassword, Role: model.RoleViewer}, actor: testAdminActor, wantErr: ErrInvalidInput, wantText: "username"},
		{name: "uppercase username", input: CreateUserInput{Username: "Bob", Password: testUserPassword, Role: model.RoleViewer}, actor: testAdminActor, wantErr: ErrInvalidInput, wantText: "username"},
		{name: "long username", input: CreateUserInput{Username: strings.Repeat("a", 65), Password: testUserPassword, Role: model.RoleViewer}, actor: testAdminActor, wantErr: ErrInvalidInput, wantText: "username"},
		{name: "duplicate username", input: CreateUserInput{Username: "amy", Password: testUserPassword, Role: model.RoleViewer}, actor: testAdminActor, wantErr: ErrConflict, wantText: "already exists"},
		{name: "unknown role", input: CreateUserInput{Username: "zed", Password: testUserPassword, Role: model.RoleUnknown}, actor: testAdminActor, wantErr: ErrInvalidInput, wantText: "role"},
		{name: "short password", input: CreateUserInput{Username: "zed", Password: "short", Role: model.RoleViewer}, actor: testAdminActor, wantErr: ErrInvalidInput, wantText: "too short"},
		{name: "long password", input: CreateUserInput{Username: "zed", Password: strings.Repeat("a", 73), Role: model.RoleViewer}, actor: testAdminActor, wantErr: ErrInvalidInput, wantText: "too long"},
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
		Username: "carol", Password: testUserPassword, Role: model.RoleViewer,
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
		Username: "solo", Password: testUserPassword, Role: model.RoleAdmin,
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

	if err := users.Delete(ctx, admin.ID, testAdminActor); !errors.Is(err, ErrConflict) {
		t.Errorf("Delete(last admin) error = %v, want ErrConflict", err)
	}

	second, err := users.Create(ctx, CreateUserInput{
		Username: "backup", Password: "correct-horse-2", Role: model.RoleAdmin,
	}, testAdminActor)
	if err != nil {
		t.Fatalf("Create(backup) error = %v, want nil", err)
	}

	if err := users.Delete(ctx, second.ID, testAdminActor); err != nil {
		t.Errorf("Delete(second admin) error = %v, want nil", err)
	}
}

func TestUsersSetPassword(t *testing.T) {
	t.Parallel()

	users, _ := openUsers(t)
	ctx := context.Background()

	created, err := users.Create(ctx, CreateUserInput{
		Username: "dan", Password: testUserPassword, Role: model.RoleViewer,
	}, testAdminActor)
	if err != nil {
		t.Fatalf("Create() error = %v, want nil", err)
	}

	if err := users.SetPassword(ctx, created.ID, "brand-new-horse-9", testAdminActor); err != nil {
		t.Fatalf("SetPassword() error = %v, want nil", err)
	}

	if _, err := users.Verify(ctx, "dan", "brand-new-horse-9"); err != nil {
		t.Errorf("Verify(new) error = %v, want nil", err)
	}

	if _, err := users.Verify(ctx, "dan", testUserPassword); !errors.Is(err, ErrUnauthorized) {
		t.Errorf("Verify(old) error = %v, want ErrUnauthorized", err)
	}

	if err := users.SetPassword(ctx, created.ID, "short", testAdminActor); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("SetPassword(short) error = %v, want ErrInvalidInput", err)
	}

	if err := users.SetPassword(ctx, created.ID+99, "brand-new-horse-9", testAdminActor); !errors.Is(err, store.ErrUserNotFound) {
		t.Errorf("SetPassword(unknown) error = %v, want ErrUserNotFound", err)
	}
}

func TestUsersEnsureAdmin(t *testing.T) {
	t.Parallel()

	users, _ := openUsers(t)
	ctx := context.Background()

	seeded, err := users.EnsureAdmin(ctx, "bootstrap-horse-1")
	if err != nil {
		t.Fatalf("EnsureAdmin() error = %v, want nil", err)
	}

	if !seeded {
		t.Fatal("EnsureAdmin() seeded = false, want true on an empty table")
	}

	if _, err := users.Verify(ctx, "admin", "bootstrap-horse-1"); err != nil {
		t.Errorf("Verify(bootstrap) error = %v, want nil", err)
	}

	seeded, err = users.EnsureAdmin(ctx, "another-horse-22")
	if err != nil {
		t.Fatalf("EnsureAdmin() again error = %v, want nil", err)
	}

	if seeded {
		t.Error("EnsureAdmin() seeded = true, want false once an admin exists")
	}

	if _, err := users.Verify(ctx, "admin", "bootstrap-horse-1"); err != nil {
		t.Errorf("Verify(original) error = %v, want nil (env must not clobber)", err)
	}
}
