package store

import (
	"context"
	"testing"
	"time"

	"github.com/oralecarlangelo/touchgrass/internal/model"
)

// testContainerBlue is a stubbed container name.
const testContainerBlue = "api-blue-1"

func TestMetricInsertList(t *testing.T) {
	t.Parallel()

	db := openTestDB(t)
	ctx := context.Background()
	metrics := NewMetricStore(db)

	now := time.Now().Truncate(time.Second)

	old := model.Metric{
		ServiceID: testServiceAPI, ContainerName: testContainerBlue, SampledAt: now.Add(-time.Hour),
		CPUPercent: 10, MemBytes: 100, MemLimit: 1000, DiskBytes: 50, Restarts: 0, UptimeSecs: 100,
	}
	fresh := model.Metric{
		ServiceID: testServiceAPI, ContainerName: testContainerBlue, SampledAt: now,
		CPUPercent: 20, MemBytes: 200, MemLimit: 1000, DiskBytes: 60, Restarts: 1, UptimeSecs: 200,
	}

	if err := metrics.Insert(ctx, old); err != nil {
		t.Fatalf("Insert() error = %v, want nil", err)
	}

	if err := metrics.Insert(ctx, fresh); err != nil {
		t.Fatalf("Insert() error = %v, want nil", err)
	}

	got, err := metrics.ListByService(ctx, testServiceAPI, time.Time{}, 100)
	if err != nil {
		t.Fatalf("ListByService() error = %v, want nil", err)
	}

	if len(got) != 2 || got[0].CPUPercent != 10 || got[1].CPUPercent != 20 {
		t.Fatalf("ListByService() = %+v, want chronological pair", got)
	}

	since, err := metrics.ListByService(ctx, testServiceAPI, now, 100)
	if err != nil {
		t.Fatalf("ListByService() error = %v, want nil", err)
	}

	if len(since) != 1 || since[0].CPUPercent != 20 {
		t.Errorf("ListByService(since) = %+v, want fresh sample only", since)
	}
}

func TestMetricTrim(t *testing.T) {
	t.Parallel()

	db := openTestDB(t)
	ctx := context.Background()
	metrics := NewMetricStore(db)

	now := time.Now().Truncate(time.Second)

	if err := metrics.Insert(ctx, model.Metric{
		ServiceID: testServiceAPI, ContainerName: testContainerBlue, SampledAt: now.Add(-time.Hour),
	}); err != nil {
		t.Fatalf("Insert() error = %v, want nil", err)
	}

	if err := metrics.Insert(ctx, model.Metric{
		ServiceID: testServiceAPI, ContainerName: testContainerBlue, SampledAt: now,
	}); err != nil {
		t.Fatalf("Insert() error = %v, want nil", err)
	}

	trimmed, err := metrics.TrimBefore(ctx, now)
	if err != nil {
		t.Fatalf("TrimBefore() error = %v, want nil", err)
	}

	if trimmed != 1 {
		t.Errorf("TrimBefore() = %d, want 1", trimmed)
	}

	remaining, err := metrics.ListByService(ctx, testServiceAPI, time.Time{}, 100)
	if err != nil {
		t.Fatalf("ListByService() error = %v, want nil", err)
	}

	if len(remaining) != 1 {
		t.Errorf("remaining samples = %d, want 1", len(remaining))
	}
}
