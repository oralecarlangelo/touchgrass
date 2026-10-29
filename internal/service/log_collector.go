package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"log/slog"
	"slices"
	"sync"
	"time"

	"github.com/oralecarlangelo/touchgrass/internal/docker"
	"github.com/oralecarlangelo/touchgrass/internal/model"
	"github.com/oralecarlangelo/touchgrass/internal/store"
)

// Collection bounds: drops, never OOMs.
const (
	maxLinesPerPoll   = 1000
	maxLogLineBytes   = 8192
	maxContextLines   = 1000
	maxLogSearchLimit = 1000
	defaultLogSearch  = 100
)

// LogLister tails container logs. docker.Client is production.
type LogLister interface {
	List(ctx context.Context) ([]docker.Container, error)
	Logs(ctx context.Context, containerID, since string, tail int) ([]docker.LogLine, error)
}

// LogCollectorConfig wires a LogCollector.
type LogCollectorConfig struct {
	Services *store.ServiceStore
	Docker   LogLister
	Logs     *store.LogStore
	Cursors  interface {
		Get(context.Context, string) (model.CollectorCursor, error)
		Upsert(context.Context, model.CollectorCursor) error
	}
	Performance interface {
		UpsertLogVolume(context.Context, model.LogVolumeBucket) error
	}
	Environment        string
	Interval           time.Duration
	MaxLinesPerService int
	Logger             *slog.Logger
}

// logCursor tracks the newest kept timestamp plus the number of lines at that
// exact boundary. It deliberately retains no raw log text.
type logCursor struct {
	since    string
	maxTs    time.Time
	boundary int64
}

// logLossCounters tracks one service's backpressure losses.
type logLossCounters struct {
	drops       int64
	truncations int64
}

// LogCollector tails managed containers into the log store on a poll
// loop and serves log reads.
type LogCollector struct {
	services     *store.ServiceStore
	docker       LogLister
	logs         *store.LogStore
	cursorsStore interface {
		Get(context.Context, string) (model.CollectorCursor, error)
		Upsert(context.Context, model.CollectorCursor) error
	}
	performance interface {
		UpsertLogVolume(context.Context, model.LogVolumeBucket) error
	}
	environment        string
	interval           time.Duration
	maxLinesPerService int
	logger             *slog.Logger
	cursors            map[string]logCursor
	lossMu             sync.Mutex
	losses             map[string]*logLossCounters
}

// NewLogCollector builds a LogCollector.
func NewLogCollector(cfg LogCollectorConfig) *LogCollector {
	return &LogCollector{
		services:           cfg.Services,
		docker:             cfg.Docker,
		logs:               cfg.Logs,
		cursorsStore:       cfg.Cursors,
		performance:        cfg.Performance,
		environment:        cfg.Environment,
		interval:           cfg.Interval,
		maxLinesPerService: cfg.MaxLinesPerService,
		logger:             cfg.Logger,
		cursors:            map[string]logCursor{},
		losses:             map[string]*logLossCounters{},
	}
}

// Run collects on the interval until ctx is cancelled.
func (c *LogCollector) Run(ctx context.Context) {
	if err := c.collect(ctx); err != nil {
		c.logger.Warn("log collection failed", "error", err)
	}

	ticker := time.NewTicker(c.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := c.collect(ctx); err != nil {
				c.logger.Warn("log collection failed", "error", err)
			}
		}
	}
}

// Drops reports lines dropped to backpressure so far, all services.
func (c *LogCollector) Drops() int64 {
	c.lossMu.Lock()
	defer c.lossMu.Unlock()

	var total int64

	for _, counters := range c.losses {
		total += counters.drops
	}

	return total
}

// Stats reports one service's stored lines plus backpressure losses.
func (c *LogCollector) Stats(ctx context.Context, serviceID string) (model.LogStats, error) {
	if _, err := c.services.Get(ctx, serviceID); err != nil {
		return model.LogStats{}, err
	}

	lines, err := c.logs.CountByService(ctx, serviceID)
	if err != nil {
		return model.LogStats{}, fmt.Errorf("counting log lines: %w", err)
	}

	c.lossMu.Lock()
	defer c.lossMu.Unlock()

	stats := model.LogStats{ServiceID: serviceID, Lines: lines}

	counters, ok := c.losses[serviceID]
	if !ok {
		counters = c.loadLosses(ctx, serviceID)
		c.losses[serviceID] = counters
	}
	if counters != nil {
		stats.Drops = counters.drops
		stats.Truncations = counters.truncations
	}

	return stats, nil
}

// addDrops records poll-cap drops for a service.
func (c *LogCollector) addDrops(ctx context.Context, serviceID string, rows int64) {
	c.lossMu.Lock()
	defer c.lossMu.Unlock()

	counters, ok := c.losses[serviceID]
	if !ok {
		counters = &logLossCounters{}
		c.losses[serviceID] = counters
	}

	counters.drops += rows
	c.persistLoss(ctx, "drops", serviceID, counters.drops)
}

// addTruncations records line truncations for a service.
func (c *LogCollector) addTruncations(ctx context.Context, serviceID string, rows int64) {
	c.lossMu.Lock()
	defer c.lossMu.Unlock()

	counters, ok := c.losses[serviceID]
	if !ok {
		counters = &logLossCounters{}
		c.losses[serviceID] = counters
	}

	counters.truncations += rows
	c.persistLoss(ctx, "truncations", serviceID, counters.truncations)
}

func (c *LogCollector) loadLosses(ctx context.Context, serviceID string) *logLossCounters {
	counters := &logLossCounters{}
	if c.cursorsStore == nil {
		return counters
	}
	if cursor, err := c.cursorsStore.Get(ctx, logLossSource("drops", serviceID)); err == nil {
		counters.drops = cursor.Offset
	}
	if cursor, err := c.cursorsStore.Get(ctx, logLossSource("truncations", serviceID)); err == nil {
		counters.truncations = cursor.Offset
	}
	return counters
}

func (c *LogCollector) persistLoss(ctx context.Context, kind, serviceID string, value int64) {
	if c.cursorsStore == nil {
		return
	}
	now := time.Now().UTC()
	cursor := model.CollectorCursor{Source: logLossSource(kind, serviceID), Identity: serviceID,
		Offset: value, LastRecordedAt: now, LastSuccessAt: now, UpdatedAt: now}
	if err := c.cursorsStore.Upsert(ctx, cursor); err != nil {
		c.logger.Warn("persisting log loss counter", "service", serviceID, "error_category", "cursor_write_failed")
	}
}

func logLossSource(kind, serviceID string) string { return "docker_log_" + kind + ":" + serviceID }

func logCursorSource(containerID string) string { return "docker_log:" + containerID }

// LogSearch carries log search options. Empty Stream selects all
// streams; empty Level selects all levels, otherwise a comma-separated
// set (error, warn, info, debug).
type LogSearch struct {
	ServiceID string
	Query     string
	Stream    string
	Level     string
	Since     *time.Time
	Until     *time.Time
	Limit     int
}

// Level-scan bounds: chunked newest-first pages until the limit fills or
// the budget runs out, so rare levels cannot scan the whole table.
const (
	levelScanChunk     = 1000
	levelScanMaxChunks = 20
)

// Search validates the service and searches its lines newest first,
// stamping the parsed level on every line.
func (c *LogCollector) Search(ctx context.Context, search LogSearch) ([]model.LogLine, error) {
	if _, err := c.services.Get(ctx, search.ServiceID); err != nil {
		return nil, err
	}

	if search.Stream != "" && search.Stream != model.LogStdout && search.Stream != model.LogStderr {
		return nil, fmt.Errorf("%w: stream %q (want stdout or stderr)", ErrInvalidInput, search.Stream)
	}

	levels, err := NormalizeLogLevels(search.Level)
	if err != nil {
		return nil, err
	}

	limit := clampSearchLimit(search.Limit)

	if len(levels) == 0 {
		lines, err := c.logs.Search(ctx, store.LogFilter{
			ServiceID: search.ServiceID, Query: search.Query, Stream: search.Stream,
			Since: search.Since, Until: search.Until, Limit: limit,
		})
		if err != nil {
			return nil, err
		}

		return withLevels(lines), nil
	}

	return c.searchLevels(ctx, search, levels, limit)
}

// searchLevels pages newest-first until the level set fills the limit or
// the scan budget runs out, keeping limits exact for rare levels.
func (c *LogCollector) searchLevels(
	ctx context.Context,
	search LogSearch,
	levels map[string]bool,
	limit int,
) ([]model.LogLine, error) {
	matched := []model.LogLine{}
	beforeID := int64(0)

	for chunk := 0; chunk < levelScanMaxChunks && len(matched) < limit; chunk++ {
		page, err := c.logs.Search(ctx, store.LogFilter{
			ServiceID: search.ServiceID, Query: search.Query, Stream: search.Stream,
			Since: search.Since, Until: search.Until, BeforeID: beforeID, Limit: levelScanChunk,
		})
		if err != nil {
			return nil, err
		}

		if len(page) == 0 {
			break
		}

		for _, line := range page {
			line.Level = ParseLogLevel(line.Stream, line.Line)

			if levels[line.Level] {
				matched = append(matched, line)

				if len(matched) >= limit {
					break
				}
			}
		}

		beforeID = page[len(page)-1].ID

		if len(page) < levelScanChunk {
			break
		}
	}

	return matched, nil
}

// Context returns a line with its surroundings, levels stamped.
func (c *LogCollector) Context(
	ctx context.Context,
	id int64,
	before, after int,
) (model.LogContext, error) {
	got, err := c.logs.Context(ctx, id, clampContextLines(before), clampContextLines(after))
	if err != nil {
		return model.LogContext{}, err
	}

	got.Anchor.Level = ParseLogLevel(got.Anchor.Stream, got.Anchor.Line)

	return model.LogContext{
		Anchor: got.Anchor,
		Before: withLevels(got.Before),
		After:  withLevels(got.After),
	}, nil
}

// collect tails every managed container once.
func (c *LogCollector) collect(ctx context.Context) error {
	defs, err := c.services.All(ctx)
	if err != nil {
		return fmt.Errorf("loading services: %w", err)
	}

	containers, err := c.docker.List(ctx)
	if err != nil {
		return fmt.Errorf("listing containers: %w", err)
	}

	batch, touched, pending, err := c.tailManaged(ctx, defs, containers)
	if err != nil {
		return err
	}

	if err := c.flush(ctx, batch); err != nil {
		return err
	}
	c.flushLogVolume(ctx, batch)
	for containerID, cursor := range pending {
		c.cursors[containerID] = cursor
		c.persistLogCursor(ctx, containerID, cursor)
	}

	for serviceID := range touched {
		c.trimService(ctx, serviceID)
	}

	return nil
}

// flush stores one collection batch, skipping empty cycles. Lines sort
// oldest-first so row ids stay chronological across stream blocks.
func (c *LogCollector) flush(ctx context.Context, batch []model.LogLine) error {
	if len(batch) == 0 {
		return nil
	}

	slices.SortStableFunc(batch, func(a, b model.LogLine) int {
		return a.Ts.Compare(b.Ts)
	})

	if _, err := c.logs.InsertBatch(ctx, batch); err != nil {
		return fmt.Errorf("storing log batch: %w", err)
	}

	return nil
}

// tailAccum carries one collection cycle's batch and id sets.
type tailAccum struct {
	batch   []model.LogLine
	seen    map[string]bool
	touched map[string]bool
	pending map[string]logCursor
}

// tailManaged tails every container owned by a service definition,
// returning the batch plus the touched service ids.
func (c *LogCollector) tailManaged(
	ctx context.Context,
	defs []model.Service,
	containers []docker.Container,
) ([]model.LogLine, map[string]bool, map[string]logCursor, error) {
	accum := tailAccum{
		batch:   []model.LogLine{},
		seen:    make(map[string]bool, len(containers)),
		touched: map[string]bool{},
		pending: make(map[string]logCursor, len(containers)),
	}

	for _, def := range defs {
		if err := c.tailService(ctx, def, containers, &accum); err != nil {
			return nil, nil, nil, err
		}
	}

	for id := range c.cursors {
		if !accum.seen[id] {
			delete(c.cursors, id)
		}
	}

	return accum.batch, accum.touched, accum.pending, nil
}

// tailService tails one definition's containers, recording seen and touched ids.
func (c *LogCollector) tailService(
	ctx context.Context,
	def model.Service,
	containers []docker.Container,
	accum *tailAccum,
) error {
	names, err := serviceNames(def)
	if err != nil {
		return err
	}

	want := make(map[string]bool, len(names))
	for _, name := range names {
		want[name] = true
	}

	for _, container := range containers {
		if !belongsTo(container, def.ComposeProject, want) {
			continue
		}

		accum.seen[container.ID] = true
		accum.touched[def.ID] = true
		var cursor logCursor
		accum.batch, cursor = c.tailOne(ctx, def.ID, container, accum.batch)
		accum.pending[container.ID] = cursor
	}

	return nil
}

// trimService enforces one service's line cap after the batch insert.
func (c *LogCollector) trimService(ctx context.Context, serviceID string) {
	trimmed, err := c.logs.TrimBeyondCap(ctx, serviceID, c.maxLinesPerService)
	if err != nil {
		c.logger.Warn("log cap trim failed", "service", serviceID, "error", err)
	} else if trimmed > 0 {
		c.logger.Info("retention trimmed log lines", "service", serviceID, "rows", trimmed)
	}
}

// tailOne appends one container's new lines, advancing its cursor.
// Failures log and skip the container without failing the cycle.
func (c *LogCollector) tailOne(
	ctx context.Context,
	serviceID string,
	container docker.Container,
	batch []model.LogLine,
) ([]model.LogLine, logCursor) {
	cursor := c.loadLogCursor(ctx, container.ID)

	lines, err := c.docker.Logs(ctx, container.ID, cursor.since, maxLinesPerPoll)
	if err != nil {
		c.logger.Warn("container log tail failed", "container", container.Name, "error", err)

		return batch, cursor
	}

	keep := newTailKeep(cursor, serviceID, container.Name, time.Now())

	for i, line := range lines {
		if i >= maxLinesPerPoll {
			c.addDrops(ctx, serviceID, int64(len(lines)-i))
			c.logger.Warn("log backpressure drop", "container", container.Name,
				"rows", len(lines)-i)

			break
		}

		batch = keep.line(batch, line)
	}

	if keep.truncated > 0 {
		c.addTruncations(ctx, serviceID, keep.truncated)
		c.logger.Info("log lines truncated", "container", container.Name, "rows", keep.truncated)
	}

	return batch, keep.advanced()
}

// tailKeep filters one poll's lines to the new ones, tracking the max
// stamp as the next cursor.
type tailKeep struct {
	cursor       logCursor
	serviceID    string
	container    string
	now          time.Time
	maxTs        time.Time
	truncated    int64
	boundarySeen int64
	maxBoundary  int64
}

// newTailKeep builds a filter over the container's current cursor.
func newTailKeep(cursor logCursor, serviceID, container string, now time.Time) *tailKeep {
	return &tailKeep{
		cursor: cursor, serviceID: serviceID, container: container, now: now,
		maxTs:       cursor.maxTs,
		maxBoundary: cursor.boundary,
	}
}

// line appends one daemon line when it is newer than the cursor.
func (k *tailKeep) line(batch []model.LogLine, line docker.LogLine) []model.LogLine {
	ts := line.Timestamp
	if ts.IsZero() {
		ts = k.now
	}

	// Older than the cursor means already kept: skip even if the
	// daemon ignored since. Genuinely new lines with old stamps
	// (late-flushed buffers) are the accepted trade-off.
	if !k.cursor.maxTs.IsZero() && ts.Before(k.cursor.maxTs) {
		return batch
	}

	if ts.Equal(k.cursor.maxTs) && k.boundarySeen < k.cursor.boundary {
		k.boundarySeen++
		return batch
	}
	if len(line.Message) > maxLogLineBytes {
		k.truncated++
	}
	message := truncate(line.Message, maxLogLineBytes)

	if k.maxTs.IsZero() || ts.After(k.maxTs) {
		k.maxTs = ts
		k.maxBoundary = 1
	} else if ts.Equal(k.maxTs) {
		k.maxBoundary++
	}

	return append(batch, model.LogLine{
		ServiceID: k.serviceID, Container: k.container, Stream: line.Stream,
		Line: message, Ts: ts,
	})
}

// cursor returns the cursor advanced to this poll's max stamp.
func (k *tailKeep) advanced() logCursor {
	if k.maxTs.IsZero() {
		return k.cursor
	}

	k.cursor.since = k.maxTs.UTC().Format(time.RFC3339Nano)
	k.cursor.maxTs = k.maxTs
	k.cursor.boundary = k.maxBoundary

	return k.cursor
}

func (c *LogCollector) loadLogCursor(ctx context.Context, containerID string) logCursor {
	if cursor, ok := c.cursors[containerID]; ok {
		return cursor
	}
	if c.cursorsStore == nil {
		return logCursor{}
	}
	persisted, err := c.cursorsStore.Get(ctx, logCursorSource(containerID))
	if err != nil {
		if !errors.Is(err, store.ErrCollectorCursorNotFound) {
			c.logger.Warn("loading docker log cursor", "container", containerID, "error_category", "cursor_read_failed")
		}
		return logCursor{}
	}
	return logCursor{since: persisted.LastRecordedAt.UTC().Format(time.RFC3339Nano),
		maxTs: persisted.LastRecordedAt, boundary: persisted.Offset}
}

func (c *LogCollector) persistLogCursor(ctx context.Context, containerID string, cursor logCursor) {
	if c.cursorsStore == nil || cursor.maxTs.IsZero() {
		return
	}
	now := time.Now().UTC()
	persisted := model.CollectorCursor{Source: logCursorSource(containerID), Identity: containerID,
		Offset: cursor.boundary, LastRecordedAt: cursor.maxTs, LastSuccessAt: now, UpdatedAt: now}
	if err := c.cursorsStore.Upsert(ctx, persisted); err != nil {
		c.logger.Warn("persisting docker log cursor", "container", containerID, "error_category", "cursor_write_failed")
	}
}

func (c *LogCollector) flushLogVolume(ctx context.Context, batch []model.LogLine) {
	if c.performance == nil || len(batch) == 0 {
		return
	}
	hasher := sha256.New()
	buckets := map[string]model.LogVolumeBucket{}
	for _, line := range batch {
		writeLogIdentity(hasher, line)
		severity := ParseLogLevel(line.Stream, line.Line)
		minute := line.Ts.UTC().Truncate(time.Minute)
		key := line.ServiceID + "\x1f" + minute.Format(time.RFC3339Nano) + "\x1f" + severity
		bucket := buckets[key]
		bucket.BucketStart, bucket.ServiceID, bucket.Environment = minute, line.ServiceID, c.environment
		bucket.Severity = severity
		bucket.LineCount++
		buckets[key] = bucket
	}
	ingestKey := hex.EncodeToString(hasher.Sum(nil))
	lastRecorded := batch[0].Ts
	failed := false
	for _, bucket := range buckets {
		bucket.IngestKey = ingestKey
		if err := c.performance.UpsertLogVolume(ctx, bucket); err != nil {
			c.logger.Warn("persisting log volume", "service", bucket.ServiceID, "error_category", "log_volume_write_failed")
			failed = true
		}
	}
	for _, line := range batch[1:] {
		if line.Ts.After(lastRecorded) {
			lastRecorded = line.Ts
		}
	}
	c.recordLogVolumeCursor(ctx, lastRecorded, failed)
}

func (c *LogCollector) recordLogVolumeCursor(ctx context.Context, lastRecorded time.Time, failed bool) {
	if c.cursorsStore == nil {
		return
	}
	cursor, err := c.cursorsStore.Get(ctx, "log_volume")
	if err != nil && !errors.Is(err, store.ErrCollectorCursorNotFound) {
		c.logger.Warn("loading log volume cursor", "error_category", "cursor_read_failed")
		return
	}
	cursor.Source = "log_volume"
	if failed {
		cursor.LastError = "log_volume_write_failed"
		cursor.CoverageComplete = false
		cursor.DroppedRecords = boundedDrops(cursor.DroppedRecords, 1)
	} else {
		now := time.Now().UTC()
		cursor.LastSuccessAt = now
		cursor.LastRecordedAt = lastRecorded.UTC()
		cursor.LastError = ""
		cursor.CoverageComplete = true
	}
	if err := c.cursorsStore.Upsert(ctx, cursor); err != nil {
		c.logger.Warn("persisting log volume cursor", "error_category", "cursor_write_failed")
	}
}

func writeLogIdentity(hasher hash.Hash, line model.LogLine) {
	_, _ = fmt.Fprintf(hasher, "%s\x1f%s\x1f%s\x1f%s\x1f%s\n", line.ServiceID,
		line.Container, line.Stream, line.Ts.UTC().Format(time.RFC3339Nano), line.Line)
}

// clampSearchLimit bounds search pages.
func clampSearchLimit(limit int) int {
	if limit <= 0 {
		return defaultLogSearch
	}

	if limit > maxLogSearchLimit {
		return maxLogSearchLimit
	}

	return limit
}

// clampContextLines bounds context windows; zero stays zero.
func clampContextLines(lines int) int {
	if lines < 0 {
		return 0
	}

	if lines > maxContextLines {
		return maxContextLines
	}

	return lines
}
