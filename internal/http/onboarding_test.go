package http

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"

	nethttp "net/http"

	"github.com/oralecarlangelo/touchgrass/internal/docker"
	"github.com/oralecarlangelo/touchgrass/internal/model"
	"github.com/oralecarlangelo/touchgrass/internal/service"
	"github.com/oralecarlangelo/touchgrass/internal/store"
)

// onboardingTestServer builds a Server with Onboarding wired to fakes and
// an empty nginx stub.
func onboardingTestServer(t *testing.T, lister docker.Lister, prober service.Prober) *Server {
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
	audit := service.NewAudit(services, store.NewAuditStore(db))

	onboarding := service.NewOnboarding(service.OnboardingConfig{
		Services:   services,
		Docker:     lister,
		Prober:     prober,
		Audit:      audit,
		ScriptsDir: t.TempDir(),
		ReadDir: func(string) ([]fs.DirEntry, error) {
			return []fs.DirEntry{}, nil
		},
		ReadFile: func(string) ([]byte, error) {
			return nil, errors.New("no nginx in tests")
		},
	})

	return New(Config{
		Addr:       testDatabaseAddr,
		Version:    testVersion,
		Logger:     logger,
		Inventory:  service.NewInventory(services, lister, prober, logger),
		Audit:      audit,
		Auth:       testAuthenticator(t),
		Events:     NewHub(logger),
		Onboarding: onboarding,
		Dist:       testDist(),
	})
}

// suggestContainers is one labeled shop container on port 4201.
func suggestContainers() []docker.Container {
	return []docker.Container{
		{
			ID:    "shop-web-id",
			Name:  "shop-web-1",
			Image: "shop:test",
			State: testRunningState,
			Published: []docker.PublishedPort{
				{HostIP: "0.0.0.0", HostPort: 4201, ContainerPort: 80, Proto: "tcp"},
			},
			Labels: map[string]string{
				docker.LabelComposeProject:    "shop",
				docker.LabelComposeService:    "web",
				docker.LabelComposeWorkingDir: "/opt/shop",
			},
		},
	}
}

// suggestResponse performs an authenticated suggest and returns status + body.
func suggestResponse(t *testing.T, server *Server, target string) (int, []byte) {
	t.Helper()

	req := httptest.NewRequestWithContext(
		t.Context(),
		nethttp.MethodGet,
		"/api/onboarding/suggest?container="+target,
		nil,
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

	return res.StatusCode, body
}

// checkSuggestDraft validates the drafted fields of a suggestion.
func checkSuggestDraft(t *testing.T, got model.ServiceSuggestion) {
	t.Helper()

	if got.ServiceID != "shop" || got.Strategy != model.StrategyRecreate {
		t.Errorf("draft = (%q, %q), want (shop, recreate)", got.ServiceID, got.Strategy)
	}

	if got.HealthURL != "http://127.0.0.1:4201/" {
		t.Errorf("health_url = %q, want probed root", got.HealthURL)
	}

	if got.ComposeProject != "shop" || got.ComposeDir != "/opt/shop" || got.Service != "web" {
		t.Errorf("compose = (%q, %q, %q), want labels", got.ComposeProject, got.ComposeDir, got.Service)
	}

	if !strings.HasSuffix(got.DeployScript, "/recreate-deploy.sh") {
		t.Errorf("deploy_script = %q, want default recreate script", got.DeployScript)
	}

	if got.Confidence != model.ConfidenceHigh {
		t.Errorf("confidence = %q, want high", got.Confidence)
	}

	if got.Reasons == nil || got.Warnings == nil {
		t.Error("draft has nil reasons or warnings (must never be null)")
	}
}

func TestHandleOnboardingSuggest(t *testing.T) {
	t.Parallel()

	server := onboardingTestServer(t, stubLister{containers: suggestContainers()}, stubProber{})

	status, body := suggestResponse(t, server, "shop-web-1")
	if status != nethttp.StatusOK {
		t.Fatalf("status = %d, want %d (body: %s)", status, nethttp.StatusOK, body)
	}

	var got model.ServiceSuggestion

	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decoding body: %v", err)
	}

	checkSuggestDraft(t, got)
}

func TestHandleOnboardingSuggestErrors(t *testing.T) {
	t.Parallel()

	managed := []docker.Container{
		{
			ID:    "api-blue-id",
			Name:  testBlueContainer,
			State: testRunningState,
			Labels: map[string]string{
				docker.LabelComposeProject: "ticketnation",
				docker.LabelComposeService: testBlueService,
			},
		},
	}

	tests := []struct {
		name       string
		containers []docker.Container
		target     string
		wantStatus int
		wantCode   string
	}{
		{
			name:       "unknown container",
			containers: suggestContainers(),
			target:     "nope",
			wantStatus: nethttp.StatusBadRequest,
			wantCode:   testInvalidRequest,
		},
		{
			name:       "missing container query",
			containers: suggestContainers(),
			target:     "",
			wantStatus: nethttp.StatusBadRequest,
			wantCode:   testInvalidRequest,
		},
		{
			name:       "already managed",
			containers: managed,
			target:     testBlueContainer,
			wantStatus: nethttp.StatusConflict,
			wantCode:   "conflict",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			server := onboardingTestServer(t, stubLister{containers: tt.containers}, stubProber{})

			status, body := suggestResponse(t, server, tt.target)
			if status != tt.wantStatus {
				t.Fatalf("status = %d, want %d (body: %s)", status, tt.wantStatus, body)
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
