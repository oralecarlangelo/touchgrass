package store

import (
	"context"
	"testing"
	"time"

	"github.com/oralecarlangelo/touchgrass/internal/model"
)

func TestHTTPHistogramBoundariesAndMerge(t *testing.T) {
	t.Parallel()

	histogram := model.HTTPHistogram{}
	for _, value := range []float64{10, 10.001, 25, 50, 100, 250, 500, 1000, 2500, 5000, 5000.001} {
		histogram.Observe(value)
	}

	want := [10]int64{1, 2, 1, 1, 1, 1, 1, 1, 1, 1}
	if histogram.Counts != want {
		t.Fatalf("histogram counts = %v, want %v", histogram.Counts, want)
	}

	other := model.HTTPHistogram{Counts: [10]int64{1, 0, 0, 0, 0, 0, 0, 0, 0, 1}}
	histogram.Merge(other)
	want[0]++
	want[9]++
	if histogram.Counts != want {
		t.Fatalf("merged histogram = %v, want %v", histogram.Counts, want)
	}
}

func TestPerformanceStoreHTTPUpsertIsIdempotent(t *testing.T) {
	t.Parallel()

	db := openTestDB(t)
	store := NewPerformanceStore(db)
	ctx := context.Background()
	bucket := testHTTPBucket("batch-1")

	if err := store.UpsertHTTP(ctx, bucket); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertHTTP(ctx, bucket); err != nil {
		t.Fatal(err)
	}

	got, err := store.ListHTTP(ctx, model.PerformanceRange{Start: bucket.BucketStart.Add(-time.Minute), End: bucket.BucketStart.Add(time.Minute), Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].RequestCount != bucket.RequestCount || got[0].Histogram != bucket.Histogram {
		t.Fatalf("ListHTTP() = %+v", got)
	}
}

func TestPerformanceStoreHTTPAddsDistinctBatches(t *testing.T) {
	t.Parallel()

	db := openTestDB(t)
	store := NewPerformanceStore(db)
	ctx := context.Background()
	first := testHTTPBucket("batch-1")
	second := testHTTPBucket("batch-2")

	if err := store.UpsertHTTP(ctx, first); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertHTTP(ctx, second); err != nil {
		t.Fatal(err)
	}

	got, err := store.ListHTTP(ctx, model.PerformanceRange{Start: first.BucketStart.Add(-time.Minute), End: first.BucketStart.Add(time.Minute), Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].RequestCount != 4 || got[0].Histogram.Counts[3] != 4 {
		t.Fatalf("merged bucket = %+v", got)
	}
}

func TestPerformanceStoreHourlyRollupAndEmptyRead(t *testing.T) {
	t.Parallel()

	db := openTestDB(t)
	store := NewPerformanceStore(db)
	ctx := context.Background()
	first := testHTTPBucket("batch-1")
	second := testHTTPBucket("batch-2")
	second.BucketStart = second.BucketStart.Add(time.Minute)

	for _, bucket := range []model.HTTPMetricBucket{first, second} {
		if err := store.UpsertHTTP(ctx, bucket); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.RollupHTTP(ctx, first.BucketStart.Truncate(time.Hour), first.BucketStart.Truncate(time.Hour).Add(time.Hour)); err != nil {
		t.Fatal(err)
	}

	got, err := store.ListHTTPHourly(ctx, model.PerformanceRange{Start: first.BucketStart.Truncate(time.Hour), End: first.BucketStart.Truncate(time.Hour).Add(time.Hour), Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].RequestCount != 4 {
		t.Fatalf("hourly rollup = %+v", got)
	}

	empty, err := store.ListHTTP(ctx, model.PerformanceRange{Start: first.BucketStart.Add(24 * time.Hour), End: first.BucketStart.Add(25 * time.Hour), Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if empty == nil || len(empty) != 0 {
		t.Fatalf("empty ListHTTP() = %#v, want []", empty)
	}
}

func TestPerformanceStoreBoundedRetention(t *testing.T) {
	t.Parallel()

	db := openTestDB(t)
	store := NewPerformanceStore(db)
	ctx := context.Background()
	base := testHTTPBucket("batch-0")
	for index := range 5 {
		bucket := base
		bucket.BucketStart = base.BucketStart.Add(time.Duration(index) * time.Minute)
		bucket.IngestKey = "batch-" + string(rune('0'+index))
		if err := store.UpsertHTTP(ctx, bucket); err != nil {
			t.Fatal(err)
		}
	}

	deleted, err := store.TrimHTTPBefore(ctx, base.BucketStart.Add(5*time.Minute), 2)
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 2 {
		t.Fatalf("TrimHTTPBefore() = %d, want 2", deleted)
	}

	remaining, err := store.ListHTTP(ctx, model.PerformanceRange{Start: base.BucketStart.Add(-time.Minute), End: base.BucketStart.Add(10 * time.Minute), Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(remaining) != 3 {
		t.Fatalf("remaining rows = %d, want 3", len(remaining))
	}
}

func TestMaintenanceRetainsPerformanceInBoundedBatches(t *testing.T) {
	t.Parallel()

	db := openTestDB(t)
	performance := NewPerformanceStore(db)
	ctx := context.Background()
	base := testHTTPBucket("retention-0")
	for index := range 3 {
		bucket := base
		bucket.BucketStart = base.BucketStart.Add(time.Duration(index) * time.Minute)
		bucket.IngestKey = "retention-" + string(rune('0'+index))
		if err := performance.UpsertHTTP(ctx, bucket); err != nil {
			t.Fatal(err)
		}
	}

	result, err := NewMaintenanceStore(db).Run(ctx, model.MaintenancePolicy{
		Now: base.BucketStart.Add(24 * time.Hour), RetentionPerformance: time.Hour,
		RetentionPerformanceHourly: 30 * 24 * time.Hour, BatchSize: 1, MaxBytes: 1 << 30,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.DeletedRows != 1 || result.DeletedByTable["http_metric_buckets"] != 1 {
		t.Fatalf("maintenance result = %+v", result)
	}
}

func testHTTPBucket(ingestKey string) model.HTTPMetricBucket {
	return model.HTTPMetricBucket{
		BucketStart: time.Date(2026, 10, 16, 1, 2, 0, 0, time.UTC),
		ServiceID:   "tn-api", Environment: "prod", Method: "GET",
		RouteFamily: "account_orders", Upstream: "blue", StatusClass: 2,
		Histogram:    model.HTTPHistogram{Counts: [10]int64{0, 0, 0, 2}},
		RequestCount: 2, ErrorCount: 0, DurationTotalMS: 200,
		UpstreamConnectTotalMS: 10, UpstreamHeaderTotalMS: 40, UpstreamResponseTotalMS: 180,
		BytesSent: 4096, RequestBytes: 1024, IngestKey: ingestKey,
	}
}
