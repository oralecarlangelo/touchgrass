package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/oralecarlangelo/touchgrass/internal/model"
)

func TestUserStoreRoundTrip(t *testing.T) {
	t.Parallel()

	db := openTestDB(t)
	ctx := context.Background()
	users := NewUserStore(db)

	created, err := users.Create(ctx, model.User{
		Username: "bob", PasswordHash: "hash-bob", Role: model.RoleViewer,
	})
	if err != nil {
		t.Fatalf("Create() error = %v, want nil", err)
	}

	if created.ID == 0 || created.CreatedAt.IsZero() {
		t.Fatalf("Create() = %+v, want id and timestamp", created)
	}

	byID, err := users.ByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("ByID() error = %v, want nil", err)
	}

	if byID.Username != "bob" || byID.Role != model.RoleViewer || byID.Disabled() {
		t.Errorf("ByID() = %+v, want bob the enabled viewer", byID)
	}

	byName, err := users.ByUsername(ctx, "bob")
	if err != nil {
		t.Fatalf("ByUsername() error = %v, want nil", err)
	}

	if byName.ID != created.ID {
		t.Errorf("ByUsername() id = %d, want %d", byName.ID, created.ID)
	}

	if _, err := users.ByID(ctx, created.ID+99); !errors.Is(err, ErrUserNotFound) {
		t.Errorf("ByID() error = %v, want ErrUserNotFound", err)
	}

	if _, err := users.ByUsername(ctx, "nobody"); !errors.Is(err, ErrUserNotFound) {
		t.Errorf("ByUsername() error = %v, want ErrUserNotFound", err)
	}
}

func TestUserStoreUpdateAndDelete(t *testing.T) {
	t.Parallel()

	db := openTestDB(t)
	ctx := context.Background()
	users := NewUserStore(db)

	created, err := users.Create(ctx, model.User{
		Username: "carol", PasswordHash: "hash-carol", Role: model.RoleViewer,
	})
	if err != nil {
		t.Fatalf("Create() error = %v, want nil", err)
	}

	disabled := time.Now()
	created.Role = model.RoleAdmin
	created.PasswordHash = "hash-carol-2"
	created.DisabledAt = &disabled

	if err := users.Update(ctx, created); err != nil {
		t.Fatalf("Update() error = %v, want nil", err)
	}

	got, err := users.ByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("ByID() error = %v, want nil", err)
	}

	if got.Role != model.RoleAdmin || got.PasswordHash != "hash-carol-2" || !got.Disabled() {
		t.Errorf("ByID() after update = %+v, want disabled admin with new hash", got)
	}

	if err := users.Update(ctx, model.User{ID: created.ID + 99}); !errors.Is(err, ErrUserNotFound) {
		t.Errorf("Update() error = %v, want ErrUserNotFound", err)
	}

	if err := users.Delete(ctx, created.ID); err != nil {
		t.Fatalf("Delete() error = %v, want nil", err)
	}

	if _, err := users.ByID(ctx, created.ID); !errors.Is(err, ErrUserNotFound) {
		t.Errorf("ByID() after delete error = %v, want ErrUserNotFound", err)
	}

	if err := users.Delete(ctx, created.ID); !errors.Is(err, ErrUserNotFound) {
		t.Errorf("Delete() twice error = %v, want ErrUserNotFound", err)
	}
}

func TestUserStoreListAndAdminCount(t *testing.T) {
	t.Parallel()

	db := openTestDB(t)
	ctx := context.Background()
	users := NewUserStore(db)

	for _, user := range []model.User{
		{Username: "amy", PasswordHash: "hash-amy", Role: model.RoleAdmin},
		{Username: "ben", PasswordHash: "hash-ben", Role: model.RoleViewer},
	} {
		if _, err := users.Create(ctx, user); err != nil {
			t.Fatalf("Create(%s) error = %v, want nil", user.Username, err)
		}
	}

	listed, err := users.List(ctx, 10)
	if err != nil {
		t.Fatalf("List() error = %v, want nil", err)
	}

	if len(listed) != 2 || listed[0].Username != "amy" || listed[1].Username != "ben" {
		t.Fatalf("List() = %+v, want amy then ben", listed)
	}

	count, err := users.CountEnabledAdmins(ctx)
	if err != nil {
		t.Fatalf("CountEnabledAdmins() error = %v, want nil", err)
	}

	if count != 1 {
		t.Errorf("CountEnabledAdmins() = %d, want 1", count)
	}
}
