package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/oralecarlangelo/touchgrass/internal/model"
	"github.com/oralecarlangelo/touchgrass/internal/store"
)

// Database errors reported to handlers.
var (
	// ErrDatabaseUnconfigured reports postgres-dependent work without a container.
	ErrDatabaseUnconfigured = errors.New("service: postgres is not configured")
	// ErrDatabaseBusy reports a backup/restore request while a job runs.
	ErrDatabaseBusy = errors.New("service: a database job is already running")
)

// dockerBin is the only binary the database runner executes.
const dockerBin = "docker"

// jobsListLimit bounds GET /api/databases/jobs to the last 20 runs.
const jobsListLimit = 20

// shaSuffix is the checksum sidecar appended to each backup name.
const shaSuffix = ".sha256"

// backupNameRe is the strict backup-name shape: no separators, so no
// traversal is expressible. Restore and verify reject anything else.
var backupNameRe = regexp.MustCompile(`^[A-Za-z0-9_.-]+\.dump$`)

// backupHostileRe matches runes outside the backup-name shape.
var backupHostileRe = regexp.MustCompile(`[^A-Za-z0-9_.-]+`)

// postgresVersionRe extracts the numeric version from SELECT version().
var postgresVersionRe = regexp.MustCompile(`PostgreSQL (\d+(?:\.\d+)?)`)

// CommandRunner executes name with args, streaming stdin to the process
// and the process stdout to stdout. Stderr folds into the error.
type CommandRunner func(
	ctx context.Context,
	name string,
	args []string,
	stdin io.Reader,
	stdout io.Writer,
) error

// DatabaseConfig wires a Database.
type DatabaseConfig struct {
	Container      string
	User           string
	DBName         string
	BackupDir      string
	BackupKeep     int
	RedisContainer string
	DBPath         string
	Jobs           *store.DBJobStore
	Audit          *store.AuditStore
	SQLite         *store.DB
	Run            CommandRunner
	Logger         *slog.Logger
}

// Database serves postgres backup/restore jobs plus postgres, redis,
// and sqlite health. All postgres access goes through docker exec;
// jobs run async behind a one-at-a-time guard.
type Database struct {
	container      string
	user           string
	dbName         string
	backupDir      string
	keep           int
	redisContainer string
	dbPath         string
	jobs           *store.DBJobStore
	audits         *store.AuditStore
	sqlite         *store.DB
	run            CommandRunner
	logger         *slog.Logger
	mutex          sync.Mutex
	busy           bool
}

// NewDatabase builds a Database. A nil Run defaults to execCommand,
// an empty BackupDir to ./backups, and a non-positive BackupKeep to 1.
func NewDatabase(cfg DatabaseConfig) *Database {
	run := cfg.Run
	if run == nil {
		run = execCommand
	}

	backupDir := cfg.BackupDir
	if backupDir == "" {
		backupDir = "./backups"
	}

	keep := max(cfg.BackupKeep, 1)

	return &Database{
		container:      cfg.Container,
		user:           cfg.User,
		dbName:         cfg.DBName,
		backupDir:      backupDir,
		keep:           keep,
		redisContainer: cfg.RedisContainer,
		dbPath:         cfg.DBPath,
		jobs:           cfg.Jobs,
		audits:         cfg.Audit,
		sqlite:         cfg.SQLite,
		run:            run,
		logger:         cfg.Logger,
	}
}

// execCommand runs name with args, wiring stdin/stdout through.
func execCommand(
	ctx context.Context,
	name string,
	args []string,
	stdin io.Reader,
	stdout io.Writer,
) error {
	//nolint:gosec // production callers pass the docker literal; args are fixed flags plus validated config.
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin = stdin
	cmd.Stdout = stdout

	var stderr bytes.Buffer

	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if trimmed := strings.TrimSpace(stderr.String()); trimmed != "" {
			return fmt.Errorf("exec %s failed: %w: %s", name, err, trimmed)
		}

		return fmt.Errorf("exec %s failed: %w", name, err)
	}

	return nil
}

// dockerArgs builds a docker exec invocation for container. Interactive
// keeps stdin open for pg_restore, which reads the dump from stdin.
func dockerArgs(container string, interactive bool, toolArgs ...string) []string {
	args := []string{"exec"}

	if interactive {
		args = append(args, "-i")
	}

	args = append(args, container)

	return append(args, toolArgs...)
}

// execOut runs name and returns trimmed stdout.
func (d *Database) execOut(
	ctx context.Context,
	name string,
	args []string,
	stdin io.Reader,
) (string, error) {
	var stdout bytes.Buffer

	if err := d.run(ctx, name, args, stdin, &stdout); err != nil {
		return "", err
	}

	return strings.TrimSpace(stdout.String()), nil
}

// psql runs one read query and returns trimmed stdout.
func (d *Database) psql(ctx context.Context, query string) (string, error) {
	return d.execOut(ctx, dockerBin, dockerArgs(d.container, false,
		"psql", "-U", d.user, "-d", d.dbName, "-tA", "-c", query,
	), nil)
}

// postgresInt runs a single-number query.
func (d *Database) postgresInt(ctx context.Context, query string) (int64, error) {
	raw, err := d.psql(ctx, query)
	if err != nil {
		return 0, err
	}

	value, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parsing postgres number %q: %w", raw, err)
	}

	return value, nil
}

// postgresVersion reports the numeric server version (e.g. "16.3").
func (d *Database) postgresVersion(ctx context.Context) (string, error) {
	raw, err := d.psql(ctx, "SELECT version()")
	if err != nil {
		return "", err
	}

	match := postgresVersionRe.FindStringSubmatch(raw)
	if match == nil {
		return "", fmt.Errorf("parsing postgres version %q: unexpected format", raw)
	}

	return match[1], nil
}

// Databases reports postgres, redis, and sqlite health. Probes degrade
// per field: a failed check leaves its entry unreachable or its stat
// null instead of failing the whole call.
func (d *Database) Databases(ctx context.Context) ([]model.Database, error) {
	return []model.Database{
		d.postgresHealth(ctx),
		d.redisHealth(ctx),
		d.sqliteHealth(ctx),
	}, nil
}

// postgresHealth probes the postgres container via pg_isready plus psql.
func (d *Database) postgresHealth(ctx context.Context) model.Database {
	db := model.Database{
		ID:         model.DatabasePostgres,
		Label:      model.DatabasePostgresLabel,
		Configured: d.container != "",
	}
	db.LastBackupAt = d.lastBackupAt()

	if !db.Configured {
		return db
	}

	if err := d.run(ctx, dockerBin, dockerArgs(d.container, false,
		"pg_isready", "-U", d.user, "-d", d.dbName,
	), nil, io.Discard); err != nil {
		d.logger.Debug("postgres is not ready", "error", err)

		return db
	}

	db.Reachable = true

	if version, err := d.postgresVersion(ctx); err != nil {
		d.logger.Debug("postgres version probe failed", "error", err)
	} else {
		db.Version = &version
	}

	db.SizeBytes = d.probeInt(ctx, "SELECT pg_database_size(current_database())", "size")
	db.ConnectionsUsed = d.probeInt(ctx, "SELECT count(*) FROM pg_stat_activity", "connections")
	db.ConnectionsMax = d.probeInt(ctx, "SHOW max_connections", "max connections")
	db.UptimeSecs = d.probeInt(
		ctx,
		"SELECT EXTRACT(EPOCH FROM now() - pg_postmaster_start_time())::bigint",
		"uptime",
	)

	return db
}

// probeInt runs one postgres number query, degrading to nil on failure.
func (d *Database) probeInt(ctx context.Context, query, noun string) *int64 {
	value, err := d.postgresInt(ctx, query)
	if err != nil {
		d.logger.Debug("postgres probe failed", "stat", noun, "error", err)

		return nil
	}

	return &value
}

// redisHealth probes the redis container via redis-cli info.
func (d *Database) redisHealth(ctx context.Context) model.Database {
	db := model.Database{
		ID:         model.DatabaseRedis,
		Label:      model.DatabaseRedisLabel,
		Configured: d.redisContainer != "",
	}

	if !db.Configured {
		return db
	}

	server, err := d.execOut(ctx, dockerBin, dockerArgs(
		d.redisContainer, false, "redis-cli", "info", "server",
	), nil)
	if err != nil {
		d.logger.Debug("redis is not ready", "error", err)

		return db
	}

	db.Reachable = true

	if version := infoField(server, "redis_version"); version != "" {
		db.Version = &version
	}

	db.UptimeSecs = infoSeconds(server, "uptime_in_seconds")

	memory, err := d.execOut(ctx, dockerBin, dockerArgs(
		d.redisContainer, false, "redis-cli", "info", "memory",
	), nil)
	if err != nil {
		d.logger.Debug("redis memory probe failed", "error", err)

		return db
	}

	if used, err := infoNumber(memory, "used_memory"); err != nil {
		d.logger.Debug("redis memory probe failed", "error", err)
	} else {
		db.UsedMemoryBytes = &used
	}

	return db
}

// infoField extracts one key:value line from redis INFO output.
func infoField(info, key string) string {
	for line := range strings.Lines(info) {
		name, value, ok := strings.Cut(strings.TrimSpace(line), ":")
		if ok && name == key {
			return value
		}
	}

	return ""
}

// infoNumber parses one numeric redis INFO field.
func infoNumber(info, key string) (int64, error) {
	raw := infoField(info, key)

	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parsing redis %s %q: %w", key, raw, err)
	}

	return value, nil
}

// infoSeconds parses one redis INFO uptime field, degrading to nil.
func infoSeconds(info, key string) *int64 {
	secs, err := infoNumber(info, key)
	if err != nil {
		return nil
	}

	return &secs
}

// sqliteHealth reports the embedded database version, size, and integrity.
func (d *Database) sqliteHealth(ctx context.Context) model.Database {
	db := model.Database{
		ID:         model.DatabaseSQLite,
		Label:      model.DatabaseSQLiteLabel,
		Configured: true,
	}
	size := databaseBytes(d.dbPath)
	db.SizeBytes = &size

	health, err := d.sqlite.SQLiteHealth(ctx)
	if err != nil {
		d.logger.Debug("sqlite health probe failed", "error", err)

		return db
	}

	db.Reachable = true
	db.Version = &health.Version
	db.Integrity = &health.Integrity

	return db
}

// lastBackupAt returns the newest backup mtime, or nil when empty.
func (d *Database) lastBackupAt() *time.Time {
	backups, err := d.Backups()
	if err != nil || len(backups) == 0 {
		return nil
	}

	newest := backups[0].CreatedAt

	return &newest
}

// Backups lists pg_dump artifacts newest first. A missing backup
// directory reads as empty; files outside the backup-name shape are
// skipped so stray files never surface as restorable.
func (d *Database) Backups() ([]model.BackupInfo, error) {
	entries, err := os.ReadDir(d.backupDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []model.BackupInfo{}, nil
		}

		return nil, fmt.Errorf("listing backups: %w", err)
	}

	backups := []model.BackupInfo{}

	for _, entry := range entries {
		if entry.IsDir() || !backupNameRe.MatchString(entry.Name()) {
			continue
		}

		info, err := entry.Info()
		if err != nil {
			return nil, fmt.Errorf("statting backup %q: %w", entry.Name(), err)
		}

		sha := model.BackupSHAMissing
		if _, err := os.Stat(filepath.Join(d.backupDir, entry.Name()+shaSuffix)); err == nil {
			sha = model.BackupSHAPresent
		}

		backups = append(backups, model.BackupInfo{
			Name:      entry.Name(),
			SizeBytes: info.Size(),
			CreatedAt: info.ModTime().UTC(),
			SHA256:    sha,
		})
	}

	slices.SortFunc(backups, func(a, b model.BackupInfo) int {
		if cmp := b.CreatedAt.Compare(a.CreatedAt); cmp != 0 {
			return cmp
		}

		return strings.Compare(b.Name, a.Name)
	})

	return backups, nil
}

// Job returns one job by id.
func (d *Database) Job(ctx context.Context, id int64) (model.DBJob, error) {
	return d.jobs.Get(ctx, id)
}

// Jobs returns job history newest first, bounded to the last 20 runs.
func (d *Database) Jobs(ctx context.Context) ([]model.DBJob, error) {
	return d.jobs.List(ctx, jobsListLimit)
}

// validateBackupName rejects traversal and non-dump names.
func validateBackupName(name string) error {
	if !backupNameRe.MatchString(name) {
		return fmt.Errorf("%w: backup name %q (want <name>.dump)", ErrInvalidInput, name)
	}

	return nil
}

// backupFileName renders <db>-<UTC>.dump, sanitized to the backup-name shape.
func backupFileName(dbName string, at time.Time) string {
	return sanitizeName(dbName) + "-" + at.Format("20060102T150405Z") + ".dump"
}

// safetyFileName renders <db>-pre-restore-<UTC>.dump for pre-restore copies.
func safetyFileName(dbName string, at time.Time) string {
	return sanitizeName(dbName) + "-pre-restore-" + at.Format("20060102T150405Z") + ".dump"
}

// sanitizeName maps backup-name-hostile runes to underscores.
func sanitizeName(name string) string {
	return backupHostileRe.ReplaceAllString(name, "_")
}

// claim guards one job at a time across backup and restore.
func (d *Database) claim() error {
	d.mutex.Lock()
	defer d.mutex.Unlock()

	if d.busy {
		return ErrDatabaseBusy
	}

	d.busy = true

	return nil
}

// release clears the job guard.
func (d *Database) release() {
	d.mutex.Lock()
	defer d.mutex.Unlock()

	d.busy = false
}

// Active reports whether a database job is in flight.
func (d *Database) Active() bool {
	d.mutex.Lock()
	defer d.mutex.Unlock()

	return d.busy
}

// Reconcile fails jobs left running by a restart: the in-memory
// guard clears on boot, but their rows would read running forever.
func (d *Database) Reconcile(ctx context.Context) error {
	failed, err := d.jobs.FailRunning(ctx, "interrupted by restart")
	if err != nil {
		return fmt.Errorf("reconciling database jobs: %w", err)
	}

	if failed > 0 {
		d.logger.Info("database jobs interrupted by restart", "count", failed)
	}

	return nil
}

// StartBackup begins an async pg_dump backup, returning the job id.
func (d *Database) StartBackup(ctx context.Context, actor string) (int64, error) {
	if d.container == "" {
		return 0, ErrDatabaseUnconfigured
	}

	return d.launch(ctx, model.DBJobBackup, backupFileName(d.dbName, time.Now().UTC()), actor)
}

// StartRestore begins an async restore of one backup, returning the job id.
func (d *Database) StartRestore(ctx context.Context, name, actor string) (int64, error) {
	if err := validateBackupName(name); err != nil {
		return 0, err
	}

	if d.container == "" {
		return 0, ErrDatabaseUnconfigured
	}

	if _, err := os.Stat(filepath.Join(d.backupDir, name)); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, fmt.Errorf("%w: backup %q not found", ErrInvalidInput, name)
		}

		return 0, fmt.Errorf("statting backup %q: %w", name, err)
	}

	return d.launch(ctx, model.DBJobRestore, name, actor)
}

// launch claims the guard, records a running job, and starts it async.
func (d *Database) launch(ctx context.Context, kind, target, actor string) (int64, error) {
	if err := d.claim(); err != nil {
		return 0, err
	}

	id, err := d.jobs.Create(ctx, model.DBJob{
		Kind:       kind,
		Target:     target,
		Status:     model.DBJobRunning,
		StartedAt:  time.Now(),
		FinishedAt: nil,
	})
	if err != nil {
		d.release()

		return 0, fmt.Errorf("creating %s job: %w", kind, err)
	}

	//nolint:gosec // database jobs intentionally outlive the request; WithoutCancel detaches from cancellation.
	go d.execute(context.WithoutCancel(ctx), jobRequest{
		id:     id,
		kind:   kind,
		target: target,
		actor:  actor,
	})

	return id, nil
}

// jobRequest carries one async run.
type jobRequest struct {
	id     int64
	kind   string
	target string
	actor  string
}

// execute runs one backup or restore, then records and audits it.
func (d *Database) execute(ctx context.Context, req jobRequest) {
	defer d.release()

	var (
		detail string
		err    error
	)

	if req.kind == model.DBJobRestore {
		detail, err = d.runRestore(ctx, req.target)
	} else {
		detail, err = d.runBackup(ctx, req.target)
	}

	d.finishJob(context.Background(), jobOutcome{
		id:     req.id,
		kind:   req.kind,
		target: req.target,
		actor:  req.actor,
		detail: detail,
		jobErr: err,
	})
}

// jobOutcome carries one finished run for recording.
type jobOutcome struct {
	id     int64
	kind   string
	target string
	actor  string
	detail string
	jobErr error
}

// finishJob audits a job outcome, then stamps it terminal. The audit
// lands first so a terminal status implies its audit entry exists.
func (d *Database) finishJob(ctx context.Context, outcome jobOutcome) {
	status := model.DBJobSuccess
	result := model.AuditSuccess
	detail := outcome.detail

	if outcome.jobErr != nil {
		status = model.DBJobFailed
		result = model.AuditFailure
		detail = outcome.jobErr.Error()
		d.logger.Error("database job failed",
			"job", outcome.id, "kind", outcome.kind, "error", outcome.jobErr)
	}

	auditDetail := outcome.kind + " " + outcome.target + ": " + status
	if detail != "" {
		auditDetail += "; " + detail
	}

	action := model.AuditDBBackup
	if outcome.kind == model.DBJobRestore {
		action = model.AuditDBRestore
	}

	d.audit(ctx, outcome.actor, action, result, auditDetail)

	if err := d.jobs.Finish(ctx, outcome.id, status, detail, time.Now()); err != nil {
		d.logger.Error("finishing database job failed", "job", outcome.id, "error", err)
	}
}

// audit records a db_backup/db_restore entry; failures log, never fail the job.
func (d *Database) audit(ctx context.Context, actor, action, result, detail string) {
	if _, err := d.audits.Insert(ctx, model.Audit{
		Actor:     actor,
		Action:    action,
		Result:    result,
		Detail:    detail,
		CreatedAt: time.Now(),
	}); err != nil {
		d.logger.Error("database audit failed", "action", action, "error", err)
	}
}

// runBackup dumps postgres to name at 0600, writes its sidecar, and
// trims the directory to the newest keep artifacts.
func (d *Database) runBackup(ctx context.Context, name string) (string, error) {
	if err := os.MkdirAll(d.backupDir, 0o700); err != nil {
		return "", fmt.Errorf("creating backup dir: %w", err)
	}

	path := filepath.Join(d.backupDir, name)

	//nolint:gosec // name is generated in the no-separator backup shape; the dir is admin-configured.
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", fmt.Errorf("creating backup file: %w", err)
	}

	if err := d.run(ctx, dockerBin, dockerArgs(d.container, false,
		"pg_dump", "-U", d.user, "-d", d.dbName, "-Fc",
	), nil, file); err != nil {
		_ = file.Close()
		_ = os.Remove(path)

		return "", fmt.Errorf("dumping postgres: %w", err)
	}

	if err := file.Close(); err != nil {
		_ = os.Remove(path)

		return "", fmt.Errorf("closing backup file: %w", err)
	}

	if err := writeSHA256(path); err != nil {
		_ = os.Remove(path)

		return "", err
	}

	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("statting backup file: %w", err)
	}

	if err := d.trimBackups(); err != nil {
		return "", err
	}

	return fmt.Sprintf("%s (%d bytes)", name, info.Size()), nil
}

// trimBackups keeps the newest keep artifacts, deleting older dumps
// with their sidecars. Per-file failures warn; only the listing is fatal.
func (d *Database) trimBackups() error {
	backups, err := d.Backups()
	if err != nil {
		return err
	}

	for _, backup := range backups[min(len(backups), d.keep):] {
		if err := os.Remove(filepath.Join(d.backupDir, backup.Name)); err != nil {
			d.logger.Warn("trimming old backup failed", "backup", backup.Name, "error", err)

			continue
		}

		if err := os.Remove(filepath.Join(d.backupDir, backup.Name+shaSuffix)); err != nil &&
			!errors.Is(err, os.ErrNotExist) {
			d.logger.Warn("trimming backup checksum failed", "backup", backup.Name, "error", err)
		}
	}

	return nil
}

// writeSHA256 stores the sha256sum sidecar for path at 0600.
func writeSHA256(path string) error {
	//nolint:gosec // path joins the admin-configured dir with a validated backup name.
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("hashing backup: %w", err)
	}
	defer func() {
		_ = file.Close()
	}()

	hash := sha256.New()

	if _, err := io.Copy(hash, file); err != nil {
		return fmt.Errorf("hashing backup: %w", err)
	}

	sidecar := hex.EncodeToString(hash.Sum(nil)) + "  " + filepath.Base(path) + "\n"

	if err := os.WriteFile(path+shaSuffix, []byte(sidecar), 0o600); err != nil {
		return fmt.Errorf("writing backup checksum: %w", err)
	}

	return nil
}

// checkSHA256 verifies path against its sidecar. A missing sidecar
// verifies as true: there is nothing to contradict.
func checkSHA256(path string) (bool, error) {
	//nolint:gosec // path joins the admin-configured dir with a validated backup name.
	raw, err := os.ReadFile(path + shaSuffix)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return true, nil
		}

		return false, fmt.Errorf("reading backup checksum: %w", err)
	}

	//nolint:gosec // path joins the admin-configured dir with a validated backup name.
	file, err := os.Open(path)
	if err != nil {
		return false, fmt.Errorf("hashing backup: %w", err)
	}
	defer func() {
		_ = file.Close()
	}()

	hash := sha256.New()

	if _, err := io.Copy(hash, file); err != nil {
		return false, fmt.Errorf("hashing backup: %w", err)
	}

	want, _, _ := strings.Cut(string(raw), " ")

	return hex.EncodeToString(hash.Sum(nil)) == strings.TrimSpace(want), nil
}

// Verify checks one backup: the sha256 sidecar when present, then
// pg_restore --list through docker exec reading the dump on stdin.
func (d *Database) Verify(ctx context.Context, name string) (model.BackupVerify, error) {
	if err := validateBackupName(name); err != nil {
		return model.BackupVerify{}, err
	}

	if d.container == "" {
		return model.BackupVerify{}, ErrDatabaseUnconfigured
	}

	path := filepath.Join(d.backupDir, name)

	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return model.BackupVerify{}, fmt.Errorf("%w: backup %q not found", ErrInvalidInput, name)
		}

		return model.BackupVerify{}, fmt.Errorf("statting backup %q: %w", name, err)
	}

	ok, err := checkSHA256(path)
	if err != nil {
		return model.BackupVerify{}, err
	}

	if !ok {
		return model.BackupVerify{Name: name, OK: false}, nil
	}

	//nolint:gosec // path joins the admin-configured dir with a validated backup name.
	file, err := os.Open(path)
	if err != nil {
		return model.BackupVerify{}, fmt.Errorf("opening backup %q: %w", name, err)
	}
	defer func() {
		_ = file.Close()
	}()

	if err := d.run(ctx, dockerBin, dockerArgs(d.container, true, "pg_restore", "--list"),
		file, io.Discard); err != nil {
		d.logger.Debug("backup verify failed", "backup", name, "error", err)

		return model.BackupVerify{Name: name, OK: false}, nil
	}

	return model.BackupVerify{Name: name, OK: true}, nil
}

// runRestore restores name over the live database: a safety backup
// first, then terminate backends, pg_restore on stdin (never DROP
// DATABASE), then SELECT 1 plus a public table count.
func (d *Database) runRestore(ctx context.Context, name string) (string, error) {
	safety := safetyFileName(d.dbName, time.Now().UTC())

	if _, err := d.runBackup(ctx, safety); err != nil {
		return "", fmt.Errorf("safety backup failed: %w", err)
	}

	if err := d.terminateBackends(ctx); err != nil {
		return "", err
	}

	path := filepath.Join(d.backupDir, name)

	//nolint:gosec // path joins the admin-configured dir with a validated backup name.
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("opening backup %q: %w", name, err)
	}
	defer func() {
		_ = file.Close()
	}()

	if err := d.run(ctx, dockerBin, dockerArgs(d.container, true,
		"pg_restore", "-U", d.user, "-d", d.dbName, "--clean", "--if-exists",
	), file, io.Discard); err != nil {
		return "", fmt.Errorf("restoring postgres: %w", err)
	}

	one, err := d.psql(ctx, "SELECT 1")
	if err != nil {
		return "", fmt.Errorf("verifying restore: %w", err)
	}

	if one != "1" {
		return "", fmt.Errorf("verifying restore: SELECT 1 returned %q", one)
	}

	tables, err := d.postgresInt(ctx, "SELECT count(*) FROM pg_tables WHERE schemaname = 'public'")
	if err != nil {
		return "", fmt.Errorf("verifying restore: %w", err)
	}

	return fmt.Sprintf("%s restored (%d public tables, safety backup %s)", name, tables, safety), nil
}

// terminateBackends disconnects other sessions so pg_restore owns the database.
func (d *Database) terminateBackends(ctx context.Context) error {
	quoted := strings.ReplaceAll(d.dbName, "'", "''")

	_, err := d.psql(ctx, "SELECT pg_terminate_backend(pid) FROM pg_stat_activity "+
		"WHERE datname = '"+quoted+"' AND pid <> pg_backend_pid()")
	if err != nil {
		return fmt.Errorf("terminating postgres backends: %w", err)
	}

	return nil
}
