//go:build integration

package service

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/oralecarlangelo/touchgrass/internal/docker"
	"github.com/oralecarlangelo/touchgrass/internal/model"
	"github.com/oralecarlangelo/touchgrass/internal/store"
)

type securityReplayFixture struct {
	Container       model.ContainerInstance `json:"container"`
	Processes       []docker.Process        `json:"processes"`
	Changes         []docker.LayerChange    `json:"changes"`
	SensitiveInputs struct {
		Environment      []string          `json:"environment"`
		Labels           map[string]string `json:"labels"`
		MountSource      string            `json:"mount_source"`
		ProcessArguments []string          `json:"process_arguments"`
	} `json:"sensitive_inputs"`
}

type securityReplayResult struct {
	Findings []model.SecurityFinding `json:"findings"`
	Summary  model.SecuritySummary   `json:"summary"`
}

func TestSecurityReplayCorrelatesOneCriticalFinding(t *testing.T) {
	t.Parallel()
	fixture := loadSecurityReplay(t)
	security, db, _ := openSecurityReplay(t)
	result := runSecurityReplay(t, security, fixture)

	criticalCorrelations := make([]model.SecurityFinding, 0, 1)
	for _, finding := range result.Findings {
		if finding.Severity == model.SecuritySeverityCritical &&
			finding.RuleID == "correlation.added_executable_running" {
			criticalCorrelations = append(criticalCorrelations, finding)
		}
	}
	if len(criticalCorrelations) != 1 {
		t.Fatalf("critical correlation findings = %+v, want exactly one episode", criticalCorrelations)
	}
	finding := criticalCorrelations[0]
	if finding.Episode != 1 || finding.Evidence["contributing_rules"] !=
		"diff.added_executable,process.high_risk_path" {
		t.Fatalf("critical correlation = %+v, want first episode with explicit contributing rules", finding)
	}
	if result.Summary.Critical < 1 {
		t.Fatalf("security summary = %+v, want critical replay signal", result.Summary)
	}
	_ = db
}

func TestSecurityReplayDoesNotPersistSecrets(t *testing.T) {
	t.Parallel()
	fixture := loadSecurityReplay(t)
	canaries := []string{
		"CANARY_ENV_SECRET_7b8c9d",
		"CANARY_LABEL_SECRET_8c9d0e",
		"CANARY_MOUNT_SECRET_9d0e1f",
		"CANARY_ARGUMENT_SECRET_0e1f2a",
	}
	fixture.SensitiveInputs.Environment = []string{"TOKEN=" + canaries[0]}
	fixture.SensitiveInputs.Labels = map[string]string{"private.label": canaries[1]}
	fixture.SensitiveInputs.MountSource = "/host/" + canaries[2]
	fixture.SensitiveInputs.ProcessArguments = []string{"--token=" + canaries[3]}

	security, db, paths := openSecurityReplay(t)
	result := runSecurityReplay(t, security, fixture)
	jsonResponse, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	renderedFixture, err := json.Marshal(struct {
		Container model.ContainerInstance `json:"container"`
		Processes []docker.Process        `json:"processes"`
		Changes   []docker.LayerChange    `json:"changes"`
	}{fixture.Container, fixture.Processes, fixture.Changes})
	if err != nil {
		t.Fatal(err)
	}

	backupPath := filepath.Join(filepath.Dir(paths.database), "security-replay-backup.db")
	if err := db.Backup(context.Background(), backupPath); err != nil {
		t.Fatal(err)
	}
	paths.backup = backupPath
	for _, artifact := range []struct {
		name string
		data []byte
	}{
		{name: "logs", data: paths.logs.Bytes()},
		{name: "JSON response", data: jsonResponse},
		{name: "rendered fixture", data: renderedFixture},
	} {
		assertNoCanaries(t, artifact.name, artifact.data, canaries)
	}
	for _, path := range []string{paths.database, paths.database + "-wal", paths.database + "-shm", paths.backup} {
		contents, readErr := os.ReadFile(path) //nolint:gosec // every candidate is derived from this test's TempDir database path.
		if readErr != nil {
			if os.IsNotExist(readErr) {
				continue
			}
			t.Fatal(readErr)
		}
		assertNoCanaries(t, filepath.Base(path), contents, canaries)
	}
}

func TestContainerSecuritySoakBaseline(t *testing.T) {
	if os.Getenv("TOUCHGRASS_SECURITY_SOAK_BASELINE") != "1" {
		t.Skip("baseline is only used by scripts/container-security-soak.sh")
	}
	_, db, _ := openSecurityReplay(t)
	if _, err := db.SQLiteHealth(context.Background()); err != nil {
		t.Fatal(err)
	}
	holdSecuritySoak(t)
}

func TestContainerSecuritySoak(t *testing.T) {
	count := securitySoakCount(t)
	security, db, paths := openSecurityReplay(t)
	source := securitySoakSource(t, count)
	containers := store.NewContainerStore(db)
	inventory := NewFleetInventory(FleetInventoryConfig{
		Services: store.NewServiceStore(db), Containers: containers, Docker: source,
		Interval: 30 * time.Second, InspectLimit: 4, Security: store.NewSecurityStore(db),
		Evaluator: security, Logger: slog.New(slog.DiscardHandler),
	})

	beforeGoroutines := runtime.NumGoroutine()
	beforeBytes := securitySoakDatabaseBytes(t, paths.database)
	reconciliation := make([]time.Duration, 0, 5)
	for range 5 {
		started := time.Now()
		summary, err := inventory.Reconcile(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if summary.PresentCount != count {
			t.Fatalf("present containers = %d, want %d", summary.PresentCount, count)
		}
		reconciliation = append(reconciliation, time.Since(started))
	}

	processScanner := NewProcessScanner(ProcessScannerConfig{
		Inventory: inventory, Docker: source, Security: security, Policies: store.NewSecurityStore(db),
		Interval: 30 * time.Second, Workers: 4, Logger: slog.New(slog.DiscardHandler),
	})
	for range 5 {
		if err := processScanner.Scan(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	diffScanner := NewDiffScanner(DiffScannerConfig{
		Inventory: inventory, Docker: source, Security: security, Policies: store.NewSecurityStore(db),
		Interval: 10 * time.Minute, Workers: 4, Logger: slog.New(slog.DiscardHandler),
	})
	if err := diffScanner.Scan(context.Background()); err != nil {
		t.Fatal(err)
	}

	eventSource := &securitySoakEventSource{event: docker.Event{
		SourceEventID: "security-soak-event", OccurredAt: time.Now().UTC(), Action: "start",
		ContainerID: "security-soak-000", ContainerName: "generic-soak-000",
	}}
	eventCollector := NewContainerEventCollector(ContainerEventCollectorConfig{
		Services: store.NewServiceStore(db), Docker: eventSource,
		Writer: store.NewPerformanceStore(db), Environment: "soak", Logger: slog.New(slog.DiscardHandler),
	})
	eventStarted := time.Now()
	if err := eventCollector.runStream(context.Background()); err != nil {
		t.Fatal(err)
	}
	eventDelay := time.Since(eventStarted)

	sort.Slice(reconciliation, func(i, j int) bool { return reconciliation[i] < reconciliation[j] })
	p95 := reconciliation[len(reconciliation)-1]
	afterBytes := securitySoakDatabaseBytes(t, paths.database)
	growth := afterBytes - beforeBytes
	time.Sleep(50 * time.Millisecond)
	goroutineDelta := runtime.NumGoroutine() - beforeGoroutines
	if p95 > 5*time.Second {
		t.Fatalf("reconciliation p95 = %v, want <= 5s", p95)
	}
	if eventDelay > 5*time.Second {
		t.Fatalf("event update = %v, want <= 5s", eventDelay)
	}
	if growth > 16<<20 {
		t.Fatalf("SQLite growth = %d bytes, want <= 16 MiB", growth)
	}
	if goroutineDelta > 2 {
		t.Fatalf("goroutine delta = %d, want <= 2", goroutineDelta)
	}
	t.Logf("SECURITY_SOAK containers=%d reconciliation_p95_ms=%.2f event_update_ms=%.2f goroutine_delta=%d sqlite_growth_bytes=%d",
		count, float64(p95)/float64(time.Millisecond), float64(eventDelay)/float64(time.Millisecond), goroutineDelta, growth)
	holdSecuritySoak(t)
}

type securityReplayArtifacts struct {
	database string
	backup   string
	logs     *bytes.Buffer
}

func openSecurityReplay(t *testing.T) (*Security, *store.DB, *securityReplayArtifacts) {
	t.Helper()
	database := filepath.Join(t.TempDir(), "security-replay.db")
	db, err := store.Open(database)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("closing replay database: %v", err)
		}
	})
	if _, err := db.MigrateUp(context.Background()); err != nil {
		t.Fatal(err)
	}
	securityStore := store.NewSecurityStore(db)
	if _, err := securityStore.Activate(context.Background(), "integration-replay"); err != nil {
		t.Fatal(err)
	}
	logs := &bytes.Buffer{}
	logger := slog.New(slog.NewJSONHandler(logs, nil))
	security := NewSecurity(SecurityConfig{
		Store: securityStore, Notifications: store.NewNotificationStore(db),
		Cursors: store.NewCursorStore(db), Emit: func(model.Event) {}, Logger: logger,
	})
	return security, db, &securityReplayArtifacts{database: database, logs: logs}
}

func loadSecurityReplay(t *testing.T) securityReplayFixture {
	t.Helper()
	contents, err := os.ReadFile(filepath.Join("testdata", "compromised-web-container.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fixture securityReplayFixture
	if err := json.Unmarshal(contents, &fixture); err != nil {
		t.Fatal(err)
	}
	fixture.Container.FirstSeenAt = time.Now().UTC()
	fixture.Container.LastSeenAt = fixture.Container.FirstSeenAt
	return fixture
}

func runSecurityReplay(t *testing.T, security *Security, fixture securityReplayFixture) securityReplayResult {
	t.Helper()
	process := EvaluateProcesses(fixture.Container.ID, docker.ProcessSnapshot{
		Processes: fixture.Processes, Complete: true,
	}, model.SecurityPolicy{}, len(fixture.Processes), false)
	diff := EvaluateLayerChanges(fixture.Container.ID, fixture.Changes, model.SecurityPolicy{}, true)
	observations := append(process, diff...)
	observations = append(observations, CorrelateProcessAndDiff(fixture.Container.ID, process, diff)...)
	for _, observation := range observations {
		observation.ObservedAt = fixture.Container.LastSeenAt
		if _, err := security.Evaluate(context.Background(), model.SecurityEvaluation{
			Observation: observation, BaselineFingerprint: fixture.Container.ConfigurationFingerprint,
		}); err != nil {
			t.Fatal(err)
		}
	}
	findings, err := security.Findings(context.Background(), model.SecurityFindingQuery{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	summary, err := security.Summary(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return securityReplayResult{Findings: findings, Summary: summary}
}

func assertNoCanaries(t *testing.T, artifact string, contents []byte, canaries []string) {
	t.Helper()
	for _, canary := range canaries {
		if bytes.Contains(contents, []byte(canary)) || strings.Contains(string(contents), canary) {
			t.Fatalf("%s contains privacy canary %q", artifact, canary)
		}
	}
}

type securitySoakDocker interface {
	docker.InventoryLister
	docker.ProcessLister
	docker.DiffLister
}

type fakeSecuritySoakDocker struct {
	containers []docker.Container
}

func (f *fakeSecuritySoakDocker) ListAll(context.Context) ([]docker.Container, error) {
	return append([]docker.Container{}, f.containers...), nil
}

func (f *fakeSecuritySoakDocker) Inspect(_ context.Context, id string) (docker.Inspection, error) {
	limit := int64(128)
	return docker.Inspection{
		ID: id, ImageID: "sha256:generic-security-soak", User: "1000", ReadonlyRootFS: true,
		NetworkMode: "bridge", RestartPolicy: "unless-stopped", MemoryBytes: 256 << 20,
		NanoCPUs: 1_000_000_000, PIDsLimit: &limit, Mounts: []docker.InspectionMount{},
	}, nil
}

func (f *fakeSecuritySoakDocker) Processes(context.Context, string) (docker.ProcessSnapshot, error) {
	return docker.ProcessSnapshot{Processes: []docker.Process{{PID: 1, User: "1000", Command: "/usr/bin/generic-web"}}, Complete: true}, nil
}

func (f *fakeSecuritySoakDocker) Diff(context.Context, string) ([]docker.LayerChange, error) {
	return []docker.LayerChange{}, nil
}

type labeledSecuritySoakDocker struct {
	client *docker.Client
	key    string
	value  string
}

func (l *labeledSecuritySoakDocker) ListAll(ctx context.Context) ([]docker.Container, error) {
	values, err := l.client.ListAll(ctx)
	if err != nil {
		return nil, err
	}
	filtered := make([]docker.Container, 0, len(values))
	for _, value := range values {
		if value.Labels[l.key] == l.value {
			filtered = append(filtered, value)
		}
	}
	return filtered, nil
}

func (l *labeledSecuritySoakDocker) Inspect(ctx context.Context, id string) (docker.Inspection, error) {
	return l.client.Inspect(ctx, id)
}

func (l *labeledSecuritySoakDocker) Processes(ctx context.Context, id string) (docker.ProcessSnapshot, error) {
	return l.client.Processes(ctx, id)
}

func (l *labeledSecuritySoakDocker) Diff(ctx context.Context, id string) ([]docker.LayerChange, error) {
	return l.client.Diff(ctx, id)
}

func securitySoakSource(t *testing.T, count int) securitySoakDocker {
	t.Helper()
	label := os.Getenv("TOUCHGRASS_SECURITY_SOAK_LABEL")
	if label != "" {
		parts := strings.SplitN(label, "=", 2)
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
			t.Fatalf("invalid TOUCHGRASS_SECURITY_SOAK_LABEL %q", label)
		}
		client, err := docker.New()
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = client.Close() })
		return &labeledSecuritySoakDocker{client: client, key: parts[0], value: parts[1]}
	}
	values := make([]docker.Container, 0, count)
	for index := range count {
		id := "security-soak-" + strconv.Itoa(index)
		values = append(values, docker.Container{
			ID: id, Name: "generic-soak-" + strconv.Itoa(index), Image: "generic-web@sha256:fixture",
			ImageID: "sha256:generic-security-soak", State: "running", Status: "Up",
			Labels: map[string]string{},
		})
	}
	return &fakeSecuritySoakDocker{containers: values}
}

func securitySoakCount(t *testing.T) int {
	t.Helper()
	value := 100
	if configured := os.Getenv("TOUCHGRASS_SECURITY_SOAK_CONTAINERS"); configured != "" {
		parsed, err := strconv.Atoi(configured)
		if err != nil {
			t.Fatal(err)
		}
		value = parsed
	}
	if value < 1 || value > 100 {
		t.Fatalf("security soak container count = %d, want 1..100", value)
	}
	return value
}

func securitySoakDatabaseBytes(t *testing.T, database string) int64 {
	t.Helper()
	var total int64
	for _, path := range []string{database, database + "-wal", database + "-shm"} {
		info, err := os.Stat(path)
		if err == nil {
			total += info.Size()
		} else if !os.IsNotExist(err) {
			t.Fatal(err)
		}
	}
	return total
}

func holdSecuritySoak(t *testing.T) {
	t.Helper()
	ready := os.Getenv("TOUCHGRASS_SECURITY_SOAK_READY_FILE")
	if ready == "" {
		return
	}
	if err := os.WriteFile(ready, []byte("ready\n"), 0o600); err != nil { //nolint:gosec // the release harness intentionally supplies its private ready-file path.
		t.Fatal(err)
	}
	duration := 10 * time.Second
	if raw := os.Getenv("TOUCHGRASS_SECURITY_SOAK_HOLD"); raw != "" {
		parsed, err := time.ParseDuration(raw)
		if err != nil {
			t.Fatal(err)
		}
		duration = parsed
	}
	time.Sleep(duration)
}

type securitySoakEventSource struct {
	event docker.Event
	once  sync.Once
}

func (s *securitySoakEventSource) EventStream(context.Context, string) (<-chan docker.Event, <-chan error) {
	events := make(chan docker.Event, 1)
	errors := make(chan error)
	s.once.Do(func() { events <- s.event })
	close(events)
	close(errors)
	return events, errors
}
