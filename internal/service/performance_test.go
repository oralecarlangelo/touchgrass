package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/oralecarlangelo/touchgrass/internal/model"
	"github.com/oralecarlangelo/touchgrass/internal/store"
)

func testPerformance(t *testing.T, now time.Time) (*Performance, *store.DB) {
	t.Helper()
	db := openInventoryDB(t)
	return NewPerformance(PerformanceConfig{
		Services: store.NewServiceStore(db), Store: store.NewPerformanceStore(db),
		Metrics: store.NewMetricStore(db), Deploys: store.NewDeployStore(db),
		Now: func() time.Time { return now },
	}), db
}

func seedHTTPBucket(t *testing.T, db *store.DB, at time.Time, requests, failures int64, latencyMS float64, key string) {
	t.Helper()
	histogram := model.HTTPHistogram{}
	for range requests {
		histogram.Observe(latencyMS)
	}
	if err := store.NewPerformanceStore(db).UpsertHTTP(context.Background(), model.HTTPMetricBucket{
		BucketStart: at, ServiceID: testServiceAPI, Environment: "prod", Method: "GET",
		RouteFamily: "account_orders", Upstream: "api", StatusClass: 2,
		Histogram: histogram, RequestCount: requests, ErrorCount: failures,
		DurationTotalMS: float64(requests) * latencyMS, IngestKey: key,
	}); err != nil {
		t.Fatal(err)
	}
}

func TestPerformanceSummaryDefaultsComparesAndExtractsPercentiles(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	performance, db := testPerformance(t, now)
	seedHTTPBucket(t, db, now.Add(-90*time.Minute), 10, 0, 100, "previous")
	seedHTTPBucket(t, db, now.Add(-30*time.Minute), 20, 2, 500, "current")

	summary, err := performance.Summary(context.Background(), model.PerformanceQuery{ServiceID: testServiceAPI})
	if err != nil {
		t.Fatal(err)
	}
	if summary.Resolution != model.PerformanceResolutionMinute || !summary.Start.Equal(now.Add(-time.Hour)) || !summary.End.Equal(now) {
		t.Fatalf("normalized summary range = %+v", summary)
	}
	if summary.RequestCount != 20 || summary.ErrorRate != 10 || summary.P50MS != 500 || summary.P95MS != 500 || summary.P99MS != 500 {
		t.Fatalf("summary = %+v", summary)
	}
	if summary.Previous.RequestCount != 10 || summary.Previous.P95MS != 100 {
		t.Fatalf("previous = %+v", summary.Previous)
	}
	if len(summary.Anomalies) == 0 || !strings.Contains(summary.Anomalies[0].Summary, "correlates with") {
		t.Fatalf("anomalies = %+v", summary.Anomalies)
	}
}

func TestPerformanceRangeRollupAndValidation(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	performance, _ := testPerformance(t, now)

	summary, err := performance.Summary(context.Background(), model.PerformanceQuery{
		Start: now.Add(-26 * time.Hour), End: now,
	})
	if err != nil || summary.Resolution != model.PerformanceResolutionHour {
		t.Fatalf("26h summary = %+v, %v", summary, err)
	}

	invalid := []model.PerformanceQuery{
		{Start: now, End: now.Add(-time.Minute)},
		{Start: now.Add(-8 * 24 * time.Hour), End: now, Resolution: model.PerformanceResolutionMinute},
		{Start: now.Add(-31 * 24 * time.Hour), End: now, Resolution: model.PerformanceResolutionHour},
		{Start: now.Add(-time.Hour), End: now, Sort: "raw_sql"},
		{Start: now.Add(-time.Hour), End: now, RouteFamily: "/account/orders?email=secret"},
		{Start: now.Add(-time.Hour), End: now, Limit: 101},
	}
	for _, query := range invalid {
		if _, err := performance.Summary(context.Background(), query); !errors.Is(err, ErrInvalidInput) {
			t.Errorf("Summary(%+v) error = %v, want ErrInvalidInput", query, err)
		}
	}
}

func TestPerformanceQueriesValidateServiceAndReturnEmptySlices(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	performance, _ := testPerformance(t, now)
	query := model.PerformanceQuery{ServiceID: testServiceAPI, Start: now.Add(-time.Hour), End: now}

	timeline, err := performance.Timeline(context.Background(), query)
	if err != nil || timeline.Points == nil || timeline.Events == nil || timeline.Deployments == nil {
		t.Fatalf("empty timeline = %+v, %v", timeline, err)
	}
	routes, err := performance.Routes(context.Background(), query)
	if err != nil || routes == nil || len(routes) != 0 {
		t.Fatalf("empty routes = %+v, %v", routes, err)
	}
	postgres, err := performance.Postgres(context.Background(), query)
	if err != nil || postgres == nil {
		t.Fatalf("empty postgres = %+v, %v", postgres, err)
	}
	redis, err := performance.Redis(context.Background(), query)
	if err != nil || redis == nil {
		t.Fatalf("empty redis = %+v, %v", redis, err)
	}

	_, err = performance.Summary(context.Background(), model.PerformanceQuery{ServiceID: testUnknownServiceID})
	if !errors.Is(err, store.ErrServiceNotFound) {
		t.Fatalf("unknown service error = %v", err)
	}
}

func TestPerformanceTimelineAlignsAvailableSignals(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	at := now.Add(-30 * time.Minute)
	performance, db := testPerformance(t, now)
	seedHTTPBucket(t, db, at, 5, 1, 250, "timeline")
	if err := store.NewPerformanceStore(db).UpsertLogVolume(context.Background(), model.LogVolumeBucket{
		BucketStart: at, ServiceID: testServiceAPI, Environment: "prod", Severity: model.LogLevelError,
		LineCount: 4, IngestKey: "logs",
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.NewPerformanceStore(db).UpsertContainerEvent(context.Background(), model.ContainerEvent{
		SourceEventID: "event-timeline", OccurredAt: at, ServiceID: testServiceAPI,
		Environment: "prod", EventType: "restart", ContainerName: "api-1",
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.NewMetricStore(db).Insert(context.Background(), model.Metric{
		ServiceID: testServiceAPI, ContainerName: "api-1", SampledAt: at,
		CPUPercent: 35, MemBytes: 50, MemLimit: 100,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.NewMetricStore(db).Insert(context.Background(), model.Metric{
		ServiceID: testServiceAPI, ContainerName: "api-2", SampledAt: at,
		CPUPercent: 15, MemBytes: 50, MemLimit: 0,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.NewDeployStore(db).Record(context.Background(), model.Deploy{
		ServiceID: testServiceAPI, Type: model.DeployDeploy, Outcome: model.DeploySuccess, CreatedAt: at,
	}); err != nil {
		t.Fatal(err)
	}

	timeline, err := performance.Timeline(context.Background(), model.PerformanceQuery{
		ServiceID: testServiceAPI, Start: now.Add(-time.Hour), End: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(timeline.Points) != 1 || timeline.Points[0].RequestCount != 5 || timeline.Points[0].LogErrors != 4 {
		t.Fatalf("timeline points = %+v", timeline.Points)
	}
	if timeline.Points[0].CPUPercent == nil || *timeline.Points[0].CPUPercent != 25 ||
		timeline.Points[0].MemoryPercent == nil || *timeline.Points[0].MemoryPercent != 50 {
		t.Fatalf("resource point = %+v", timeline.Points[0])
	}
	if len(timeline.Events) != 1 || len(timeline.Deployments) != 1 {
		t.Fatalf("markers = events:%+v deploys:%+v", timeline.Events, timeline.Deployments)
	}
}

func TestPerformanceCollectorStatusMarksMissingStaleAndPartial(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	performance, db := testPerformance(t, now)
	if err := store.NewCursorStore(db).Upsert(context.Background(), model.CollectorCursor{
		Source: "nginx", LastSuccessAt: now.Add(-10 * time.Minute),
		LastRecordedAt: now.Add(-11 * time.Minute), LastError: "nginx_partial", DroppedRecords: 3,
	}); err != nil {
		t.Fatal(err)
	}

	statuses, err := performance.CollectorStatus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	bySource := map[string]model.CollectorStatus{}
	for _, status := range statuses {
		bySource[status.Source] = status
	}
	if !bySource["nginx"].Stale || bySource["nginx"].LastError != "nginx_partial" || bySource["nginx"].DroppedRecords != 3 {
		t.Fatalf("nginx status = %+v", bySource["nginx"])
	}
	if !bySource["postgres"].Stale || !bySource["postgres"].LastSuccessAt.IsZero() {
		t.Fatalf("missing postgres status = %+v", bySource["postgres"])
	}
}
