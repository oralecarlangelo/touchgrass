package http

import (
	"context"
	"log/slog"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	nethttp "net/http"

	"github.com/oralecarlangelo/touchgrass/internal/docker"
	"github.com/oralecarlangelo/touchgrass/internal/model"
	"github.com/oralecarlangelo/touchgrass/internal/service"
	"github.com/oralecarlangelo/touchgrass/internal/store"
)

// testVersion is the stubbed binary version.
const testVersion = "test-version"

// testPassword is the harness admin password.
const testPassword = "test-password"

const (
	testHealthPath     = "/api/health"
	testKeysPath       = "/api/keys"
	testIssueRulesPath = "/api/issues/rules"
	testInvalidRequest = "invalid_request"
	testServiceAPI     = "tn-api"
	testUnknownService = "unknown service"
)

// hashTestPassword generates the harness bcrypt hash once.
var hashTestPassword = sync.OnceValues(func() ([]byte, error) {
	return HashPassword(testPassword)
})

// testAuthenticator builds an Authenticator for the harness password.
func testAuthenticator(t *testing.T) *Authenticator {
	t.Helper()

	hash, err := hashTestPassword()
	if err != nil {
		t.Fatalf("HashPassword() error = %v, want nil", err)
	}

	return NewAuthenticator(hash, false)
}

// stubStatsLister fakes docker listing plus stats with empty data.
type stubStatsLister struct{}

func (s stubStatsLister) List(_ context.Context) ([]docker.Container, error) {
	return []docker.Container{}, nil
}

func (s stubStatsLister) SizedList(_ context.Context) ([]docker.Container, []int64, error) {
	return []docker.Container{}, []int64{}, nil
}

func (s stubStatsLister) Stats(_ context.Context, _ string) (docker.Stats, error) {
	return docker.Stats{}, nil
}

func (s stubStatsLister) Logs(
	_ context.Context,
	_ string,
	_ string,
	_ int,
) ([]docker.LogLine, error) {
	return []docker.LogLine{}, nil
}

// fullTestServer builds a Server with every service on a seeded store.
// The sampler is wired but never run; tests drive methods via HTTP.
func fullTestServer(t *testing.T) (*Server, *store.DB) {
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

	sampler := service.NewSampler(service.SamplerConfig{
		Services:      services,
		Docker:        stubStatsLister{},
		Metrics:       store.NewMetricStore(db),
		Rules:         store.NewRuleStore(db),
		Notifications: store.NewNotificationStore(db),
		Deploys:       store.NewDeployStore(db),
		Occurrences:   store.NewOccurrenceStore(db),
		Issues:        store.NewIssueStore(db),
		IssueRules:    store.NewIssueRuleStore(db),
		Logs:          store.NewLogStore(db),
		Interval:      time.Hour,
		Retention: service.Retention{
			Metrics: time.Hour, Notifications: time.Hour, Deploys: time.Hour, Errors: time.Hour, Logs: time.Hour,
		},
		Logger: logger,
	})

	deploys := service.NewDeploys(services, store.NewDeployStore(db))
	audit := service.NewAudit(services, store.NewAuditStore(db))

	server := New(Config{
		Addr:      "127.0.0.1:0",
		Version:   testVersion,
		Logger:    logger,
		Inventory: service.NewInventory(services, stubStatsLister{}, stubProber{}, logger),
		Sampler:   sampler,
		Deploys:   deploys,
		Cutover: service.NewCutover(service.CutoverConfig{
			Services:      services,
			Docker:        stubStatsLister{},
			Deploys:       deploys,
			Probes:        store.NewDeployStore(db),
			Notifications: store.NewNotificationStore(db),
			Audit:         audit,
			Prober:        stubProber{},
			Emit:          func(model.Event) {},
			Timeout:       time.Minute,
			ProbeInterval: time.Millisecond,
			Logger:        logger,
		}),
		Audit:  audit,
		Auth:   testAuthenticator(t),
		Events: NewHub(logger),
		Ingestor: service.NewIngestor(service.IngestorConfig{
			Services:       services,
			Keys:           store.NewKeyStore(db),
			Occurrences:    store.NewOccurrenceStore(db),
			Issues:         store.NewIssueStore(db),
			IssueRules:     store.NewIssueRuleStore(db),
			Logs:           store.NewLogStore(db),
			MaxOccurrences: 1000,
			Logger:         logger,
		}),
		Logs: service.NewLogCollector(service.LogCollectorConfig{
			Services:           services,
			Docker:             stubStatsLister{},
			Logs:               store.NewLogStore(db),
			Interval:           time.Hour,
			MaxLinesPerService: 1000,
			Logger:             logger,
		}),
		Dist: testDist(),
	})

	return server, db
}

// authCookie logs in and returns the session cookie.
func authCookie(t *testing.T, server *Server) *nethttp.Cookie {
	t.Helper()

	req := httptest.NewRequestWithContext(
		t.Context(),
		nethttp.MethodPost,
		"/api/auth/login",
		strings.NewReader(`{"password":"test-password"}`),
	)
	rec := httptest.NewRecorder()

	server.handler.ServeHTTP(rec, req)

	res := rec.Result()

	defer func() {
		if err := res.Body.Close(); err != nil {
			t.Errorf("closing body: %v", err)
		}
	}()

	if res.StatusCode != nethttp.StatusNoContent {
		t.Fatalf("login status = %d, want 204", res.StatusCode)
	}

	cookies := res.Cookies()
	if len(cookies) == 0 {
		t.Fatal("login set no cookies")
	}

	return cookies[0]
}
