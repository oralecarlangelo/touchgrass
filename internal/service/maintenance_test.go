package service

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/oralecarlangelo/touchgrass/internal/model"
)

type maintenanceStoreStub struct {
	mu      sync.Mutex
	results []error
	calls   chan struct{}
}

func (s *maintenanceStoreStub) Run(_ context.Context, policy model.MaintenancePolicy) (model.MaintenanceResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var err error
	if len(s.results) > 0 {
		err = s.results[0]
		s.results = s.results[1:]
	}
	if s.calls != nil {
		s.calls <- struct{}{}
	}

	return model.MaintenanceResult{RanAt: policy.Now}, err
}

type backupStub struct {
	mu           sync.Mutex
	destinations []string
}

type fileBackupStub struct{}

func (fileBackupStub) Backup(_ context.Context, destination string) error {
	return os.WriteFile(destination, []byte("snapshot"), 0o600)
}

func (b *backupStub) Backup(_ context.Context, destination string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.destinations = append(b.destinations, destination)

	return nil
}

type maintenanceClockStub struct {
	mu    sync.Mutex
	now   time.Time
	ticks chan time.Time
}

func (c *maintenanceClockStub) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.now
}

func (c *maintenanceClockStub) After(time.Duration) <-chan time.Time {
	return c.ticks
}

func (c *maintenanceClockStub) advance(duration time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(duration)
	now := c.now
	c.mu.Unlock()
	c.ticks <- now
}

func TestMaintenanceRejectsUnsafeBackupPath(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	maintenance := NewMaintenance(MaintenanceConfig{
		Store: &maintenanceStoreStub{}, Backupper: &backupStub{},
		Interval: time.Minute, BackupDir: dir,
	})

	err := maintenance.Backup(context.Background(), filepath.Join(dir, "..", "outside.db"))
	if err == nil {
		t.Fatal("Backup() error = nil, want unsafe-path error")
	}
}

func TestMaintenanceStatusStale(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 10, 16, 0, 0, 0, 0, time.UTC)
	clock := &maintenanceClockStub{now: now, ticks: make(chan time.Time, 1)}

	maintenance := NewMaintenance(MaintenanceConfig{
		Store: &maintenanceStoreStub{}, Backupper: &backupStub{}, Clock: clock,
		Interval: time.Minute, BackupDir: t.TempDir(), Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})

	if status := maintenance.Status(); !status.Stale {
		t.Fatalf("initial Status().Stale = false, want true")
	}
	maintenance.runOnce(context.Background())
	if status := maintenance.Status(); status.Stale || status.LastError != "" {
		t.Fatalf("fresh Status() = %+v", status)
	}
	clock.mu.Lock()
	clock.now = clock.now.Add(3 * time.Minute)
	clock.mu.Unlock()
	if status := maintenance.Status(); !status.Stale {
		t.Fatalf("late Status().Stale = false, want true")
	}
}

func TestMaintenanceCancellation(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	maintenance := NewMaintenance(MaintenanceConfig{
		Store: &maintenanceStoreStub{}, Backupper: &backupStub{},
		Interval: time.Minute, BackupDir: t.TempDir(),
	})
	done := make(chan struct{})
	go func() {
		maintenance.Run(ctx)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Run() did not stop after cancellation")
	}
}

func TestMaintenanceContinuesAfterStoreError(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 10, 16, 0, 0, 0, 0, time.UTC)
	clock := &maintenanceClockStub{now: now, ticks: make(chan time.Time, 1)}
	calls := make(chan struct{}, 2)
	maintenance := NewMaintenance(MaintenanceConfig{
		Store:     &maintenanceStoreStub{results: []error{errors.New("busy"), nil}, calls: calls},
		Backupper: &backupStub{}, Clock: clock, Interval: time.Minute,
		BackupDir: t.TempDir(), Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		maintenance.Run(ctx)
		close(done)
	}()

	<-calls
	clock.advance(time.Minute)
	<-calls
	cancel()
	<-done

	status := maintenance.Status()
	if status.LastError != "" || status.LastSuccessAt.IsZero() {
		t.Fatalf("Status() after recovery = %+v", status)
	}
}

func TestMaintenancePrunesOldSQLiteBackups(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	now := time.Date(2026, 10, 16, 0, 0, 0, 0, time.UTC)
	clock := &maintenanceClockStub{now: now, ticks: make(chan time.Time, 1)}

	maintenance := NewMaintenance(MaintenanceConfig{
		Store: &maintenanceStoreStub{}, Backupper: fileBackupStub{}, Clock: clock,
		Interval: time.Minute, BackupInterval: time.Hour, BackupDir: dir, BackupKeep: 2,
		Logger: slog.New(slog.DiscardHandler),
	})
	if err := os.WriteFile(filepath.Join(dir, "keep-me.txt"), []byte("unrelated"), 0o600); err != nil {
		t.Fatal(err)
	}

	for range 3 {
		maintenance.runOnce(context.Background())
		clock.mu.Lock()
		clock.now = clock.now.Add(time.Hour)
		clock.mu.Unlock()
	}

	matches, err := filepath.Glob(filepath.Join(dir, "touchgrass-*.db"))
	if err != nil {
		t.Fatal(err)
	}

	if len(matches) != 2 {
		t.Fatalf("retained snapshots = %v, want newest 2", matches)
	}

	if _, err := os.Stat(filepath.Join(dir, "keep-me.txt")); err != nil {
		t.Fatalf("unrelated file was changed: %v", err)
	}
}
