package service

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"sync"
	"time"

	"github.com/oralecarlangelo/touchgrass/internal/docker"
	"github.com/oralecarlangelo/touchgrass/internal/model"
)

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
		group.Go(func() {
			s.sampleFleetOne(ctx, c, owners[c.ID], now)
		})
	}

	group.Wait()
	s.sampleHost(ctx, now)

	return nil
}

// sampleFleetOne stats one container and records its fleet row. Managed
// rows duplicate the metrics table deliberately so the fleet view is
// one query. Failures log and skip without failing the cycle.
func (s *Sampler) sampleFleetOne(
	ctx context.Context,
	c docker.Container,
	serviceID string,
	now time.Time,
) {
	stats, err := s.docker.Stats(ctx, c.ID)
	if err != nil {
		s.logger.Warn("fleet stats failed", "container", c.Name, "error", err)

		return
	}

	sample := model.ContainerSample{
		SampledAt:     now,
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
	diskUsed, diskTotal := hostDisk()

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
