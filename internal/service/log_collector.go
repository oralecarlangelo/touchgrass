package service

import (
	"context"
	"fmt"
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
	Services           *store.ServiceStore
	Docker             LogLister
	Logs               *store.LogStore
	Interval           time.Duration
	MaxLinesPerService int
	Logger             *slog.Logger
}

// logCursor tracks one container's tail position: the newest kept
// timestamp plus that line's identity, so an inclusive --since
// boundary never duplicates. The daemon returns stdout-then-stderr
// blocks (not chronological), so the cursor must be the max stamp,
// never the last line.
type logCursor struct {
	since      string
	maxTs      time.Time
	lastTs     string
	lastStream string
	lastLine   string
}

// logLossCounters tracks one service's backpressure losses.
type logLossCounters struct {
	drops       int64
	truncations int64
}

// LogCollector tails managed containers into the log store on a poll
// loop and serves log reads.
type LogCollector struct {
	services           *store.ServiceStore
	docker             LogLister
	logs               *store.LogStore
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

	if counters, ok := c.losses[serviceID]; ok {
		stats.Drops = counters.drops
		stats.Truncations = counters.truncations
	}

	return stats, nil
}

// addDrops records poll-cap drops for a service.
func (c *LogCollector) addDrops(serviceID string, rows int64) {
	c.lossMu.Lock()
	defer c.lossMu.Unlock()

	counters, ok := c.losses[serviceID]
	if !ok {
		counters = &logLossCounters{}
		c.losses[serviceID] = counters
	}

	counters.drops += rows
}

// addTruncations records line truncations for a service.
func (c *LogCollector) addTruncations(serviceID string, rows int64) {
	c.lossMu.Lock()
	defer c.lossMu.Unlock()

	counters, ok := c.losses[serviceID]
	if !ok {
		counters = &logLossCounters{}
		c.losses[serviceID] = counters
	}

	counters.truncations += rows
}

// Search validates the service and searches its lines newest first.
func (c *LogCollector) Search(
	ctx context.Context,
	serviceID, query string,
	since, until *time.Time,
	limit int,
) ([]model.LogLine, error) {
	if _, err := c.services.Get(ctx, serviceID); err != nil {
		return nil, err
	}

	return c.logs.Search(ctx, store.LogFilter{
		ServiceID: serviceID, Query: query,
		Since: since, Until: until, Limit: clampSearchLimit(limit),
	})
}

// Context returns a line with its surroundings.
func (c *LogCollector) Context(
	ctx context.Context,
	id int64,
	before, after int,
) (model.LogContext, error) {
	return c.logs.Context(ctx, id, clampContextLines(before), clampContextLines(after))
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

	batch, touched, err := c.tailManaged(ctx, defs, containers)
	if err != nil {
		return err
	}

	if err := c.flush(ctx, batch); err != nil {
		return err
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
}

// tailManaged tails every container owned by a service definition,
// returning the batch plus the touched service ids.
func (c *LogCollector) tailManaged(
	ctx context.Context,
	defs []model.Service,
	containers []docker.Container,
) ([]model.LogLine, map[string]bool, error) {
	accum := tailAccum{
		batch:   []model.LogLine{},
		seen:    make(map[string]bool, len(containers)),
		touched: map[string]bool{},
	}

	for _, def := range defs {
		if err := c.tailService(ctx, def, containers, &accum); err != nil {
			return nil, nil, err
		}
	}

	for id := range c.cursors {
		if !accum.seen[id] {
			delete(c.cursors, id)
		}
	}

	return accum.batch, accum.touched, nil
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
		accum.batch = c.tailOne(ctx, def.ID, container, accum.batch)
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
) []model.LogLine {
	cursor := c.cursors[container.ID]

	lines, err := c.docker.Logs(ctx, container.ID, cursor.since, maxLinesPerPoll)
	if err != nil {
		c.logger.Warn("container log tail failed", "container", container.Name, "error", err)

		return batch
	}

	keep := newTailKeep(cursor, serviceID, container.Name, time.Now())

	for i, line := range lines {
		if i >= maxLinesPerPoll {
			c.addDrops(serviceID, int64(len(lines)-i))
			c.logger.Warn("log backpressure drop", "container", container.Name,
				"rows", len(lines)-i)

			break
		}

		batch = keep.line(batch, line)
	}

	if keep.truncated > 0 {
		c.addTruncations(serviceID, keep.truncated)
		c.logger.Info("log lines truncated", "container", container.Name, "rows", keep.truncated)
	}

	c.cursors[container.ID] = keep.advanced()

	return batch
}

// tailKeep filters one poll's lines to the new ones, tracking the max
// stamp as the next cursor.
type tailKeep struct {
	cursor    logCursor
	serviceID string
	container string
	now       time.Time
	maxTs     time.Time
	maxStamp  string
	maxStream string
	maxMsg    string
	truncated int64
	skipped   bool
}

// newTailKeep builds a filter over the container's current cursor.
func newTailKeep(cursor logCursor, serviceID, container string, now time.Time) *tailKeep {
	return &tailKeep{
		cursor: cursor, serviceID: serviceID, container: container, now: now,
		maxTs:    cursor.maxTs,
		maxStamp: cursor.lastTs, maxStream: cursor.lastStream, maxMsg: cursor.lastLine,
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

	stamp := ts.UTC().Format(time.RFC3339Nano)

	if len(line.Message) > maxLogLineBytes {
		k.truncated++
	}

	message := truncate(line.Message, maxLogLineBytes)
	boundary := !k.skipped && k.isRecordedMax(stamp, line.Stream, message)

	if boundary {
		k.skipped = true

		return batch
	}

	if k.maxTs.IsZero() || ts.After(k.maxTs) {
		k.maxTs = ts
		k.maxStamp, k.maxStream, k.maxMsg = stamp, line.Stream, message
	}

	return append(batch, model.LogLine{
		ServiceID: k.serviceID, Container: k.container, Stream: line.Stream,
		Line: message, Ts: ts,
	})
}

// isRecordedMax reports whether a line is the cursor's recorded max.
func (k *tailKeep) isRecordedMax(stamp, stream, message string) bool {
	return k.cursor.lastLine != "" &&
		stamp == k.cursor.lastTs && stream == k.cursor.lastStream && message == k.cursor.lastLine
}

// cursor returns the cursor advanced to this poll's max stamp.
func (k *tailKeep) advanced() logCursor {
	if k.maxTs.IsZero() {
		return k.cursor
	}

	k.cursor.since = k.maxTs.UTC().Format(time.RFC3339Nano)
	k.cursor.maxTs = k.maxTs
	k.cursor.lastTs, k.cursor.lastStream, k.cursor.lastLine = k.maxStamp, k.maxStream, k.maxMsg

	return k.cursor
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
