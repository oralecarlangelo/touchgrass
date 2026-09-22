package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/oralecarlangelo/touchgrass/internal/docker"
	"github.com/oralecarlangelo/touchgrass/internal/model"
	"github.com/oralecarlangelo/touchgrass/internal/store"
)

func TestSampleFleetCollects(t *testing.T) {
	t.Parallel()

	containers := testContainers()
	sampler, db := testSampler(t, testFleetLister(containers, false))
	ctx := context.Background()

	sampler.sample(ctx)

	fleet := store.NewFleetStore(db)

	latest, err := fleet.LatestContainers(ctx)
	if err != nil {
		t.Fatalf("LatestContainers() error = %v, want nil", err)
	}

	if len(latest) != 3 {
		t.Fatalf("fleet rows = %d, want 3 (every container sampled)", len(latest))
	}

	byName := make(map[string]model.FleetContainer, len(latest))

	for _, row := range latest {
		byName[row.Name] = row
	}

	checkManagedFleetRow(t, byName[containers[0].Name])
	checkUnmanagedFleetRow(t, byName[containers[2].Name])

	if latest[0].Name != containers[2].Name {
		t.Errorf("first row = %q, want the 40%% cpu container", latest[0].Name)
	}

	// Exactly one host row per interval; trimming the future counts it.
	hosts, err := fleet.TrimHostsBefore(ctx, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("TrimHostsBefore() error = %v, want nil", err)
	}

	if hosts != 1 {
		t.Errorf("host rows = %d, want 1", hosts)
	}
}

// testFleetLister fakes stats for the shared fixture containers,
// optionally failing the managed blue container.
func testFleetLister(containers []docker.Container, failBlue bool) stubStatsLister {
	lister := stubStatsLister{
		containers: containers,
		sizes:      []int64{100, 200, 300},
		stats: map[string]docker.Stats{
			containers[0].ID: {CPUPercent: 12.5, MemBytes: 200, MemLimit: 1000, Restarts: 2},
			containers[1].ID: {CPUPercent: 5, MemBytes: 100, MemLimit: 1000, Restarts: 0},
			containers[2].ID: {CPUPercent: 40, MemBytes: 900, MemLimit: 1000, Restarts: 7},
		},
	}

	if failBlue {
		delete(lister.stats, containers[0].ID)
		lister.statsErr = map[string]error{containers[0].ID: errors.New("no such container")}
	}

	return lister
}

// checkManagedFleetRow asserts the managed blue row carries its service.
func checkManagedFleetRow(t *testing.T, blue model.FleetContainer) {
	t.Helper()

	if !blue.Managed || blue.ServiceID != testServiceAPI {
		t.Errorf("blue = %+v, want managed tn-api row", blue)
	}

	if blue.CPUPercent != 12.5 || blue.MemBytes != 200 || blue.MemLimit != 1000 || blue.Restarts != 2 {
		t.Errorf("blue = %+v, want mapped stats", blue)
	}

	if blue.Project != testComposeProject || blue.State != testRunningState {
		t.Errorf("blue = %+v, want project and state", blue)
	}
}

// checkUnmanagedFleetRow asserts the unrelated row samples unmanaged.
func checkUnmanagedFleetRow(t *testing.T, unrelated model.FleetContainer) {
	t.Helper()

	if unrelated.Managed || unrelated.ServiceID != "" {
		t.Errorf("unrelated = %+v, want unmanaged row without service", unrelated)
	}

	if unrelated.Project != testOtherProject {
		t.Errorf("unrelated project = %q, want %q", unrelated.Project, testOtherProject)
	}
}

func TestSampleFleetSkipsFailedStats(t *testing.T) {
	t.Parallel()

	containers := testContainers()
	sampler, db := testSampler(t, testFleetLister(containers, true))
	ctx := context.Background()

	sampler.sample(ctx)

	latest, err := store.NewFleetStore(db).LatestContainers(ctx)
	if err != nil {
		t.Fatalf("LatestContainers() error = %v, want nil", err)
	}

	if len(latest) != 2 {
		t.Errorf("fleet rows = %d, want 2 (failed container skipped)", len(latest))
	}
}

func TestHostHistoryInvalidMetric(t *testing.T) {
	t.Parallel()

	sampler, _ := testSampler(t, stubStatsLister{})

	for _, metric := range []string{"", "disk", "CPU"} {
		if _, err := sampler.HostHistory(context.Background(), metric, 1); !errors.Is(err, ErrInvalidInput) {
			t.Errorf("HostHistory(%q) error = %v, want ErrInvalidInput", metric, err)
		}
	}
}

func TestHostHistoryPoints(t *testing.T) {
	t.Parallel()

	sampler, db := testSampler(t, stubStatsLister{})
	ctx := context.Background()
	fleet := store.NewFleetStore(db)

	cpu, load := 25.0, 1.5
	used := int64(2048)

	if err := fleet.InsertHost(ctx, model.HostSample{
		SampledAt:  time.Now(),
		CPUPercent: &cpu,
		MemUsed:    &used,
		Load1:      &load,
	}); err != nil {
		t.Fatalf("InsertHost() error = %v, want nil", err)
	}

	tests := []struct {
		name     string
		metric   string
		expected float64
	}{
		{name: "cpu average", metric: model.MetricCPU, expected: 25},
		{name: "mem used bytes", metric: model.MetricMem, expected: 2048},
		{name: "load", metric: model.MetricLoad, expected: 1.5},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			points, err := sampler.HostHistory(ctx, tt.metric, 1)
			if err != nil {
				t.Fatalf("HostHistory() error = %v, want nil", err)
			}

			if len(points) != 1 || points[0].Value != tt.expected {
				t.Errorf("points = %+v, want one point at %v", points, tt.expected)
			}
		})
	}
}

func TestHostHistoryHours(t *testing.T) {
	t.Parallel()

	sampler, db := testSampler(t, stubStatsLister{})
	ctx := context.Background()
	fleet := store.NewFleetStore(db)

	cpu := 10.0
	old := model.HostSample{SampledAt: time.Now().Add(-100 * time.Hour), CPUPercent: &cpu}

	if err := fleet.InsertHost(ctx, old); err != nil {
		t.Fatalf("InsertHost() error = %v, want nil", err)
	}

	tests := []struct {
		name     string
		hours    int
		expected int
	}{
		{name: "narrow window excludes", hours: 1, expected: 0},
		{name: "default is 24h", hours: 0, expected: 0},
		{name: "above max clamps to 168h", hours: 999, expected: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			points, err := sampler.HostHistory(ctx, model.MetricCPU, tt.hours)
			if err != nil {
				t.Fatalf("HostHistory() error = %v, want nil", err)
			}

			if len(points) != tt.expected {
				t.Errorf("points = %d, want %d", len(points), tt.expected)
			}
		})
	}
}

func TestFleetTrimUsesMetricsRetention(t *testing.T) {
	t.Parallel()

	sampler, db := testSampler(t, stubStatsLister{})
	ctx := context.Background()
	fleet := store.NewFleetStore(db)

	now := time.Now().Truncate(time.Second)
	stale := now.Add(-2 * time.Hour)

	cpu := 10.0

	if err := fleet.InsertHost(ctx, model.HostSample{SampledAt: stale, CPUPercent: &cpu}); err != nil {
		t.Fatalf("InsertHost() error = %v, want nil", err)
	}

	if err := fleet.InsertHost(ctx, model.HostSample{SampledAt: now, CPUPercent: &cpu}); err != nil {
		t.Fatalf("InsertHost() error = %v, want nil", err)
	}

	if err := fleet.InsertContainer(ctx, model.ContainerSample{ContainerName: "stale-1", SampledAt: stale}); err != nil {
		t.Fatalf("InsertContainer() error = %v, want nil", err)
	}

	if err := fleet.InsertContainer(ctx, model.ContainerSample{ContainerName: "fresh-1", SampledAt: now}); err != nil {
		t.Fatalf("InsertContainer() error = %v, want nil", err)
	}

	sampler.trim(ctx)

	latest, err := fleet.LatestContainers(ctx)
	if err != nil {
		t.Fatalf("LatestContainers() error = %v, want nil", err)
	}

	if len(latest) != 1 || latest[0].Name != "fresh-1" {
		t.Errorf("latest = %+v, want only fresh-1", latest)
	}

	remaining, err := fleet.TrimHostsBefore(ctx, now.Add(time.Hour))
	if err != nil {
		t.Fatalf("TrimHostsBefore() error = %v, want nil", err)
	}

	if remaining != 1 {
		t.Errorf("host rows = %d, want 1 (stale trimmed under metrics retention)", remaining)
	}
}
