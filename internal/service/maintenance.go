package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/oralecarlangelo/touchgrass/internal/model"
)

const (
	defaultSQLiteBackupInterval = 24 * time.Hour
	defaultSQLiteBackupKeep     = 14
)

// MaintenanceRunner is the bounded store surface used by the scheduler.
type MaintenanceRunner interface {
	Run(context.Context, model.MaintenancePolicy) (model.MaintenanceResult, error)
}

// SQLiteBackupper writes a consistent SQLite snapshot.
type SQLiteBackupper interface {
	Backup(context.Context, string) error
}

// MaintenanceClock makes scheduling and stale-state tests deterministic.
type MaintenanceClock interface {
	Now() time.Time
	After(time.Duration) <-chan time.Time
}

type realMaintenanceClock struct{}

func (realMaintenanceClock) Now() time.Time { return time.Now().UTC() }
func (realMaintenanceClock) After(duration time.Duration) <-chan time.Time {
	return time.After(duration)
}

// MaintenanceConfig wires bounded retention, checkpoint, and backup work.
type MaintenanceConfig struct {
	Store          MaintenanceRunner
	Backupper      SQLiteBackupper
	Policy         model.MaintenancePolicy
	Interval       time.Duration
	BackupInterval time.Duration
	BackupDir      string
	BackupKeep     int
	Clock          MaintenanceClock
	Logger         *slog.Logger
}

// Maintenance schedules bounded SQLite housekeeping without blocking boot.
type Maintenance struct {
	store          MaintenanceRunner
	backupper      SQLiteBackupper
	policy         model.MaintenancePolicy
	interval       time.Duration
	backupInterval time.Duration
	backupDir      string
	backupKeep     int
	clock          MaintenanceClock
	logger         *slog.Logger

	mu           sync.RWMutex
	status       model.MaintenanceStatus
	lastBackupAt time.Time
}

// NewMaintenance builds the SQLite maintenance scheduler.
func NewMaintenance(cfg MaintenanceConfig) *Maintenance {
	interval := cfg.Interval
	if interval <= 0 {
		interval = 5 * time.Minute
	}
	backupInterval := cfg.BackupInterval
	if backupInterval <= 0 {
		backupInterval = defaultSQLiteBackupInterval
	}
	clock := cfg.Clock
	if clock == nil {
		clock = realMaintenanceClock{}
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}

	backupKeep := cfg.BackupKeep
	if backupKeep <= 0 {
		backupKeep = defaultSQLiteBackupKeep
	}

	return &Maintenance{
		store: cfg.Store, backupper: cfg.Backupper, policy: cfg.Policy,
		interval: interval, backupInterval: backupInterval,
		backupDir: cfg.BackupDir, backupKeep: backupKeep, clock: clock, logger: logger,
	}
}

// Run performs an immediate pass and repeats until cancellation. A failed pass
// is recorded and retried; it never terminates the loop.
func (m *Maintenance) Run(ctx context.Context) {
	select {
	case <-ctx.Done():
		return
	default:
	}

	m.runOnce(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-m.clock.After(m.interval):
			m.runOnce(ctx)
		}
	}
}

func (m *Maintenance) runOnce(ctx context.Context) {
	now := m.clock.Now().UTC()
	policy := m.policy
	policy.Now = now
	result, err := m.store.Run(ctx, policy)

	m.mu.Lock()
	m.status.LastRunAt = now
	if err != nil {
		m.status.LastError = err.Error()
		m.mu.Unlock()
		m.logger.Warn("sqlite maintenance failed", "error", err)

		return
	}
	m.status.LastSuccessAt = now
	m.status.LastError = ""
	m.status.LastResult = result
	backupDue := m.lastBackupAt.IsZero() || now.Sub(m.lastBackupAt) >= m.backupInterval
	m.mu.Unlock()

	if backupDue {
		destination := filepath.Join(m.backupDir, "touchgrass-"+now.Format("20060102T150405Z")+".db")
		if err := m.Backup(ctx, destination); err != nil {
			m.mu.Lock()
			m.status.LastError = err.Error()
			m.mu.Unlock()
			m.logger.Warn("sqlite backup failed", "error", err)

			return
		}

		if err := m.pruneBackups(); err != nil {
			m.mu.Lock()
			m.status.LastError = err.Error()
			m.mu.Unlock()
			m.logger.Warn("sqlite backup retention failed", "error", err)

			return
		}

		m.mu.Lock()
		m.lastBackupAt = now
		m.mu.Unlock()
	}

	if result.OverBudget {
		m.logger.Warn("sqlite storage budget exceeded", "bytes", result.DatabaseBytes, "max_bytes", result.MaxBytes)
	}
}

func (m *Maintenance) pruneBackups() error {
	entries, err := os.ReadDir(m.backupDir)
	if err != nil {
		return fmt.Errorf("reading sqlite backup directory: %w", err)
	}

	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()

		stamp := strings.TrimSuffix(strings.TrimPrefix(name, "touchgrass-"), ".db")
		if entry.Type().IsRegular() && strings.HasPrefix(name, "touchgrass-") && strings.HasSuffix(name, ".db") {
			if _, err := time.Parse("20060102T150405Z", stamp); err == nil {
				names = append(names, name)
			}
		}
	}

	sort.Strings(names)

	for _, name := range names[:max(0, len(names)-m.backupKeep)] {
		if err := os.Remove(filepath.Join(m.backupDir, name)); err != nil {
			return fmt.Errorf("removing expired sqlite backup %q: %w", name, err)
		}
	}

	return nil
}

// Backup validates destination confinement before writing a snapshot.
func (m *Maintenance) Backup(ctx context.Context, destination string) error {
	if m.backupper == nil {
		return errors.New("backing up sqlite: backupper is not configured")
	}
	if err := os.MkdirAll(m.backupDir, 0o700); err != nil {
		return fmt.Errorf("creating sqlite backup directory: %w", err)
	}

	base, err := filepath.Abs(filepath.Clean(m.backupDir))
	if err != nil {
		return fmt.Errorf("resolving sqlite backup directory: %w", err)
	}
	destination, err = filepath.Abs(filepath.Clean(destination))
	if err != nil {
		return fmt.Errorf("resolving sqlite backup destination: %w", err)
	}
	if filepath.Dir(destination) != base {
		return errors.New("backing up sqlite: destination must be directly inside the configured backup directory")
	}

	return m.backupper.Backup(ctx, destination)
}

// Status returns freshness without hiding the last successful result.
func (m *Maintenance) Status() model.MaintenanceStatus {
	m.mu.RLock()
	status := m.status
	m.mu.RUnlock()

	status.Stale = status.LastSuccessAt.IsZero() || m.clock.Now().Sub(status.LastSuccessAt) > 2*m.interval

	return status
}
