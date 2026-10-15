package service

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/oralecarlangelo/touchgrass/internal/model"
)

type fakePostgresSource struct {
	snapshot model.PostgresSnapshot
	err      error
}

func (f fakePostgresSource) Collect(context.Context) (model.PostgresSnapshot, error) {
	return f.snapshot, f.err
}

type fakeRedisSource struct {
	snapshot model.RedisSnapshot
	err      error
}

func (f fakeRedisSource) Collect(context.Context) (model.RedisSnapshot, error) {
	return f.snapshot, f.err
}

type fakePerformanceWriter struct {
	mu           sync.Mutex
	postgres     []model.PostgresMetricBucket
	statements   []model.PostgresStatementBucket
	redis        []model.RedisMetricBucket
	cursors      map[string]model.CollectorCursor
	failPostgres int
}

func newFakePerformanceWriter() *fakePerformanceWriter {
	return &fakePerformanceWriter{cursors: map[string]model.CollectorCursor{}}
}

func (f *fakePerformanceWriter) UpsertHTTP(context.Context, model.HTTPMetricBucket) error { return nil }
func (f *fakePerformanceWriter) UpsertPostgres(_ context.Context, bucket model.PostgresMetricBucket) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failPostgres > 0 {
		f.failPostgres--
		return errors.New("write failed")
	}
	f.postgres = append(f.postgres, bucket)
	return nil
}
func (f *fakePerformanceWriter) UpsertPostgresStatement(_ context.Context, bucket model.PostgresStatementBucket) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.statements = append(f.statements, bucket)
	return nil
}
func (f *fakePerformanceWriter) UpsertRedis(_ context.Context, bucket model.RedisMetricBucket) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.redis = append(f.redis, bucket)
	return nil
}
func (f *fakePerformanceWriter) UpsertLogVolume(context.Context, model.LogVolumeBucket) error {
	return nil
}
func (f *fakePerformanceWriter) Get(_ context.Context, source string) (model.CollectorCursor, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	cursor, ok := f.cursors[source]
	if !ok {
		return model.CollectorCursor{}, errors.New("not found")
	}
	return cursor, nil
}
func (f *fakePerformanceWriter) Upsert(_ context.Context, cursor model.CollectorCursor) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cursors[cursor.Source] = cursor
	return nil
}

type fakePerformanceClock struct {
	now   time.Time
	after chan time.Time
}

func (f *fakePerformanceClock) Now() time.Time                       { return f.now }
func (f *fakePerformanceClock) After(time.Duration) <-chan time.Time { return f.after }

func TestPerformanceCollectorIsolatesSourceFailuresAndPersistsStatus(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 10, 16, 3, 4, 45, 0, time.UTC)
	writer := newFakePerformanceWriter()
	writer.cursors["postgres"] = model.CollectorCursor{Source: "postgres", LastSuccessAt: now.Add(-time.Minute), DroppedRecords: 7}
	collector := NewPerformanceCollector(PerformanceCollectorConfig{
		ServiceID: "tn-api", Environment: "prod", Writer: writer,
		Postgres: fakePostgresSource{err: errors.New("password=user@example.com")},
		Redis: fakeRedisSource{snapshot: model.RedisSnapshot{SampledAt: now, Metrics: map[string]model.NumericSample{
			"connected_clients": {Value: 8},
		}}},
		Clock: &fakePerformanceClock{now: now, after: make(chan time.Time)}, Logger: slog.New(slog.DiscardHandler),
	})

	collector.collect(context.Background())

	if len(writer.redis) != 1 || writer.redis[0].BucketStart != now.Truncate(time.Minute) {
		t.Fatalf("redis buckets = %+v", writer.redis)
	}
	postgresStatus := writer.cursors["postgres"]
	if postgresStatus.LastError != "postgres_unavailable" || !postgresStatus.LastSuccessAt.Equal(now.Add(-time.Minute)) || postgresStatus.DroppedRecords != 8 {
		t.Fatalf("postgres status = %+v", postgresStatus)
	}
	if redisStatus := writer.cursors["redis"]; redisStatus.LastError != "" || !redisStatus.LastSuccessAt.Equal(now) {
		t.Fatalf("redis status = %+v", redisStatus)
	}
}

func TestPerformanceCollectorComputesDeltasAndResetsWithoutNegatives(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 10, 16, 3, 4, 45, 0, time.UTC)
	writer := newFakePerformanceWriter()
	source := &sequencePostgresSource{snapshots: []model.PostgresSnapshot{
		{SampledAt: now, Metrics: map[string]model.NumericSample{"xact_commit": {Value: 100, Cumulative: true}}},
		{SampledAt: now.Add(time.Minute), Metrics: map[string]model.NumericSample{"xact_commit": {Value: 125, Cumulative: true}}},
		{SampledAt: now.Add(2 * time.Minute), Metrics: map[string]model.NumericSample{"xact_commit": {Value: 4, Cumulative: true}}},
	}}
	clock := &fakePerformanceClock{now: now, after: make(chan time.Time)}
	collector := NewPerformanceCollector(PerformanceCollectorConfig{ServiceID: "tn-api", Environment: "prod", Writer: writer, Postgres: source, Clock: clock, Logger: slog.New(slog.DiscardHandler)})

	collector.collect(context.Background())
	clock.now = now.Add(time.Minute)
	collector.collect(context.Background())
	clock.now = now.Add(2 * time.Minute)
	collector.collect(context.Background())

	if len(writer.postgres) != 3 || writer.postgres[0].Delta != 0 || writer.postgres[1].Delta != 25 || writer.postgres[2].Delta != 0 || !writer.postgres[2].Reset {
		t.Fatalf("postgres deltas = %+v", writer.postgres)
	}
}

func TestPerformanceCollectorRunStopsOnCancellation(t *testing.T) {
	t.Parallel()

	clock := &fakePerformanceClock{now: time.Now().UTC(), after: make(chan time.Time)}
	collector := NewPerformanceCollector(PerformanceCollectorConfig{ServiceID: "tn-api", Environment: "prod", Writer: newFakePerformanceWriter(), Clock: clock, Logger: slog.New(slog.DiscardHandler)})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { collector.Run(ctx); close(done) }()
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("collector did not stop")
	}
}

func TestPerformanceCollectorRetryKeepsOriginalDelta(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 10, 16, 3, 4, 0, 0, time.UTC)
	writer := newFakePerformanceWriter()
	source := &sequencePostgresSource{snapshots: []model.PostgresSnapshot{
		{SampledAt: now, Metrics: map[string]model.NumericSample{"xact_commit": {Value: 100, Cumulative: true}}},
		{SampledAt: now.Add(time.Minute), Metrics: map[string]model.NumericSample{"xact_commit": {Value: 125, Cumulative: true}}},
		{SampledAt: now.Add(time.Minute), Metrics: map[string]model.NumericSample{"xact_commit": {Value: 125, Cumulative: true}}},
	}}
	clock := &fakePerformanceClock{now: now, after: make(chan time.Time)}
	collector := NewPerformanceCollector(PerformanceCollectorConfig{ServiceID: "tn-api", Environment: "prod", Writer: writer, Postgres: source, Clock: clock, Logger: slog.New(slog.DiscardHandler)})
	collector.collect(context.Background())
	writer.failPostgres = 1
	clock.now = now.Add(time.Minute)
	collector.collect(context.Background())
	collector.collect(context.Background())

	if got := writer.postgres[len(writer.postgres)-1]; got.Delta != 25 {
		t.Fatalf("retry delta = %+v, want 25", got)
	}
}

func TestPerformanceCollectorBoundsDroppedRecords(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 10, 16, 3, 4, 0, 0, time.UTC)
	writer := newFakePerformanceWriter()
	writer.cursors["postgres"] = model.CollectorCursor{Source: "postgres", DroppedRecords: maximumDroppedRecords}
	collector := NewPerformanceCollector(PerformanceCollectorConfig{
		ServiceID: "tn-api", Environment: "prod", Writer: writer,
		Postgres: fakePostgresSource{err: errors.New("down")},
		Clock:    &fakePerformanceClock{now: now, after: make(chan time.Time)}, Logger: slog.New(slog.DiscardHandler),
	})
	collector.collect(context.Background())
	if got := writer.cursors["postgres"].DroppedRecords; got != maximumDroppedRecords {
		t.Fatalf("dropped records = %d", got)
	}
}

type sequencePostgresSource struct {
	mu        sync.Mutex
	snapshots []model.PostgresSnapshot
}

func (s *sequencePostgresSource) Collect(context.Context) (model.PostgresSnapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	snapshot := s.snapshots[0]
	s.snapshots = s.snapshots[1:]
	return snapshot, nil
}
