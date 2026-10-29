package store

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/oralecarlangelo/touchgrass/internal/model"
)

func TestContainerStoreReconcilePersistsStoppedContainer(t *testing.T) {
	t.Parallel()

	store := NewContainerStore(openTestDB(t))
	now := time.Now().UTC().Truncate(time.Millisecond)
	err := store.Reconcile(context.Background(), model.InventoryBatch{
		ObservedAt: now,
		Complete:   true,
		Instances: []model.ContainerInstance{{
			ID:             "stopped-id",
			Name:           "worker-1",
			State:          "exited",
			Classification: model.ContainerClassificationUnknown,
		}},
	})
	if err != nil {
		t.Fatalf("Reconcile() error = %v, want nil", err)
	}

	got, err := store.Get(context.Background(), "stopped-id")
	if err != nil {
		t.Fatalf("Get() error = %v, want nil", err)
	}
	if got.State != "exited" || !got.Current || !got.Present {
		t.Errorf("Get() = %+v, want current present exited container", got)
	}
	if !got.FirstSeenAt.Equal(now) || !got.LastSeenAt.Equal(now) {
		t.Errorf("Get() timestamps = (%v, %v), want %v", got.FirstSeenAt, got.LastSeenAt, now)
	}
}

func TestContainerStoreMarksApprovedOrManagedAbsenceMissingExpected(t *testing.T) {
	t.Parallel()
	store := NewContainerStore(openTestDB(t))
	ctx := context.Background()
	now := time.Now().UTC()
	if err := store.Reconcile(ctx, model.InventoryBatch{ObservedAt: now, Complete: true, Instances: []model.ContainerInstance{
		{ID: "managed", Name: "managed", Classification: model.ContainerClassificationManaged},
		{ID: "approved", Name: "approved", Classification: model.ContainerClassificationApprovedObserved},
		{ID: "unknown", Name: "unknown", Classification: model.ContainerClassificationUnknown},
	}}); err != nil {
		t.Fatal(err)
	}
	if err := store.Reconcile(ctx, model.InventoryBatch{ObservedAt: now.Add(time.Minute), Complete: true}); err != nil {
		t.Fatal(err)
	}
	current, err := store.List(ctx, model.ContainerQuery{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(current) != 2 {
		t.Fatalf("current containers = %+v, want two missing expected", current)
	}
	for _, instance := range current {
		if instance.Classification != model.ContainerClassificationMissingExpected || instance.Present || !instance.Current {
			t.Fatalf("missing expected instance = %+v", instance)
		}
	}
}

func TestContainerStoreListAllRunningIsNotPageLimited(t *testing.T) {
	t.Parallel()
	store := NewContainerStore(openTestDB(t))
	instances := make([]model.ContainerInstance, 501)
	for index := range instances {
		instances[index] = model.ContainerInstance{
			ID: fmt.Sprintf("container-%03d", index), Name: fmt.Sprintf("container-%03d", index),
			State: "running", Classification: model.ContainerClassificationUnknown,
		}
	}
	if err := store.Reconcile(context.Background(), model.InventoryBatch{
		ObservedAt: time.Now().UTC(), Complete: true, Instances: instances,
	}); err != nil {
		t.Fatal(err)
	}
	got, err := store.ListAllRunning(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 501 {
		t.Fatalf("running containers = %d, want 501", len(got))
	}
}

func TestContainerStoreReconcileKeepsNameReuseDistinct(t *testing.T) {
	t.Parallel()

	store := NewContainerStore(openTestDB(t))
	ctx := context.Background()
	first := time.Now().UTC().Add(-time.Minute).Truncate(time.Millisecond)
	second := first.Add(time.Minute)

	for _, batch := range []model.InventoryBatch{
		{ObservedAt: first, Complete: true, Instances: []model.ContainerInstance{{ID: "old-id", Name: "api-1", State: "exited"}}},
		{ObservedAt: second, Complete: true, Instances: []model.ContainerInstance{{ID: "new-id", Name: "api-1", State: "running"}}},
	} {
		if err := store.Reconcile(ctx, batch); err != nil {
			t.Fatalf("Reconcile() error = %v, want nil", err)
		}
	}

	got, err := store.List(ctx, model.ContainerQuery{IncludeHistorical: true, Limit: 10})
	if err != nil {
		t.Fatalf("List() error = %v, want nil", err)
	}
	if len(got) != 2 {
		t.Fatalf("List() length = %d, want 2", len(got))
	}

	byID := map[string]model.ContainerInstance{got[0].ID: got[0], got[1].ID: got[1]}
	if byID["old-id"].Current || byID["old-id"].Present || byID["old-id"].AbsentAt == nil {
		t.Errorf("old instance = %+v, want historical and absent", byID["old-id"])
	}
	if !byID["new-id"].Current || !byID["new-id"].Present {
		t.Errorf("new instance = %+v, want current and present", byID["new-id"])
	}
}

func TestContainerStoreReconcileDoesNotMarkMissingOnIncompleteBatch(t *testing.T) {
	t.Parallel()

	store := NewContainerStore(openTestDB(t))
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Millisecond)
	if err := store.Reconcile(ctx, model.InventoryBatch{ObservedAt: now, Complete: true, Instances: []model.ContainerInstance{
		{ID: "api-id", Name: "api-1"},
		{ID: "worker-id", Name: "worker-1"},
	}}); err != nil {
		t.Fatalf("initial Reconcile() error = %v, want nil", err)
	}
	if err := store.Reconcile(ctx, model.InventoryBatch{ObservedAt: now.Add(time.Second), Complete: false, Instances: []model.ContainerInstance{
		{ID: "api-id", Name: "api-1"},
	}}); err != nil {
		t.Fatalf("incomplete Reconcile() error = %v, want nil", err)
	}

	worker, err := store.Get(ctx, "worker-id")
	if err != nil {
		t.Fatalf("Get() error = %v, want nil", err)
	}
	if !worker.Current || !worker.Present || worker.AbsentAt != nil {
		t.Errorf("worker after incomplete batch = %+v, want unchanged current presence", worker)
	}
}

func TestContainerStoreListReturnsNonNilEmpty(t *testing.T) {
	t.Parallel()

	store := NewContainerStore(openTestDB(t))
	got, err := store.List(context.Background(), model.ContainerQuery{})
	if err != nil {
		t.Fatalf("List() error = %v, want nil", err)
	}
	if got == nil || len(got) != 0 {
		t.Errorf("List() = %#v, want non-nil empty slice", got)
	}
}

func TestMigration0019PreservesLegacyFleetSamples(t *testing.T) {
	t.Parallel()

	db, err := Open(":memory:")
	if err != nil {
		t.Fatalf("Open() error = %v, want nil", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()
	if _, err := db.sql.ExecContext(ctx, schemaTable); err != nil {
		t.Fatalf("creating migration ledger: %v", err)
	}
	files, err := migrationList()
	if err != nil {
		t.Fatalf("migrationList() error = %v", err)
	}
	for _, file := range files {
		if file.version == "0019" {
			break
		}
		if err := db.apply(ctx, file); err != nil {
			t.Fatalf("apply(%s) error = %v", file.version, err)
		}
	}

	_, err = db.sql.ExecContext(ctx, `INSERT INTO container_samples
		(sampled_at, container_name, project, managed, service_id, state, cpu_percent, mem_bytes, mem_limit, restarts)
		VALUES (?, 'legacy-api-1', 'legacy', 1, 'tn-api', 'running', 3, 4, 5, 6)`, formatTime(time.Now().UTC()))
	if err != nil {
		t.Fatalf("inserting legacy sample: %v", err)
	}
	if err := db.apply(ctx, migration{version: "0019", name: "0019_container_inventory.sql"}); err != nil {
		t.Fatalf("apply(0019) error = %v", err)
	}

	var name string
	var containerID *string
	if err := db.sql.QueryRowContext(ctx, "SELECT container_name, container_id FROM container_samples").Scan(&name, &containerID); err != nil {
		t.Fatalf("querying legacy sample: %v", err)
	}
	if name != "legacy-api-1" || containerID != nil {
		t.Errorf("legacy sample = (%q, %v), want preserved name and null container id", name, containerID)
	}
}
