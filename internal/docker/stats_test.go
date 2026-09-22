package docker

import (
	"testing"

	"github.com/docker/docker/api/types/container"
)

func TestCPUPercent(t *testing.T) {
	t.Parallel()

	base := container.StatsResponse{}
	base.CPUStats.CPUUsage.TotalUsage = 200
	base.PreCPUStats.CPUUsage.TotalUsage = 100
	base.CPUStats.SystemUsage = 2000
	base.PreCPUStats.SystemUsage = 1000
	base.CPUStats.OnlineCPUs = 4

	tests := []struct {
		name     string
		mutate   func(*container.StatsResponse)
		expected float64
	}{
		{
			name:     "standard four cpu",
			mutate:   func(*container.StatsResponse) {},
			expected: 40,
		},
		{
			name: "zero system delta",
			mutate: func(s *container.StatsResponse) {
				s.CPUStats.SystemUsage = s.PreCPUStats.SystemUsage
			},
			expected: 0,
		},
		{
			name: "percpu fallback",
			mutate: func(s *container.StatsResponse) {
				s.CPUStats.OnlineCPUs = 0
				s.CPUStats.CPUUsage.PercpuUsage = []uint64{1, 1}
			},
			expected: 20,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			stats := base
			tt.mutate(&stats)

			if got := cpuPercent(stats); got != tt.expected {
				t.Errorf("cpuPercent() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestMemUsage(t *testing.T) {
	t.Parallel()

	const mib = 1048576

	tests := []struct {
		name     string
		usage    uint64
		stats    map[string]uint64
		expected uint64
	}{
		{
			name:     "cgroup v2 subtracts inactive file",
			usage:    170 * mib,
			stats:    map[string]uint64{"inactive_file": 25 * mib},
			expected: 145 * mib,
		},
		{
			name:     "cgroup v1 falls back to total",
			usage:    170 * mib,
			stats:    map[string]uint64{"total_inactive_file": 25 * mib},
			expected: 145 * mib,
		},
		{
			name:     "no cache reports raw usage",
			usage:    170 * mib,
			stats:    map[string]uint64{},
			expected: 170 * mib,
		},
		{
			name:     "oversized cache clamps to zero",
			usage:    10 * mib,
			stats:    map[string]uint64{"inactive_file": 25 * mib},
			expected: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var stats container.StatsResponse

			stats.MemoryStats.Usage = tt.usage
			stats.MemoryStats.Stats = tt.stats

			if got := memUsage(stats); got != tt.expected {
				t.Errorf("memUsage() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestStartedAt(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		raw      string
		expected bool
	}{
		{name: "valid rfc3339", raw: "2026-09-21T10:00:00Z", expected: true},
		{name: "garbage tolerated", raw: "not-a-time", expected: false},
		{name: "empty tolerated", raw: "", expected: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var info container.InspectResponse

			info.ContainerJSONBase = &container.ContainerJSONBase{
				State: &container.State{StartedAt: tt.raw},
			}

			got := startedAt(info)

			if tt.expected && got.IsZero() {
				t.Errorf("startedAt(%q) is zero, want parsed time", tt.raw)
			}

			if !tt.expected && !got.IsZero() {
				t.Errorf("startedAt(%q) = %v, want zero time", tt.raw, got)
			}

			if tt.expected && got.Year() != 2026 {
				t.Errorf("startedAt(%q) = %v, want 2026", tt.raw, got)
			}
		})
	}
}

func TestShortID(t *testing.T) {
	t.Parallel()

	if got := shortID("aaaabbbbccccddddeeee"); got != "aaaabbbbcccc" {
		t.Errorf("shortID() = %q, want truncated", got)
	}

	if got := shortID("abc"); got != "abc" {
		t.Errorf("shortID() = %q, want abc", got)
	}
}
