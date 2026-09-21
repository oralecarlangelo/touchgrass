package http

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http/httptest"
	"testing"

	nethttp "net/http"

	"github.com/oralecarlangelo/touchgrass/internal/docker"
	"github.com/oralecarlangelo/touchgrass/internal/probe"
	"github.com/oralecarlangelo/touchgrass/internal/service"
	"github.com/oralecarlangelo/touchgrass/internal/store"
)

// stubLister is a fake docker.Lister.
type stubLister struct {
	containers []docker.Container
	err        error
}

func (s stubLister) List(_ context.Context) ([]docker.Container, error) {
	return s.containers, s.err
}

// stubProber is a fake service.Prober.
type stubProber struct{}

func (s stubProber) Check(_ context.Context, _ string) (probe.Result, error) {
	return probe.Result{Healthy: true, StatusCode: nethttp.StatusOK}, nil
}

// servicesTestServer builds a Server backed by a seeded store and fakes.
func servicesTestServer(t *testing.T, lister docker.Lister) *Server {
	t.Helper()

	db, err := store.Open(":memory:")
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

	logger := slog.New(slog.DiscardHandler)
	services := store.NewServiceStore(db)
	inv := service.NewInventory(services, lister, stubProber{}, logger)

	return New(Config{
		Addr:      "127.0.0.1:0",
		Version:   testVersion,
		Logger:    logger,
		Inventory: inv,
		Audit:     service.NewAudit(services, store.NewAuditStore(db)),
		Auth:      testAuthenticator(t),
		Events:    NewHub(logger),
		Dist:      testDist(),
	})
}

func TestHandleServices(t *testing.T) {
	t.Parallel()

	containers := []docker.Container{
		{
			ID:      "aaaabbbbccccddddeeee",
			Name:    "ticketnation-api-blue-1",
			Image:   "ticketnation-api:latest",
			ImageID: "sha256:11112222333344445555",
			State:   "running",
			Status:  "Up 2 hours",
			Ports:   []string{"127.0.0.1:4101->4000/tcp"},
			Labels: map[string]string{
				docker.LabelComposeProject: "ticketnation",
				docker.LabelComposeService: "api-blue",
			},
		},
	}

	server := servicesTestServer(t, stubLister{containers: containers})

	req := httptest.NewRequestWithContext(t.Context(), nethttp.MethodGet, "/api/services", nil)
	req.AddCookie(authCookie(t, server))

	rec := httptest.NewRecorder()

	server.handler.ServeHTTP(rec, req)

	res := rec.Result()

	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("reading body: %v", err)
	}

	if err := res.Body.Close(); err != nil {
		t.Fatalf("closing body: %v", err)
	}

	if res.StatusCode != nethttp.StatusOK {
		t.Fatalf("status = %d, want %d (body: %s)", res.StatusCode, nethttp.StatusOK, body)
	}

	var got servicesResponse

	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decoding body: %v", err)
	}

	if len(got.Services) != 3 {
		t.Fatalf("services = %d, want 3", len(got.Services))
	}

	for _, view := range got.Services {
		if view.Colors == nil || view.Containers == nil {
			t.Errorf("service %q has nil colors or containers (must never be null)", view.ID)
		}
	}

	if got.Services[0].ID != "admin-fe" {
		t.Errorf("services[0].id = %q, want admin-fe (ordered by id)", got.Services[0].ID)
	}
}

func TestHandleServicesDockerError(t *testing.T) {
	t.Parallel()

	server := servicesTestServer(t, stubLister{err: errors.New("daemon down")})

	req := httptest.NewRequestWithContext(t.Context(), nethttp.MethodGet, "/api/services", nil)
	req.AddCookie(authCookie(t, server))

	rec := httptest.NewRecorder()

	server.handler.ServeHTTP(rec, req)

	res := rec.Result()

	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("reading body: %v", err)
	}

	if err := res.Body.Close(); err != nil {
		t.Fatalf("closing body: %v", err)
	}

	if res.StatusCode != nethttp.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", res.StatusCode)
	}

	var got errorResponse

	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decoding body: %v", err)
	}

	if got.Code != "services_unavailable" || got.Error == "" {
		t.Errorf("envelope = %+v, want precise error with code", got)
	}
}
