package store

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/oralecarlangelo/touchgrass/internal/model"
)

func TestServiceStoreAll(t *testing.T) {
	t.Parallel()

	db := openTestDB(t)

	services, err := NewServiceStore(db).All(context.Background())
	if err != nil {
		t.Fatalf("All() error = %v, want nil", err)
	}

	if len(services) != 3 {
		t.Fatalf("All() returned %d services, want 3", len(services))
	}

	want := map[string]model.Strategy{
		testServiceAPI: model.StrategyBlueGreen,
		"tn-fe":        model.StrategyRecreate,
		"admin-fe":     model.StrategyRecreate,
	}

	for _, service := range services {
		expected, ok := want[service.ID]
		if !ok {
			t.Errorf("All() unexpected service id %q", service.ID)

			continue
		}

		if service.Strategy != expected {
			t.Errorf("All() service %q strategy = %q, want %q", service.ID, service.Strategy, expected)
		}

		if len(service.Config) == 0 {
			t.Errorf("All() service %q has empty config", service.ID)
		}
	}
}

func TestServiceStoreGet(t *testing.T) {
	t.Parallel()

	db := openTestDB(t)
	ctx := context.Background()

	service, err := NewServiceStore(db).Get(ctx, testServiceAPI)
	if err != nil {
		t.Fatalf("Get() error = %v, want nil", err)
	}

	if service.Name != testServiceAPI || service.ComposeProject != "ticketnation" {
		t.Errorf("Get() = %+v, want tn-api in project ticketnation", service)
	}

	_, err = NewServiceStore(db).Get(ctx, "nope")
	if !errors.Is(err, ErrServiceNotFound) {
		t.Errorf("Get() error = %v, want ErrServiceNotFound", err)
	}
}

func TestServiceStoreUpdateConfig(t *testing.T) {
	t.Parallel()

	db := openTestDB(t)
	ctx := context.Background()
	services := NewServiceStore(db)

	config := json.RawMessage(`{"service":"app","health_url":"http://127.0.0.1:3002/health"}`)

	if err := services.UpdateConfig(ctx, "admin-fe", config); err != nil {
		t.Fatalf("UpdateConfig() error = %v, want nil", err)
	}

	got, err := services.Get(ctx, "admin-fe")
	if err != nil {
		t.Fatalf("Get() error = %v, want nil", err)
	}

	if string(got.Config) != string(config) {
		t.Errorf("Get() config = %s, want %s", got.Config, config)
	}

	if err := services.UpdateConfig(ctx, "nope", config); !errors.Is(err, ErrServiceNotFound) {
		t.Errorf("UpdateConfig() error = %v, want ErrServiceNotFound", err)
	}
}
