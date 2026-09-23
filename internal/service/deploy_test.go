package service

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"sync"
	"testing"

	"github.com/oralecarlangelo/touchgrass/internal/docker"
	"github.com/oralecarlangelo/touchgrass/internal/model"
	"github.com/oralecarlangelo/touchgrass/internal/probe"
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

// stubProber is a fake Prober.
type stubProber struct {
	healthy map[string]bool
	err     error
}

func (s stubProber) Check(_ context.Context, target string) (probe.Result, error) {
	if s.err != nil {
		return probe.Result{}, s.err
	}

	if s.healthy[target] {
		return probe.Result{Healthy: true, StatusCode: 200}, nil
	}

	return probe.Result{Healthy: false, StatusCode: 500}, nil
}

// recordingProber is a fake Prober that records probed targets.
type recordingProber struct {
	mu      sync.Mutex
	targets []string
	healthy map[string]bool
}

func (s *recordingProber) Check(_ context.Context, target string) (probe.Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.targets = append(s.targets, target)

	if s.healthy[target] {
		return probe.Result{Healthy: true, StatusCode: 200}, nil
	}

	return probe.Result{Healthy: false, StatusCode: 500}, nil
}

func (s *recordingProber) probed() []string {
	s.mu.Lock()
	defer s.mu.Unlock()

	return append([]string{}, s.targets...)
}

// openInventoryDB returns a seeded in-memory store.
func openInventoryDB(t *testing.T) *store.DB {
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

	return db
}

// testContainers returns one tn-api, one tn-fe, and one unrelated container.
func testContainers() []docker.Container {
	return []docker.Container{
		{
			ID:      "aaaabbbbccccddddeeee",
			Name:    testBlueContainerName,
			Image:   "ticketnation-api:latest",
			ImageID: "sha256:11112222333344445555",
			State:   testRunningState,
			Status:  "Up 2 hours",
			Ports:   []string{"127.0.0.1:4101->4000/tcp"},
			Labels: map[string]string{
				docker.LabelComposeProject: testComposeProject,
				docker.LabelComposeService: testBlueService,
			},
		},
		{
			ID:      "ffff0000111122223333",
			Name:    "ticketnation-fe-fe-1",
			Image:   "ticketnation-fe:latest",
			ImageID: "sha256:99998888777766665555",
			State:   testRunningState,
			Status:  "Up 3 days",
			Ports:   []string{"127.0.0.1:3000->3000/tcp"},
			Labels: map[string]string{
				docker.LabelComposeProject: "ticketnation-fe",
				docker.LabelComposeService: "fe",
			},
		},
		{
			ID:     "unrelated",
			Name:   "some-db-1",
			Labels: map[string]string{docker.LabelComposeProject: testOtherProject},
		},
	}
}

// testInventory builds an Inventory on the seeded store with fakes.
func testInventory(t *testing.T, lister docker.Lister, prober Prober) *Inventory {
	t.Helper()

	db := openInventoryDB(t)

	return NewInventory(store.NewServiceStore(db), lister, prober, slog.New(slog.DiscardHandler))
}

// serviceViews indexes Services() output by id.
func serviceViews(t *testing.T, inv *Inventory) map[string]model.ServiceView {
	t.Helper()

	views, err := inv.Services(context.Background())
	if err != nil {
		t.Fatalf("Services() error = %v, want nil", err)
	}

	if len(views) != 3 {
		t.Fatalf("Services() returned %d views, want 3", len(views))
	}

	byID := map[string]model.ServiceView{}
	for _, view := range views {
		byID[view.ID] = view
	}

	return byID
}

func TestServicesBlueGreen(t *testing.T) {
	t.Parallel()

	prober := stubProber{healthy: map[string]bool{testBlueHealthURL: true}}
	inv := testInventory(t, stubLister{containers: testContainers()}, prober)

	api := serviceViews(t, inv)[testServiceAPI]

	if api.Strategy != model.StrategyBlueGreen {
		t.Errorf("tn-api strategy = %q, want bluegreen", api.Strategy)
	}

	// The seed nginx conf (/etc/...) does not exist in tests, so live color
	// degrades to unknown without failing the request.
	if api.LiveColor != "unknown" {
		t.Errorf("tn-api live color = %q, want unknown (no nginx conf in test)", api.LiveColor)
	}

	if len(api.Colors) != 2 {
		t.Fatalf("tn-api colors = %d, want 2", len(api.Colors))
	}

	if api.Colors[0].Health != model.HealthHealthy {
		t.Errorf("tn-api blue health = %q, want healthy", api.Colors[0].Health)
	}

	if len(api.Containers) != 1 || api.Containers[0].SHA != "111122223333" {
		t.Errorf("tn-api containers = %+v, want the blue container with short SHA", api.Containers)
	}
}

func TestServicesRecreate(t *testing.T) {
	t.Parallel()

	prober := stubProber{healthy: map[string]bool{"http://127.0.0.1:3000": true}}
	inv := testInventory(t, stubLister{containers: testContainers()}, prober)

	views := serviceViews(t, inv)

	fe := views["admin-fe"]
	if fe.Strategy != model.StrategyRecreate || fe.LiveColor != "" {
		t.Errorf("admin-fe = (%q, %q), want (recreate, empty live color)", fe.Strategy, fe.LiveColor)
	}

	if fe.Health != model.HealthUnhealthy {
		t.Errorf("admin-fe health = %q, want unhealthy (stubbed down)", fe.Health)
	}

	if fe.Colors == nil || fe.Containers == nil {
		t.Error("admin-fe colors/containers must be non-nil (never null in JSON)")
	}

	market := views[testServiceFE]
	if market.Health != model.HealthHealthy {
		t.Errorf("tn-fe health = %q, want healthy", market.Health)
	}

	if len(market.Containers) != 1 || market.Containers[0].Name != "ticketnation-fe-fe-1" {
		t.Errorf("tn-fe containers = %+v, want the fe container", market.Containers)
	}
}

func TestServicesDockerError(t *testing.T) {
	t.Parallel()

	inv := testInventory(t, stubLister{err: errors.New("daemon down")}, stubProber{})

	if _, err := inv.Services(context.Background()); err == nil {
		t.Error("Services() error = nil, want docker failure")
	}
}

func TestShortID(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		id       string
		expected string
	}{
		{name: "long id truncated", id: "aaaabbbbccccddddeeee", expected: "aaaabbbbcccc"},
		{name: "short id kept", id: "abc", expected: "abc"},
		{name: "empty id", id: "", expected: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := shortID(tt.id); got != tt.expected {
				t.Errorf("shortID(%q) = %q, want %q", tt.id, got, tt.expected)
			}
		})
	}
}

func TestServicesSkipsStoppedIdleColor(t *testing.T) {
	t.Parallel()

	// Only the blue container runs; green is stopped and idle (live color
	// degrades to unknown without an nginx conf in tests).
	prober := &recordingProber{healthy: map[string]bool{
		testBlueHealthURL:  true,
		testGreenHealthURL: true,
	}}
	inv := testInventory(t, stubLister{containers: testContainers()}, prober)

	api := serviceViews(t, inv)[testServiceAPI]

	if got := prober.probed(); !slices.Contains(got, testBlueHealthURL) {
		t.Errorf("probed = %v, want blue URL probed (container running)", got)
	}

	if got := prober.probed(); slices.Contains(got, testGreenHealthURL) {
		t.Errorf("probed = %v, want green URL skipped (stopped idle color)", got)
	}

	if len(api.Colors) != 2 {
		t.Fatalf("tn-api colors = %d, want 2", len(api.Colors))
	}

	if api.Colors[1].Health != model.HealthUnhealthy {
		t.Errorf("tn-api green health = %q, want unhealthy without a probe", api.Colors[1].Health)
	}
}

func TestServicesProbesBothColorsWhenRunning(t *testing.T) {
	t.Parallel()

	containers := append(testContainers(), docker.Container{
		ID:    "gggghhhhiiiijjjjkkkk",
		Name:  "ticketnation-api-green-1",
		State: testRunningState,
		Labels: map[string]string{
			docker.LabelComposeProject: testComposeProject,
			docker.LabelComposeService: testGreenService,
		},
	})

	prober := &recordingProber{healthy: map[string]bool{
		testBlueHealthURL:  true,
		testGreenHealthURL: true,
	}}
	inv := testInventory(t, stubLister{containers: containers}, prober)

	api := serviceViews(t, inv)[testServiceAPI]

	if got := prober.probed(); !slices.Contains(got, testGreenHealthURL) {
		t.Errorf("probed = %v, want green URL probed (container running)", got)
	}

	if len(api.Colors) != 2 {
		t.Fatalf("tn-api colors = %d, want 2", len(api.Colors))
	}

	if api.Colors[1].Health != model.HealthHealthy {
		t.Errorf("tn-api green health = %q, want healthy", api.Colors[1].Health)
	}
}
