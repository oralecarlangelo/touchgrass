package service

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/oralecarlangelo/touchgrass/internal/model"
	"github.com/oralecarlangelo/touchgrass/internal/store"
)

// successSteps is the step order of a clean apply.
var successSteps = []string{"render", "validate", "write", "restart", "probe", "done"}

func TestEnvApplySuccess(t *testing.T) {
	t.Parallel()

	fixture := newEnvFixture(t, healthyProber())
	fixture.writeTarget(t, testEnvOldFile)
	fixture.stage(t, "ZED", "last", false)
	fixture.stage(t, "ALPHA", "first=with=equals", true)

	before := fixture.diff(t)
	run := fixture.apply(t, "initial keys")

	requireEqual(t, "run state", run.State, model.EnvRunSucceeded)
	requireEqual(t, "run has revision", run.RevisionID != nil, true)
	requireEqual(t, "run finished", run.FinishedAt != nil, true)
	requireEqual(t, "steps", strings.Join(stepNames(run), ","), strings.Join(successSteps, ","))
	requireEqual(t, "target", fixture.readTarget(t, ""), envHeader+"ALPHA=first=with=equals\nZED=last\n")
	requireEqual(t, "backup", fixture.readTarget(t, backupSuffix), testEnvOldFile)
	requireEqual(t, "target dir entries (no temp left)", fixture.targetDirCount(t), 2)

	info, err := os.Stat(fixture.target)
	requireNoError(t, err, "Stat(target)")
	requireEqual(t, "target mode", info.Mode().Perm(), os.FileMode(0o600))

	view := fixture.view(t)
	requireEqual(t, "pending", view.PendingCount, 0)
	requireEqual(t, "applied summary", view.AppliedRevision.Summary, "initial keys")
	requireEqual(t, "validation ok", view.LastValidation.OK, true)
	requireEqual(t, "apply in progress", view.ApplyInProgress, false)

	revisions, err := fixture.env.Revisions(context.Background(), testEnvServiceID)
	requireNoError(t, err, "Revisions()")
	requireEqual(t, "revision count", len(revisions), 1)
	requireEqual(t, "revision applied", revisions[0].Applied, true)
	requireEqual(t, "revision sha", revisions[0].RenderedSHA256, before.RenderedSHA256)
	requireEqual(t, "revision added", strings.Join(revisions[0].Added, ","), "ALPHA,ZED")

	checkApplyAudit(t, fixture, model.AuditEnvApply, "initial keys")
	checkEnvEvents(t, fixture, run.ID, successSteps)
	checkValidatorEnv(t, fixture)
}

// checkApplyAudit asserts the newest audit row is action naming keys only.
func checkApplyAudit(t *testing.T, fixture *envFixture, action, wantDetail string) {
	t.Helper()

	entries := fixture.auditActions(t)
	if len(entries) == 0 {
		t.Fatal("no audit rows")
	}

	newest := entries[0]
	requireEqual(t, "audit action", newest.Action, action)
	requireEqual(t, "audit names ALPHA", strings.Contains(newest.Detail, "ALPHA"), true)
	requireEqual(t, "audit carries summary", strings.Contains(newest.Detail, wantDetail), true)
	requireEqual(t, "audit leaks value", strings.Contains(newest.Detail, "first=with"), false)
}

// checkEnvEvents asserts env.apply SSE events carry run id and steps.
func checkEnvEvents(t *testing.T, fixture *envFixture, runID string, wantSteps []string) {
	t.Helper()

	seen := []string{}

	for _, event := range fixture.events.all() {
		requireEqual(t, "event type", event.Type, model.EventEnvApply)
		requireEqual(t, "event run id", event.RunID, runID)
		requireEqual(t, "event service", event.ServiceID, testEnvServiceID)
		requireEqual(t, "event has time", event.At.IsZero(), false)

		if len(seen) == 0 || seen[len(seen)-1] != event.Step {
			seen = append(seen, event.Step)
		}
	}

	requireEqual(t, "event steps", strings.Join(seen, ","), strings.Join(wantSteps, ","))
}

// checkValidatorEnv asserts the validator saw the candidate path as the
// trailing argument and through the environment.
func checkValidatorEnv(t *testing.T, fixture *envFixture) {
	t.Helper()

	fixture.host.mutex.Lock()
	defer fixture.host.mutex.Unlock()

	for i, command := range fixture.host.commands {
		if !strings.HasPrefix(command, "validate") {
			continue
		}

		wantPrefix := testEnvValidate + " " + filepath.Dir(fixture.target)
		requireEqual(t, "validate candidate arg", strings.HasPrefix(command, wantPrefix), true)

		joined := strings.Join(fixture.host.envs[i], " ")
		requireEqual(t, "validate env candidate", strings.Contains(joined, "TOUCHGRASS_ENV_CANDIDATE="), true)
		requireEqual(t, "validate env target", strings.Contains(joined, "TOUCHGRASS_ENV_TARGET="+fixture.target), true)
	}
}

func TestEnvRenderDeterministic(t *testing.T) {
	t.Parallel()

	fixture := newEnvFixture(t, healthyProber())
	fixture.stage(t, "B", "2", false)
	fixture.stage(t, "A", "1", true)

	first := fixture.diff(t)
	second := fixture.diff(t)

	requireEqual(t, "sha non-empty", first.RenderedSHA256 != "", true)
	requireEqual(t, "sha stable", first.RenderedSHA256, second.RenderedSHA256)
	requireEqual(t, "added sorted", strings.Join(first.Added, ","), "A,B")
}

func TestEnvApplyValidationFailure(t *testing.T) {
	t.Parallel()

	fixture := newEnvFixture(t, healthyProber())
	fixture.writeTarget(t, testEnvOldFile)
	fixture.host.validate = func(emit func(string)) error {
		emit(testEnvInvalidKey)
		emit("INVALID env-svc:API_KEY: empty")

		return errors.New("exit status 1")
	}

	fixture.stage(t, testEnvAPIKey, "", true)
	fixture.stage(t, "DB_URL", "x", false)
	fixture.stage(t, "PORT", "1", false)

	run := fixture.apply(t, "bad")
	last := run.Steps[len(run.Steps)-1]

	requireEqual(t, "run state", run.State, model.EnvRunFailed)
	requireEqual(t, "run revision", run.RevisionID == nil, true)
	requireEqual(t, "steps", strings.Join(stepNames(run), ","), "render,validate,failed")
	requireEqual(t, "run error", strings.Contains(run.Error, "env_validation_failed"), true)
	requireEqual(t, "run validation lines", len(run.ValidationLines), 2)
	requireEqual(t, "run restored", run.Restored, false)
	requireEqual(t, "failure names validation", strings.Contains(last.Message, "env_validation_failed"), true)
	requireEqual(t, "target untouched", fixture.readTarget(t, ""), testEnvOldFile)
	requireEqual(t, "target dir entries (temp removed)", fixture.targetDirCount(t), 1)
	requireEqual(t, "restart runs", fixture.host.count("restart"), 0)

	view := fixture.view(t)
	requireEqual(t, "validation ok", view.LastValidation.OK, false)
	requireEqual(t, "validation lines", len(view.LastValidation.Lines), 2)
	requireEqual(t, "pending still staged", view.PendingCount, 3)
	requireEqual(t, "DB_URL required", entryByKey(t, view, "DB_URL").Required, true)
	requireEqual(t, "API_KEY required", entryByKey(t, view, testEnvAPIKey).Required, true)
	requireEqual(t, "PORT required", entryByKey(t, view, "PORT").Required, false)
	requireEqual(t, "newest audit", fixture.auditActions(t)[0].Action, model.AuditEnvApplyFailed)
}

func TestEnvApplyRestartFailureRestoresBackup(t *testing.T) {
	t.Parallel()

	fixture := newEnvFixture(t, healthyProber())
	fixture.writeTarget(t, testEnvOldFile)

	calls := 0
	fixture.host.restart = func(context.Context) error {
		calls++
		if calls == 1 {
			return errors.New("systemctl failed")
		}

		return nil
	}

	fixture.stage(t, "NEW", "v", false)

	run := fixture.apply(t, "")
	messages := []string{}

	for _, step := range run.Steps {
		messages = append(messages, step.Message)
	}

	requireEqual(t, "run state", run.State, model.EnvRunFailed)
	requireEqual(t, "run restored flag", run.Restored, true)
	requireEqual(t, "run error", run.Error, "restart: restart command failed")
	requireEqual(t, "target restored", fixture.readTarget(t, ""), testEnvOldFile)
	requireEqual(t, "restart runs (fail + rerun)", fixture.host.count("restart"), 2)
	requireEqual(t, "restore note", slices.Contains(messages, "restored previous file"), true)
	requireEqual(t, "rerun note", slices.Contains(messages, "restarted on previous file"), true)

	view := fixture.view(t)
	requireEqual(t, "pending kept", view.PendingCount, 1)
	requireEqual(t, "no revision", view.AppliedRevision == nil, true)
}

func TestEnvApplyProbeTimeoutRestores(t *testing.T) {
	t.Parallel()

	fixture := newEnvFixture(t, stubProber{healthy: map[string]bool{}})
	fixture.stage(t, "NEW", "v", false)

	run := fixture.apply(t, "")
	last := run.Steps[len(run.Steps)-1]

	requireEqual(t, "run state", run.State, model.EnvRunFailed)
	requireEqual(t, "failure names probe", strings.Contains(last.Message, "probe"), true)
	requireEqual(t, "target removed (no previous file)", fixture.readTarget(t, ""), "")
	requireEqual(t, "restart runs", fixture.host.count("restart"), 2)
}

func TestEnvApplyGuards(t *testing.T) {
	t.Parallel()

	fixture := newEnvFixture(t, healthyProber())
	ctx := context.Background()

	_, err := fixture.env.Apply(ctx, testEnvServiceID, testEnvServiceID, "", testEnvActor)
	requireIs(t, "Apply(no pending)", err, ErrEnvNothingToApply)

	fixture.stage(t, "A", "1", false)

	_, err = fixture.env.Apply(ctx, testEnvServiceID, "wrong", "", testEnvActor)
	requireIs(t, "Apply(wrong confirm)", err, ErrEnvConfirmationMismatch)

	_, err = fixture.env.Apply(ctx, "tn-api", "tn-api", "", testEnvActor)
	requireIs(t, "Apply(unconfigured)", err, ErrEnvNotConfigured)
}

func TestEnvApplyInProgressLock(t *testing.T) {
	t.Parallel()

	fixture := newEnvFixture(t, healthyProber())
	ctx := context.Background()

	fixture.stage(t, "A", "1", false)

	release := make(chan struct{})
	fixture.host.restart = func(ctx context.Context) error {
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	runID, err := fixture.env.Apply(ctx, testEnvServiceID, testEnvServiceID, "held", testEnvActor)
	requireNoError(t, err, "Apply()")
	requireEqual(t, "Active() during run", fixture.env.Active(testEnvServiceID), true)

	_, err = fixture.env.Apply(ctx, testEnvServiceID, testEnvServiceID, "", testEnvActor)
	requireIs(t, "concurrent Apply()", err, ErrEnvApplyInProgress)

	_, err = fixture.env.Rollback(ctx, testEnvServiceID, 1, testEnvServiceID, testEnvActor)
	requireIs(t, "Rollback() during run", err, ErrEnvApplyInProgress)
	held := fixture.view(t)
	requireEqual(t, "View() in progress", held.ApplyInProgress, true)
	requireEqual(t, "View() active run id", held.ActiveRunID, runID)

	running, err := fixture.env.Run(ctx, testEnvServiceID, runID)
	requireNoError(t, err, "Run()")
	requireEqual(t, "run state while held", running.State, model.EnvRunRunning)

	close(release)
	fixture.await(t, runID)
	requireEqual(t, "Active() after run", fixture.env.Active(testEnvServiceID), false)

	_, err = fixture.env.Run(ctx, testEnvServiceID, "nope")
	requireIs(t, "Run(unknown)", err, store.ErrEnvRunNotFound)
}

func TestEnvRollbackStagesSnapshot(t *testing.T) {
	t.Parallel()

	fixture := newEnvFixture(t, healthyProber())
	ctx := context.Background()

	fixture.stage(t, "KEEP", "same", false)
	fixture.stage(t, "EDIT", "v1", false)
	fixture.stage(t, "GONE", "bye", true)
	first := fixture.apply(t, "rev one")

	fixture.stage(t, "EDIT", "v2", false)
	fixture.stage(t, "NEW", "n", false)
	fixture.remove(t, "GONE")
	fixture.apply(t, "rev two")
	requireEqual(t, "target after rev two", fixture.readTarget(t, ""), envHeader+"EDIT=v2\nKEEP=same\nNEW=n\n")

	_, err := fixture.env.Rollback(ctx, testEnvServiceID, *first.RevisionID, "wrong", testEnvActor)
	requireIs(t, "Rollback(wrong confirm)", err, ErrEnvConfirmationMismatch)

	_, err = fixture.env.Rollback(ctx, testEnvServiceID, 999, testEnvServiceID, testEnvActor)
	requireIs(t, "Rollback(unknown)", err, store.ErrEnvRevisionNotFound)

	runID, err := fixture.env.Rollback(ctx, testEnvServiceID, *first.RevisionID, testEnvServiceID, testEnvActor)
	requireNoError(t, err, "Rollback()")

	run := fixture.await(t, runID)
	requireEqual(t, "rollback run state", run.State, model.EnvRunSucceeded)
	requireEqual(t, "target after rollback", fixture.readTarget(t, ""), envHeader+"EDIT=v1\nGONE=bye\nKEEP=same\n")

	checkRollbackRevisions(t, fixture)

	actions := []string{}
	for _, entry := range fixture.auditActions(t) {
		actions = append(actions, entry.Action)
	}

	requireEqual(t, "audit has env.rollback", slices.Contains(actions, model.AuditEnvRollback), true)
}

// checkRollbackRevisions asserts the three-revision history after a rollback.
func checkRollbackRevisions(t *testing.T, fixture *envFixture) {
	t.Helper()

	revisions, err := fixture.env.Revisions(context.Background(), testEnvServiceID)
	requireNoError(t, err, "Revisions()")
	requireEqual(t, "revision count", len(revisions), 3)
	requireEqual(t, "newest applied", revisions[0].Applied, true)
	requireEqual(t, "middle applied", revisions[1].Applied, false)
	requireEqual(t, "oldest applied", revisions[2].Applied, false)

	newest := revisions[0]
	requireEqual(t, "rollback added", strings.Join(newest.Added, ","), "GONE")
	requireEqual(t, "rollback changed", strings.Join(newest.Changed, ","), "EDIT")
	requireEqual(t, "rollback removed", strings.Join(newest.Removed, ","), "NEW")
	requireEqual(t, "rollback summary", strings.HasPrefix(newest.Summary, "rollback to revision"), true)
	requireEqual(t, "rollback sha matches original", newest.RenderedSHA256, revisions[2].RenderedSHA256)
	requireEqual(t, "middle removed", strings.Join(revisions[1].Removed, ","), "GONE")
	requireEqual(t, "middle added", strings.Join(revisions[1].Added, ","), "NEW")

	_, err = fixture.env.Rollback(context.Background(), testEnvServiceID, newest.ID, testEnvServiceID, testEnvActor)
	requireIs(t, "Rollback(current)", err, ErrEnvNothingToApply)
}

func TestEnvReconcileFailsOrphans(t *testing.T) {
	t.Parallel()

	fixture := newEnvFixture(t, healthyProber())
	ctx := context.Background()
	envStore := store.NewEnvStore(fixture.db)

	requireNoError(t, envStore.InsertRun(ctx, model.EnvRun{
		ID: "orphan", ServiceID: testEnvServiceID, State: model.EnvRunRunning, StartedAt: time.Now(),
	}), "InsertRun()")
	requireNoError(t, fixture.env.Reconcile(ctx), "Reconcile()")

	run, err := fixture.env.Run(ctx, testEnvServiceID, "orphan")
	requireNoError(t, err, "Run(orphan)")
	requireEqual(t, "orphan state", run.State, model.EnvRunFailed)
}

func TestParseFileMode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		mode    string
		want    os.FileMode
		wantErr bool
	}{
		{mode: "", want: 0o600},
		{mode: "0600", want: 0o600},
		{mode: "640", want: 0o640},
		{mode: "0644", want: 0o644},
		{mode: "999", wantErr: true},
		{mode: "rw-r--r--", wantErr: true},
	}

	for _, tt := range tests {
		t.Run("mode "+tt.mode, func(t *testing.T) {
			t.Parallel()

			got, err := parseFileMode(tt.mode)
			requireEqual(t, "parseFileMode error", err != nil, tt.wantErr)

			if !tt.wantErr {
				requireEqual(t, "parseFileMode("+tt.mode+")", got, tt.want)
			}
		})
	}
}

// writeCmdFixture configures the fixture service with read_cmd/write_cmd
// adapters instead of a touchgrass-owned target.
func writeCmdFixture(t *testing.T) *envFixture {
	t.Helper()

	fixture := newEnvFixture(t, healthyProber())
	services := store.NewServiceStore(fixture.db)
	config := map[string]any{
		"service": testAdminService, "health_url": "http://127.0.0.1:1/health",
		"env": model.EnvConfig{
			Target:      model.EnvTarget{Path: fixture.target, Owner: "ops", Mode: "0600"},
			ValidateCmd: testEnvValidate, RestartCmd: testEnvRestart, ProbeURL: testEnvProbeURL,
			ReadCmd: "sudo read-env env-svc", WriteCmd: "sudo write-env env-svc", ProbeTimeoutSecs: 1,
		},
	}

	raw, err := json.Marshal(config)
	requireNoError(t, err, "marshal write_cmd config")
	requireNoError(t, services.UpdateConfig(context.Background(), testEnvServiceID, raw), "UpdateConfig()")

	return fixture
}

func TestEnvApplyWriteCmd(t *testing.T) {
	t.Parallel()

	fixture := writeCmdFixture(t)
	fixture.stage(t, "B", "2", false)
	fixture.stage(t, "A", "1", true)

	run := fixture.apply(t, "via write_cmd")

	requireEqual(t, "run state", run.State, model.EnvRunSucceeded)
	requireEqual(t, "steps", strings.Join(stepNames(run), ","), strings.Join(successSteps, ","))
	requireEqual(t, "write_cmd stdin", fixture.host.lastInput("write-env"), envHeader+"A=1\nB=2\n")
	requireEqual(t, "target untouched by touchgrass", fixture.readTarget(t, ""), "")
	requireEqual(t, "private candidate removed", fixture.targetDirCount(t), 0)
	requireEqual(t, "validate ran", fixture.host.count("validate"), 1)
	requireEqual(t, "restart ran", fixture.host.count("restart"), 1)
}

func TestEnvApplyWriteCmdFailure(t *testing.T) {
	t.Parallel()

	fixture := writeCmdFixture(t)
	fixture.host.write = func(_ []byte, emit func(string)) error {
		emit("missing required keys: DB_URL, API_KEY")

		return errors.New("exit status 2")
	}
	fixture.stage(t, "PORT", "1", false)

	run := fixture.apply(t, "")

	requireEqual(t, "run state", run.State, model.EnvRunFailed)
	requireEqual(t, "run error", run.Error, "write: write command failed")
	requireEqual(t, "validation lines", strings.Join(run.ValidationLines, "|"), "missing required keys: DB_URL, API_KEY")
	requireEqual(t, "restored", run.Restored, false)
	requireEqual(t, "restart never ran", fixture.host.count("restart"), 0)

	view := fixture.view(t)
	requireEqual(t, "last validation ok", view.LastValidation.OK, false)
	requireEqual(t, "missing required", strings.Join(view.LastValidation.MissingRequired, ","), "DB_URL,API_KEY")
	requireEqual(t, "pending kept", view.PendingCount, 1)
}

func TestEnvApplyWriteCmdRestartFailureReplaysPrevious(t *testing.T) {
	t.Parallel()

	fixture := writeCmdFixture(t)
	fixture.stage(t, "A", "old", false)
	fixture.apply(t, "baseline")

	calls := 0
	fixture.host.restart = func(context.Context) error {
		calls++
		if calls == 1 {
			return errors.New("boom")
		}

		return nil
	}

	fixture.stage(t, "A", "new", false)

	run := fixture.apply(t, "")

	requireEqual(t, "run state", run.State, model.EnvRunFailed)
	requireEqual(t, "restored via write_cmd", run.Restored, true)
	requireEqual(t, "write_cmd replayed previous", fixture.host.lastInput("write-env"), envHeader+"A=old\n")
	requireEqual(t, "write_cmd calls", fixture.host.count("write-env"), 3)
}

func TestEnvImportReadCmd(t *testing.T) {
	t.Parallel()

	fixture := writeCmdFixture(t)
	fixture.host.output = []byte("PORT=1\nDB_PASSWORD=x\n")

	result, err := fixture.env.Import(context.Background(), testEnvServiceID, false, testEnvActor)
	requireNoError(t, err, "Import()")
	requireEqual(t, "imported", result.Imported, 2)
	requireEqual(t, "secret guessed", result.SecretGuessed, 1)
	requireEqual(t, "read_cmd ran", fixture.host.count("read-env"), 1)
}

func TestEnvViewAppliedValue(t *testing.T) {
	t.Parallel()

	fixture := newEnvFixture(t, healthyProber())
	fixture.stage(t, "PLAIN", "old", false)
	fixture.stage(t, "TOKEN", "s1", true)
	fixture.apply(t, "baseline")
	fixture.stage(t, "PLAIN", "new", false)
	fixture.stage(t, "TOKEN", "s2", true)
	fixture.stage(t, "FRESH", "f", false)

	view := fixture.view(t)
	plain := entryByKey(t, view, "PLAIN")
	requireEqual(t, "PLAIN value", valueOf(plain), "new")
	requireEqual(t, "PLAIN applied_value set", plain.AppliedValue != nil, true)
	requireEqual(t, "PLAIN applied_value", *plain.AppliedValue, "old")
	requireEqual(t, "TOKEN applied_value hidden", entryByKey(t, view, "TOKEN").AppliedValue == nil, true)
	requireEqual(t, "FRESH applied_value absent", entryByKey(t, view, "FRESH").AppliedValue == nil, true)
	requireEqual(t, "active run id idle", view.ActiveRunID, "")
}

func TestMissingRequired(t *testing.T) {
	t.Parallel()

	got := missingRequired([]string{
		"checking env-svc", "Missing required keys: DB_URL, API_KEY", "missing required key: PORT", "DB_URL again",
	})
	requireEqual(t, "missingRequired", strings.Join(got, ","), "DB_URL,API_KEY,PORT")
}
