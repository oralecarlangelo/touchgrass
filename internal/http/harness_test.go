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

	"golang.org/x/crypto/bcrypt"

	"github.com/oralecarlangelo/touchgrass/internal/docker"
	"github.com/oralecarlangelo/touchgrass/internal/model"
	"github.com/oralecarlangelo/touchgrass/internal/service"
	"github.com/oralecarlangelo/touchgrass/internal/store"
)

// testVersion is the stubbed binary version.
const testVersion = "test-version"

// testPassword is the harness admin password.
const testPassword = "test-password"

// testViewerUsername and testViewerPassword are the harness read-only login.
const (
	testViewerUsername = "viewer"
	testViewerPassword = "viewer-password-1"
)

const (
	testHealthPath     = "/api/health"
	testKeysPath       = "/api/keys"
	testIssueRulesPath = "/api/issues/rules"
	testServicesPath   = "/api/services"
	testDeploysPath    = "/api/services/tn-api/deploys"
	testCutoverPath    = "/api/services/tn-api/cutover"
	testInvalidRequest = "invalid_request"
	testServiceAPI     = "tn-api"
	testUnknownService = "unknown service"
	testBlueContainer  = "ticketnation-api-blue-1"
	testBlueService    = "api-blue"
	testRunningState   = "running"
	testMalformedJSON  = "{oops"
)

// hashTestPassword generates the harness admin bcrypt hash once.
var hashTestPassword = sync.OnceValues(func() ([]byte, error) {
	return bcrypt.GenerateFromPassword([]byte(testPassword), bcrypt.DefaultCost)
})

// hashViewerPassword generates the harness viewer bcrypt hash once.
var hashViewerPassword = sync.OnceValues(func() ([]byte, error) {
	return bcrypt.GenerateFromPassword([]byte(testViewerPassword), bcrypt.DefaultCost)
})

// testAuthenticator builds an Authenticator over managed users.
func testAuthenticator(t *testing.T, users *service.Users) *Authenticator {
	t.Helper()

	if users == nil {
		t.Fatal("testAuthenticator users = nil, want service")
	}

	return NewAuthenticator(users, false)
}

// seedTestUsers inserts the harness admin and viewer rows. Hashes are
// precomputed once per package run so seeding stays fast.
func seedTestUsers(t *testing.T, db *store.DB) {
	t.Helper()

	adminHash, err := hashTestPassword()
	if err != nil {
		t.Fatalf("hashing admin password: %v", err)
	}

	viewerHash, err := hashViewerPassword()
	if err != nil {
		t.Fatalf("hashing viewer password: %v", err)
	}

	users := store.NewUserStore(db)
	ctx := context.Background()

	for _, user := range []model.User{
		{Username: defaultLoginUsername, PasswordHash: string(adminHash), Role: model.RoleAdmin},
		{Username: testViewerUsername, PasswordHash: string(viewerHash), Role: model.RoleViewer},
	} {
		if _, err := users.Create(ctx, user); err != nil {
			t.Fatalf("Create(%s) error = %v, want nil", user.Username, err)
		}
	}
}

// stubStatsLister fakes docker listing plus stats with empty data.
type stubStatsLister struct{}

func (s stubStatsLister) List(_ context.Context) ([]docker.Container, error) {
	return []docker.Container{}, nil
}

func (s stubStatsLister) ListAll(_ context.Context) ([]docker.Container, error) {
	return []docker.Container{}, nil
}

func (s stubStatsLister) Inspect(_ context.Context, id string) (docker.Inspection, error) {
	return docker.Inspection{ID: id}, nil
}

func (s stubStatsLister) SizedList(_ context.Context) ([]docker.Container, []int64, error) {
	return []docker.Container{}, []int64{}, nil
}

func (s stubStatsLister) Stats(_ context.Context, _ string) (docker.Stats, error) {
	return docker.Stats{}, nil
}

func (s stubStatsLister) Images(_ context.Context) ([]docker.Image, error) {
	return []docker.Image{
		{ID: "sha256:harness", RepoTags: []string{"app:test"}, Size: 42, Created: 1700000000},
	}, nil
}

func (s stubStatsLister) PruneImages(_ context.Context) (docker.PruneReport, error) {
	return docker.PruneReport{Deleted: 1, ReclaimedBytes: 42}, nil
}

func (s stubStatsLister) DaemonInfo(_ context.Context) (docker.DaemonInfo, error) {
	return docker.DaemonInfo{ServerVersion: "stub-daemon", ImageCount: 1}, nil
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

	seedTestUsers(t, db)

	logger := slog.New(slog.DiscardHandler)
	services := store.NewServiceStore(db)
	performance := service.NewPerformance(service.PerformanceConfig{
		Services: services,
		Store:    store.NewPerformanceStore(db),
		Metrics:  store.NewMetricStore(db),
		Deploys:  store.NewDeployStore(db),
	})

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
		SDKLogs:       store.NewSDKLogStore(db),
		Fleet:         store.NewFleetStore(db),
		Performance:   performance,
		Interval:      time.Hour,
		Retention: service.Retention{
			Metrics: time.Hour, Notifications: time.Hour, Deploys: time.Hour, Errors: time.Hour, Logs: time.Hour,
			SDKLogs: time.Hour,
		},
		Logger: logger,
	})

	deploys := service.NewDeploys(services, store.NewDeployStore(db))
	audit := service.NewAudit(services, store.NewAuditStore(db))
	managedUsers := service.NewUsers(store.NewUserStore(db), audit)
	fleetInventory := service.NewFleetInventory(service.FleetInventoryConfig{
		Services: services, Containers: store.NewContainerStore(db), Docker: stubStatsLister{},
		Interval: time.Minute, InspectLimit: 2, Logger: logger,
		Security: store.NewSecurityStore(db), Audit: audit,
	})

	server := New(Config{
		Addr:      testDatabaseAddr,
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
		Auth:   testAuthenticator(t, managedUsers),
		Users:  managedUsers,
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
		SDKLogs: service.NewLogIngestor(service.LogIngestorConfig{
			Services: services,
			Keys:     store.NewKeyStore(db),
			SDKLogs:  store.NewSDKLogStore(db),
		}),
		System: service.NewSystem(service.SystemConfig{
			Docker:    stubStatsLister{},
			Version:   "stub-version",
			DBPath:    t.TempDir() + "/missing.db",
			StartedAt: time.Now(),
			Logger:    logger,
		}),
		Performance:    performance,
		FleetInventory: fleetInventory,
		Dist:           testDist(),
		Docs:           testDocs(),
	})

	return server, db
}

// authCookie logs in as admin and returns the session cookie. The
// password-only body pins backward compatibility with v1 clients.
func authCookie(t *testing.T, server *Server) *nethttp.Cookie {
	t.Helper()

	return loginCookie(t, server, `{"password":"test-password"}`)
}

// viewerCookie logs in as the read-only viewer.
func viewerCookie(t *testing.T, server *Server) *nethttp.Cookie {
	t.Helper()

	return loginCookie(t, server, `{"username":"viewer","password":"viewer-password-1"}`)
}

// loginCookie posts one login body and returns the session cookie.
func loginCookie(t *testing.T, server *Server, body string) *nethttp.Cookie {
	t.Helper()

	req := httptest.NewRequestWithContext(
		t.Context(),
		nethttp.MethodPost,
		"/api/auth/login",
		strings.NewReader(body),
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
