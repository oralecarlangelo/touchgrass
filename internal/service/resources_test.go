package service

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/oralecarlangelo/touchgrass/internal/docker"
	"github.com/oralecarlangelo/touchgrass/internal/model"
	"github.com/oralecarlangelo/touchgrass/internal/store"
)

// stubStatsLister fakes docker listing plus stats.
type stubStatsLister struct {
	containers []docker.Container
	sizes      []int64
	stats      map[string]docker.Stats
	statsErr   map[string]error
	listErr    error
}

func (s stubStatsLister) List(_ context.Context) ([]docker.Container, error) {
	return s.containers, s.listErr
}

func (s stubStatsLister) SizedList(_ context.Context) ([]docker.Container, []int64, error) {
	return s.containers, s.sizes, s.listErr
}

func (s stubStatsLister) Stats(_ context.Context, id string) (docker.Stats, error) {
	if err, ok := s.statsErr[id]; ok {
		return docker.Stats{}, err
	}

	return s.stats[id], nil
}

// testSampler builds a Sampler on a seeded store with fakes.
func testSampler(t *testing.T, lister StatsLister) (*Sampler, *store.DB) {
	t.Helper()

	db := openInventoryDB(t)

	sampler := NewSampler(SamplerConfig{
		Services:      store.NewServiceStore(db),
		Docker:        lister,
		Metrics:       store.NewMetricStore(db),
		Rules:         store.NewRuleStore(db),
		Notifications: store.NewNotificationStore(db),
		Deploys:       store.NewDeployStore(db),
		Occurrences:   store.NewOccurrenceStore(db),
		Issues:        store.NewIssueStore(db),
		IssueRules:    store.NewIssueRuleStore(db),
		Logs:          store.NewLogStore(db),
		SDKLogs:       store.NewSDKLogStore(db),
		Interval:      5 * time.Millisecond,
		Retention: Retention{
			Metrics: time.Hour, Notifications: time.Hour, Deploys: time.Hour, Errors: time.Hour, Logs: time.Hour,
			SDKLogs: time.Hour,
		},
		Logger: slog.New(slog.DiscardHandler),
	})

	return sampler, db
}

func TestSampleCollects(t *testing.T) {
	t.Parallel()

	containers := testContainers()
	lister := stubStatsLister{
		containers: containers,
		sizes:      []int64{100, 200, 300},
		stats: map[string]docker.Stats{
			containers[0].ID: {
				CPUPercent: 12.5, MemBytes: 200, MemLimit: 1000,
				Restarts: 2, StartedAt: time.Now().Add(-time.Hour),
			},
			containers[1].ID: {
				CPUPercent: 5, MemBytes: 100, MemLimit: 1000,
				Restarts: 0, StartedAt: time.Now().Add(-2 * time.Hour),
			},
		},
	}

	sampler, db := testSampler(t, lister)
	ctx := context.Background()

	sampler.sample(ctx)

	metrics, err := store.NewMetricStore(db).ListByService(ctx, testServiceAPI, time.Time{}, 100)
	if err != nil {
		t.Fatalf("ListByService() error = %v, want nil", err)
	}

	if len(metrics) != 1 {
		t.Fatalf("tn-api samples = %d, want 1", len(metrics))
	}

	got := metrics[0]
	if got.CPUPercent != 12.5 || got.MemBytes != 200 || got.DiskBytes != 100 || got.Restarts != 2 {
		t.Errorf("sample = %+v, want mapped stats", got)
	}

	if got.UptimeSecs < 3590 || got.UptimeSecs > 3610 {
		t.Errorf("uptime = %d, want ~3600", got.UptimeSecs)
	}

	fe, err := store.NewMetricStore(db).ListByService(ctx, testServiceFE, time.Time{}, 100)
	if err != nil {
		t.Fatalf("ListByService() error = %v, want nil", err)
	}

	if len(fe) != 1 {
		t.Errorf("tn-fe samples = %d, want 1 (unrelated container ignored)", len(fe))
	}
}

func TestSampleSkipsFailedStats(t *testing.T) {
	t.Parallel()

	containers := testContainers()
	lister := stubStatsLister{
		containers: containers,
		sizes:      []int64{100, 200, 300},
		stats: map[string]docker.Stats{
			containers[1].ID: {CPUPercent: 5, MemBytes: 100, MemLimit: 1000},
		},
		statsErr: map[string]error{containers[0].ID: errors.New("no such container")},
	}

	sampler, db := testSampler(t, lister)
	ctx := context.Background()

	sampler.sample(ctx)

	metrics, err := store.NewMetricStore(db).ListByService(ctx, testServiceAPI, time.Time{}, 100)
	if err != nil {
		t.Fatalf("ListByService() error = %v, want nil", err)
	}

	if len(metrics) != 0 {
		t.Errorf("tn-api samples = %d, want 0 (failed stats skipped)", len(metrics))
	}

	fe, err := store.NewMetricStore(db).ListByService(ctx, testServiceFE, time.Time{}, 100)
	if err != nil {
		t.Fatalf("ListByService() error = %v, want nil", err)
	}

	if len(fe) != 1 {
		t.Errorf("tn-fe samples = %d, want 1 (sibling unaffected)", len(fe))
	}
}

// insertMemSample records one memory sample for the breach tests.
func insertMemSample(t *testing.T, db *store.DB, memBytes uint64) {
	t.Helper()

	err := store.NewMetricStore(db).Insert(context.Background(), model.Metric{
		ServiceID: testServiceFE, ContainerName: "fe-1",
		MemBytes: memBytes, MemLimit: 1000, SampledAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("Insert() error = %v, want nil", err)
	}
}

// evaluateOK runs one evaluation cycle, failing on error.
func evaluateOK(t *testing.T, sampler *Sampler) {
	t.Helper()

	if err := sampler.evaluate(context.Background()); err != nil {
		t.Fatalf("evaluate() error = %v, want nil", err)
	}
}

// notificationCount returns the stored notification count.
func notificationCount(t *testing.T, db *store.DB) int {
	t.Helper()

	notifications, err := store.NewNotificationStore(db).List(context.Background(), "", 10)
	if err != nil {
		t.Fatalf("List() error = %v, want nil", err)
	}

	return len(notifications)
}

// breachRule creates a zero-duration mem rule that fires immediately.
func breachRule(t *testing.T, db *store.DB) {
	t.Helper()

	_, err := store.NewRuleStore(db).Create(context.Background(), model.RuleCreate{
		ServiceID: testServiceFE, Metric: model.MetricMem, Threshold: 50, DurationSecs: 0,
	})
	if err != nil {
		t.Fatalf("Create() error = %v, want nil", err)
	}
}

func TestEvaluateFiresNewBreach(t *testing.T) {
	t.Parallel()

	sampler, db := testSampler(t, stubStatsLister{})

	breachRule(t, db)
	insertMemSample(t, db, 800)
	evaluateOK(t, sampler)

	if got := notificationCount(t, db); got != 1 {
		t.Fatalf("notifications = %d, want 1", got)
	}

	// Sustained breach must not refire.
	evaluateOK(t, sampler)

	if got := notificationCount(t, db); got != 1 {
		t.Errorf("notifications = %d, want 1 (no refire)", got)
	}
}

func TestEvaluateRefiresAfterRecovery(t *testing.T) {
	t.Parallel()

	sampler, db := testSampler(t, stubStatsLister{})

	breachRule(t, db)
	insertMemSample(t, db, 800)
	evaluateOK(t, sampler)

	insertMemSample(t, db, 100)
	evaluateOK(t, sampler)

	if got := notificationCount(t, db); got != 1 {
		t.Fatalf("notifications = %d, want 1 after recovery", got)
	}

	insertMemSample(t, db, 800)
	evaluateOK(t, sampler)

	if got := notificationCount(t, db); got != 2 {
		t.Errorf("notifications = %d, want 2 (refire after recovery)", got)
	}
}

func TestEvaluateSustainedDuration(t *testing.T) {
	t.Parallel()

	sampler, db := testSampler(t, stubStatsLister{})
	ctx := context.Background()

	if _, err := store.NewRuleStore(db).Create(ctx, model.RuleCreate{
		ServiceID: testServiceFE, Metric: model.MetricCPU, Threshold: 10, DurationSecs: 3600,
	}); err != nil {
		t.Fatalf("Create() error = %v, want nil", err)
	}

	if err := store.NewMetricStore(db).Insert(ctx, model.Metric{
		ServiceID: testServiceFE, ContainerName: "fe-1", CPUPercent: 50, SampledAt: time.Now(),
	}); err != nil {
		t.Fatalf("Insert() error = %v, want nil", err)
	}

	// Breaching but not yet for an hour: no fire.
	if err := sampler.evaluate(ctx); err != nil {
		t.Fatalf("evaluate() error = %v, want nil", err)
	}

	notifications, err := store.NewNotificationStore(db).List(ctx, "", 10)
	if err != nil {
		t.Fatalf("List() error = %v, want nil", err)
	}

	if len(notifications) != 0 {
		t.Errorf("notifications = %d, want 0 (duration not met)", len(notifications))
	}
}

func TestMetricValue(t *testing.T) {
	t.Parallel()

	sample := model.Metric{CPUPercent: 25, MemBytes: 500, MemLimit: 1000, DiskBytes: 42}

	tests := []struct {
		name     string
		metric   string
		expected float64
	}{
		{name: "cpu percent", metric: model.MetricCPU, expected: 25},
		{name: "mem percent", metric: model.MetricMem, expected: 50},
		{name: "disk bytes", metric: model.MetricDisk, expected: 42},
		{name: "unknown metric", metric: testBogusValue, expected: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := metricValue(tt.metric, sample); got != tt.expected {
				t.Errorf("metricValue(%q) = %v, want %v", tt.metric, got, tt.expected)
			}
		})
	}

	zero := model.Metric{MemBytes: 500, MemLimit: 0}
	if got := metricValue(model.MetricMem, zero); got != 0 {
		t.Errorf("metricValue(mem with zero limit) = %v, want 0", got)
	}
}

func TestRunSamplesUntilCancelled(t *testing.T) {
	t.Parallel()

	sampler, db := testSampler(t, stubStatsLister{
		containers: testContainers(),
		sizes:      []int64{100, 200, 300},
		stats: map[string]docker.Stats{
			testContainers()[0].ID: {CPUPercent: 1, MemBytes: 10, MemLimit: 100},
			testContainers()[1].ID: {CPUPercent: 2, MemBytes: 20, MemLimit: 100},
		},
	})

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})

	go func() {
		defer close(done)

		sampler.Run(ctx)
	}()

	time.Sleep(25 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run() did not stop after cancel")
	}

	metrics, err := store.NewMetricStore(db).ListByService(context.Background(), testServiceAPI, time.Time{}, 100)
	if err != nil {
		t.Fatalf("ListByService() error = %v, want nil", err)
	}

	if len(metrics) == 0 {
		t.Error("no samples collected before cancel")
	}
}

func TestCreateRuleValidation(t *testing.T) {
	t.Parallel()

	sampler, _ := testSampler(t, stubStatsLister{})
	ctx := context.Background()

	created, err := sampler.CreateRule(ctx, model.RuleCreate{
		ServiceID: testServiceAPI, Metric: model.MetricDisk, Threshold: 1000, DurationSecs: 60,
	})
	if err != nil {
		t.Fatalf("CreateRule() error = %v, want nil", err)
	}

	if created.ID == 0 {
		t.Error("CreateRule() id = 0, want non-zero")
	}

	tests := []struct {
		name   string
		create model.RuleCreate
	}{
		{name: testUnknownServiceName, create: model.RuleCreate{ServiceID: testUnknownServiceID, Metric: "cpu", Threshold: 1, DurationSecs: 1}},
		{name: "bad metric", create: model.RuleCreate{ServiceID: testServiceAPI, Metric: "nope", Threshold: 1, DurationSecs: 1}},
		{name: "zero threshold", create: model.RuleCreate{ServiceID: testServiceAPI, Metric: "cpu", Threshold: 0, DurationSecs: 1}},
		{name: "zero duration", create: model.RuleCreate{ServiceID: testServiceAPI, Metric: "cpu", Threshold: 1, DurationSecs: 0}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if _, err := sampler.CreateRule(ctx, tt.create); err == nil {
				t.Errorf("CreateRule(%+v) error = nil, want error", tt.create)
			}
		})
	}

	if err := sampler.DeleteRule(ctx, created.ID); err != nil {
		t.Errorf("DeleteRule() error = %v, want nil", err)
	}
}
