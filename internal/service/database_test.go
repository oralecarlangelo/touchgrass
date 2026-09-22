package service

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/oralecarlangelo/touchgrass/internal/model"
	"github.com/oralecarlangelo/touchgrass/internal/store"
)

// fakeRunner routes docker exec calls to a canned handler, recording calls.
type fakeRunner struct {
	mutex   sync.Mutex
	calls   [][]string
	handler func(args []string, stdin io.Reader, stdout io.Writer) error
}

func (f *fakeRunner) run(
	_ context.Context,
	_ string,
	args []string,
	stdin io.Reader,
	stdout io.Writer,
) error {
	f.mutex.Lock()
	defer f.mutex.Unlock()

	f.calls = append(f.calls, args)

	return f.handler(args, stdin, stdout)
}

// query returns a copy of the recorded docker argument lists.
func (f *fakeRunner) query() [][]string {
	f.mutex.Lock()
	defer f.mutex.Unlock()

	return append([][]string{}, f.calls...)
}

// count returns the number of recorded docker calls.
func (f *fakeRunner) count() int {
	f.mutex.Lock()
	defer f.mutex.Unlock()

	return len(f.calls)
}

// testDatabase builds a Database on temp storage with a fake runner.
func testDatabase(
	t *testing.T,
	runner *fakeRunner,
	mutate func(*DatabaseConfig),
) (*Database, *store.DB, string) {
	t.Helper()

	db := openInventoryDB(t)
	dir := t.TempDir()

	cfg := DatabaseConfig{
		Container: "pg-test", User: "postgres", DBName: "ticketnation",
		BackupDir: dir, BackupKeep: 14,
		RedisContainer: "redis-test", DBPath: filepath.Join(dir, "touchgrass.db"),
		Jobs: store.NewDBJobStore(db), Audit: store.NewAuditStore(db), SQLite: db,
		Run: runner.run, Logger: slog.New(slog.DiscardHandler),
	}

	if mutate != nil {
		mutate(&cfg)
	}

	return NewDatabase(cfg), db, dir
}

// Container tools asserted in database tests.
const (
	testToolReady   = "pg_isready"
	testToolPSQL    = "psql"
	testToolDump    = "pg_dump"
	testToolRestore = "pg_restore"
	testToolRedis   = "redis-cli"
)

// execTool finds the container tool in a docker exec argument list.
func execTool(args []string) string {
	for _, arg := range args {
		switch arg {
		case testToolReady, testToolPSQL, testToolDump, testToolRestore, testToolRedis:
			return arg
		}
	}

	return ""
}

// psqlAnswer maps known read queries to canned psql -tA output.
func psqlAnswer(query string) (string, bool) {
	switch {
	case query == "SELECT version()":
		return "PostgreSQL 16.3 on x86_64-pc-linux-gnu, compiled by gcc, 64-bit\n", true
	case query == "SELECT pg_database_size(current_database())":
		return "12345678\n", true
	case query == "SELECT count(*) FROM pg_stat_activity":
		return "7\n", true
	case query == "SHOW max_connections":
		return "100\n", true
	case query == "SELECT EXTRACT(EPOCH FROM now() - pg_postmaster_start_time())::bigint":
		return "86400\n", true
	case query == "SELECT 1":
		return "1\n", true
	case query == "SELECT count(*) FROM pg_tables WHERE schemaname = 'public'":
		return "42\n", true
	case strings.HasPrefix(query, "SELECT pg_terminate_backend(pid)"):
		return "t\n", true
	default:
		return "", false
	}
}

// cannedPostgres emulates a healthy postgres plus redis: pg_isready ok,
// psql answers, pg_dump streams dump bytes, pg_restore drains stdin ok.
func cannedPostgres(dump []byte) func(args []string, stdin io.Reader, stdout io.Writer) error {
	return func(args []string, stdin io.Reader, stdout io.Writer) error {
		switch execTool(args) {
		case testToolReady:
			return nil
		case testToolPSQL:
			out, ok := psqlAnswer(args[len(args)-1])
			if !ok {
				return errors.New("unexpected query")
			}

			_, err := io.WriteString(stdout, out)

			return err
		case testToolDump:
			_, err := stdout.Write(dump)

			return err
		case testToolRestore:
			_, err := io.Copy(io.Discard, stdin)

			return err
		case testToolRedis:
			section := args[len(args)-1]

			var out string

			switch section {
			case "server":
				out = "# Server\nredis_version:7.2.4\nuptime_in_seconds:3600\n"
			case "memory":
				out = "# Memory\nused_memory:1048576\n"
			default:
				return errors.New("unexpected info section")
			}

			_, err := io.WriteString(stdout, out)

			return err
		default:
			return errors.New("unexpected tool")
		}
	}
}

// waitJob polls one job to a terminal status.
func waitJob(t *testing.T, svc *Database, id int64) model.DBJob {
	t.Helper()

	deadline := time.Now().Add(5 * time.Second)

	for time.Now().Before(deadline) {
		job, err := svc.Job(context.Background(), id)
		if err != nil {
			t.Fatalf("Job() error = %v, want nil", err)
		}

		if job.Status != model.DBJobRunning {
			return job
		}

		time.Sleep(5 * time.Millisecond)
	}

	t.Fatalf("job %d still running after 5s", id)

	return model.DBJob{}
}

// auditActions returns the recorded audit actions newest first.
func auditActions(t *testing.T, db *store.DB) []string {
	t.Helper()

	entries, err := store.NewAuditStore(db).List(context.Background(), "", 100)
	if err != nil {
		t.Fatalf("List() error = %v, want nil", err)
	}

	actions := make([]string, 0, len(entries))

	for _, entry := range entries {
		actions = append(actions, entry.Action+":"+entry.Result)
	}

	return actions
}

// seedBackup writes one dump file with an optional sidecar, stamped at mtime.
func seedBackup(
	t *testing.T,
	dir, name string,
	raw []byte,
	mtime time.Time,
	withSidecar bool,
) {
	t.Helper()

	if err := os.WriteFile(filepath.Join(dir, name), raw, 0o600); err != nil {
		t.Fatalf("seeding %s: %v", name, err)
	}

	if withSidecar {
		if err := writeSHA256(filepath.Join(dir, name)); err != nil {
			t.Fatalf("seeding sidecar: %v", err)
		}
	}

	at := mtime.UTC()

	if err := os.Chtimes(filepath.Join(dir, name), at, at); err != nil {
		t.Fatalf("stamping %s: %v", name, err)
	}
}

func TestValidateBackupName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		backup  string
		wantErr bool
	}{
		{name: "plain dump", backup: "ticketnation-20260922T120000Z.dump", wantErr: false},
		{name: "safety name", backup: "db-pre-restore-20260922T120000Z.dump", wantErr: false},
		{name: "dots and dashes", backup: "a.b-c_d.dump", wantErr: false},
		{name: "wrong suffix", backup: "a.sql", wantErr: true},
		{name: "uppercase suffix", backup: "a.DUMP", wantErr: true},
		{name: "empty", backup: "", wantErr: true},
		{name: "parent traversal", backup: "../a.dump", wantErr: true},
		{name: "nested path", backup: "a/b.dump", wantErr: true},
		{name: "space", backup: "a b.dump", wantErr: true},
		{name: "newline", backup: "a.dump\n", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := validateBackupName(tt.backup)

			if tt.wantErr && !errors.Is(err, ErrInvalidInput) {
				t.Errorf("validateBackupName(%q) error = %v, want ErrInvalidInput", tt.backup, err)
			}

			if !tt.wantErr && err != nil {
				t.Errorf("validateBackupName(%q) error = %v, want nil", tt.backup, err)
			}
		})
	}
}

func TestBackupFileNames(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name string
		got  string
		want string
	}{
		{name: "backup", got: backupFileName("ticketnation", at), want: "ticketnation-20260922T120000Z.dump"},
		{
			name: "safety",
			got:  safetyFileName("ticketnation", at),
			want: "ticketnation-pre-restore-20260922T120000Z.dump",
		},
		{name: "hostile db", got: backupFileName("a/b c", at), want: "a_b_c-20260922T120000Z.dump"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if tt.got != tt.want {
				t.Errorf("name = %q, want %q", tt.got, tt.want)
			}

			if err := validateBackupName(tt.got); err != nil {
				t.Errorf("generated name %q fails validation: %v", tt.got, err)
			}
		})
	}
}

// healthyDatabases returns the three health entries from a canned backend.
func healthyDatabases(t *testing.T) []model.Database {
	t.Helper()

	runner := &fakeRunner{handler: cannedPostgres([]byte("dump"))}
	svc, _, _ := testDatabase(t, runner, nil)

	databases, err := svc.Databases(context.Background())
	if err != nil {
		t.Fatalf("Databases() error = %v, want nil", err)
	}

	if len(databases) != 3 {
		t.Fatalf("Databases() = %d entries, want 3", len(databases))
	}

	return databases
}

func TestDatabasesPostgres(t *testing.T) {
	t.Parallel()

	postgres := healthyDatabases(t)[0]

	if postgres.ID != model.DatabasePostgres || !postgres.Configured || !postgres.Reachable {
		t.Errorf("postgres = %+v, want configured reachable", postgres)
	}

	checkPostgresStats(t, postgres)

	if postgres.LastBackupAt != nil {
		t.Errorf("postgres last_backup_at = %v, want nil on empty dir", postgres.LastBackupAt)
	}
}

// checkPostgresStats validates version/size/connection/uptime probes.
func checkPostgresStats(t *testing.T, postgres model.Database) {
	t.Helper()

	if postgres.Version == nil || *postgres.Version != "16.3" {
		t.Errorf("postgres version = %v, want 16.3", postgres.Version)
	}

	if postgres.SizeBytes == nil || *postgres.SizeBytes != 12345678 {
		t.Errorf("postgres size = %v, want 12345678", postgres.SizeBytes)
	}

	if postgres.ConnectionsUsed == nil || *postgres.ConnectionsUsed != 7 {
		t.Errorf("postgres connections_used = %v, want 7", postgres.ConnectionsUsed)
	}

	if postgres.ConnectionsMax == nil || *postgres.ConnectionsMax != 100 {
		t.Errorf("postgres connections_max = %v, want 100", postgres.ConnectionsMax)
	}

	if postgres.UptimeSecs == nil || *postgres.UptimeSecs != 86400 {
		t.Errorf("postgres uptime = %v, want 86400", postgres.UptimeSecs)
	}
}

func TestDatabasesRedis(t *testing.T) {
	t.Parallel()

	redis := healthyDatabases(t)[1]

	if redis.ID != model.DatabaseRedis || !redis.Configured || !redis.Reachable {
		t.Errorf("redis = %+v, want configured reachable", redis)
	}

	if redis.Version == nil || *redis.Version != "7.2.4" {
		t.Errorf("redis version = %v, want 7.2.4", redis.Version)
	}

	if redis.UptimeSecs == nil || *redis.UptimeSecs != 3600 {
		t.Errorf("redis uptime = %v, want 3600", redis.UptimeSecs)
	}

	if redis.UsedMemoryBytes == nil || *redis.UsedMemoryBytes != 1048576 {
		t.Errorf("redis memory = %v, want 1048576", redis.UsedMemoryBytes)
	}
}

func TestDatabasesSQLite(t *testing.T) {
	t.Parallel()

	sqlite := healthyDatabases(t)[2]

	if sqlite.ID != model.DatabaseSQLite || !sqlite.Configured || !sqlite.Reachable {
		t.Errorf("sqlite = %+v, want configured reachable", sqlite)
	}

	if sqlite.Version == nil || *sqlite.Version == "" {
		t.Errorf("sqlite version = %v, want sqlite_version()", sqlite.Version)
	}

	if sqlite.Integrity == nil || *sqlite.Integrity != "ok" {
		t.Errorf("sqlite integrity = %v, want ok", sqlite.Integrity)
	}
}

func TestDatabasesUnconfigured(t *testing.T) {
	t.Parallel()

	runner := &fakeRunner{handler: cannedPostgres(nil)}
	svc, _, _ := testDatabase(t, runner, func(cfg *DatabaseConfig) {
		cfg.Container = ""
		cfg.RedisContainer = ""
	})

	databases, err := svc.Databases(context.Background())
	if err != nil {
		t.Fatalf("Databases() error = %v, want nil", err)
	}

	if databases[0].Configured || databases[0].Reachable {
		t.Errorf("postgres = %+v, want unconfigured", databases[0])
	}

	if databases[1].Configured || databases[1].Reachable {
		t.Errorf("redis = %+v, want unconfigured", databases[1])
	}

	if !databases[2].Configured || !databases[2].Reachable {
		t.Errorf("sqlite = %+v, want configured reachable", databases[2])
	}

	if runner.count() != 0 {
		t.Errorf("runner calls = %d, want 0 without containers", runner.count())
	}
}

func TestDatabasesUnreachable(t *testing.T) {
	t.Parallel()

	runner := &fakeRunner{handler: func(
		_ []string,
		_ io.Reader,
		_ io.Writer,
	) error {
		return errors.New("no such container")
	}}
	svc, _, dir := testDatabase(t, runner, nil)

	seedBackup(t, dir, "old-20260920T120000Z.dump", []byte("old"), time.Now().Add(-48*time.Hour), false)

	databases, err := svc.Databases(context.Background())
	if err != nil {
		t.Fatalf("Databases() error = %v, want nil", err)
	}

	postgres := databases[0]

	if !postgres.Configured || postgres.Reachable {
		t.Errorf("postgres = %+v, want configured unreachable", postgres)
	}

	if postgres.Version != nil || postgres.SizeBytes != nil {
		t.Errorf("postgres stats = (%v, %v), want nils", postgres.Version, postgres.SizeBytes)
	}

	if postgres.LastBackupAt == nil {
		t.Error("postgres last_backup_at is nil, want newest mtime despite unreachable")
	}

	if databases[1].Reachable {
		t.Errorf("redis = %+v, want unreachable", databases[1])
	}
}

func TestDatabasesDegradedStat(t *testing.T) {
	t.Parallel()

	runner := &fakeRunner{handler: func(
		args []string,
		stdin io.Reader,
		stdout io.Writer,
	) error {
		if execTool(args) == testToolPSQL && args[len(args)-1] == "SELECT version()" {
			return errors.New("version query failed")
		}

		return cannedPostgres(nil)(args, stdin, stdout)
	}}
	svc, _, _ := testDatabase(t, runner, nil)

	databases, err := svc.Databases(context.Background())
	if err != nil {
		t.Fatalf("Databases() error = %v, want nil", err)
	}

	postgres := databases[0]

	if !postgres.Reachable {
		t.Fatalf("postgres = %+v, want reachable", postgres)
	}

	if postgres.Version != nil {
		t.Errorf("postgres version = %v, want nil on query failure", postgres.Version)
	}

	if postgres.SizeBytes == nil || *postgres.SizeBytes != 12345678 {
		t.Errorf("postgres size = %v, want 12345678", postgres.SizeBytes)
	}
}

func TestBackupsList(t *testing.T) {
	t.Parallel()

	runner := &fakeRunner{handler: cannedPostgres(nil)}
	svc, _, dir := testDatabase(t, runner, nil)

	now := time.Now().Truncate(time.Second)
	seedBackup(t, dir, "old-20260920T120000Z.dump", []byte("old"), now.Add(-48*time.Hour), true)
	seedBackup(t, dir, "new-20260922T120000Z.dump", []byte("new"), now, false)

	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("x"), 0o600); err != nil {
		t.Fatalf("seeding stray file: %v", err)
	}

	if err := os.Mkdir(filepath.Join(dir, "dir.dump"), 0o700); err != nil {
		t.Fatalf("seeding stray dir: %v", err)
	}

	backups, err := svc.Backups()
	if err != nil {
		t.Fatalf("Backups() error = %v, want nil", err)
	}

	if len(backups) != 2 {
		t.Fatalf("Backups() = %d entries, want 2", len(backups))
	}

	if backups[0].Name != "new-20260922T120000Z.dump" || backups[1].Name != "old-20260920T120000Z.dump" {
		t.Errorf("Backups() order = (%q, %q), want newest first", backups[0].Name, backups[1].Name)
	}

	if backups[0].SHA256 != model.BackupSHAMissing || backups[1].SHA256 != model.BackupSHAPresent {
		t.Errorf("Backups() sha = (%q, %q), want missing then present", backups[0].SHA256, backups[1].SHA256)
	}

	if backups[0].SizeBytes != 3 || !backups[0].CreatedAt.Equal(now.UTC()) {
		t.Errorf("Backups()[0] = %+v, want 3 bytes at seed time", backups[0])
	}
}

func TestBackupsMissingDir(t *testing.T) {
	t.Parallel()

	runner := &fakeRunner{handler: cannedPostgres(nil)}
	missing := filepath.Join(t.TempDir(), "no-such-dir")
	svc, _, _ := testDatabase(t, runner, func(cfg *DatabaseConfig) {
		cfg.BackupDir = missing
	})

	backups, err := svc.Backups()
	if err != nil {
		t.Fatalf("Backups() error = %v, want nil", err)
	}

	if len(backups) != 0 {
		t.Errorf("Backups() = %d entries, want 0", len(backups))
	}
}

func TestStartBackup(t *testing.T) {
	t.Parallel()

	dump := []byte("pg-dump-bytes")
	runner := &fakeRunner{handler: cannedPostgres(dump)}
	svc, db, dir := testDatabase(t, runner, nil)

	id, err := svc.StartBackup(context.Background(), testActor)
	if err != nil {
		t.Fatalf("StartBackup() error = %v, want nil", err)
	}

	if id == 0 {
		t.Fatal("StartBackup() id = 0, want non-zero")
	}

	job := waitJob(t, svc, id)

	if job.Kind != model.DBJobBackup || job.Status != model.DBJobSuccess {
		t.Fatalf("job = %+v, want successful backup", job)
	}

	if job.FinishedAt == nil {
		t.Error("job finished_at is nil, want timestamp")
	}

	checkBackupArtifacts(t, dir, job, dump)

	if !strings.Contains(job.Detail, job.Target) ||
		!strings.Contains(job.Detail, strconv.Itoa(len(dump))+" bytes") {
		t.Errorf("job detail = %q, want name and byte count", job.Detail)
	}

	actions := auditActions(t, db)

	if len(actions) != 1 || actions[0] != model.AuditDBBackup+":"+model.AuditSuccess {
		t.Errorf("audit = %v, want one db_backup success", actions)
	}
}

// checkBackupArtifacts validates the dump bytes, 0600 mode, and sidecar.
func checkBackupArtifacts(t *testing.T, dir string, job model.DBJob, dump []byte) {
	t.Helper()

	path := filepath.Join(dir, job.Target)

	//nolint:gosec // test reads the backup its own job just wrote to a temp dir.
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading backup: %v", err)
	}

	if string(raw) != string(dump) {
		t.Errorf("backup = %q, want dumped bytes", raw)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("statting backup: %v", err)
	}

	if info.Mode().Perm() != 0o600 {
		t.Errorf("backup mode = %o, want 600", info.Mode().Perm())
	}

	//nolint:gosec // test reads the sidecar its own job just wrote to a temp dir.
	sidecar, err := os.ReadFile(path + ".sha256")
	if err != nil {
		t.Fatalf("reading sidecar: %v", err)
	}

	if !strings.Contains(string(sidecar), job.Target) {
		t.Errorf("sidecar = %q, want sha256sum line naming the backup", sidecar)
	}
}

func TestStartBackupUnconfigured(t *testing.T) {
	t.Parallel()

	runner := &fakeRunner{handler: cannedPostgres(nil)}
	svc, _, _ := testDatabase(t, runner, func(cfg *DatabaseConfig) {
		cfg.Container = ""
	})

	if _, err := svc.StartBackup(context.Background(), testActor); !errors.Is(err, ErrDatabaseUnconfigured) {
		t.Errorf("StartBackup() error = %v, want ErrDatabaseUnconfigured", err)
	}
}

func TestStartBackupBusy(t *testing.T) {
	t.Parallel()

	release := make(chan struct{})

	runner := &fakeRunner{handler: func(
		args []string,
		stdin io.Reader,
		stdout io.Writer,
	) error {
		if execTool(args) == testToolDump {
			<-release
		}

		return cannedPostgres([]byte("dump"))(args, stdin, stdout)
	}}
	svc, _, dir := testDatabase(t, runner, nil)

	seedBackup(t, dir, "any-20260922T120000Z.dump", []byte("x"), time.Now(), false)

	id, err := svc.StartBackup(context.Background(), testActor)
	if err != nil {
		t.Fatalf("StartBackup() error = %v, want nil", err)
	}

	deadline := time.Now().Add(5 * time.Second)

	for !svc.Active() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}

	if !svc.Active() {
		t.Fatal("Active() = false while a job runs, want true")
	}

	if _, err := svc.StartBackup(context.Background(), testActor); !errors.Is(err, ErrDatabaseBusy) {
		t.Errorf("second StartBackup() error = %v, want ErrDatabaseBusy", err)
	}

	if _, err := svc.StartRestore(
		context.Background(), "any-20260922T120000Z.dump", testActor,
	); !errors.Is(err, ErrDatabaseBusy) {
		t.Errorf("StartRestore() during backup error = %v, want ErrDatabaseBusy", err)
	}

	close(release)

	job := waitJob(t, svc, id)

	if job.Status != model.DBJobSuccess {
		t.Errorf("job = %+v, want success after release", job)
	}

	if svc.Active() {
		t.Error("Active() = true after finish, want false")
	}
}

func TestStartBackupDumpFailure(t *testing.T) {
	t.Parallel()

	runner := &fakeRunner{handler: func(
		args []string,
		stdin io.Reader,
		stdout io.Writer,
	) error {
		if execTool(args) == testToolDump {
			return errors.New("pg_dump: connection refused")
		}

		return cannedPostgres(nil)(args, stdin, stdout)
	}}
	svc, db, dir := testDatabase(t, runner, nil)

	id, err := svc.StartBackup(context.Background(), testActor)
	if err != nil {
		t.Fatalf("StartBackup() error = %v, want nil", err)
	}

	job := waitJob(t, svc, id)

	if job.Status != model.DBJobFailed {
		t.Fatalf("job = %+v, want failed", job)
	}

	if !strings.Contains(job.Detail, "dumping postgres") {
		t.Errorf("job detail = %q, want dump context", job.Detail)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading backup dir: %v", err)
	}

	if len(entries) != 0 {
		t.Errorf("backup dir holds %d files, want 0 after failed dump", len(entries))
	}

	actions := auditActions(t, db)

	if len(actions) != 1 || actions[0] != model.AuditDBBackup+":"+model.AuditFailure {
		t.Errorf("audit = %v, want one db_backup failure", actions)
	}
}

func TestBackupTrimsKeep(t *testing.T) {
	t.Parallel()

	runner := &fakeRunner{handler: cannedPostgres([]byte("new"))}
	svc, _, dir := testDatabase(t, runner, func(cfg *DatabaseConfig) {
		cfg.BackupKeep = 2
	})

	now := time.Now().Truncate(time.Second)
	seedBackup(t, dir, "a-20260918T120000Z.dump", []byte("a"), now.Add(-96*time.Hour), true)
	seedBackup(t, dir, "b-20260919T120000Z.dump", []byte("b"), now.Add(-72*time.Hour), true)
	seedBackup(t, dir, "c-20260920T120000Z.dump", []byte("c"), now.Add(-48*time.Hour), true)

	id, err := svc.StartBackup(context.Background(), testActor)
	if err != nil {
		t.Fatalf("StartBackup() error = %v, want nil", err)
	}

	job := waitJob(t, svc, id)

	if job.Status != model.DBJobSuccess {
		t.Fatalf("job = %+v, want success", job)
	}

	backups, err := svc.Backups()
	if err != nil {
		t.Fatalf("Backups() error = %v, want nil", err)
	}

	if len(backups) != 2 {
		t.Fatalf("Backups() = %d entries, want 2", len(backups))
	}

	if backups[0].Name != job.Target || backups[1].Name != "c-20260920T120000Z.dump" {
		t.Errorf("Backups() = (%q, %q), want newest two", backups[0].Name, backups[1].Name)
	}

	for _, stale := range []string{"a-20260918T120000Z.dump", "b-20260919T120000Z.dump"} {
		if _, err := os.Stat(filepath.Join(dir, stale)); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s still exists, want trimmed", stale)
		}

		if _, err := os.Stat(filepath.Join(dir, stale+".sha256")); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s sidecar still exists, want trimmed", stale)
		}
	}
}

func TestStartRestore(t *testing.T) {
	t.Parallel()

	runner := &fakeRunner{handler: cannedPostgres([]byte("safety-dump"))}
	svc, db, dir := testDatabase(t, runner, nil)

	seedBackup(t, dir, "src-20260922T120000Z.dump", []byte("source-dump"), time.Now(), true)

	id, err := svc.StartRestore(context.Background(), "src-20260922T120000Z.dump", testActor)
	if err != nil {
		t.Fatalf("StartRestore() error = %v, want nil", err)
	}

	job := waitJob(t, svc, id)

	if job.Kind != model.DBJobRestore || job.Status != model.DBJobSuccess {
		t.Fatalf("job = %+v, want successful restore", job)
	}

	if !strings.Contains(job.Detail, "42 public tables") ||
		!strings.Contains(job.Detail, "safety backup") {
		t.Errorf("job detail = %q, want table count and safety name", job.Detail)
	}

	safety, err := filepath.Glob(filepath.Join(dir, "*-pre-restore-*.dump"))
	if err != nil {
		t.Fatalf("globbing safety backups: %v", err)
	}

	if len(safety) != 1 {
		t.Fatalf("safety backups = %d, want 1", len(safety))
	}

	checkRestoreCalls(t, runner.query())

	actions := auditActions(t, db)

	if len(actions) != 1 || actions[0] != model.AuditDBRestore+":"+model.AuditSuccess {
		t.Errorf("audit = %v, want one db_restore success", actions)
	}
}

// checkRestoreCalls pins the restore exec sequence: terminate plus
// pg_restore --clean --if-exists, and never a database drop.
func checkRestoreCalls(t *testing.T, calls [][]string) {
	t.Helper()

	var (
		sawTerminate bool
		sawRestore   bool
	)

	for _, call := range calls {
		joined := strings.Join(call, " ")

		if strings.Contains(strings.ToLower(joined), "drop database") {
			t.Errorf("restore drops the database: %q", joined)
		}

		if strings.Contains(joined, "pg_terminate_backend") {
			sawTerminate = true
		}

		if execTool(call) == testToolRestore && strings.Contains(joined, "--clean --if-exists") {
			sawRestore = true
		}
	}

	if !sawTerminate {
		t.Error("restore never terminated backends")
	}

	if !sawRestore {
		t.Error("restore never ran pg_restore --clean --if-exists")
	}
}

func TestStartRestoreValidation(t *testing.T) {
	t.Parallel()

	runner := &fakeRunner{handler: cannedPostgres([]byte("dump"))}

	t.Run("bad name", func(t *testing.T) {
		t.Parallel()

		svc, _, _ := testDatabase(t, runner, nil)

		_, err := svc.StartRestore(context.Background(), "../evil.dump", testActor)

		if !errors.Is(err, ErrInvalidInput) {
			t.Errorf("StartRestore() error = %v, want ErrInvalidInput", err)
		}
	})

	t.Run("missing file", func(t *testing.T) {
		t.Parallel()

		svc, _, _ := testDatabase(t, runner, nil)

		_, err := svc.StartRestore(context.Background(), "gone-20260922T120000Z.dump", testActor)

		if !errors.Is(err, ErrInvalidInput) {
			t.Errorf("StartRestore() error = %v, want ErrInvalidInput", err)
		}
	})

	t.Run("unconfigured", func(t *testing.T) {
		t.Parallel()

		svc, _, dir := testDatabase(t, runner, func(cfg *DatabaseConfig) {
			cfg.Container = ""
		})

		seedBackup(t, dir, "src-20260922T120000Z.dump", []byte("x"), time.Now(), false)

		_, err := svc.StartRestore(context.Background(), "src-20260922T120000Z.dump", testActor)

		if !errors.Is(err, ErrDatabaseUnconfigured) {
			t.Errorf("StartRestore() error = %v, want ErrDatabaseUnconfigured", err)
		}
	})
}

func TestRestoreSafetyFailure(t *testing.T) {
	t.Parallel()

	runner := &fakeRunner{handler: func(
		args []string,
		stdin io.Reader,
		stdout io.Writer,
	) error {
		if execTool(args) == testToolDump {
			return errors.New("pg_dump: disk full")
		}

		return cannedPostgres(nil)(args, stdin, stdout)
	}}
	svc, _, dir := testDatabase(t, runner, nil)

	seedBackup(t, dir, "src-20260922T120000Z.dump", []byte("source"), time.Now(), false)

	id, err := svc.StartRestore(context.Background(), "src-20260922T120000Z.dump", testActor)
	if err != nil {
		t.Fatalf("StartRestore() error = %v, want nil", err)
	}

	job := waitJob(t, svc, id)

	if job.Status != model.DBJobFailed {
		t.Fatalf("job = %+v, want failed", job)
	}

	if !strings.Contains(job.Detail, "safety backup failed") {
		t.Errorf("job detail = %q, want safety context", job.Detail)
	}

	for _, call := range runner.query() {
		if execTool(call) == testToolRestore {
			t.Errorf("pg_restore ran despite safety failure: %q", call)
		}
	}
}

func TestRestoreVerifyFailure(t *testing.T) {
	t.Parallel()

	runner := &fakeRunner{handler: func(
		args []string,
		stdin io.Reader,
		stdout io.Writer,
	) error {
		if execTool(args) == testToolPSQL && args[len(args)-1] == "SELECT 1" {
			return errors.New("connection refused")
		}

		return cannedPostgres([]byte("safety"))(args, stdin, stdout)
	}}
	svc, _, dir := testDatabase(t, runner, nil)

	seedBackup(t, dir, "src-20260922T120000Z.dump", []byte("source"), time.Now(), false)

	id, err := svc.StartRestore(context.Background(), "src-20260922T120000Z.dump", testActor)
	if err != nil {
		t.Fatalf("StartRestore() error = %v, want nil", err)
	}

	job := waitJob(t, svc, id)

	if job.Status != model.DBJobFailed {
		t.Fatalf("job = %+v, want failed", job)
	}

	if !strings.Contains(job.Detail, "verifying restore") {
		t.Errorf("job detail = %q, want verify context", job.Detail)
	}
}

func TestVerifyOK(t *testing.T) {
	t.Parallel()

	t.Run("with sidecar", func(t *testing.T) {
		t.Parallel()

		runner := &fakeRunner{handler: cannedPostgres(nil)}
		svc, _, dir := testDatabase(t, runner, nil)

		seedBackup(t, dir, "good-20260922T120000Z.dump", []byte("good"), time.Now(), true)

		got, err := svc.Verify(context.Background(), "good-20260922T120000Z.dump")
		if err != nil {
			t.Fatalf("Verify() error = %v, want nil", err)
		}

		if !got.OK || got.Name != "good-20260922T120000Z.dump" {
			t.Errorf("Verify() = %+v, want ok", got)
		}
	})

	t.Run("without sidecar", func(t *testing.T) {
		t.Parallel()

		runner := &fakeRunner{handler: cannedPostgres(nil)}
		svc, _, dir := testDatabase(t, runner, nil)

		seedBackup(t, dir, "nosha-20260922T120000Z.dump", []byte("x"), time.Now(), false)

		got, err := svc.Verify(context.Background(), "nosha-20260922T120000Z.dump")
		if err != nil {
			t.Fatalf("Verify() error = %v, want nil", err)
		}

		if !got.OK {
			t.Errorf("Verify() = %+v, want ok via pg_restore", got)
		}
	})
}

func TestVerifyCorrupt(t *testing.T) {
	t.Parallel()

	t.Run("checksum mismatch", func(t *testing.T) {
		t.Parallel()

		runner := &fakeRunner{handler: cannedPostgres(nil)}
		svc, _, dir := testDatabase(t, runner, nil)

		seedBackup(t, dir, "bad-20260922T120000Z.dump", []byte("original"), time.Now(), true)

		if err := os.WriteFile(
			filepath.Join(dir, "bad-20260922T120000Z.dump"), []byte("tampered"), 0o600,
		); err != nil {
			t.Fatalf("tampering: %v", err)
		}

		got, err := svc.Verify(context.Background(), "bad-20260922T120000Z.dump")
		if err != nil {
			t.Fatalf("Verify() error = %v, want nil", err)
		}

		if got.OK {
			t.Errorf("Verify() = %+v, want not ok", got)
		}

		for _, call := range runner.query() {
			if execTool(call) == testToolRestore {
				t.Errorf("pg_restore ran despite checksum mismatch: %q", call)
			}
		}
	})

	t.Run("corrupt archive", func(t *testing.T) {
		t.Parallel()

		runner := &fakeRunner{handler: func(
			args []string,
			stdin io.Reader,
			stdout io.Writer,
		) error {
			if execTool(args) == testToolRestore {
				return errors.New("pg_restore: not a valid archive")
			}

			return cannedPostgres(nil)(args, stdin, stdout)
		}}
		svc, _, dir := testDatabase(t, runner, nil)

		seedBackup(t, dir, "junk-20260922T120000Z.dump", []byte("junk"), time.Now(), false)

		got, err := svc.Verify(context.Background(), "junk-20260922T120000Z.dump")
		if err != nil {
			t.Fatalf("Verify() error = %v, want nil", err)
		}

		if got.OK {
			t.Errorf("Verify() = %+v, want not ok", got)
		}
	})
}

func TestVerifyValidation(t *testing.T) {
	t.Parallel()

	t.Run("bad name", func(t *testing.T) {
		t.Parallel()

		runner := &fakeRunner{handler: cannedPostgres(nil)}
		svc, _, _ := testDatabase(t, runner, nil)

		if _, err := svc.Verify(context.Background(), "../evil.dump"); !errors.Is(err, ErrInvalidInput) {
			t.Errorf("Verify() error = %v, want ErrInvalidInput", err)
		}
	})

	t.Run("missing file", func(t *testing.T) {
		t.Parallel()

		runner := &fakeRunner{handler: cannedPostgres(nil)}
		svc, _, _ := testDatabase(t, runner, nil)

		if _, err := svc.Verify(
			context.Background(), "gone-20260922T120000Z.dump",
		); !errors.Is(err, ErrInvalidInput) {
			t.Errorf("Verify() error = %v, want ErrInvalidInput", err)
		}
	})

	t.Run("unconfigured", func(t *testing.T) {
		t.Parallel()

		runner := &fakeRunner{handler: cannedPostgres(nil)}
		svc, _, dir := testDatabase(t, runner, func(cfg *DatabaseConfig) {
			cfg.Container = ""
		})

		seedBackup(t, dir, "any-20260922T120000Z.dump", []byte("x"), time.Now(), false)

		if _, err := svc.Verify(
			context.Background(), "any-20260922T120000Z.dump",
		); !errors.Is(err, ErrDatabaseUnconfigured) {
			t.Errorf("Verify() error = %v, want ErrDatabaseUnconfigured", err)
		}
	})
}

func TestJobsBound(t *testing.T) {
	t.Parallel()

	runner := &fakeRunner{handler: cannedPostgres(nil)}
	svc, db, _ := testDatabase(t, runner, nil)

	jobs := store.NewDBJobStore(db)

	for range 25 {
		if _, err := jobs.Create(context.Background(), model.DBJob{
			Kind: model.DBJobBackup, Target: "a.dump",
			Status: model.DBJobSuccess, StartedAt: time.Now(),
		}); err != nil {
			t.Fatalf("Create() error = %v, want nil", err)
		}
	}

	got, err := svc.Jobs(context.Background())
	if err != nil {
		t.Fatalf("Jobs() error = %v, want nil", err)
	}

	if len(got) != 20 {
		t.Fatalf("Jobs() = %d jobs, want 20", len(got))
	}

	for i := 1; i < len(got); i++ {
		if got[i-1].ID <= got[i].ID {
			t.Fatalf("Jobs() ids not newest first: %+v", got)
		}
	}
}

func TestReconcileFailsRunning(t *testing.T) {
	t.Parallel()

	runner := &fakeRunner{handler: cannedPostgres(nil)}
	svc, db, _ := testDatabase(t, runner, nil)

	jobs := store.NewDBJobStore(db)
	ctx := context.Background()

	id, err := jobs.Create(ctx, model.DBJob{
		Kind: model.DBJobBackup, Target: "a.dump",
		Status: model.DBJobRunning, StartedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("Create() error = %v, want nil", err)
	}

	if err := svc.Reconcile(ctx); err != nil {
		t.Fatalf("Reconcile() error = %v, want nil", err)
	}

	got, err := jobs.Get(ctx, id)
	if err != nil {
		t.Fatalf("Get() error = %v, want nil", err)
	}

	if got.Status != model.DBJobFailed {
		t.Errorf("Get() status = %q, want failed", got.Status)
	}

	if runner.count() != 0 {
		t.Errorf("docker calls = %d, want 0 (reconcile is local)", runner.count())
	}
}

func TestJobUnknown(t *testing.T) {
	t.Parallel()

	runner := &fakeRunner{handler: cannedPostgres(nil)}
	svc, _, _ := testDatabase(t, runner, nil)

	if _, err := svc.Job(context.Background(), 999); !errors.Is(err, store.ErrDBJobNotFound) {
		t.Errorf("Job() error = %v, want ErrDBJobNotFound", err)
	}
}
