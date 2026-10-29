package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"runtime"
	"sync"
	"time"

	"github.com/oralecarlangelo/touchgrass/internal/docker"
	"github.com/oralecarlangelo/touchgrass/internal/model"
	"github.com/oralecarlangelo/touchgrass/internal/store"
)

const dockerEventCursorSource = "docker_events"

// EventStreamer is the bounded Docker lifecycle source.
type EventStreamer interface {
	EventStream(context.Context, string) (<-chan docker.Event, <-chan error)
}

// ContainerEventWriter persists events and durable stream progress.
type ContainerEventWriter interface {
	UpsertContainerEvent(context.Context, model.ContainerEvent) error
	Get(context.Context, string) (model.CollectorCursor, error)
	Upsert(context.Context, model.CollectorCursor) error
}

// ContainerEventCollectorConfig wires lifecycle collection.
type ContainerEventCollectorConfig struct {
	Services         *store.ServiceStore
	Docker           EventStreamer
	Writer           ContainerEventWriter
	Environment      string
	Expected         func(string) bool
	Logger           *slog.Logger
	RequestReconcile func()
}

// ContainerEventCollector persists bounded Docker lifecycle transitions.
type ContainerEventCollector struct {
	services         *store.ServiceStore
	docker           EventStreamer
	writer           ContainerEventWriter
	environment      string
	expected         func(string) bool
	logger           *slog.Logger
	requestReconcile func()
}

// NewContainerEventCollector builds a collector.
func NewContainerEventCollector(cfg ContainerEventCollectorConfig) *ContainerEventCollector {
	expected := cfg.Expected
	if expected == nil {
		expected = func(string) bool { return false }
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	requestReconcile := cfg.RequestReconcile
	if requestReconcile == nil {
		requestReconcile = func() {}
	}
	return &ContainerEventCollector{services: cfg.Services, docker: cfg.Docker, writer: cfg.Writer,
		environment: cfg.Environment, expected: expected, logger: logger, requestReconcile: requestReconcile}
}

// Run reconnects after bounded stream failures until cancellation.
func (c *ContainerEventCollector) Run(ctx context.Context) {
	if c == nil || c.docker == nil || c.writer == nil {
		return
	}
	for {
		err := c.runStream(ctx)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			c.logger.Warn("docker event stream failed", "error_category", "docker_events_unavailable")
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Second):
		}
	}
}

func (c *ContainerEventCollector) runStream(ctx context.Context) error {
	definitions, err := c.services.All(ctx)
	if err != nil {
		return fmt.Errorf("loading services for docker events: %w", err)
	}
	cursor, err := c.writer.Get(ctx, dockerEventCursorSource)
	if err != nil && !errors.Is(err, store.ErrCollectorCursorNotFound) {
		return fmt.Errorf("loading docker event cursor: %w", err)
	}
	since := ""
	if !cursor.LastRecordedAt.IsZero() {
		since = cursor.LastRecordedAt.UTC().Format(time.RFC3339Nano)
	}
	events, streamErrors := c.docker.EventStream(ctx, since)
	for events != nil || streamErrors != nil {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case event, ok := <-events:
			if !ok {
				events = nil
				continue
			}
			if err := c.persistEvent(ctx, definitions, event, &cursor); err != nil {
				return err
			}
		case streamErr, ok := <-streamErrors:
			if !ok {
				streamErrors = nil
				continue
			}
			if streamErr != nil {
				now := time.Now().UTC()
				cursor.Source = dockerEventCursorSource
				cursor.LastError = "docker_events_unavailable"
				cursor.CoverageComplete = false
				cursor.UpdatedAt = now
				if err := c.writer.Upsert(ctx, cursor); err != nil {
					c.logger.Warn("persisting docker event failure", "error_category", "cursor_write_failed")
				}
				c.requestReconcile()
				return errors.New("docker event stream unavailable")
			}
		}
	}
	c.requestReconcile()
	return nil
}

func (c *ContainerEventCollector) persistEvent(ctx context.Context, definitions []model.Service, event docker.Event, cursor *model.CollectorCursor) error {
	serviceID := ownerForDockerEvent(definitions, event)
	bounded := model.ContainerEvent{SourceEventID: event.SourceEventID, OccurredAt: event.OccurredAt,
		ContainerID: event.ContainerID, ServiceID: serviceID, Environment: c.environment, EventType: event.Action,
		ContainerName: event.ContainerName, ComposeService: event.ComposeService,
		Expected: serviceID != "" && c.expected(serviceID)}
	if err := c.writer.UpsertContainerEvent(ctx, bounded); err != nil {
		return fmt.Errorf("persisting docker event: %w", err)
	}
	now := time.Now().UTC()
	cursor.Source = dockerEventCursorSource
	cursor.Identity = event.SourceEventID
	if event.OccurredAt.After(cursor.LastRecordedAt) {
		cursor.LastRecordedAt = event.OccurredAt
	}
	cursor.LastSuccessAt, cursor.UpdatedAt, cursor.LastError = now, now, ""
	cursor.CoverageComplete = true
	if err := c.writer.Upsert(ctx, *cursor); err != nil {
		return fmt.Errorf("persisting docker event cursor: %w", err)
	}
	c.requestReconcile()
	return nil
}

func ownerForDockerEvent(definitions []model.Service, event docker.Event) string {
	container := docker.Container{Name: event.ContainerName, Labels: map[string]string{
		docker.LabelComposeProject: event.ComposeProject, docker.LabelComposeService: event.ComposeService,
	}}
	for _, definition := range definitions {
		names, err := serviceNames(definition)
		if err != nil {
			continue
		}
		wanted := make(map[string]bool, len(names))
		for _, name := range names {
			wanted[name] = true
		}
		if belongsTo(container, definition.ComposeProject, wanted) {
			return definition.ID
		}
	}
	return ""
}

// Host history window bounds served by the fleet history endpoint.
const (
	defaultHistoryHours = 24
	maxHistoryHours     = 168
	maxHistoryPoints    = 1500
)

// sampleFleet records one row per container plus one host row. Managed
// containers match their service owners; every other container samples
// as unmanaged. Only a broken service config fails the cycle — per
// container and host failures log and skip.
func (s *Sampler) sampleFleet(
	ctx context.Context,
	defs []model.Service,
	containers []docker.Container,
	statsByID map[string]docker.Stats,
	now time.Time,
) error {
	owners := make(map[string]string, len(containers))

	for _, def := range defs {
		names, err := serviceNames(def)
		if err != nil {
			return err
		}

		want := make(map[string]bool, len(names))

		for _, name := range names {
			want[name] = true
		}

		for _, c := range containers {
			if belongsTo(c, def.ComposeProject, want) {
				owners[c.ID] = def.ID
			}
		}
	}

	var group sync.WaitGroup

	for _, c := range containers {
		stats, ok := statsByID[c.ID]
		if !ok {
			continue
		}
		group.Go(func() {
			s.recordFleetSample(ctx, c, owners[c.ID], stats, now)
		})
	}

	group.Wait()
	s.sampleHost(ctx, now)

	return nil
}

// recordFleetSample records one already-collected container's fleet row. Managed
// rows duplicate the metrics table deliberately so the fleet view is
// one query.
func (s *Sampler) recordFleetSample(
	ctx context.Context,
	c docker.Container,
	serviceID string,
	stats docker.Stats,
	now time.Time,
) {
	sample := model.ContainerSample{
		SampledAt:     now,
		ContainerID:   c.ID,
		ContainerName: c.Name,
		Project:       c.Labels[docker.LabelComposeProject],
		Managed:       serviceID != "",
		ServiceID:     serviceID,
		State:         c.State,
		CPUPercent:    stats.CPUPercent,
		MemBytes:      stats.MemBytes,
		MemLimit:      stats.MemLimit,
		Restarts:      stats.Restarts,
	}

	if err := s.fleet.InsertContainer(ctx, sample); err != nil {
		s.logger.Warn("fleet insert failed", "container", c.Name, "error", err)
	}
}

// sampleHost records one host row reusing the system snapshot helpers.
func (s *Sampler) sampleHost(ctx context.Context, now time.Time) {
	memUsed, memTotal := hostMem()
	diskUsed, diskTotal := hostDisk(s.hostRoot)

	sample := model.HostSample{
		SampledAt:  now,
		CPUPercent: s.sampleHostCPU(),
		MemUsed:    memUsed,
		MemTotal:   memTotal,
		DiskUsed:   diskUsed,
		DiskTotal:  diskTotal,
		Load1:      hostLoad1(),
	}

	if err := s.fleet.InsertHost(ctx, sample); err != nil {
		s.logger.Warn("host sample insert failed", "error", err)
	}
}

// sampleHostCPU reports host CPU usage since the previous sample. It
// reuses the system cpu parsers; the delta state is sampler-owned so
// interval sampling stays independent of on-demand snapshots. It
// returns nil on non-Linux hosts, on read failure, and on the first
// call, which has no delta yet.
func (s *Sampler) sampleHostCPU() *float64 {
	if runtime.GOOS != goosLinux {
		return nil
	}

	raw, err := os.ReadFile("/proc/stat")
	if err != nil {
		return nil
	}

	current, err := parseCPUStat(string(raw))
	if err != nil {
		return nil
	}

	s.cpuMu.Lock()
	defer s.cpuMu.Unlock()

	usage, ok := cpuUsage(s.cpuLast, current)
	s.cpuLast = &current

	if !ok {
		return nil
	}

	return &usage
}

// FleetContainers returns the latest sample per container, hottest CPU
// first.
func (s *Sampler) FleetContainers(ctx context.Context) ([]model.FleetContainer, error) {
	return s.fleet.LatestContainers(ctx)
}

// HostHistory returns bucket-averaged host points for metric over the
// trailing hours window, oldest first. Buckets size to cap the series
// at maxHistoryPoints.
func (s *Sampler) HostHistory(
	ctx context.Context,
	metric string,
	hours int,
) ([]model.HistoryPoint, error) {
	switch metric {
	case model.MetricCPU, model.MetricMem, model.MetricLoad:
	default:
		return nil, fmt.Errorf("%w: metric %q (want cpu, mem, or load)", ErrInvalidInput, metric)
	}

	if hours <= 0 {
		hours = defaultHistoryHours
	}

	if hours > maxHistoryHours {
		hours = maxHistoryHours
	}

	bucket := (int64(hours)*3600 + maxHistoryPoints - 1) / maxHistoryPoints
	since := time.Now().Add(-time.Duration(hours) * time.Hour)

	return s.fleet.History(ctx, metric, since, bucket)
}
