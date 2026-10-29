package service

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/oralecarlangelo/touchgrass/internal/docker"
	"github.com/oralecarlangelo/touchgrass/internal/model"
	"github.com/oralecarlangelo/touchgrass/internal/store"
)

type fakeEventStreamer struct {
	mu     sync.Mutex
	events []docker.Event
	since  []string
}

type errorEventStreamer struct{}

func (errorEventStreamer) EventStream(context.Context, string) (<-chan docker.Event, <-chan error) {
	events := make(chan docker.Event)
	errorsOut := make(chan error, 1)
	errorsOut <- errors.New("daemon unavailable")
	close(events)
	close(errorsOut)
	return events, errorsOut
}

func (f *fakeEventStreamer) EventStream(_ context.Context, since string) (<-chan docker.Event, <-chan error) {
	f.mu.Lock()
	f.since = append(f.since, since)
	events := append([]docker.Event{}, f.events...)
	f.mu.Unlock()
	eventChannel := make(chan docker.Event, len(events))
	errorChannel := make(chan error)
	for _, event := range events {
		eventChannel <- event
	}
	close(eventChannel)
	close(errorChannel)
	return eventChannel, errorChannel
}

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

func TestContainerEventCollectorPersistsContainerID(t *testing.T) {
	t.Parallel()

	db := openInventoryDB(t)
	performance := store.NewPerformanceStore(db)
	at := time.Date(2026, 10, 16, 4, 5, 6, 0, time.UTC)
	streamer := &fakeEventStreamer{events: []docker.Event{{
		SourceEventID: "event-1", OccurredAt: at, Action: "die", ContainerID: testBlueContainerID,
		ContainerName: testBlueContainerName, ComposeProject: testComposeProject, ComposeService: testBlueService,
	}}}
	collector := NewContainerEventCollector(ContainerEventCollectorConfig{
		Services: store.NewServiceStore(db), Docker: streamer, Writer: performance,
		Environment: "prod", Expected: func(serviceID string) bool { return serviceID == testServiceAPI },
		Logger: slog.New(slog.DiscardHandler),
	})
	if err := collector.runStream(context.Background()); err != nil {
		t.Fatal(err)
	}

	events, err := performance.ListContainerEvents(context.Background(), model.PerformanceRange{ServiceID: testServiceAPI, Start: at.Add(-time.Second), End: at.Add(time.Second)})
	if err != nil || len(events) != 1 || !events[0].Expected || events[0].EventType != "die" ||
		events[0].ContainerID != testBlueContainerID {
		t.Fatalf("events = %+v, %v", events, err)
	}
	if err := collector.runStream(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(streamer.since) != 2 || streamer.since[1] != at.Format(time.RFC3339Nano) {
		t.Fatalf("stream since values = %v", streamer.since)
	}
}

func TestContainerEventCollectorStopsOnCancellation(t *testing.T) {
	t.Parallel()

	db := openInventoryDB(t)
	collector := NewContainerEventCollector(ContainerEventCollectorConfig{
		Services: store.NewServiceStore(db), Docker: &blockingEventStreamer{},
		Writer: store.NewPerformanceStore(db), Environment: "prod", Logger: slog.New(slog.DiscardHandler),
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { collector.Run(ctx); close(done) }()
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("event collector did not stop after cancellation")
	}
}

func TestContainerEventCollectorMarksCursorDegraded(t *testing.T) {
	t.Parallel()

	db := openInventoryDB(t)
	performance := store.NewPerformanceStore(db)
	requests := 0
	collector := NewContainerEventCollector(ContainerEventCollectorConfig{
		Services: store.NewServiceStore(db), Docker: errorEventStreamer{}, Writer: performance,
		Environment: "prod", Logger: slog.New(slog.DiscardHandler), RequestReconcile: func() { requests++ },
	})
	if err := collector.runStream(context.Background()); err == nil {
		t.Fatal("runStream() error = nil, want daemon failure")
	}
	cursor, err := performance.Get(context.Background(), dockerEventCursorSource)
	if err != nil {
		t.Fatal(err)
	}
	if cursor.LastError != "docker_events_unavailable" || cursor.UpdatedAt.IsZero() {
		t.Fatalf("failure cursor = %+v", cursor)
	}
	if requests != 1 {
		t.Errorf("reconcile requests = %d, want 1", requests)
	}
}

func TestContainerEventCollectorRequestsCoalescedReconcile(t *testing.T) {
	t.Parallel()

	db := openInventoryDB(t)
	fleet := NewFleetInventory(FleetInventoryConfig{
		Services:   store.NewServiceStore(db),
		Containers: store.NewContainerStore(db), Docker: &fakeInventoryDocker{}, Interval: time.Hour,
	})
	collector := NewContainerEventCollector(ContainerEventCollectorConfig{
		Services: store.NewServiceStore(db), Writer: store.NewPerformanceStore(db), Environment: "prod",
		RequestReconcile: fleet.RequestReconcile, Logger: slog.New(slog.DiscardHandler),
	})
	event := docker.Event{SourceEventID: "one", OccurredAt: time.Now().UTC(), Action: "start", ContainerID: "id", ContainerName: "api-1"}
	cursor := model.CollectorCursor{}
	if err := collector.persistEvent(context.Background(), nil, event, &cursor); err != nil {
		t.Fatalf("persistEvent() error = %v", err)
	}
	event.SourceEventID = "two"
	if err := collector.persistEvent(context.Background(), nil, event, &cursor); err != nil {
		t.Fatalf("persistEvent() error = %v", err)
	}
	if got := len(fleet.requests); got != 1 {
		t.Errorf("coalesced reconcile queue = %d, want 1", got)
	}
}

func TestContainerEventCollectorRepairsClosedStreamWithFullReconcile(t *testing.T) {
	t.Parallel()

	db := openInventoryDB(t)
	requests := 0
	collector := NewContainerEventCollector(ContainerEventCollectorConfig{
		Services: store.NewServiceStore(db), Docker: &fakeEventStreamer{},
		Writer: store.NewPerformanceStore(db), Environment: "prod",
		RequestReconcile: func() { requests++ }, Logger: slog.New(slog.DiscardHandler),
	})
	if err := collector.runStream(context.Background()); err != nil {
		t.Fatalf("runStream() error = %v, want nil", err)
	}
	if requests != 1 {
		t.Errorf("reconcile requests = %d, want 1 after stream close", requests)
	}
}

type blockingEventStreamer struct{}

func (blockingEventStreamer) EventStream(ctx context.Context, _ string) (<-chan docker.Event, <-chan error) {
	events := make(chan docker.Event)
	errorsOut := make(chan error)
	go func() {
		<-ctx.Done()
		close(events)
		close(errorsOut)
	}()
	return events, errorsOut
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
