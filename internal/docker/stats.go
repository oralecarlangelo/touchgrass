package docker

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/docker/docker/api/types/container"
)

// Stats is one container's resource snapshot.
type Stats struct {
	CPUPercent float64
	MemBytes   uint64
	MemLimit   uint64
	DiskBytes  int64
	Restarts   int
	StartedAt  time.Time
}

// Stats collects a one-shot resource snapshot for id.
func (c *Client) Stats(ctx context.Context, id string) (Stats, error) {
	stats, err := c.statsJSON(ctx, id)
	if err != nil {
		return Stats{}, err
	}

	info, err := c.cli.ContainerInspect(ctx, id)
	if err != nil {
		return Stats{}, fmt.Errorf("inspecting container %s: %w", shortID(id), err)
	}

	return Stats{
		CPUPercent: cpuPercent(stats),
		MemBytes:   memUsage(stats),
		MemLimit:   stats.MemoryStats.Limit,
		Restarts:   info.RestartCount,
		StartedAt:  startedAt(info),
	}, nil
}

// SizedList lists running containers with writable-layer sizes for disk use.
func (c *Client) SizedList(ctx context.Context) ([]Container, []int64, error) {
	summaries, err := c.cli.ContainerList(ctx, container.ListOptions{All: false, Size: true})
	if err != nil {
		return nil, nil, fmt.Errorf("listing containers with size: %w", err)
	}

	containers := make([]Container, 0, len(summaries))
	sizes := make([]int64, 0, len(summaries))

	for _, summary := range summaries {
		containers = append(containers, fromSummary(summary))
		sizes = append(sizes, summary.SizeRw)
	}

	return containers, sizes, nil
}

// statsJSON fetches one stats snapshot without streaming.
func (c *Client) statsJSON(ctx context.Context, id string) (container.StatsResponse, error) {
	resp, err := c.cli.ContainerStats(ctx, id, false)
	if err != nil {
		return container.StatsResponse{}, fmt.Errorf("reading stats for %s: %w", shortID(id), err)
	}

	defer func() {
		_ = resp.Body.Close()
	}()

	var stats container.StatsResponse

	if err := json.NewDecoder(resp.Body).Decode(&stats); err != nil {
		return container.StatsResponse{}, fmt.Errorf("decoding stats for %s: %w", shortID(id), err)
	}

	return stats, nil
}

// cpuPercent replicates docker stats CPU% from one snapshot with its predecessor.
func cpuPercent(stats container.StatsResponse) float64 {
	cpuDelta := float64(stats.CPUStats.CPUUsage.TotalUsage - stats.PreCPUStats.CPUUsage.TotalUsage)
	systemDelta := float64(stats.CPUStats.SystemUsage - stats.PreCPUStats.SystemUsage)

	if cpuDelta <= 0 || systemDelta <= 0 {
		return 0
	}

	cpus := float64(stats.CPUStats.OnlineCPUs)
	if cpus <= 0 {
		cpus = float64(len(stats.CPUStats.CPUUsage.PercpuUsage))
	}

	if cpus <= 0 {
		cpus = 1
	}

	return cpuDelta / systemDelta * cpus * 100
}

// memUsage replicates docker stats MEM USAGE: raw usage minus inactive
// file cache (cgroup v2 `inactive_file`, cgroup v1
// `total_inactive_file`), clamped at zero.
func memUsage(stats container.StatsResponse) uint64 {
	usage := stats.MemoryStats.Usage
	cache := stats.MemoryStats.Stats["inactive_file"]

	if cache == 0 {
		cache = stats.MemoryStats.Stats["total_inactive_file"]
	}

	if cache >= usage {
		return 0
	}

	return usage - cache
}

// startedAt parses the container start time, tolerating garbage.
func startedAt(info container.InspectResponse) time.Time {
	if info.ContainerJSONBase == nil || info.State == nil {
		return time.Time{}
	}

	started, err := time.Parse(time.RFC3339, info.State.StartedAt)
	if err != nil {
		return time.Time{}
	}

	return started
}

// shortID truncates identifiers for errors.
func shortID(id string) string {
	const maxLen = 12

	if len(id) > maxLen {
		return id[:maxLen]
	}

	return id
}
