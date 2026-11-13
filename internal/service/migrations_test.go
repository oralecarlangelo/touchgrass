package service

import (
	"bytes"
	"context"
	"errors"
	"io"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/oralecarlangelo/touchgrass/internal/dbmigrate"
	"github.com/oralecarlangelo/touchgrass/internal/docker"
	"github.com/oralecarlangelo/touchgrass/internal/model"
	"github.com/oralecarlangelo/touchgrass/internal/secret"
	"github.com/oralecarlangelo/touchgrass/internal/store"
	"github.com/oralecarlangelo/touchgrass/internal/testfixture"
)

const (
	// testMigrationPassword must never reach a response, an audit row,
	// a log line, or stored output.
	testMigrationPassword = "hunter2-Long-Passw0rd"
	testMigrationURL      = "ledger://app:" + testMigrationPassword + "@db.internal:5432/billing"
	testMigrationLabel    = "db.internal/billing"
	testMigrationAdmin    = "owner"
	testLedgerTool        = "ledger"
	testGrantedName       = "Billing worker"
	testMigrationsPath    = "/srv/app/changes"
	testManifestPath      = "/srv/app/ledger.json"
	testLedgerManifest    = "ledger.json"
	testFirstChange       = "001_init"
	testBrokenChange      = "002_broken"
	testWorkerService     = "worker"
	testFailureLog        = "boom"
)

// ledgerTool is a made-up migration tool. The pillar is exercised
// through it so that nothing here leans on Prisma: the shared code
// must work for any Tool.
type ledgerTool struct {
	name string

	mutex      sync.Mutex
	records    []dbmigrate.Record
	historyErr error
}

func (l *ledgerTool) Name() string { return l.name }

func (*ledgerTool) Layout() dbmigrate.Layout {
	return dbmigrate.Layout{MigrationsDir: "changes", Manifests: []string{testLedgerManifest}}
}

func (*ledgerTool) DefaultCommand() []string { return []string{testLedgerTool} }

func (*ledgerTool) Describe(databaseURL string) (dbmigrate.Target, error) {
	if !strings.HasPrefix(databaseURL, "ledger://") {
		return dbmigrate.Target{}, dbmigrate.ErrUnsupportedDatabase
	}

	return dbmigrate.Target{Label: testMigrationLabel}, nil
}

func (l *ledgerTool) History(_ context.Context, _ string) ([]dbmigrate.Record, error) {
	l.mutex.Lock()
	defer l.mutex.Unlock()

	return l.records, l.historyErr
}

func (*ledgerTool) Source(migrations, manifests dbmigrate.Files) (dbmigrate.Source, error) {
	source := dbmigrate.Source{SeedCommand: string(manifests[testLedgerManifest])}

	for name, content := range migrations {
		source.Migrations = append(source.Migrations, dbmigrate.Shipped{Name: name, Checksum: string(content)})
	}

	return source, nil
}

func (*ledgerTool) Command(action dbmigrate.Action, name string) ([]string, error) {
	switch action {
	case dbmigrate.ActionDeploy:
		return []string{"up"}, nil
	case dbmigrate.ActionSeed:
		return []string{"seed"}, nil
	case dbmigrate.ActionResolveApplied:
		return []string{"mark", "applied", name}, nil
	case dbmigrate.ActionResolveRolledBack:
		return []string{"mark", "undone", name}, nil
	default:
		return nil, dbmigrate.ErrUnsupportedAction
	}
}

// setHistory replaces what History returns.
func (l *ledgerTool) setHistory(records []dbmigrate.Record, err error) {
	l.mutex.Lock()
	defer l.mutex.Unlock()

	l.records, l.historyErr = records, err
}

// fakeEngine is the container engine the pillar reads and runs in.
type fakeEngine struct {
	containers []docker.Container
	files      map[string]map[string][]byte
	exitCode   int
	output     string
	execErr    error
	// hold, when set, blocks Exec until it is closed.
	hold chan struct{}

	mutex sync.Mutex
	execs []docker.ExecSpec
}

func (f *fakeEngine) List(_ context.Context) ([]docker.Container, error) {
	return f.containers, nil
}

func (*fakeEngine) Workdir(_ context.Context, _ string) (string, error) {
	return "/srv/app", nil
}

func (f *fakeEngine) ReadFiles(_ context.Context, _, containerPath string) (map[string][]byte, error) {
	files, ok := f.files[containerPath]
	if !ok {
		return nil, docker.ErrPathNotFound
	}

	return files, nil
}

func (f *fakeEngine) Exec(ctx context.Context, _ string, spec docker.ExecSpec, output io.Writer) (int, error) {
	f.mutex.Lock()
	f.execs = append(f.execs, spec)
	f.mutex.Unlock()

	if f.hold != nil {
		select {
		case <-f.hold:
		case <-ctx.Done():
			return 0, ctx.Err()
		}
	}

	if _, err := io.WriteString(output, f.output); err != nil {
		return 0, err
	}

	return f.exitCode, f.execErr
}

func (f *fakeEngine) executed() []docker.ExecSpec {
	f.mutex.Lock()
	defer f.mutex.Unlock()

	return slices.Clone(f.execs)
}

// migrationFixture is the pillar on the generic two-service fixture.
type migrationFixture struct {
	db         *store.DB
	migrations *Migrations
	tool       *ledgerTool
	engine     *fakeEngine
}

// workerContainer is the running container of the granted service.
func workerContainer() docker.Container {
	return docker.Container{
		ID: "c0ffee", Name: "billing-worker-1", Image: "billing/worker:42",
		Labels: map[string]string{docker.LabelComposeProject: "billing", docker.LabelComposeService: testWorkerService},
	}
}

// newMigrationFixture builds the pillar. withKey false leaves the env
// key out, the way a deployment without the env pillar runs.
func newMigrationFixture(t *testing.T, withKey bool) migrationFixture {
	t.Helper()

	_, db := openScopedUsers(t)
	services := store.NewServiceStore(db)

	var key *secret.Key

	if withKey {
		loaded, err := secret.New(bytes.Repeat([]byte{7}, secret.KeySize))
		if err != nil {
			t.Fatalf("secret.New() error = %v, want nil", err)
		}

		key = loaded
	}

	tool := &ledgerTool{name: testLedgerTool}
	engine := &fakeEngine{
		containers: []docker.Container{workerContainer()},
		files:      map[string]map[string][]byte{testMigrationsPath: {}},
	}

	migrations := NewMigrations(MigrationsConfig{
		Services: services,
		Configs:  store.NewMigrationConfigStore(db),
		Runs:     store.NewMigrationRunStore(db),
		Audit:    NewAudit(services, store.NewAuditStore(db)),
		Key:      key,
		Tools:    dbmigrate.NewRegistry(tool, &ledgerTool{name: "other-tool"}),
		Docker:   engine,
	})

	t.Cleanup(migrations.Close)

	return migrationFixture{db: db, migrations: migrations, tool: tool, engine: engine}
}

// configure saves a working setup for the granted service.
func (f migrationFixture) configure(t *testing.T) {
	t.Helper()

	if _, err := f.migrations.SaveConfig(context.Background(), testfixture.GenericGranted, model.MigrationConfigInput{
		Tool: testLedgerTool, DatabaseURL: testMigrationURL,
	}, testMigrationAdmin); err != nil {
		t.Fatalf("SaveConfig() error = %v, want nil", err)
	}
}

// awaitRun waits for a run to leave the running state.
func (f migrationFixture) awaitRun(t *testing.T, id int64) model.MigrationRun {
	t.Helper()

	deadline := time.Now().Add(5 * time.Second)

	for time.Now().Before(deadline) {
		run, err := f.migrations.Run(context.Background(), testfixture.GenericGranted, id)
		if err != nil {
			t.Fatalf("Run() error = %v, want nil", err)
		}

		if run.State != model.MigrationRunRunning {
			// The audit row is written just after the run is finished.
			f.awaitAudit(t, id)

			return run
		}

		time.Sleep(5 * time.Millisecond)
	}

	t.Fatalf("run %d did not finish", id)

	return model.MigrationRun{}
}

// awaitAudit waits for the audit row of a run.
func (f migrationFixture) awaitAudit(t *testing.T, id int64) {
	t.Helper()

	prefix := "run " + strconv.FormatInt(id, 10) + ":"
	deadline := time.Now().Add(5 * time.Second)

	for time.Now().Before(deadline) {
		for _, entry := range migrationAudit(t, f.db) {
			if strings.HasPrefix(entry.Detail, prefix) {
				return
			}
		}

		time.Sleep(5 * time.Millisecond)
	}

	t.Fatalf("no audit row for run %d", id)
}

// migrationAudit returns every audit entry oldest first, whatever its
// result (auditRows insists on success).
func migrationAudit(t *testing.T, db *store.DB) []model.Audit {
	t.Helper()

	entries, err := store.NewAuditStore(db).List(context.Background(), "", 100)
	if err != nil {
		t.Fatalf("List() error = %v, want nil", err)
	}

	slices.Reverse(entries)

	return entries
}

func ok(minutes int) *time.Time {
	moment := time.Date(2026, 11, 1, 10, minutes, 0, 0, time.UTC)

	return &moment
}

func TestMigrationStatusOfAnUnconfiguredService(t *testing.T) {
	t.Parallel()

	fixture := newMigrationFixture(t, true)

	status, err := fixture.migrations.Status(context.Background(), testfixture.GenericGranted)
	if err != nil {
		t.Fatalf("Status() error = %v, want nil", err)
	}

	if status.Configured || status.Verdict != model.MigrationVerdictUnknown || status.Migrations == nil {
		t.Errorf("Status() = %+v, want not configured, unknown, and an empty list", status)
	}

	if _, err := fixture.migrations.Status(context.Background(), "no-such-service"); !errors.Is(err, store.ErrServiceNotFound) {
		t.Errorf("Status() of an unknown service error = %v, want ErrServiceNotFound", err)
	}
}

func TestMigrationStatusComparesHistoryWithTheRunningBuild(t *testing.T) {
	t.Parallel()

	fixture := newMigrationFixture(t, true)
	fixture.configure(t)

	fixture.tool.setHistory([]dbmigrate.Record{
		{Name: testFirstChange, Checksum: "a", StartedAt: *ok(0), FinishedAt: ok(1)},
		{Name: "002_edit", Checksum: "b", StartedAt: *ok(2), FinishedAt: ok(3)},
	}, nil)
	fixture.engine.files[testMigrationsPath] = map[string][]byte{
		testFirstChange: []byte("a"), "002_edit": []byte("CHANGED"), "003_new": []byte("c"),
	}
	fixture.engine.files[testManifestPath] = map[string][]byte{testLedgerManifest: []byte("ledger seed --all")}

	status, err := fixture.migrations.Status(context.Background(), testfixture.GenericGranted)
	if err != nil {
		t.Fatalf("Status() error = %v, want nil", err)
	}

	wantCounts := model.MigrationCounts{Applied: 1, Modified: 1, Pending: 1}
	if status.Counts != wantCounts || status.Verdict != model.MigrationVerdictDrift {
		t.Errorf("Status() counts %+v verdict %q, want %+v and drift", status.Counts, status.Verdict, wantCounts)
	}

	if !status.History.OK || status.History.Target != testMigrationLabel {
		t.Errorf("Status() history = %+v, want ok against %s", status.History, testMigrationLabel)
	}

	if !status.Shipped.OK || status.Shipped.Target != "billing-worker-1:"+testMigrationsPath {
		t.Errorf("Status() shipped = %+v, want ok from the worker container", status.Shipped)
	}

	if !status.Seed.Configured || status.Seed.Command != "ledger seed --all" {
		t.Errorf("Status() seed = %+v, want the declared seed", status.Seed)
	}

	if status.Tool != testLedgerTool || !status.Configured {
		t.Errorf("Status() tool %q configured %t, want %s and true", status.Tool, status.Configured, testLedgerTool)
	}
}

// A side that cannot be read is part of the answer. The other side is
// still reported, and no credential leaks through the reason.
func TestMigrationStatusWhenTheDatabaseIsDown(t *testing.T) {
	t.Parallel()

	fixture := newMigrationFixture(t, true)
	fixture.configure(t)
	fixture.tool.setHistory(nil, errors.New("dial "+testMigrationURL+": refused; password "+testMigrationPassword))
	fixture.engine.files[testMigrationsPath] = map[string][]byte{testFirstChange: []byte("a")}

	status, err := fixture.migrations.Status(context.Background(), testfixture.GenericGranted)
	if err != nil {
		t.Fatalf("Status() error = %v, want nil", err)
	}

	if status.History.OK || status.Verdict != model.MigrationVerdictUnknown {
		t.Errorf("Status() history %+v verdict %q, want not ok and unknown", status.History, status.Verdict)
	}

	if strings.Contains(status.History.Error, testMigrationPassword) || !strings.Contains(status.History.Error, "refused") {
		t.Errorf("Status() history error = %q, want the reason without the password", status.History.Error)
	}

	if status.Counts.Pending != 1 {
		t.Errorf("Status() counts = %+v, want the shipped migration still listed as pending", status.Counts)
	}
}

// Without the build's files an applied migration is not called missing.
func TestMigrationStatusWithNoRunningContainer(t *testing.T) {
	t.Parallel()

	fixture := newMigrationFixture(t, true)
	fixture.configure(t)
	fixture.engine.containers = nil
	fixture.tool.setHistory([]dbmigrate.Record{{Name: testFirstChange, Checksum: "a", StartedAt: *ok(0), FinishedAt: ok(1)}}, nil)

	status, err := fixture.migrations.Status(context.Background(), testfixture.GenericGranted)
	if err != nil {
		t.Fatalf("Status() error = %v, want nil", err)
	}

	if status.Shipped.OK || !strings.Contains(status.Shipped.Error, testWorkerService) {
		t.Errorf("Status() shipped = %+v, want not ok naming the compose service", status.Shipped)
	}

	// Without the files an applied migration is not called missing.
	if status.Counts.Applied != 1 || status.Verdict != model.MigrationVerdictInSync {
		t.Errorf("Status() counts %+v verdict %q, want 1 applied and in_sync", status.Counts, status.Verdict)
	}
}

func TestMigrationStatusWithNoMigrationsDirectory(t *testing.T) {
	t.Parallel()

	fixture := newMigrationFixture(t, true)
	fixture.configure(t)
	delete(fixture.engine.files, testMigrationsPath)
	fixture.engine.files[testManifestPath] = map[string][]byte{testLedgerManifest: []byte("ledger seed")}

	status, err := fixture.migrations.Status(context.Background(), testfixture.GenericGranted)
	if err != nil {
		t.Fatalf("Status() error = %v, want nil", err)
	}

	if status.Shipped.OK || !status.Seed.Configured {
		t.Errorf("Status() shipped %+v seed %+v, want not ok and the seed still found", status.Shipped, status.Seed)
	}
}

func TestSaveMigrationConfigSealsTheDatabaseURL(t *testing.T) {
	t.Parallel()

	fixture := newMigrationFixture(t, true)
	ctx := context.Background()

	saved, err := fixture.migrations.SaveConfig(ctx, testfixture.GenericGranted, model.MigrationConfigInput{
		Tool: testLedgerTool, DatabaseURL: "  " + testMigrationURL + "\n", Container: testWorkerService,
		Workdir: "/srv/app", MigrationsDir: "db/changes", Command: []string{"bin/ledger", "--quiet"},
	}, testMigrationAdmin)
	if err != nil {
		t.Fatalf("SaveConfig() error = %v, want nil", err)
	}

	if saved.Database != testMigrationLabel || saved.UpdatedBy != testMigrationAdmin {
		t.Errorf("SaveConfig() = %+v, want the redacted target and the actor", saved)
	}

	row, err := store.NewMigrationConfigStore(fixture.db).Get(ctx, testfixture.GenericGranted)
	if err != nil {
		t.Fatalf("Get() error = %v, want nil", err)
	}

	if bytes.Contains(row.URLCiphertext, []byte(testMigrationPassword)) || bytes.Contains(row.URLCiphertext, []byte("ledger://")) {
		t.Error("the stored database url is not encrypted")
	}

	wantAuditRows(t, fixture.db, []auditRow{{
		service: testfixture.GenericGranted, actor: testMigrationAdmin, action: model.AuditMigrationConfig,
		detail: "configured ledger against " + testMigrationLabel,
	}})

	// The envelope is bound to its service: moved to another row it
	// must not open.
	row.ServiceID = testfixture.GenericOther
	if err := store.NewMigrationConfigStore(fixture.db).Put(ctx, row); err != nil {
		t.Fatalf("Put() error = %v, want nil", err)
	}

	if _, err := fixture.migrations.Status(ctx, testfixture.GenericOther); err == nil {
		t.Error("Status() opened a database url that was moved from another service")
	}
}

func TestSaveMigrationConfigKeepsTheURLWhenNoneIsSent(t *testing.T) {
	t.Parallel()

	fixture := newMigrationFixture(t, true)
	fixture.configure(t)

	ctx := context.Background()
	configs := store.NewMigrationConfigStore(fixture.db)

	before, err := configs.Get(ctx, testfixture.GenericGranted)
	if err != nil {
		t.Fatalf("Get() error = %v, want nil", err)
	}

	saved, err := fixture.migrations.SaveConfig(ctx, testfixture.GenericGranted, model.MigrationConfigInput{
		Tool: testLedgerTool, Container: testWorkerService,
	}, "second-admin")
	if err != nil {
		t.Fatalf("SaveConfig() without a url error = %v, want nil", err)
	}

	after, err := configs.Get(ctx, testfixture.GenericGranted)
	if err != nil {
		t.Fatalf("Get() error = %v, want nil", err)
	}

	if !bytes.Equal(before.URLCiphertext, after.URLCiphertext) || saved.Container != testWorkerService || saved.UpdatedBy != "second-admin" {
		t.Errorf("SaveConfig() = %+v, want the same sealed url with the new container", saved)
	}

	// A different tool reads a different database: the old URL must
	// not silently carry over to it.
	_, err = fixture.migrations.SaveConfig(ctx, testfixture.GenericGranted, model.MigrationConfigInput{Tool: "other-tool"}, testMigrationAdmin)
	if !errors.Is(err, ErrInvalidInput) {
		t.Errorf("SaveConfig() changing tool without a url error = %v, want ErrInvalidInput", err)
	}
}

func TestSaveMigrationConfigRejects(t *testing.T) {
	t.Parallel()

	valid := model.MigrationConfigInput{Tool: testLedgerTool, DatabaseURL: testMigrationURL}

	tests := []struct {
		name   string
		change func(*model.MigrationConfigInput)
	}{
		{name: "unknown tool", change: func(in *model.MigrationConfigInput) { in.Tool = "flyway" }},
		{name: "missing url on first save", change: func(in *model.MigrationConfigInput) { in.DatabaseURL = "" }},
		{name: "url the tool cannot read", change: func(in *model.MigrationConfigInput) {
			in.DatabaseURL = "mystery://app:" + testMigrationPassword + "@db/x"
		}},
		{name: "container with a space", change: func(in *model.MigrationConfigInput) { in.Container = "api; rm -rf /" }},
		{name: "relative workdir", change: func(in *model.MigrationConfigInput) { in.Workdir = "srv/app" }},
		{name: "absolute migrations dir", change: func(in *model.MigrationConfigInput) { in.MigrationsDir = "/etc" }},
		{name: "migrations dir escaping the workdir", change: func(in *model.MigrationConfigInput) { in.MigrationsDir = "../../etc" }},
		{name: "unclean migrations dir", change: func(in *model.MigrationConfigInput) { in.MigrationsDir = "a/../b" }},
		{name: "empty command argument", change: func(in *model.MigrationConfigInput) { in.Command = []string{"npx", ""} }},
		{name: "command argument with a newline", change: func(in *model.MigrationConfigInput) { in.Command = []string{"npx\nrm"} }},
		{name: "too many command arguments", change: func(in *model.MigrationConfigInput) {
			in.Command = slices.Repeat([]string{"x"}, migrationCommandMaxArgs+1)
		}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			fixture := newMigrationFixture(t, true)
			input := valid
			test.change(&input)

			_, err := fixture.migrations.SaveConfig(context.Background(), testfixture.GenericGranted, input, testMigrationAdmin)
			if !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("SaveConfig() error = %v, want ErrInvalidInput", err)
			}

			if strings.Contains(err.Error(), testMigrationPassword) {
				t.Errorf("SaveConfig() error %q holds the password", err)
			}

			if rows := auditRows(t, fixture.db); len(rows) != 0 {
				t.Errorf("a rejected config wrote audit rows: %+v", rows)
			}
		})
	}
}

func TestMigrationConfigNeedsTheEnvKey(t *testing.T) {
	t.Parallel()

	fixture := newMigrationFixture(t, false)

	_, err := fixture.migrations.SaveConfig(context.Background(), testfixture.GenericGranted, model.MigrationConfigInput{
		Tool: testLedgerTool, DatabaseURL: testMigrationURL,
	}, testMigrationAdmin)
	if !errors.Is(err, ErrMigrationsDisabled) {
		t.Errorf("SaveConfig() without a key error = %v, want ErrMigrationsDisabled", err)
	}

	status, err := fixture.migrations.Status(context.Background(), testfixture.GenericGranted)
	if err != nil || status.Configured {
		t.Errorf("Status() without a key = %+v, %v, want not configured", status, err)
	}
}

func TestDeleteMigrationConfig(t *testing.T) {
	t.Parallel()

	fixture := newMigrationFixture(t, true)
	fixture.configure(t)

	ctx := context.Background()

	for range 2 {
		if err := fixture.migrations.DeleteConfig(ctx, testfixture.GenericGranted, testMigrationAdmin); err != nil {
			t.Fatalf("DeleteConfig() error = %v, want nil", err)
		}
	}

	if _, found, err := fixture.migrations.Config(ctx, testfixture.GenericGranted); err != nil || found {
		t.Errorf("Config() after delete = found %t, %v, want not found", found, err)
	}

	// The second delete removed nothing and so audits nothing.
	wantAuditRows(t, fixture.db, []auditRow{
		{service: testfixture.GenericGranted, actor: testMigrationAdmin, action: model.AuditMigrationConfig, detail: "configured ledger against " + testMigrationLabel},
		{service: testfixture.GenericGranted, actor: testMigrationAdmin, action: model.AuditMigrationConfig, detail: "removed migration setup"},
	})
}

func TestRedactScrubsThePasswordOfAnyURLShape(t *testing.T) {
	t.Parallel()

	// The URLs are assembled so no literal reads as a credential.
	const (
		scheme   = "ledger://app:"
		scrubbed = "s3cretpw"
	)

	tests := []struct {
		name string
		url  string
		text string
		want string
	}{
		{
			name: "single host",
			url:  scheme + scrubbed + "@db:5432/shop",
			text: "auth failed for " + scrubbed,
			want: "auth failed for [redacted]",
		},
		{
			name: "several hosts with ports",
			url:  scheme + scrubbed + "@db1:27017,db2:27017/shop?replicaSet=rs0",
			text: "auth failed for " + scrubbed,
			want: "auth failed for [redacted]",
		},
		{
			name: "encoded password in both forms",
			url:  scheme + "p%40ss%2Fword@db/shop",
			text: "tried p@ss/word and p%40ss%2Fword",
			want: "tried [redacted] and [redacted]",
		},
		{
			name: "whole url",
			url:  scheme + scrubbed + "@db/shop",
			text: "cannot reach " + scheme + scrubbed + "@db/shop",
			want: "cannot reach [database url]",
		},
		{
			name: "no password",
			url:  "ledger://db/shop",
			text: "cannot reach db",
			want: "cannot reach db",
		},
		{
			name: "short password is left alone",
			url:  scheme + "abc@db/shop",
			text: "abc is not a word to blank",
			want: "abc is not a word to blank",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			if got := redact(test.text, []byte(test.url)); got != test.want {
				t.Errorf("redact() = %q, want %q", got, test.want)
			}
		})
	}
}
