package service

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/oralecarlangelo/touchgrass/internal/dbstats"
	"github.com/oralecarlangelo/touchgrass/internal/model"
)

const (
	defaultPerformanceInterval = time.Minute
	maximumDroppedRecords      = int64(1_000_000_000)
)

// HTTPSource reads privacy-safe timing observations and a durable cursor.
type HTTPSource interface {
	Read(context.Context) ([]model.HTTPObservation, model.CollectorCursor, error)
}

type httpSourceAcknowledger interface {
	Acknowledge(model.CollectorCursor)
}

// PostgresSource reads one bounded PostgreSQL snapshot.
type PostgresSource interface {
	Collect(context.Context) (model.PostgresSnapshot, error)
}

// RedisSource reads one bounded Redis snapshot.
type RedisSource interface {
	Collect(context.Context) (model.RedisSnapshot, error)
}

// LogVolumeSource reads pre-aggregated log volume and a durable cursor.
type LogVolumeSource interface {
	ReadLogVolume(context.Context) ([]model.LogVolumeBucket, model.CollectorCursor, error)
}

// PerformanceWriter persists native aggregates and collector health.
type PerformanceWriter interface {
	UpsertHTTP(context.Context, model.HTTPMetricBucket) error
	UpsertPostgres(context.Context, model.PostgresMetricBucket) error
	UpsertPostgresStatement(context.Context, model.PostgresStatementBucket) error
	UpsertRedis(context.Context, model.RedisMetricBucket) error
	UpsertLogVolume(context.Context, model.LogVolumeBucket) error
	Get(context.Context, string) (model.CollectorCursor, error)
	Upsert(context.Context, model.CollectorCursor) error
}

// PerformanceClock makes collector scheduling deterministic in tests.
type PerformanceClock interface {
	Now() time.Time
	After(time.Duration) <-chan time.Time
}

type realPerformanceClock struct{}

func (realPerformanceClock) Now() time.Time { return time.Now().UTC() }
func (realPerformanceClock) After(duration time.Duration) <-chan time.Time {
	return time.After(duration)
}

// PerformanceCollectorConfig wires independent agentless sources.
type PerformanceCollectorConfig struct {
	ServiceID   string
	Environment string
	Interval    time.Duration
	HTTP        HTTPSource
	Postgres    PostgresSource
	Redis       RedisSource
	LogVolume   LogVolumeSource
	Writer      PerformanceWriter
	Clock       PerformanceClock
	Logger      *slog.Logger
}

// PerformanceCollector samples enabled sources without allowing one source
// failure to stop the rest.
type PerformanceCollector struct {
	serviceID         string
	environment       string
	interval          time.Duration
	http              HTTPSource
	postgres          PostgresSource
	redis             RedisSource
	logVolume         LogVolumeSource
	writer            PerformanceWriter
	clock             PerformanceClock
	logger            *slog.Logger
	postgresBaseline  map[string]float64
	statementBaseline map[string]model.PostgresStatementSnapshot
	redisBaseline     map[string]float64
}

// NewPerformanceCollector builds the native-source collector.
func NewPerformanceCollector(cfg PerformanceCollectorConfig) *PerformanceCollector {
	interval := cfg.Interval
	if interval <= 0 {
		interval = defaultPerformanceInterval
	}
	clock := cfg.Clock
	if clock == nil {
		clock = realPerformanceClock{}
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &PerformanceCollector{
		serviceID: cfg.ServiceID, environment: cfg.Environment, interval: interval,
		http: cfg.HTTP, postgres: cfg.Postgres, redis: cfg.Redis, logVolume: cfg.LogVolume,
		writer: cfg.Writer, clock: clock, logger: logger,
		postgresBaseline: map[string]float64{}, statementBaseline: map[string]model.PostgresStatementSnapshot{},
		redisBaseline: map[string]float64{},
	}
}

// Run samples immediately and then at the configured interval until cancelled.
func (c *PerformanceCollector) Run(ctx context.Context) {
	if c == nil || c.writer == nil || c.serviceID == "" {
		return
	}
	for {
		c.collect(ctx)
		select {
		case <-ctx.Done():
			return
		case <-c.clock.After(c.interval):
		}
	}
}

func (c *PerformanceCollector) collect(ctx context.Context) {
	if err := ctx.Err(); err != nil {
		return
	}
	minute := c.clock.Now().UTC().Truncate(time.Minute)
	if c.http != nil {
		c.collectHTTP(ctx, minute)
	}
	if c.postgres != nil {
		c.collectPostgres(ctx, minute)
	}
	if c.redis != nil {
		c.collectRedis(ctx, minute)
	}
	if c.logVolume != nil {
		c.collectLogVolume(ctx)
	}
}

func (c *PerformanceCollector) collectPostgres(ctx context.Context, minute time.Time) {
	snapshot, err := c.postgres.Collect(ctx)
	if err != nil {
		c.recordFailure(ctx, "postgres", "postgres_unavailable")
		return
	}
	for metric, sample := range snapshot.Metrics {
		delta, reset := metricDelta(c.postgresBaseline, metric, sample)
		bucket := model.PostgresMetricBucket{
			BucketStart: minute, ServiceID: c.serviceID,
			Environment: c.environment, Metric: metric, Value: sample.Value, Delta: delta, Reset: reset,
		}
		if err := c.writer.UpsertPostgres(ctx, bucket); err != nil {
			c.recordFailure(ctx, "postgres", "postgres_write_failed")
			return
		}
		if sample.Cumulative {
			c.postgresBaseline[metric] = sample.Value
		}
	}
	for queryID, statement := range snapshot.Statements {
		bucket := c.statementBucket(minute, queryID, statement)
		if err := c.writer.UpsertPostgresStatement(ctx, bucket); err != nil {
			c.recordFailure(ctx, "postgres", "postgres_write_failed")
			return
		}

		c.statementBaseline[queryID] = statement
	}

	c.recordSuccess(ctx, "postgres", snapshot.SampledAt, snapshot.Errors, 0)
}

func (c *PerformanceCollector) collectRedis(ctx context.Context, minute time.Time) {
	snapshot, err := c.redis.Collect(ctx)
	if err != nil {
		c.recordFailure(ctx, "redis", "redis_unavailable")
		return
	}
	for metric, sample := range snapshot.Metrics {
		delta, reset := metricDelta(c.redisBaseline, metric, sample)
		bucket := model.RedisMetricBucket{
			BucketStart: minute, ServiceID: c.serviceID,
			Environment: c.environment, Metric: metric, Value: sample.Value, Delta: delta, Reset: reset,
		}
		if err := c.writer.UpsertRedis(ctx, bucket); err != nil {
			c.recordFailure(ctx, "redis", "redis_write_failed")
			return
		}
		if sample.Cumulative {
			c.redisBaseline[metric] = sample.Value
		}
	}
	for family, command := range snapshot.Commands {
		for metric, value := range map[string]float64{"command_calls": command.Calls, "command_usec": command.Microseconds} {
			key := metric + ":" + family
			delta, reset := metricDelta(c.redisBaseline, key, model.NumericSample{Value: value, Cumulative: true})
			bucket := model.RedisMetricBucket{
				BucketStart: minute, ServiceID: c.serviceID, Environment: c.environment,
				Metric: metric, CommandFamily: family, Value: value, Delta: delta, Reset: reset,
			}
			if err := c.writer.UpsertRedis(ctx, bucket); err != nil {
				c.recordFailure(ctx, "redis", "redis_write_failed")
				return
			}

			c.redisBaseline[key] = value
		}
	}

	c.recordSuccess(ctx, "redis", snapshot.SampledAt, snapshot.Errors, 0)
}

func metricDelta(baseline map[string]float64, key string, sample model.NumericSample) (float64, bool) {
	if !sample.Cumulative {
		return 0, false
	}
	previous, exists := baseline[key]
	delta, reset, valid := dbstats.CounterDelta(sample.Value, previous, exists)
	if !valid {
		return 0, reset
	}
	return delta, false
}

func (c *PerformanceCollector) statementBucket(minute time.Time, queryID string, current model.PostgresStatementSnapshot) model.PostgresStatementBucket {
	previous, exists := c.statementBaseline[queryID]
	reset := exists && statementReset(current, previous)
	bucket := model.PostgresStatementBucket{BucketStart: minute, ServiceID: c.serviceID, Environment: c.environment, QueryID: queryID, Reset: reset}
	if !exists || reset {
		return bucket
	}
	bucket.Calls = current.Calls - previous.Calls
	bucket.TotalExecMS = current.TotalExecMS - previous.TotalExecMS
	bucket.Rows = current.Rows - previous.Rows
	bucket.SharedBlocksHit = current.SharedBlocksHit - previous.SharedBlocksHit
	bucket.SharedBlocksRead = current.SharedBlocksRead - previous.SharedBlocksRead
	bucket.TempBlocksRead = current.TempBlocksRead - previous.TempBlocksRead
	bucket.TempBlocksWritten = current.TempBlocksWritten - previous.TempBlocksWritten
	return bucket
}

func statementReset(current, previous model.PostgresStatementSnapshot) bool {
	return current.Calls < previous.Calls || current.TotalExecMS < previous.TotalExecMS ||
		current.Rows < previous.Rows || current.SharedBlocksHit < previous.SharedBlocksHit ||
		current.SharedBlocksRead < previous.SharedBlocksRead || current.TempBlocksRead < previous.TempBlocksRead ||
		current.TempBlocksWritten < previous.TempBlocksWritten
}

func (c *PerformanceCollector) collectHTTP(ctx context.Context, minute time.Time) {
	observations, cursor, err := c.http.Read(ctx)
	if err != nil {
		c.recordFailure(ctx, "nginx", "nginx_unavailable")
		return
	}
	for _, bucket := range aggregateHTTP(observations, minute, c.serviceID, c.environment, cursor) {
		if err := c.writer.UpsertHTTP(ctx, bucket); err != nil {
			c.recordFailure(ctx, "nginx", "nginx_write_failed")
			return
		}
	}

	cursor = c.mergePersistedDrops(ctx, boundedCursor(cursor))
	cursor.CoverageComplete = true
	if err := c.writer.Upsert(ctx, cursor); err != nil {
		c.logger.Warn("persisting nginx collector cursor", "error_category", "cursor_write_failed")
		return
	}

	if acknowledger, ok := c.http.(httpSourceAcknowledger); ok {
		acknowledger.Acknowledge(cursor)
	}
}

func (c *PerformanceCollector) mergePersistedDrops(ctx context.Context, cursor model.CollectorCursor) model.CollectorCursor {
	persisted, err := c.writer.Get(ctx, cursor.Source)
	if err == nil && persisted.DroppedRecords > cursor.DroppedRecords {
		cursor.DroppedRecords = persisted.DroppedRecords
	}

	return cursor
}

func aggregateHTTP(observations []model.HTTPObservation, minute time.Time, serviceID, environment string, cursor model.CollectorCursor) []model.HTTPMetricBucket {
	buckets := map[string]model.HTTPMetricBucket{}
	for _, observation := range observations {
		bucketStart := observation.Timestamp.UTC().Truncate(time.Minute)
		if bucketStart.IsZero() {
			bucketStart = minute
		}
		statusClass := observation.Status / 100
		key := fmt.Sprintf("%s\x1f%s\x1f%s\x1f%s\x1f%d", bucketStart.Format(time.RFC3339), observation.Method, observation.RouteFamily, observation.Upstream, statusClass)
		bucket := buckets[key]
		bucket.BucketStart, bucket.ServiceID, bucket.Environment = bucketStart, serviceID, environment
		bucket.Method, bucket.RouteFamily, bucket.Upstream, bucket.StatusClass = observation.Method, observation.RouteFamily, observation.Upstream, statusClass
		bucket.RequestCount++
		if observation.Status >= 500 {
			bucket.ErrorCount++
		}
		bucket.Histogram.Observe(observation.DurationMS)
		bucket.DurationTotalMS += observation.DurationMS
		bucket.UpstreamConnectTotalMS += observation.UpstreamConnectMS
		bucket.UpstreamHeaderTotalMS += observation.UpstreamHeaderMS
		bucket.UpstreamResponseTotalMS += observation.UpstreamResponseMS
		bucket.BytesSent += observation.BytesSent
		bucket.RequestBytes += observation.RequestBytes
		bucket.IngestKey = fmt.Sprintf("%s:%d", cursor.Identity, cursor.Offset)
		buckets[key] = bucket
	}
	result := make([]model.HTTPMetricBucket, 0, len(buckets))
	for _, bucket := range buckets {
		result = append(result, bucket)
	}
	return result
}

func (c *PerformanceCollector) collectLogVolume(ctx context.Context) {
	buckets, cursor, err := c.logVolume.ReadLogVolume(ctx)
	if err != nil {
		c.recordFailure(ctx, "log_volume", "log_volume_unavailable")
		return
	}
	for _, bucket := range buckets {
		if err := c.writer.UpsertLogVolume(ctx, bucket); err != nil {
			c.recordFailure(ctx, "log_volume", "log_volume_write_failed")
			return
		}
	}
	cursor.CoverageComplete = true
	if err := c.writer.Upsert(ctx, boundedCursor(cursor)); err != nil {
		c.logger.Warn("persisting log volume cursor", "error_category", "cursor_write_failed")
	}
}

func (c *PerformanceCollector) recordFailure(ctx context.Context, source, category string) {
	cursor, _ := c.writer.Get(ctx, source)
	cursor.Source = source
	cursor.LastError = category
	cursor.CoverageComplete = false
	cursor.DroppedRecords = boundedDrops(cursor.DroppedRecords, 1)
	cursor.UpdatedAt = c.clock.Now().UTC()
	if err := c.writer.Upsert(ctx, cursor); err != nil {
		c.logger.Warn("persisting collector failure", "source", source, "error_category", "cursor_write_failed")
	}
}

func (c *PerformanceCollector) recordSuccess(ctx context.Context, source string, recorded time.Time, partial []string, dropped int64) {
	cursor, _ := c.writer.Get(ctx, source)
	cursor.Source = source
	if recorded.IsZero() {
		recorded = c.clock.Now().UTC()
	}
	cursor.LastRecordedAt = recorded.UTC()
	cursor.LastSuccessAt = c.clock.Now().UTC()
	cursor.LastError = strings.Join(partial, ",")
	cursor.CoverageComplete = len(partial) == 0
	cursor.DroppedRecords = boundedDrops(cursor.DroppedRecords, dropped)
	cursor.UpdatedAt = c.clock.Now().UTC()
	if err := c.writer.Upsert(ctx, cursor); err != nil {
		c.logger.Warn("persisting collector success", "source", source, "error_category", "cursor_write_failed")
	}
}

func boundedDrops(current, added int64) int64 {
	if added <= 0 {
		return current
	}
	if current >= maximumDroppedRecords-added {
		return maximumDroppedRecords
	}
	return current + added
}

func boundedCursor(cursor model.CollectorCursor) model.CollectorCursor {
	if cursor.DroppedRecords > maximumDroppedRecords {
		cursor.DroppedRecords = maximumDroppedRecords
	}
	return cursor
}
