package http

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	nethttp "net/http"

	"github.com/oralecarlangelo/touchgrass/internal/docker"
	"github.com/oralecarlangelo/touchgrass/internal/model"
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
		Addr:      testDatabaseAddr,
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
			Name:    testBlueContainer,
			Image:   "ticketnation-api:latest",
			ImageID: "sha256:11112222333344445555",
			State:   testRunningState,
			Status:  "Up 2 hours",
			Ports:   []string{"127.0.0.1:4101->4000/tcp"},
			Labels: map[string]string{
				docker.LabelComposeProject: "ticketnation",
				docker.LabelComposeService: testBlueService,
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

// writeCreateScript writes a stub with mode and returns its path. Mode rides
// a parameter (like the service package helper) since create validation
// needs the exec bit on temp fixtures.
func writeCreateScript(t *testing.T, dir, name string, mode os.FileMode) string {
	t.Helper()

	path := dir + "/" + name

	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), mode); err != nil {
		t.Fatalf("WriteFile(%s) error = %v, want nil", name, err)
	}

	return path
}

// createServiceBody builds a valid creation body with temp scripts.
func createServiceBody(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	deploy := writeCreateScript(t, dir, "deploy.sh", 0o755)
	rollback := writeCreateScript(t, dir, "rollback.sh", 0o755)

	return `{"id":"shop-web","strategy":"recreate","compose_project":"shop",` +
		`"compose_dir":"` + dir + `",` +
		`"config":{"service":"web","health_url":"http://127.0.0.1:4201/health",` +
		`"deploy_script":"` + deploy + `","rollback_script":"` + rollback + `"}}`
}

func TestHandleCreateService(t *testing.T) {
	t.Parallel()

	server := onboardingTestServer(t, stubLister{}, stubProber{})

	req := httptest.NewRequestWithContext(
		t.Context(),
		nethttp.MethodPost,
		"/api/services",
		strings.NewReader(createServiceBody(t)),
	)
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

	if res.StatusCode != nethttp.StatusCreated {
		t.Fatalf("status = %d, want %d (body: %s)", res.StatusCode, nethttp.StatusCreated, body)
	}

	var got service.CreateOutput

	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decoding body: %v", err)
	}

	if got.ID != "shop-web" || got.Strategy != model.StrategyRecreate {
		t.Errorf("created = (%q, %q), want (shop-web, recreate)", got.ID, got.Strategy)
	}

	listReq := httptest.NewRequestWithContext(t.Context(), nethttp.MethodGet, "/api/services", nil)
	listReq.AddCookie(authCookie(t, server))

	listRec := httptest.NewRecorder()
	server.handler.ServeHTTP(listRec, listReq)

	listRes := listRec.Result()

	listBody, err := io.ReadAll(listRes.Body)
	if err != nil {
		t.Fatalf("reading list body: %v", err)
	}

	if err := listRes.Body.Close(); err != nil {
		t.Fatalf("closing list body: %v", err)
	}

	var listed servicesResponse

	if err := json.Unmarshal(listBody, &listed); err != nil {
		t.Fatalf("decoding list body: %v", err)
	}

	if len(listed.Services) != 4 {
		t.Fatalf("services = %d, want 4 after create", len(listed.Services))
	}
}

func TestHandleCreateServiceErrors(t *testing.T) {
	t.Parallel()

	base := createServiceBody(t)

	tests := []struct {
		name       string
		body       func(string) string
		wantStatus int
		wantCode   string
	}{
		{
			name:       "malformed json",
			body:       func(string) string { return testMalformedJSON },
			wantStatus: nethttp.StatusBadRequest,
			wantCode:   testInvalidRequest,
		},
		{
			name:       "bad id",
			body:       func(body string) string { return strings.Replace(body, "shop-web", "Shop!", 1) },
			wantStatus: nethttp.StatusBadRequest,
			wantCode:   testInvalidRequest,
		},
		{
			name:       "duplicate id",
			body:       func(body string) string { return strings.Replace(body, "shop-web", "tn-api", 1) },
			wantStatus: nethttp.StatusConflict,
			wantCode:   "conflict",
		},
		{
			name:       "bad strategy",
			body:       func(body string) string { return strings.Replace(body, "recreate", "canary", 1) },
			wantStatus: nethttp.StatusBadRequest,
			wantCode:   testInvalidRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			server := onboardingTestServer(t, stubLister{}, stubProber{})

			req := httptest.NewRequestWithContext(
				t.Context(),
				nethttp.MethodPost,
				"/api/services",
				strings.NewReader(tt.body(base)),
			)
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

			if res.StatusCode != tt.wantStatus {
				t.Fatalf("status = %d, want %d (body: %s)", res.StatusCode, tt.wantStatus, body)
			}

			var got errorResponse

			if err := json.Unmarshal(body, &got); err != nil {
				t.Fatalf("decoding body: %v", err)
			}

			if got.Code != tt.wantCode || got.Error == "" {
				t.Errorf("envelope = %+v, want code %q with message", got, tt.wantCode)
			}
		})
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
