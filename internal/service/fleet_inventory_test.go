package service

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/docker/docker/errdefs"
	"github.com/oralecarlangelo/touchgrass/internal/docker"
	"github.com/oralecarlangelo/touchgrass/internal/model"
	"github.com/oralecarlangelo/touchgrass/internal/store"
)

type fakeInventoryDocker struct {
	mu          sync.Mutex
	containers  []docker.Container
	listErr     error
	inspections map[string]docker.Inspection
	inspectErrs map[string]error
	delay       time.Duration
	active      int
	peak        int
}

func (f *fakeInventoryDocker) ListAll(context.Context) ([]docker.Container, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]docker.Container{}, f.containers...), f.listErr
}

func (f *fakeInventoryDocker) Inspect(ctx context.Context, id string) (docker.Inspection, error) {
	f.mu.Lock()
	f.active++
	if f.active > f.peak {
		f.peak = f.active
	}
	delay := f.delay
	inspection := f.inspections[id]
	err := f.inspectErrs[id]
	f.mu.Unlock()

	if delay > 0 {
		select {
		case <-ctx.Done():
			err = ctx.Err()
		case <-time.After(delay):
		}
	}

	f.mu.Lock()
	f.active--
	f.mu.Unlock()
	if inspection.ID == "" {
		inspection.ID = id
	}
	return inspection, err
}

func (f *fakeInventoryDocker) peakConcurrency() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.peak
}

func TestFleetInventoryReconcileAllStates(t *testing.T) {
	t.Parallel()

	db := openInventoryDB(t)
	dockerClient := &fakeInventoryDocker{containers: []docker.Container{
		{ID: "managed-running", Name: testBlueContainerName, State: "running", Labels: map[string]string{
			docker.LabelComposeProject: testComposeProject, docker.LabelComposeService: testBlueService,
		}},
		{ID: "observed-exited", Name: "batch-1", State: "exited", Labels: map[string]string{}},
		{ID: "observed-dead", Name: "old-worker-1", State: "dead", Labels: map[string]string{}},
	}, inspections: map[string]docker.Inspection{}}
	inventory := NewFleetInventory(FleetInventoryConfig{
		Services: store.NewServiceStore(db), Containers: store.NewContainerStore(db),
		Docker: dockerClient, Interval: time.Minute, InspectLimit: 2,
	})

	summary, err := inventory.Reconcile(context.Background())
	if err != nil {
		t.Fatalf("Reconcile() error = %v, want nil", err)
	}
	if !summary.Complete || summary.PresentCount != 3 {
		t.Errorf("Reconcile() summary = %+v, want complete with 3 present", summary)
	}
	got, err := inventory.Containers(context.Background(), model.ContainerQuery{Limit: 10})
	if err != nil {
		t.Fatalf("Containers() error = %v, want nil", err)
	}
	if len(got) != 3 {
		t.Fatalf("Containers() length = %d, want 3", len(got))
	}
	byID := map[string]model.ContainerInstance{}
	for _, item := range got {
		byID[item.ID] = item
	}
	if byID["managed-running"].Classification != model.ContainerClassificationManaged ||
		byID["observed-exited"].State != "exited" || byID["observed-dead"].State != "dead" {
		t.Errorf("Containers() = %+v, want mapped ownership and lifecycle states", byID)
	}
}

func TestFleetInventoryReconcileSkipsVanishedContainer(t *testing.T) {
	t.Parallel()

	db := openInventoryDB(t)
	dockerClient := &fakeInventoryDocker{
		containers:  []docker.Container{{ID: "stable", Name: "stable-1"}, {ID: "vanished", Name: "vanished-1"}},
		inspections: map[string]docker.Inspection{},
		inspectErrs: map[string]error{"vanished": errdefs.NotFound(errors.New("gone"))},
	}
	inventory := NewFleetInventory(FleetInventoryConfig{
		Services:   store.NewServiceStore(db),
		Containers: store.NewContainerStore(db), Docker: dockerClient, Interval: time.Minute, InspectLimit: 2,
	})

	if _, err := inventory.Reconcile(context.Background()); err != nil {
		t.Fatalf("Reconcile() error = %v, want nil for inspect/list race", err)
	}
	got, err := inventory.Containers(context.Background(), model.ContainerQuery{})
	if err != nil {
		t.Fatalf("Containers() error = %v", err)
	}
	if len(got) != 1 || got[0].ID != "stable" {
		t.Errorf("Containers() = %+v, want only stable instance", got)
	}
}

func TestFleetInventoryInspectFailurePreservesLastKnownPosture(t *testing.T) {
	t.Parallel()
	db := openInventoryDB(t)
	dockerClient := &fakeInventoryDocker{
		containers:  []docker.Container{{ID: "stable", Name: "stable-1", State: "running"}},
		inspections: map[string]docker.Inspection{"stable": {Privileged: true, User: "1000"}},
		inspectErrs: map[string]error{},
	}
	inventory := NewFleetInventory(FleetInventoryConfig{
		Services: store.NewServiceStore(db), Containers: store.NewContainerStore(db),
		Docker: dockerClient, Interval: time.Minute, InspectLimit: 1,
	})
	if _, err := inventory.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	dockerClient.inspectErrs["stable"] = errors.New("inspect unavailable")
	if _, err := inventory.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	got, err := inventory.Container(context.Background(), "stable")
	if err != nil {
		t.Fatal(err)
	}
	if !got.Inspection.Privileged || got.Inspection.User != "1000" || got.Inspection.CoverageComplete {
		t.Fatalf("inspection = %+v, want last-known values marked incomplete", got.Inspection)
	}
}

func TestFleetInventoryCreatesUnknownAndMissingExpectedFindings(t *testing.T) {
	t.Parallel()
	db := openInventoryDB(t)
	securityStore := store.NewSecurityStore(db)
	if _, err := securityStore.Activate(context.Background(), "admin"); err != nil {
		t.Fatal(err)
	}
	security := NewSecurity(SecurityConfig{
		Store: securityStore, Cursors: store.NewCursorStore(db), Emit: func(model.Event) {},
	})
	dockerClient := &fakeInventoryDocker{
		containers:  []docker.Container{{ID: "observed", Name: "observed", ImageID: "sha256:image", State: "running"}},
		inspections: map[string]docker.Inspection{"observed": {}}, inspectErrs: map[string]error{},
	}
	inventory := NewFleetInventory(FleetInventoryConfig{
		Services: store.NewServiceStore(db), Containers: store.NewContainerStore(db), Docker: dockerClient,
		Interval: time.Millisecond, InspectLimit: 1, Security: securityStore, Evaluator: security,
	})
	if _, err := inventory.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	findings, err := security.Findings(context.Background(), model.SecurityFindingQuery{Status: model.SecurityFindingStatusOpen})
	if err != nil || !containsFindingRule(findings, "inventory.unknown_container") {
		t.Fatalf("unknown findings=%+v err=%v", findings, err)
	}
	instance, err := inventory.Container(context.Background(), "observed")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := securityStore.Approve(context.Background(), model.ContainerApproval{
		ContainerID: "observed", ImageFingerprint: instance.ImageID,
		ConfigurationFingerprint: instance.ConfigurationFingerprint, Actor: "admin",
	}); err != nil {
		t.Fatal(err)
	}
	dockerClient.containers = []docker.Container{}
	if _, err := inventory.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	time.Sleep(3 * time.Millisecond)
	if _, err := inventory.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	findings, err = security.Findings(context.Background(), model.SecurityFindingQuery{Status: model.SecurityFindingStatusOpen})
	if err != nil || !containsFindingRule(findings, "inventory.missing_expected") {
		t.Fatalf("missing expected findings=%+v err=%v", findings, err)
	}
	dockerClient.containers = []docker.Container{{ID: "replacement", Name: "observed", ImageID: "sha256:new", State: "running"}}
	dockerClient.inspections["replacement"] = docker.Inspection{}
	if _, err := inventory.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	resolved, err := security.Findings(context.Background(), model.SecurityFindingQuery{Status: model.SecurityFindingStatusResolved})
	if err != nil || !containsFindingRule(resolved, "inventory.missing_expected") {
		t.Fatalf("replacement recovery findings=%+v err=%v", resolved, err)
	}
}

func TestFleetInventoryReconcileNameReuse(t *testing.T) {
	t.Parallel()

	db := openInventoryDB(t)
	dockerClient := &fakeInventoryDocker{inspections: map[string]docker.Inspection{}}
	inventory := NewFleetInventory(FleetInventoryConfig{
		Services:   store.NewServiceStore(db),
		Containers: store.NewContainerStore(db), Docker: dockerClient, Interval: time.Minute, InspectLimit: 1,
	})

	dockerClient.containers = []docker.Container{{ID: "old", Name: "api-1", State: "exited"}}
	if _, err := inventory.Reconcile(context.Background()); err != nil {
		t.Fatalf("first Reconcile() error = %v", err)
	}
	dockerClient.containers = []docker.Container{{ID: "new", Name: "api-1", State: "running"}}
	if _, err := inventory.Reconcile(context.Background()); err != nil {
		t.Fatalf("second Reconcile() error = %v", err)
	}

	got, err := inventory.Containers(context.Background(), model.ContainerQuery{IncludeHistorical: true})
	if err != nil {
		t.Fatalf("Containers() error = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("Containers() length = %d, want 2", len(got))
	}
	old, err := inventory.Container(context.Background(), "old")
	if err != nil {
		t.Fatalf("Container(old) error = %v", err)
	}
	if old.Current || old.Present {
		t.Errorf("old container = %+v, want historical", old)
	}
}

func TestFleetInventoryMarksIncompleteOnListFailure(t *testing.T) {
	t.Parallel()

	db := openInventoryDB(t)
	inventory := NewFleetInventory(FleetInventoryConfig{
		Services:   store.NewServiceStore(db),
		Containers: store.NewContainerStore(db), Docker: &fakeInventoryDocker{listErr: errors.New("daemon down")},
		Interval: time.Minute, InspectLimit: 1,
	})

	if _, err := inventory.Reconcile(context.Background()); err == nil {
		t.Fatal("Reconcile() error = nil, want daemon error")
	}
	summary, err := store.NewContainerStore(db).Summary(context.Background())
	if err != nil {
		t.Fatalf("Summary() error = %v", err)
	}
	if summary.Complete || summary.LastErrorCategory != "docker_inventory_unavailable" {
		t.Errorf("Summary() = %+v, want incomplete daemon category", summary)
	}
}

func TestFleetInventoryBoundsInspectConcurrency(t *testing.T) {
	t.Parallel()

	db := openInventoryDB(t)
	containers := make([]docker.Container, 12)
	for index := range containers {
		containers[index] = docker.Container{ID: string(rune('a' + index)), Name: "worker-" + string(rune('a'+index))}
	}
	dockerClient := &fakeInventoryDocker{containers: containers, inspections: map[string]docker.Inspection{}, delay: 10 * time.Millisecond}
	inventory := NewFleetInventory(FleetInventoryConfig{
		Services:   store.NewServiceStore(db),
		Containers: store.NewContainerStore(db), Docker: dockerClient, Interval: time.Minute, InspectLimit: 3,
	})

	if _, err := inventory.Reconcile(context.Background()); err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if got := dockerClient.peakConcurrency(); got > 3 || got < 2 {
		t.Errorf("peak Inspect concurrency = %d, want 2..3", got)
	}
}

func TestFleetInventoryStopsOnCancellation(t *testing.T) {
	db := openInventoryDB(t)
	inventory := NewFleetInventory(FleetInventoryConfig{
		Services:   store.NewServiceStore(db),
		Containers: store.NewContainerStore(db), Docker: &fakeInventoryDocker{inspections: map[string]docker.Inspection{}},
		Interval: time.Hour, InspectLimit: 1,
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		inventory.Run(ctx)
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Run() did not stop after cancellation")
	}
}
