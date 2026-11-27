package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/oralecarlangelo/touchgrass/internal/docker"
	"github.com/oralecarlangelo/touchgrass/internal/model"
)

// goosLinux gates /proc readers: they only exist on Linux hosts.
const goosLinux = "linux"

const systemHeartbeatTimeout = 8 * time.Second

// DaemonQuerier is the docker subset the System service needs.
type DaemonQuerier interface {
	Images(ctx context.Context) ([]docker.Image, error)
	PruneImages(ctx context.Context) (docker.PruneReport, error)
	DaemonInfo(ctx context.Context) (docker.DaemonInfo, error)
}

// SystemConfig wires the System service.
type SystemConfig struct {
	Docker    DaemonQuerier
	Version   string
	DBPath    string
	HostRoot  string
	StartedAt time.Time
	Logger    *slog.Logger
}

// cpuSample is one /proc/stat aggregate line, reduced to the two
// counters CPU usage needs.
type cpuSample struct {
	idle  uint64
	total uint64
}

// System serves host and daemon introspection: image inventory,
// dangling prune, and the system snapshot.
type System struct {
	docker    DaemonQuerier
	version   string
	dbPath    string
	hostRoot  string
	startedAt time.Time
	logger    *slog.Logger

	cpuMu   sync.Mutex
	cpuLast *cpuSample

	snapshotMu sync.RWMutex
	snapshot   model.SystemSnapshot
	maxAge     time.Duration

	storageMu        sync.RWMutex
	storageRefreshMu sync.Mutex
	images           []model.ImageView
	imagesFreshness  model.CollectionFreshness
	storage          model.StorageUsage
	storageMaxAge    time.Duration
	imageRefresh     chan struct{}
}

// NewSystem builds a System.
func NewSystem(cfg SystemConfig) *System {
	hostRoot := cfg.HostRoot
	if hostRoot == "" {
		hostRoot = "/"
	}

	system := &System{
		docker:       cfg.Docker,
		version:      cfg.Version,
		dbPath:       cfg.DBPath,
		hostRoot:     hostRoot,
		startedAt:    cfg.StartedAt,
		logger:       cfg.Logger,
		imageRefresh: make(chan struct{}, 1),
	}
	system.snapshot = system.hostSnapshot()
	system.snapshot.Freshness.Stale = true
	system.images = []model.ImageView{}
	system.storage.Entries = []model.StorageEntry{}
	system.imagesFreshness = model.CollectionFreshness{Stale: true, LastErrorCategory: "not_collected"}
	system.storage.Freshness = model.CollectionFreshness{Stale: true, LastErrorCategory: "not_collected"}

	return system
}

// Images returns local daemon images, newest first.
func (s *System) Images(context.Context) ([]model.ImageView, error) {
	s.storageMu.RLock()
	images := append([]model.ImageView{}, s.images...)
	s.storageMu.RUnlock()

	return images, nil
}

// ImagesFreshness describes whether the cached image inventory is current.
func (s *System) ImagesFreshness() model.CollectionFreshness {
	s.storageMu.RLock()
	freshness := s.imagesFreshness
	maxAge := s.storageMaxAge
	s.storageMu.RUnlock()

	if freshness.ObservedAt != nil && maxAge > 0 && time.Since(*freshness.ObservedAt) > maxAge {
		freshness.Stale = true
		freshness.LastErrorCategory = "stale"
	}

	return freshness
}

func (s *System) collectImages(ctx context.Context) ([]model.ImageView, error) {
	images, err := s.docker.Images(ctx)
	if err != nil {
		return nil, err
	}

	views := make([]model.ImageView, 0, len(images))

	for _, img := range images {
		views = append(views, model.ImageView{
			ID:         img.ID,
			RepoTags:   img.RepoTags,
			SizeBytes:  img.Size,
			CreatedAt:  time.Unix(img.Created, 0).UTC(),
			Containers: img.Containers,
			Dangling:   img.Dangling,
		})
	}

	return views, nil
}

// PruneImages removes dangling images only and reports the result.
func (s *System) PruneImages(ctx context.Context) (model.ImagePruneView, error) {
	report, err := s.docker.PruneImages(ctx)
	if err != nil {
		return model.ImagePruneView{}, err
	}

	s.logger.Info("pruned dangling images", "deleted", report.Deleted, "reclaimed", report.ReclaimedBytes)
	s.storageMu.Lock()
	s.imagesFreshness.Stale = true
	s.imagesFreshness.LastErrorCategory = "refresh_pending"
	s.storageMu.Unlock()

	select {
	case s.imageRefresh <- struct{}{}:
	default:
	}

	return model.ImagePruneView{
		Deleted:        report.Deleted,
		ReclaimedBytes: report.ReclaimedBytes,
	}, nil
}

// Snapshot assembles the host + daemon + self picture. Host fields
// the platform cannot provide stay null; a daemon failure degrades to
// a null docker section instead of failing the whole snapshot.
func (s *System) Snapshot(ctx context.Context) (model.SystemSnapshot, error) {
	snapshot := s.hostSnapshot()

	info, err := s.docker.DaemonInfo(ctx)
	if err != nil {
		s.logger.Warn("daemon info failed; snapshot degrades", "error", err)

		return snapshot, nil
	}

	snapshot.Docker = daemonSnapshot(info)

	return snapshot, nil
}

// CachedSnapshot returns the last background snapshot without Docker I/O.
func (s *System) CachedSnapshot() model.SystemSnapshot {
	s.snapshotMu.RLock()
	snapshot := s.snapshot
	maxAge := s.maxAge
	s.snapshotMu.RUnlock()

	if snapshot.Freshness.ObservedAt != nil && maxAge > 0 && time.Since(*snapshot.Freshness.ObservedAt) > maxAge {
		snapshot.Freshness.Stale = true
	}

	return snapshot
}

// Run refreshes the system snapshot until ctx is cancelled. Docker failures
// preserve the last known daemon section while host and self data advance.
func (s *System) Run(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = time.Minute
	}

	s.snapshotMu.Lock()
	s.maxAge = 2 * interval
	s.snapshotMu.Unlock()

	_ = s.Refresh(ctx)

	var storageLoop sync.WaitGroup

	storageLoop.Go(func() { s.runStorageRefresh(ctx, interval) })
	defer storageLoop.Wait()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_ = s.Refresh(ctx)
		}
	}
}

// Refresh collects one background system heartbeat.
func (s *System) Refresh(ctx context.Context) error {
	started := time.Now()
	snapshot := s.hostSnapshot()

	callCtx, cancel := context.WithTimeout(ctx, systemHeartbeatTimeout)
	defer cancel()

	info, err := s.docker.DaemonInfo(callCtx)
	observedAt := time.Now().UTC()

	snapshot.Freshness = model.CollectionFreshness{
		ObservedAt: &observedAt, Complete: err == nil, Stale: err != nil,
	}
	if err != nil {
		snapshot.Freshness.LastErrorCategory = "collection_failed"
	}

	s.snapshotMu.Lock()
	if err == nil {
		snapshot.Docker = daemonSnapshot(info)
	} else {
		snapshot.Docker = s.snapshot.Docker
	}

	s.snapshot = snapshot
	s.snapshotMu.Unlock()

	if err != nil {
		s.logger.Warn("system heartbeat degraded",
			"duration_ms", time.Since(started).Milliseconds(), "error", err)

		return err
	}

	s.logger.Info("system heartbeat complete", "duration_ms", time.Since(started).Milliseconds())

	return nil
}

func (s *System) hostSnapshot() model.SystemSnapshot {
	hostname, err := os.Hostname()
	if err != nil {
		s.logger.Debug("hostname lookup failed; leaving blank", "error", err)

		hostname = ""
	}

	memUsed, memTotal := hostMem()
	diskUsed, diskTotal := hostDisk(s.hostRoot)

	snapshot := model.SystemSnapshot{
		Hostname:       hostname,
		OS:             runtime.GOOS,
		Arch:           runtime.GOARCH,
		UptimeSecs:     hostUptimeSecs(),
		Load1:          hostLoad1(),
		CPUPercent:     s.hostCPUPercent(),
		MemUsedBytes:   memUsed,
		MemTotalBytes:  memTotal,
		DiskUsedBytes:  diskUsed,
		DiskTotalBytes: diskTotal,
		Self: model.SelfSnapshot{
			Version:       s.version,
			UptimeSecs:    int64(time.Since(s.startedAt).Seconds()),
			DatabaseBytes: databaseBytes(s.dbPath),
		},
	}

	return snapshot
}

func daemonSnapshot(info docker.DaemonInfo) *model.DaemonSnapshot {
	return &model.DaemonSnapshot{
		ServerVersion:   info.ServerVersion,
		Architecture:    info.Architecture,
		OperatingSystem: info.OperatingSystem,
		KernelVersion:   info.KernelVersion,
		NCPU:            info.NCPU,
		MemTotalBytes:   info.MemTotal,
		ContainersRun:   info.ContainersRun,
		ContainersStop:  info.ContainersStop,
		ImageCount:      info.ImageCount,
	}
}

// databaseBytes sums the SQLite file plus WAL sidecars, if present.
func databaseBytes(path string) int64 {
	var total int64

	for _, suffix := range []string{"", "-wal", "-shm"} {
		info, err := os.Stat(path + suffix)
		if err == nil {
			total += info.Size()
		}
	}

	return total
}

// hostUptimeSecs reads Linux uptime; nil elsewhere or on failure.
func hostUptimeSecs() *int64 {
	if runtime.GOOS != goosLinux {
		return nil
	}

	raw, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return nil
	}

	secs, err := parseUptime(string(raw))
	if err != nil {
		return nil
	}

	return &secs
}

// parseUptime parses the leading seconds from /proc/uptime content.
func parseUptime(raw string) (int64, error) {
	field, _, _ := strings.Cut(strings.TrimSpace(raw), " ")

	seconds, err := strconv.ParseFloat(field, 64)
	if err != nil {
		return 0, fmt.Errorf("parsing uptime %q: %w", field, err)
	}

	return int64(seconds), nil
}

// hostLoad1 reads the Linux 1-minute load average; nil elsewhere.
func hostLoad1() *float64 {
	if runtime.GOOS != goosLinux {
		return nil
	}

	raw, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return nil
	}

	load, err := parseLoadAvg(string(raw))
	if err != nil {
		return nil
	}

	return &load
}

// parseLoadAvg parses the first field of /proc/loadavg content.
func parseLoadAvg(raw string) (float64, error) {
	field, _, _ := strings.Cut(strings.TrimSpace(raw), " ")

	load, err := strconv.ParseFloat(field, 64)
	if err != nil {
		return 0, fmt.Errorf("parsing loadavg %q: %w", field, err)
	}

	return load, nil
}

// hostCPUPercent reports host CPU usage since the previous snapshot.
// It returns nil on non-Linux hosts, on read failure, and on the
// first call, which has no delta yet.
func (s *System) hostCPUPercent() *float64 {
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

// parseCPUStat reduces the aggregate "cpu" line of /proc/stat to its
// idle and total counters. Guest times already count toward user/nice,
// so they are not double-counted.
func parseCPUStat(raw string) (cpuSample, error) {
	line, _, _ := strings.Cut(raw, "\n")
	fields := strings.Fields(line)

	if len(fields) < 5 || fields[0] != "cpu" {
		return cpuSample{}, fmt.Errorf("parsing cpu stat %q: want aggregate cpu line", line)
	}

	counters := make([]uint64, 0, len(fields)-1)

	for _, field := range fields[1:] {
		value, err := strconv.ParseUint(field, 10, 64)
		if err != nil {
			return cpuSample{}, fmt.Errorf("parsing cpu stat %q: %w", line, err)
		}

		counters = append(counters, value)
	}

	var total uint64

	for _, value := range counters {
		total += value
	}

	// Fields: user nice system idle iowait irq softirq steal...
	idle := counters[3]
	if len(counters) > 4 {
		idle += counters[4]
	}

	return cpuSample{idle: idle, total: total}, nil
}

// cpuUsage derives usage percent from two samples. It reports false
// when there is no previous sample or the counters did not advance.
func cpuUsage(last *cpuSample, current cpuSample) (float64, bool) {
	if last == nil {
		return 0, false
	}

	if current.total <= last.total {
		return 0, false
	}

	idleDelta := current.idle - last.idle
	totalDelta := current.total - last.total

	if idleDelta > totalDelta {
		return 0, false
	}

	return (1 - float64(idleDelta)/float64(totalDelta)) * 100, true
}

// hostMem reports used/total host memory bytes; nils elsewhere or on
// failure. Values come from /proc/meminfo kB fields.
func hostMem() (*int64, *int64) {
	if runtime.GOOS != goosLinux {
		return nil, nil
	}

	raw, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return nil, nil
	}

	used, total, err := parseMeminfo(string(raw))
	if err != nil {
		return nil, nil
	}

	return &used, &total
}

// parseMeminfo derives used/total bytes from /proc/meminfo content:
// used is MemTotal minus MemAvailable, both reported in kB.
func parseMeminfo(raw string) (int64, int64, error) {
	var total, available int64

	var seenTotal, seenAvailable bool

	for line := range strings.Lines(strings.TrimSpace(raw)) {
		name, value, _ := strings.Cut(line, ":")
		fields := strings.Fields(value)

		if len(fields) == 0 {
			continue
		}

		kb, err := strconv.ParseInt(fields[0], 10, 64)
		if err != nil {
			continue
		}

		switch strings.TrimSpace(name) {
		case "MemTotal":
			total, seenTotal = kb*1024, true
		case "MemAvailable":
			available, seenAvailable = kb*1024, true
		}
	}

	if !seenTotal || !seenAvailable || available > total {
		return 0, 0, errors.New("parsing meminfo: want MemTotal/MemAvailable kB fields")
	}

	return total - available, total, nil
}

// hostDisk reports used/total bytes of the root filesystem; nils on
// failure. Statfs is stdlib and portable, so this works off-Linux too.
func hostDisk(root string) (*int64, *int64) {
	var stat syscall.Statfs_t

	if root == "" {
		return nil, nil
	}
	if err := syscall.Statfs(root, &stat); err != nil {
		return nil, nil
	}

	if stat.Bsize <= 0 {
		return nil, nil
	}

	used, total := diskUsage(stat.Blocks, stat.Bavail, uint64(stat.Bsize))

	return &used, &total
}

// diskUsage derives used/total bytes from Statfs block counters,
// saturating at the int64 range instead of overflowing.
func diskUsage(blocks, bavail, bsize uint64) (int64, int64) {
	const maxInt64 = uint64(1<<63 - 1)

	saturate := func(value uint64) int64 {
		if value > maxInt64 {
			return int64(maxInt64)
		}

		return int64(value)
	}

	multiply := func(a, b uint64) uint64 {
		if a != 0 && b > ^uint64(0)/a {
			return ^uint64(0)
		}

		return a * b
	}

	if bavail > blocks {
		bavail = blocks
	}

	return saturate(multiply(blocks-bavail, bsize)), saturate(multiply(blocks, bsize))
}
