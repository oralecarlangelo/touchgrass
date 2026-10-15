package service

import (
	"context"
	"testing"
	"time"

	"github.com/oralecarlangelo/touchgrass/internal/model"
	"github.com/oralecarlangelo/touchgrass/internal/store"
)

func performanceAlertSampler(t *testing.T, now time.Time) (*Sampler, *Performance, *store.DB) {
	t.Helper()
	sampler, db := testSampler(t, stubStatsLister{})
	performance := NewPerformance(PerformanceConfig{
		Services: store.NewServiceStore(db),
		Store:    store.NewPerformanceStore(db),
		Metrics:  store.NewMetricStore(db),
		Deploys:  store.NewDeployStore(db),
		Now:      func() time.Time { return now },
	})
	sampler.performance = performance
	return sampler, performance, db
}

func markPerformanceSourceHealthy(t *testing.T, db *store.DB, source string, now time.Time) {
	t.Helper()
	if err := store.NewCursorStore(db).Upsert(context.Background(), model.CollectorCursor{
		Source: source, LastSuccessAt: now,
	}); err != nil {
		t.Fatal(err)
	}
}

func TestPerformanceAlertValuesUseCompletedMinuteBuckets(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 10, 16, 12, 2, 30, 0, time.UTC)
	_, performance, db := performanceAlertSampler(t, now)
	markPerformanceSourceHealthy(t, db, "nginx", now)
	seedHTTPBucket(t, db, now.Truncate(time.Minute), 10, 5, 2500, "in-progress")

	value, available, err := performance.AlertValue(context.Background(), testServiceAPI, model.MetricHTTPP95)
	if err != nil {
		t.Fatal(err)
	}
	if available || value != 0 {
		t.Fatalf("in-progress value = %v, available = %v; want unavailable", value, available)
	}

	seedHTTPBucket(t, db, now.Truncate(time.Minute).Add(-time.Minute), 10, 1, 500, "complete")
	value, available, err = performance.AlertValue(context.Background(), testServiceAPI, model.MetricHTTPP95)
	if err != nil {
		t.Fatal(err)
	}
	if !available || value != 500 {
		t.Fatalf("completed p95 = %v, available = %v; want 500, true", value, available)
	}
}

func TestPerformanceAlertValuesCoverBoundedMetrics(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 10, 16, 12, 2, 30, 0, time.UTC)
	_, performance, db := performanceAlertSampler(t, now)
	at := now.Truncate(time.Minute).Add(-time.Minute)
	for _, source := range []string{"nginx", "postgres", "redis"} {
		markPerformanceSourceHealthy(t, db, source, now)
	}
	seedHTTPBucket(t, db, at, 10, 2, 500, "http-values")
	for _, bucket := range []model.PostgresMetricBucket{
		{BucketStart: at, ServiceID: testServiceAPI, Environment: "prod", Metric: "connections_total", Value: 25},
		{BucketStart: at, ServiceID: testServiceAPI, Environment: "prod", Metric: "max_connections", Value: 100},
	} {
		if err := store.NewPerformanceStore(db).UpsertPostgres(context.Background(), bucket); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.NewPerformanceStore(db).UpsertRedis(context.Background(), model.RedisMetricBucket{
		BucketStart: at, ServiceID: testServiceAPI, Environment: "prod", Metric: "evicted_keys", Delta: 3,
	}); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		metric string
		want   float64
	}{
		{model.MetricHTTPP95, 500},
		{model.MetricHTTPErrorRate, 20},
		{model.MetricPostgresConnectionPct, 25},
		{model.MetricRedisEvictions, 3},
	}
	for _, test := range tests {
		value, available, err := performance.AlertValue(context.Background(), testServiceAPI, test.metric)
		if err != nil {
			t.Errorf("AlertValue(%s) error = %v", test.metric, err)
			continue
		}
		if !available || value != test.want {
			t.Errorf("AlertValue(%s) = %v, %v; want %v, true", test.metric, value, available, test.want)
		}
	}
}

func TestPerformanceAlertFiresOnceAndStaleSourcePreservesEpisode(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 10, 16, 12, 2, 30, 0, time.UTC)
	sampler, _, db := performanceAlertSampler(t, now)
	markPerformanceSourceHealthy(t, db, "nginx", now)
	seedHTTPBucket(t, db, now.Truncate(time.Minute).Add(-time.Minute), 10, 2, 500, "episode")
	rule := model.AlertRule{ID: 9001, ServiceID: testServiceAPI, Metric: model.MetricHTTPP95, Threshold: 100, DurationSecs: 1}
	sampler.breaches[rule.ID] = breachState{start: time.Now().Add(-2 * time.Second)}

	if err := sampler.evaluateRule(context.Background(), rule); err != nil {
		t.Fatal(err)
	}
	if err := sampler.evaluateRule(context.Background(), rule); err != nil {
		t.Fatal(err)
	}
	if got := notificationCount(t, db); got != 1 {
		t.Fatalf("notifications = %d, want one per episode", got)
	}
	before := sampler.breaches[rule.ID]
	markPerformanceSourceHealthy(t, db, "nginx", now.Add(-time.Hour))
	if err := sampler.evaluateRule(context.Background(), rule); err != nil {
		t.Fatal(err)
	}
	after, ok := sampler.breaches[rule.ID]
	if !ok || after != before {
		t.Fatalf("stale source changed breach from %+v to %+v", before, after)
	}
}

func TestCreateRuleAcceptsPerformanceMetrics(t *testing.T) {
	t.Parallel()

	sampler, _ := testSampler(t, stubStatsLister{})
	for _, metric := range []string{
		model.MetricHTTPP95,
		model.MetricHTTPErrorRate,
		model.MetricPostgresConnectionPct,
		model.MetricRedisEvictions,
	} {
		if _, err := sampler.CreateRule(context.Background(), model.RuleCreate{
			ServiceID: testServiceAPI, Metric: metric, Threshold: 1, DurationSecs: 60,
		}); err != nil {
			t.Errorf("CreateRule(%s) error = %v", metric, err)
		}
	}
}
