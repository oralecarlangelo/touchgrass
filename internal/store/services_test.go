package store

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/oralecarlangelo/touchgrass/internal/model"
)

const testServiceShop = "shop-web"

func TestServiceCreate(t *testing.T) {
	t.Parallel()

	db := openTestDB(t)
	ctx := context.Background()
	services := NewServiceStore(db)

	created := model.Service{
		ID:             testServiceShop,
		Name:           testServiceShop,
		Strategy:       model.StrategyRecreate,
		ComposeProject: "shop",
		ComposeDir:     "/opt/shop",
		Config:         json.RawMessage(`{"service":"web"}`),
	}

	if err := services.Create(ctx, created); err != nil {
		t.Fatalf("Create() error = %v, want nil", err)
	}

	got, err := services.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get() error = %v, want nil", err)
	}

	if got.ID != created.ID || got.Name != created.Name || got.Strategy != created.Strategy {
		t.Errorf("Get() identity = (%q, %q, %q), want (%q, %q, %q)",
			got.ID, got.Name, got.Strategy, created.ID, created.Name, created.Strategy)
	}

	if got.ComposeProject != created.ComposeProject || got.ComposeDir != created.ComposeDir {
		t.Errorf("Get() compose = (%q, %q), want (%q, %q)",
			got.ComposeProject, got.ComposeDir, created.ComposeProject, created.ComposeDir)
	}

	if string(got.Config) != string(created.Config) {
		t.Errorf("Get() config = %s, want %s", got.Config, created.Config)
	}
}

func TestServiceCreateDuplicate(t *testing.T) {
	t.Parallel()

	db := openTestDB(t)
	ctx := context.Background()
	services := NewServiceStore(db)

	created := model.Service{
		ID:             testServiceShop,
		Name:           testServiceShop,
		Strategy:       model.StrategyRecreate,
		ComposeProject: "shop",
		ComposeDir:     "/opt/shop",
		Config:         json.RawMessage(`{}`),
	}

	if err := services.Create(ctx, created); err != nil {
		t.Fatalf("Create() error = %v, want nil", err)
	}

	if err := services.Create(ctx, created); err == nil {
		t.Error("Create(duplicate) error = nil, want constraint error")
	}
}

func TestServiceAll(t *testing.T) {
	t.Parallel()

	db := openTestDB(t)
	ctx := context.Background()
	services := NewServiceStore(db)

	got, err := services.All(ctx)
	if err != nil {
		t.Fatalf("All() error = %v, want nil", err)
	}

	if len(got) != 3 {
		t.Fatalf("All() = %d services, want 3 seeded", len(got))
	}

	if got[0].ID != "admin-fe" || got[1].ID != "tn-api" || got[2].ID != "tn-fe" {
		t.Errorf("All() ids = %q, %q, %q, want ordered by id", got[0].ID, got[1].ID, got[2].ID)
	}
}

func TestServiceGetNotFound(t *testing.T) {
	t.Parallel()

	db := openTestDB(t)
	ctx := context.Background()
	services := NewServiceStore(db)

	if _, err := services.Get(ctx, "nope"); !errors.Is(err, ErrServiceNotFound) {
		t.Errorf("Get() error = %v, want ErrServiceNotFound", err)
	}
}

func TestServiceUpdateConfig(t *testing.T) {
	t.Parallel()

	db := openTestDB(t)
	ctx := context.Background()
	services := NewServiceStore(db)

	updated := json.RawMessage(`{"service":"web","health_url":"http://x/"}`)

	if err := services.UpdateConfig(ctx, testServiceAPI, updated); err != nil {
		t.Fatalf("UpdateConfig() error = %v, want nil", err)
	}

	got, err := services.Get(ctx, testServiceAPI)
	if err != nil {
		t.Fatalf("Get() error = %v, want nil", err)
	}

	if string(got.Config) != string(updated) {
		t.Errorf("Get() config = %s, want %s", got.Config, updated)
	}

	if err := services.UpdateConfig(ctx, "nope", updated); !errors.Is(err, ErrServiceNotFound) {
		t.Errorf("UpdateConfig() error = %v, want ErrServiceNotFound", err)
	}
}
