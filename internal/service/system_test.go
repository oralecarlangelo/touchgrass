package service

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/oralecarlangelo/touchgrass/internal/docker"
)

// stubDaemonQuerier scripts daemon answers for System tests.
type stubDaemonQuerier struct {
	images    []docker.Image
	imagesErr error
	prune     docker.PruneReport
	pruneErr  error
	info      docker.DaemonInfo
	infoErr   error
}

func (s stubDaemonQuerier) Images(_ context.Context) ([]docker.Image, error) {
	return s.images, s.imagesErr
}

func (s stubDaemonQuerier) PruneImages(_ context.Context) (docker.PruneReport, error) {
	return s.prune, s.pruneErr
}

func (s stubDaemonQuerier) DaemonInfo(_ context.Context) (docker.DaemonInfo, error) {
	return s.info, s.infoErr
}

func testSystem(t *testing.T, querier DaemonQuerier) *System {
	t.Helper()

	return NewSystem(SystemConfig{
		Docker:    querier,
		Version:   "stub-version",
		DBPath:    t.TempDir() + "/missing.db",
		StartedAt: time.Now().Add(-time.Hour),
		Logger:    slog.New(slog.DiscardHandler),
	})
}

func TestSystemImages(t *testing.T) {
	t.Parallel()

	querier := stubDaemonQuerier{images: []docker.Image{
		{ID: "sha256:abc", RepoTags: []string{"app:v2"}, Size: 120, Created: 1700000000, Dangling: false},
	}}

	got, err := testSystem(t, querier).Images(context.Background())
	if err != nil {
		t.Fatalf("Images() error = %v, want nil", err)
	}

	if len(got) != 1 || got[0].ID != "sha256:abc" || got[0].SizeBytes != 120 {
		t.Fatalf("Images() = %+v, want the mapped image", got)
	}

	if got[0].CreatedAt.Unix() != 1700000000 {
		t.Errorf("CreatedAt = %v, want unix 1700000000", got[0].CreatedAt)
	}

	querier.imagesErr = errors.New("daemon down")

	if _, err := testSystem(t, querier).Images(context.Background()); err == nil {
		t.Error("Images() error = nil, want daemon error")
	}
}

func TestSystemPruneImages(t *testing.T) {
	t.Parallel()

	querier := stubDaemonQuerier{prune: docker.PruneReport{Deleted: 3, ReclaimedBytes: 1024}}

	got, err := testSystem(t, querier).PruneImages(context.Background())
	if err != nil {
		t.Fatalf("PruneImages() error = %v, want nil", err)
	}

	if got.Deleted != 3 || got.ReclaimedBytes != 1024 {
		t.Errorf("PruneImages() = %+v, want 3/1024", got)
	}

	querier.pruneErr = errors.New("daemon down")

	if _, err := testSystem(t, querier).PruneImages(context.Background()); err == nil {
		t.Error("PruneImages() error = nil, want daemon error")
	}
}

func TestSystemSnapshot(t *testing.T) {
	t.Parallel()

	querier := stubDaemonQuerier{info: docker.DaemonInfo{
		ServerVersion: "28.0", Architecture: "aarch64", OperatingSystem: "Ubuntu",
		KernelVersion: "6.8", NCPU: 4, MemTotal: 8 << 30,
		ContainersRun: 5, ContainersStop: 1, ImageCount: 9,
	}}

	got, err := testSystem(t, querier).Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot() error = %v, want nil", err)
	}

	if got.Docker == nil || got.Docker.ServerVersion != "28.0" || got.Docker.ImageCount != 9 {
		t.Errorf("Docker = %+v, want mapped daemon info", got.Docker)
	}

	if got.Self.Version != "stub-version" || got.Self.UptimeSecs < 3590 {
		t.Errorf("Self = %+v, want version + ~1h uptime", got.Self)
	}

	if got.OS == "" || got.Arch == "" {
		t.Errorf("OS/Arch = %q/%q, want runtime values", got.OS, got.Arch)
	}
}

func TestSystemSnapshotDegrades(t *testing.T) {
	t.Parallel()

	querier := stubDaemonQuerier{infoErr: errors.New("daemon down")}

	got, err := testSystem(t, querier).Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot() error = %v, want nil (degraded)", err)
	}

	if got.Docker != nil {
		t.Errorf("Docker = %+v, want nil on daemon failure", got.Docker)
	}
}

func TestParseUptime(t *testing.T) {
	t.Parallel()

	secs, err := parseUptime("12345.67 98765.43\n")
	if err != nil || secs != 12345 {
		t.Errorf("parseUptime() = (%d, %v), want (12345, nil)", secs, err)
	}

	if _, err := parseUptime("nope\n"); err == nil {
		t.Error("parseUptime() error = nil, want parse error")
	}
}

func TestParseLoadAvg(t *testing.T) {
	t.Parallel()

	load, err := parseLoadAvg("1.87 1.34 0.91 2/500 12345\n")
	if err != nil || load != 1.87 {
		t.Errorf("parseLoadAvg() = (%v, %v), want (1.87, nil)", load, err)
	}

	if _, err := parseLoadAvg("nope\n"); err == nil {
		t.Error("parseLoadAvg() error = nil, want parse error")
	}
}

func TestParseCPUStat(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		raw       string
		wantIdle  uint64
		wantTotal uint64
		wantErr   bool
	}{
		{
			name:      "aggregate line",
			raw:       "cpu  100 20 30 800 50 0 10 0 0 0\ncpu0 50 10 15 400 25 0 5 0 0 0\n",
			wantIdle:  850,
			wantTotal: 1010,
		},
		{
			name:      "short line without iowait",
			raw:       "cpu 100 20 30 800\n",
			wantIdle:  800,
			wantTotal: 950,
		},
		{
			name:    "missing aggregate",
			raw:     "cpu0 50 10 15 400\n",
			wantErr: true,
		},
		{
			name:    "non-numeric counter",
			raw:     "cpu 100 nope 30 800\n",
			wantErr: true,
		},
		{
			name:    "empty",
			raw:     "",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := parseCPUStat(tt.raw)
			if tt.wantErr {
				if err == nil {
					t.Error("parseCPUStat() error = nil, want parse error")
				}

				return
			}

			if err != nil {
				t.Fatalf("parseCPUStat() error = %v, want nil", err)
			}

			if got.idle != tt.wantIdle || got.total != tt.wantTotal {
				t.Errorf("parseCPUStat() = %+v, want idle %d total %d", got, tt.wantIdle, tt.wantTotal)
			}
		})
	}
}

func TestCPUUsage(t *testing.T) {
	t.Parallel()

	last := cpuSample{idle: 800, total: 1000}

	tests := []struct {
		name    string
		last    *cpuSample
		current cpuSample
		want    float64
		wantOK  bool
	}{
		{
			name:    "half busy",
			last:    &last,
			current: cpuSample{idle: 900, total: 1200},
			want:    50,
			wantOK:  true,
		},
		{
			name:    "all idle",
			last:    &last,
			current: cpuSample{idle: 1000, total: 1200},
			want:    0,
			wantOK:  true,
		},
		{
			name:    "no previous sample",
			last:    nil,
			current: cpuSample{idle: 900, total: 1200},
			wantOK:  false,
		},
		{
			name:    "counters stalled",
			last:    &last,
			current: last,
			wantOK:  false,
		},
		{
			name:    "idle outruns total",
			last:    &last,
			current: cpuSample{idle: 1100, total: 1200},
			wantOK:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, ok := cpuUsage(tt.last, tt.current)
			if ok != tt.wantOK {
				t.Fatalf("cpuUsage() ok = %v, want %v", ok, tt.wantOK)
			}

			if ok && got != tt.want {
				t.Errorf("cpuUsage() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestParseMeminfo(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		raw       string
		wantUsed  int64
		wantTotal int64
		wantErr   bool
	}{
		{
			name:      "total minus available",
			raw:       "MemTotal:        8000000 kB\nMemFree:         1000000 kB\nMemAvailable:    6000000 kB\n",
			wantUsed:  2000000 * 1024,
			wantTotal: 8000000 * 1024,
		},
		{
			name:    "missing available",
			raw:     "MemTotal:        8000000 kB\n",
			wantErr: true,
		},
		{
			name:    "available over total",
			raw:     "MemTotal:        100 kB\nMemAvailable:    200 kB\n",
			wantErr: true,
		},
		{
			name:    "empty",
			raw:     "",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			used, total, err := parseMeminfo(tt.raw)
			if tt.wantErr {
				if err == nil {
					t.Error("parseMeminfo() error = nil, want parse error")
				}

				return
			}

			if err != nil {
				t.Fatalf("parseMeminfo() error = %v, want nil", err)
			}

			if used != tt.wantUsed || total != tt.wantTotal {
				t.Errorf("parseMeminfo() = (%d, %d), want (%d, %d)", used, total, tt.wantUsed, tt.wantTotal)
			}
		})
	}
}

func TestDiskUsage(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		blocks    uint64
		bavail    uint64
		bsize     uint64
		wantUsed  int64
		wantTotal int64
	}{
		{
			name:      "used is blocks minus bavail",
			blocks:    1000,
			bavail:    400,
			bsize:     4096,
			wantUsed:  600 * 4096,
			wantTotal: 1000 * 4096,
		},
		{
			name:      "bavail clamped to blocks",
			blocks:    1000,
			bavail:    2000,
			bsize:     512,
			wantUsed:  0,
			wantTotal: 1000 * 512,
		},
		{
			name:      "overflow saturates",
			blocks:    1<<63 - 1,
			bavail:    0,
			bsize:     4096,
			wantUsed:  1<<63 - 1,
			wantTotal: 1<<63 - 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			used, total := diskUsage(tt.blocks, tt.bavail, tt.bsize)
			if used != tt.wantUsed || total != tt.wantTotal {
				t.Errorf("diskUsage() = (%d, %d), want (%d, %d)", used, total, tt.wantUsed, tt.wantTotal)
			}
		})
	}
}

func TestSystemSnapshotResourcesNullSafe(t *testing.T) {
	t.Parallel()

	got, err := testSystem(t, stubDaemonQuerier{}).Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot() error = %v, want nil", err)
	}

	if got.CPUPercent != nil && (*got.CPUPercent < 0 || *got.CPUPercent > 100) {
		t.Errorf("CPUPercent = %v, want nil or 0-100", *got.CPUPercent)
	}

	pairs := []struct {
		name  string
		used  *int64
		total *int64
	}{
		{name: "mem", used: got.MemUsedBytes, total: got.MemTotalBytes},
		{name: "disk", used: got.DiskUsedBytes, total: got.DiskTotalBytes},
	}

	for _, pair := range pairs {
		if (pair.used == nil) != (pair.total == nil) {
			t.Errorf("%s used/total = %v/%v, want both nil or both set", pair.name, pair.used, pair.total)
		}

		if pair.used != nil && (*pair.used < 0 || *pair.used > *pair.total) {
			t.Errorf("%s used = %d total = %d, want 0 <= used <= total", pair.name, *pair.used, *pair.total)
		}
	}
}
