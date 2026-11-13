package service

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/oralecarlangelo/touchgrass/internal/dbmigrate"
	"github.com/oralecarlangelo/touchgrass/internal/model"
	"github.com/oralecarlangelo/touchgrass/internal/store"
	"github.com/oralecarlangelo/touchgrass/internal/testfixture"
)

// deployCommand is a confirmed deploy of the granted service.
func deployCommand() MigrationCommand {
	return MigrationCommand{Kind: model.MigrationRunDeploy, Confirm: testGrantedName}
}

func TestMigrationDeployRunsTheToolInTheContainer(t *testing.T) {
	t.Parallel()

	fixture := newMigrationFixture(t, true)
	fixture.configure(t)
	fixture.engine.output = "2 changes applied\n"

	started, err := fixture.migrations.Start(context.Background(), testfixture.GenericGranted, deployCommand(), testMigrationAdmin)
	if err != nil {
		t.Fatalf("Start() error = %v, want nil", err)
	}

	if started.State != model.MigrationRunRunning || started.Container != "billing-worker-1" || started.Image != "billing/worker:42" {
		t.Errorf("Start() = %+v, want a running run naming the container and image", started)
	}

	run := fixture.awaitRun(t, started.ID)

	finished := run.State == model.MigrationRunSucceeded && run.ExitCode != nil && *run.ExitCode == 0 &&
		run.Output == "2 changes applied\n" && run.Detail == "ledger up" && run.Actor == testMigrationAdmin
	if !finished {
		t.Errorf("run = %+v, want succeeded with exit 0, its output and command line", run)
	}
}

func TestMigrationDeployIsExecutedAuditedAndListed(t *testing.T) {
	t.Parallel()

	fixture := newMigrationFixture(t, true)
	fixture.configure(t)

	started, err := fixture.migrations.Start(context.Background(), testfixture.GenericGranted, deployCommand(), testMigrationAdmin)
	if err != nil {
		t.Fatalf("Start() error = %v, want nil", err)
	}

	fixture.awaitRun(t, started.ID)

	execs := fixture.engine.executed()
	if len(execs) != 1 || !slices.Equal(execs[0].Cmd, []string{testLedgerTool, "up"}) || execs[0].WorkingDir != "/srv/app" {
		t.Errorf("executed = %+v, want one `ledger up` in /srv/app", execs)
	}

	rows := auditRows(t, fixture.db)
	last := rows[len(rows)-1]

	wantDetail := "run 1: ledger up in billing-worker-1 (exit 0)"
	if last.action != model.AuditMigrationDeploy || last.actor != testMigrationAdmin || last.detail != wantDetail {
		t.Errorf("audit = %+v, want migration_deploy by %s: %q", last, testMigrationAdmin, wantDetail)
	}

	listed, err := fixture.migrations.Runs(context.Background(), testfixture.GenericGranted)
	if err != nil || len(listed) != 1 || listed[0].Output != "" {
		t.Errorf("Runs() = %+v, %v, want the run listed without output", listed, err)
	}
}

func TestMigrationCommandFailureIsRecordedAndScrubbed(t *testing.T) {
	t.Parallel()

	fixture := newMigrationFixture(t, true)
	fixture.configure(t)
	fixture.engine.exitCode = 1
	fixture.engine.output = "error connecting to " + testMigrationURL + "\nauth failed for " + testMigrationPassword + "\n"

	started, err := fixture.migrations.Start(context.Background(), testfixture.GenericGranted, deployCommand(), testMigrationAdmin)
	if err != nil {
		t.Fatalf("Start() error = %v, want nil", err)
	}

	run := fixture.awaitRun(t, started.ID)

	if run.State != model.MigrationRunFailed || run.ExitCode == nil || *run.ExitCode != 1 {
		t.Errorf("run = %+v, want failed with exit 1", run)
	}

	if strings.Contains(run.Output, testMigrationPassword) || !strings.Contains(run.Output, "auth failed for [redacted]") {
		t.Errorf("run output = %q, want the password redacted and the rest kept", run.Output)
	}

	entries := migrationAudit(t, fixture.db)
	last := entries[len(entries)-1]

	failed := last.Action == model.AuditMigrationDeploy && last.Result == model.AuditFailure &&
		strings.Contains(last.Detail, "(exit 1)")
	if !failed {
		t.Errorf("audit = %+v, want a failed migration_deploy row with exit 1", last)
	}
}

func TestMigrationCommandThatCannotRunIsFailed(t *testing.T) {
	t.Parallel()

	fixture := newMigrationFixture(t, true)
	fixture.configure(t)
	fixture.engine.execErr = errors.New("exec: executable file not found")

	started, err := fixture.migrations.Start(context.Background(), testfixture.GenericGranted, deployCommand(), testMigrationAdmin)
	if err != nil {
		t.Fatalf("Start() error = %v, want nil", err)
	}

	run := fixture.awaitRun(t, started.ID)

	if run.State != model.MigrationRunFailed || run.ExitCode != nil || !strings.Contains(run.Output, "executable file not found") {
		t.Errorf("run = %+v, want failed with no exit code and the reason in its output", run)
	}
}

func TestMigrationCommandUsesTheStoredPrefixAndWorkdir(t *testing.T) {
	t.Parallel()

	fixture := newMigrationFixture(t, true)

	if _, err := fixture.migrations.SaveConfig(context.Background(), testfixture.GenericGranted, model.MigrationConfigInput{
		Tool: testLedgerTool, DatabaseURL: testMigrationURL, Workdir: "/opt/tools",
		Command: []string{"pnpm", "exec", testLedgerTool},
	}, testMigrationAdmin); err != nil {
		t.Fatalf("SaveConfig() error = %v, want nil", err)
	}

	started, err := fixture.migrations.Start(context.Background(), testfixture.GenericGranted, deployCommand(), testMigrationAdmin)
	if err != nil {
		t.Fatalf("Start() error = %v, want nil", err)
	}

	fixture.awaitRun(t, started.ID)

	execs := fixture.engine.executed()
	if len(execs) != 1 || !slices.Equal(execs[0].Cmd, []string{"pnpm", "exec", testLedgerTool, "up"}) || execs[0].WorkingDir != "/opt/tools" {
		t.Errorf("executed = %+v, want `pnpm exec ledger up` in /opt/tools", execs)
	}
}

// A refused command must run nothing, create no run, and audit nothing.
func TestMigrationStartRefuses(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		prepare func(migrationFixture)
		command MigrationCommand
		want    error
	}{
		{
			name:    "confirmation is not the service name",
			command: MigrationCommand{Kind: model.MigrationRunDeploy, Confirm: testfixture.GenericGranted},
			want:    ErrMigrationConfirmation,
		},
		{name: "no confirmation", command: MigrationCommand{Kind: model.MigrationRunSeed}, want: ErrMigrationConfirmation},
		{
			name:    "unknown kind",
			command: MigrationCommand{Kind: "reset", Confirm: testGrantedName},
			want:    ErrInvalidInput,
		},
		{
			name:    "resolve without a resolution",
			command: MigrationCommand{Kind: model.MigrationRunResolve, Migration: testBrokenChange, Confirm: testGrantedName},
			want:    ErrInvalidInput,
		},
		{
			name: "resolve of a migration that did not fail",
			prepare: func(f migrationFixture) {
				f.tool.setHistory([]dbmigrate.Record{{Name: testFirstChange, Checksum: "a", StartedAt: *ok(0), FinishedAt: ok(1)}}, nil)
			},
			command: MigrationCommand{
				Kind: model.MigrationRunResolve, Migration: testFirstChange,
				Resolution: model.MigrationResolveRolledBack, Confirm: testGrantedName,
			},
			want: ErrInvalidInput,
		},
		{
			name:    "resolve while the history is unreadable",
			prepare: func(f migrationFixture) { f.tool.setHistory(nil, errors.New("database is down")) },
			command: MigrationCommand{
				Kind: model.MigrationRunResolve, Migration: testBrokenChange,
				Resolution: model.MigrationResolveApplied, Confirm: testGrantedName,
			},
			want: ErrConflict,
		},
		{
			name:    "seed when the build declares none",
			command: MigrationCommand{Kind: model.MigrationRunSeed, Confirm: testGrantedName},
			want:    ErrInvalidInput,
		},
		{
			name:    "no running container",
			prepare: func(f migrationFixture) { f.engine.containers = nil },
			command: deployCommand(),
			want:    ErrMigrationContainer,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			fixture := newMigrationFixture(t, true)
			fixture.configure(t)

			if test.prepare != nil {
				test.prepare(fixture)
			}

			_, err := fixture.migrations.Start(context.Background(), testfixture.GenericGranted, test.command, testMigrationAdmin)
			if !errors.Is(err, test.want) {
				t.Fatalf("Start() error = %v, want %v", err, test.want)
			}

			if execs := fixture.engine.executed(); len(execs) != 0 {
				t.Errorf("a refused command executed %+v", execs)
			}

			runs, err := fixture.migrations.Runs(context.Background(), testfixture.GenericGranted)
			if err != nil || len(runs) != 0 {
				t.Errorf("Runs() = %+v, %v, want none", runs, err)
			}

			if rows := migrationAudit(t, fixture.db); len(rows) != 1 {
				t.Errorf("audit rows = %+v, want only the setup row", rows)
			}
		})
	}
}

func TestMigrationStartNeedsASetup(t *testing.T) {
	t.Parallel()

	fixture := newMigrationFixture(t, true)

	_, err := fixture.migrations.Start(context.Background(), testfixture.GenericGranted, deployCommand(), testMigrationAdmin)
	if !errors.Is(err, ErrMigrationsNotConfigured) {
		t.Errorf("Start() error = %v, want ErrMigrationsNotConfigured", err)
	}
}

func TestMigrationResolveAndSeedRunWhenTheStateAllowsThem(t *testing.T) {
	t.Parallel()

	fixture := newMigrationFixture(t, true)
	fixture.configure(t)
	fixture.tool.setHistory([]dbmigrate.Record{{Name: testBrokenChange, Checksum: "b", StartedAt: *ok(0), Logs: testFailureLog}}, nil)
	fixture.engine.files[testManifestPath] = map[string][]byte{testLedgerManifest: []byte("ledger seed")}

	ctx := context.Background()

	resolve, err := fixture.migrations.Start(ctx, testfixture.GenericGranted, MigrationCommand{
		Kind: model.MigrationRunResolve, Migration: testBrokenChange,
		Resolution: model.MigrationResolveRolledBack, Confirm: testGrantedName,
	}, testMigrationAdmin)
	if err != nil {
		t.Fatalf("Start(resolve) error = %v, want nil", err)
	}

	fixture.awaitRun(t, resolve.ID)

	seed, err := fixture.migrations.Start(ctx, testfixture.GenericGranted,
		MigrationCommand{Kind: model.MigrationRunSeed, Confirm: testGrantedName}, "second-admin")
	if err != nil {
		t.Fatalf("Start(seed) error = %v, want nil", err)
	}

	fixture.awaitRun(t, seed.ID)

	execs := fixture.engine.executed()
	wantResolve := []string{testLedgerTool, "mark", "undone", testBrokenChange}

	if len(execs) != 2 || !slices.Equal(execs[0].Cmd, wantResolve) || !slices.Equal(execs[1].Cmd, []string{testLedgerTool, "seed"}) {
		t.Errorf("executed = %+v, want the resolve then the seed", execs)
	}

	rows := auditRows(t, fixture.db)
	actions := []string{rows[len(rows)-2].action, rows[len(rows)-1].action}

	if !slices.Equal(actions, []string{model.AuditMigrationResolve, model.AuditSeedRun}) || rows[len(rows)-1].actor != "second-admin" {
		t.Errorf("audit tail = %+v, want migration_resolve then seed_run by second-admin", rows[len(rows)-2:])
	}

	// The seed history is the list of seed runs.
	runs, err := fixture.migrations.Runs(ctx, testfixture.GenericGranted)
	if err != nil || len(runs) != 2 || runs[0].Kind != model.MigrationRunSeed || runs[0].Actor != "second-admin" {
		t.Errorf("Runs() = %+v, %v, want the seed run newest", runs, err)
	}
}

func TestOnlyOneMigrationCommandRunsPerService(t *testing.T) {
	t.Parallel()

	fixture := newMigrationFixture(t, true)
	fixture.configure(t)
	fixture.engine.hold = make(chan struct{})

	ctx := context.Background()

	first, err := fixture.migrations.Start(ctx, testfixture.GenericGranted, deployCommand(), testMigrationAdmin)
	if err != nil {
		t.Fatalf("Start() error = %v, want nil", err)
	}

	if _, err := fixture.migrations.Start(ctx, testfixture.GenericGranted, deployCommand(), testMigrationAdmin); !errors.Is(err, ErrMigrationBusy) {
		t.Errorf("second Start() error = %v, want ErrMigrationBusy", err)
	}

	close(fixture.engine.hold)
	fixture.awaitRun(t, first.ID)

	fixture.engine.hold = nil

	again, err := fixture.migrations.Start(ctx, testfixture.GenericGranted, deployCommand(), testMigrationAdmin)
	if err != nil {
		t.Fatalf("Start() after the first finished error = %v, want nil", err)
	}

	fixture.awaitRun(t, again.ID)
}

// Close cancels a command in flight; it is recorded as failed, not
// left running forever.
func TestCloseFailsARunningMigrationCommand(t *testing.T) {
	t.Parallel()

	fixture := newMigrationFixture(t, true)
	fixture.configure(t)
	fixture.engine.hold = make(chan struct{})

	started, err := fixture.migrations.Start(context.Background(), testfixture.GenericGranted, deployCommand(), testMigrationAdmin)
	if err != nil {
		t.Fatalf("Start() error = %v, want nil", err)
	}

	fixture.migrations.Close()

	run, err := fixture.migrations.Run(context.Background(), testfixture.GenericGranted, started.ID)
	if err != nil || run.State != model.MigrationRunFailed || run.FinishedAt == nil {
		t.Errorf("run after Close() = %+v, %v, want failed and finished", run, err)
	}
}

func TestRecoverFailsRunsLeftByARestart(t *testing.T) {
	t.Parallel()

	fixture := newMigrationFixture(t, true)
	ctx := context.Background()

	id, err := store.NewMigrationRunStore(fixture.db).Create(ctx, model.MigrationRun{
		ServiceID: testfixture.GenericGranted, Kind: model.MigrationRunSeed, Tool: testLedgerTool,
		Detail: "ledger seed", Actor: testMigrationAdmin, StartedAt: *ok(0),
	})
	if err != nil {
		t.Fatalf("Create() error = %v, want nil", err)
	}

	for range 2 {
		if err := fixture.migrations.Recover(ctx); err != nil {
			t.Fatalf("Recover() error = %v, want nil", err)
		}
	}

	run, err := fixture.migrations.Run(ctx, testfixture.GenericGranted, id)
	if err != nil || run.State != model.MigrationRunFailed || !strings.Contains(run.Output, "outcome is unknown") {
		t.Errorf("run after Recover() = %+v, %v, want failed with the interrupted note", run, err)
	}

	// Recovering twice audits once: the second pass finds nothing running.
	entries := migrationAudit(t, fixture.db)
	wantDetail := "run 1: ledger seed interrupted by a restart; outcome unknown"

	if len(entries) != 1 {
		t.Fatalf("audit entries = %+v, want exactly one", entries)
	}

	recorded := entries[0].Action == model.AuditSeedRun && entries[0].Result == model.AuditFailure &&
		entries[0].Actor == testMigrationAdmin && entries[0].Detail == wantDetail
	if !recorded {
		t.Errorf("audit = %+v, want a failed seed_run by %s: %q", entries[0], testMigrationAdmin, wantDetail)
	}
}

func TestMigrationRunOfAnotherServiceIsNotFound(t *testing.T) {
	t.Parallel()

	fixture := newMigrationFixture(t, true)
	fixture.configure(t)

	started, err := fixture.migrations.Start(context.Background(), testfixture.GenericGranted, deployCommand(), testMigrationAdmin)
	if err != nil {
		t.Fatalf("Start() error = %v, want nil", err)
	}

	fixture.awaitRun(t, started.ID)

	if _, err := fixture.migrations.Run(context.Background(), testfixture.GenericOther, started.ID); !errors.Is(err, store.ErrMigrationRunNotFound) {
		t.Errorf("Run() under another service error = %v, want ErrMigrationRunNotFound", err)
	}
}

func TestTailBufferKeepsTheEndOfTheOutput(t *testing.T) {
	t.Parallel()

	buffer := newTailBuffer(8)

	for _, chunk := range []string{"0123", "4567"} {
		if _, err := buffer.Write([]byte(chunk)); err != nil {
			t.Fatalf("Write() error = %v, want nil", err)
		}
	}

	if text, truncated := buffer.Text(); text != "01234567" || truncated {
		t.Errorf("Text() = %q, %t, want the whole output untruncated", text, truncated)
	}

	for range 10 {
		if _, err := buffer.Write([]byte("abcdefghij")); err != nil {
			t.Fatalf("Write() error = %v, want nil", err)
		}
	}

	if text, truncated := buffer.Text(); text != "cdefghij" || !truncated {
		t.Errorf("Text() = %q, %t, want the last 8 bytes, truncated", text, truncated)
	}
}

func TestMigrationCandidatesFollowTheDeployStrategy(t *testing.T) {
	t.Parallel()

	generic := testfixture.GenericServices()[0]

	if got, err := migrationCandidates(generic, ""); err != nil || !slices.Equal(got, []string{testWorkerService}) {
		t.Errorf("recreate candidates = %v, %v, want [worker]", got, err)
	}

	if got, err := migrationCandidates(generic, "migrator"); err != nil || !slices.Equal(got, []string{"migrator"}) {
		t.Errorf("configured candidates = %v, %v, want [migrator]", got, err)
	}

	blueGreen := model.Service{
		ID: "storefront", Strategy: model.StrategyBlueGreen,
		Config: []byte(`{"blue_service":"web-blue","green_service":"web-green","nginx_conf":"/nonexistent","marker":"# x"}`),
	}

	// With an unreadable proxy config both colors are tried, blue first.
	if got, err := migrationCandidates(blueGreen, ""); err != nil || !slices.Equal(got, []string{"web-blue", "web-green"}) {
		t.Errorf("blue-green candidates = %v, %v, want [web-blue web-green]", got, err)
	}

	if _, err := migrationCandidates(model.Service{ID: "x", Strategy: model.StrategyUnknown}, ""); err == nil {
		t.Error("unknown strategy candidates error = nil, want an error")
	}
}
