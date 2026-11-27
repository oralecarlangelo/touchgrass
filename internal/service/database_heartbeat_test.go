package service

import (
	"context"
	"errors"
	"io"
	"reflect"
	"testing"
	"time"

	"github.com/oralecarlangelo/touchgrass/internal/docker"
	"github.com/oralecarlangelo/touchgrass/internal/model"
)

const testDatabaseStopped = "exited"

func TestDatabaseHeartbeat(t *testing.T) {
	t.Parallel()

	runner := &fakeRunner{handler: func(_ []string, _ io.Reader, _ io.Writer) error {
		return errors.New("unexpected database command")
	}}
	svc, _, _ := testDatabase(t, runner, nil)
	now := time.Now()
	svc.ObserveContainers([]docker.Container{
		{ID: "primary-id", Name: "pg-test", Image: "postgres:16", State: updateStateRunning, Status: "Up 2 minutes (healthy)"},
		{ID: "replica-id", Name: "replica", Image: "docker.io/library/postgres:16", State: updateStateRunning},
		{ID: "cache-id", Name: "redis-test", Image: "redis:7", State: updateStateRunning, Status: "Up 2 minutes (unhealthy)"},
		{ID: "stopped-id", Name: "dev-mongo", Image: "mongo:8", State: testDatabaseStopped},
		{ID: "custom-id", Name: "example", Image: "example.org/custom/postgres:16", State: updateStateRunning},
	}, now, time.Minute)
	svc.sampleSQLite(t.Context())

	snapshot := svc.Databases()
	checkDatabaseObservations(t, snapshot)

	if !reflect.DeepEqual(snapshot, svc.Databases()) {
		t.Error("reads must reuse the last observation")
	}

	svc.ObserveContainers(nil, now.Add(time.Second), time.Minute)

	stale := svc.Databases()
	if len(stale.Databases) != 5 || stale.InventoryStatus != databaseStale ||
		stale.Databases[0].Reachable || stale.Databases[4].Container != "replica" ||
		stale.Databases[4].Status != databaseStale {
		t.Errorf("inventory after failed collection = %+v, want retained stale observations", stale)
	}

	svc.ObserveContainers([]docker.Container{}, now.Add(time.Second), time.Minute)

	empty := svc.Databases()
	if len(empty.Databases) != 3 || empty.InventoryStatus != "current" || empty.Databases[0].Status != "missing" {
		t.Errorf("inventory after empty collection = %+v, want missing configured targets", empty)
	}

	if calls := runner.count(); calls != 0 {
		t.Errorf("monitoring executed %d database commands, want zero", calls)
	}
}

func TestDatabaseHeartbeatKeepsKnownImageWhileInventoryEnriches(t *testing.T) {
	t.Parallel()

	svc, _, _ := testDatabase(t, &fakeRunner{}, nil)
	now := time.Now()
	item := docker.Container{
		ID: "database-instance", Name: "demo-db", Image: "postgres:17-alpine", ImageID: "sha256:123456789abc",
		State: updateStateRunning, Status: "Up (healthy)",
	}
	svc.ObserveContainers([]docker.Container{item}, now, time.Minute)
	item.Image = "123456789abc"
	item.Status = "Up (unhealthy)"
	svc.ObserveContainers([]docker.Container{item}, now.Add(time.Second), time.Minute)

	got := svc.Databases().Databases
	if len(got) != 4 || got[3].Image != "postgres:17-alpine" || got[3].Status != "unhealthy" {
		t.Fatalf("observed databases = %+v, want retained database identity with current health", got)
	}
}

func checkDatabaseObservations(t *testing.T, snapshot model.DatabaseSnapshot) {
	t.Helper()

	if len(snapshot.Databases) != 5 || snapshot.InventoryStatus != "current" {
		t.Fatalf("snapshot = %+v, want three named targets and two discoveries", snapshot)
	}

	for index, want := range []struct {
		name, status          string
		configured, reachable bool
	}{
		{name: "pg-test", status: databaseHealthy, configured: true, reachable: true},
		{name: "redis-test", status: "unhealthy", configured: true, reachable: false},
		{name: "", status: databaseHealthy, configured: true, reachable: true},
		{name: "dev-mongo", status: testDatabaseStopped, configured: false, reachable: false},
		{name: "replica", status: updateStateRunning, configured: false, reachable: false},
	} {
		got := snapshot.Databases[index]
		if got.Container != want.name || got.Status != want.status ||
			got.Configured != want.configured || got.Reachable != want.reachable {
			t.Errorf("observation %d = %+v, want %+v", index, got, want)
		}
	}

	if sqlite := snapshot.Databases[2]; sqlite.Version == nil || sqlite.Integrity != nil {
		t.Errorf("sqlite = %+v, want version without an integrity scan", sqlite)
	}
}

type stalledSQLite struct {
	started chan struct{}
}

func (s stalledSQLite) SQLiteVersion(ctx context.Context) (string, error) {
	close(s.started)
	<-ctx.Done()

	return "", ctx.Err()
}

func TestDatabaseHeartbeatReadDuringProbe(t *testing.T) {
	t.Parallel()

	prober := stalledSQLite{started: make(chan struct{})}
	svc := NewDatabase(DatabaseConfig{SQLite: prober})

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)

		svc.RunHeartbeat(ctx)
	}()

	select {
	case <-prober.started:
	case <-time.After(time.Second):
		t.Fatal("heartbeat did not start")
	}

	read := make(chan string, 1)
	go func() { read <- svc.Databases().Databases[2].Status }()

	select {
	case status := <-read:
		if status != "waiting" {
			t.Errorf("status = %q, want waiting while probe is in flight", status)
		}
	case <-time.After(time.Second):
		t.Fatal("snapshot read was blocked by the heartbeat")
	}

	cancel()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("heartbeat did not stop on cancellation")
	}
}
