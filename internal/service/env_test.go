package service

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/oralecarlangelo/touchgrass/internal/model"
	"github.com/oralecarlangelo/touchgrass/internal/secret"
	"github.com/oralecarlangelo/touchgrass/internal/store"
)

const (
	testEnvServiceID  = "env-svc"
	testEnvActor      = "admin"
	testEnvProbeURL   = "http://127.0.0.1:1/__release"
	testEnvKeyHex     = "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f"
	testEnvValidate   = "validate --root /tmp"
	testEnvRestart    = "restart env-svc"
	testEnvInvalidKey = "INVALID env-svc:DB_URL: missing"
	testEnvAPIKey     = "API_KEY"
	testEnvOldFile    = "OLD=1\n"
)

// scriptedHost runs file operations for real inside a temp dir and scripts
// the validate and restart commands.
type scriptedHost struct {
	localEnvHost

	mutex    sync.Mutex
	validate func(emit func(string)) error
	restart  func(ctx context.Context) error
	write    func(input []byte, emit func(string)) error
	output   []byte
	commands []string
	envs     [][]string
	inputs   [][]byte
}

// Output answers read_cmd with the scripted bytes.
func (h *scriptedHost) Output(_ context.Context, command string, _ []string, _ bool) ([]byte, error) {
	h.mutex.Lock()
	defer h.mutex.Unlock()

	h.commands = append(h.commands, command)

	if h.output == nil {
		return nil, errors.New("no scripted output")
	}

	return append([]byte{}, h.output...), nil
}

// lastInput returns the stdin of the most recent command with a prefix.
func (h *scriptedHost) lastInput(prefix string) string {
	h.mutex.Lock()
	defer h.mutex.Unlock()

	for i, command := range slices.Backward(h.commands) {
		if strings.HasPrefix(strings.TrimPrefix(command, "sudo "), prefix) {
			return string(h.inputs[i])
		}
	}

	return ""
}

// Run dispatches on the first word of the command.
func (h *scriptedHost) Run(
	ctx context.Context,
	command string,
	input []byte,
	env []string,
	_ bool,
	emit func(string),
) error {
	h.mutex.Lock()
	h.commands = append(h.commands, command)
	h.envs = append(h.envs, env)
	h.inputs = append(h.inputs, append([]byte{}, input...))
	validate, restart, write := h.validate, h.restart, h.write
	h.mutex.Unlock()

	command = strings.TrimPrefix(command, "sudo ")

	switch {
	case strings.HasPrefix(command, "write"):
		if write != nil {
			return write(input, emit)
		}

		return nil
	case strings.HasPrefix(command, "validate"):
		if validate != nil {
			return validate(emit)
		}

		emit("OK env-svc")

		return nil
	case strings.HasPrefix(command, "restart"):
		if restart != nil {
			return restart(ctx)
		}

		emit("restarted")

		return nil
	default:
		return errors.New("unexpected command")
	}
}

// ran returns the commands run so far.
func (h *scriptedHost) ran() []string {
	h.mutex.Lock()
	defer h.mutex.Unlock()

	return append([]string{}, h.commands...)
}

// count returns how many commands start with prefix.
func (h *scriptedHost) count(prefix string) int {
	total := 0

	for _, command := range h.ran() {
		if strings.HasPrefix(strings.TrimPrefix(command, "sudo "), prefix) {
			total++
		}
	}

	return total
}

// envFixture is one wired Env with its fakes.
type envFixture struct {
	env    *Env
	db     *store.DB
	host   *scriptedHost
	events *eventLog
	target string
	audits *store.AuditStore
}

// newEnvFixture builds an Env over a seeded store plus one env-configured
// service whose target lives in a temp dir.
func newEnvFixture(t *testing.T, prober Prober) *envFixture {
	t.Helper()

	db := openInventoryDB(t)
	target := filepath.Join(t.TempDir(), "env-svc.env")
	services := store.NewServiceStore(db)
	config := map[string]any{
		"service": testAdminService, "health_url": "http://127.0.0.1:1/health",
		"env": model.EnvConfig{
			Target: model.EnvTarget{Path: target, Mode: "0600"}, ValidateCmd: testEnvValidate,
			RestartCmd: testEnvRestart, ProbeURL: testEnvProbeURL, ProbeTimeoutSecs: 1,
		},
	}

	raw, err := json.Marshal(config)
	requireNoError(t, err, "marshal config")

	err = services.Create(context.Background(), model.Service{
		ID: testEnvServiceID, Name: testEnvServiceID, Strategy: model.StrategyRecreate,
		ComposeProject: "env", ComposeDir: t.TempDir(), Config: raw,
	})
	requireNoError(t, err, "Create(service)")

	key, err := secret.Load("", testEnvKeyHex)
	requireNoError(t, err, "secret.Load")

	host := &scriptedHost{}
	events := &eventLog{}
	audits := store.NewAuditStore(db)

	env := NewEnv(EnvConfig{
		Services: services, Store: store.NewEnvStore(db), Audit: NewAudit(services, audits), Key: key,
		Host: host, Prober: prober, Emit: events.emit, ProbeInterval: time.Millisecond,
		ProbeTimeout: 50 * time.Millisecond, RevealLimit: 3, Logger: slog.New(slog.DiscardHandler),
	})

	return &envFixture{env: env, db: db, host: host, events: events, target: target, audits: audits}
}

// requireNoError fails the test when err is set.
func requireNoError(t *testing.T, err error, what string) {
	t.Helper()

	if err != nil {
		t.Fatalf("%s error = %v, want nil", what, err)
	}
}

// requireEqual reports a mismatch without stopping the test.
func requireEqual[T comparable](t *testing.T, label string, got, want T) {
	t.Helper()

	if got != want {
		t.Errorf("%s = %v, want %v", label, got, want)
	}
}

// requireIs fails when err does not wrap want.
func requireIs(t *testing.T, label string, err, want error) {
	t.Helper()

	if !errors.Is(err, want) {
		t.Errorf("%s error = %v, want %v", label, err, want)
	}
}

// healthyProber answers healthy for the fixture probe URL.
func healthyProber() stubProber {
	return stubProber{healthy: map[string]bool{testEnvProbeURL: true}}
}

// stage stages one entry or fails the test.
func (f *envFixture) stage(t *testing.T, key, value string, isSecret bool) model.EnvEntryView {
	t.Helper()

	view, err := f.env.Stage(context.Background(), testEnvServiceID, key, value, isSecret, testEnvActor)
	requireNoError(t, err, "Stage("+key+")")

	return view
}

// remove stages a removal or fails the test.
func (f *envFixture) remove(t *testing.T, key string) {
	t.Helper()

	requireNoError(t, f.env.Remove(context.Background(), testEnvServiceID, key, testEnvActor), "Remove("+key+")")
}

// apply runs an apply to completion and returns the finished run.
func (f *envFixture) apply(t *testing.T, summary string) model.EnvRun {
	t.Helper()

	ctx := context.Background()

	runID, err := f.env.Apply(ctx, testEnvServiceID, testEnvServiceID, summary, testEnvActor)
	requireNoError(t, err, "Apply()")

	return f.await(t, runID)
}

// await waits for a run to finish.
func (f *envFixture) await(t *testing.T, runID string) model.EnvRun {
	t.Helper()

	waitCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	run, err := f.env.AwaitRun(waitCtx, testEnvServiceID, runID)
	requireNoError(t, err, "AwaitRun()")

	return run
}

// view loads the env view or fails the test.
func (f *envFixture) view(t *testing.T) model.EnvView {
	t.Helper()

	view, err := f.env.View(context.Background(), testEnvServiceID)
	requireNoError(t, err, "View()")

	return view
}

// diff loads the diff or fails the test.
func (f *envFixture) diff(t *testing.T) model.EnvDiff {
	t.Helper()

	diff, err := f.env.Diff(context.Background(), testEnvServiceID)
	requireNoError(t, err, "Diff()")

	return diff
}

// writeTarget seeds the target file.
func (f *envFixture) writeTarget(t *testing.T, content string) {
	t.Helper()

	requireNoError(t, os.WriteFile(f.target, []byte(content), 0o600), "seed target")
}

// readTarget returns the target file (plus suffix) contents or "" when absent.
func (f *envFixture) readTarget(t *testing.T, suffix string) string {
	t.Helper()

	data, err := os.ReadFile(f.target + suffix) //nolint:gosec // test temp dir path.
	if errors.Is(err, os.ErrNotExist) {
		return ""
	}

	requireNoError(t, err, "read target")

	return string(data)
}

// targetDirCount returns how many files sit beside the target.
func (f *envFixture) targetDirCount(t *testing.T) int {
	t.Helper()

	entries, err := os.ReadDir(filepath.Dir(f.target))
	requireNoError(t, err, "ReadDir(target)")

	return len(entries)
}

// auditActions lists audit rows for the fixture service newest first.
func (f *envFixture) auditActions(t *testing.T) []model.Audit {
	t.Helper()

	entries, err := f.audits.List(context.Background(), testEnvServiceID, 50)
	requireNoError(t, err, "audit List()")

	return entries
}

// entryByKey finds one entry view or fails the test.
func entryByKey(t *testing.T, view model.EnvView, key string) model.EnvEntryView {
	t.Helper()

	for _, entry := range view.Entries {
		if entry.Key == key {
			return entry
		}
	}

	t.Fatalf("entry %s missing from view", key)

	return model.EnvEntryView{}
}

// valueOf dereferences an entry value, "<nil>" when masked.
func valueOf(entry model.EnvEntryView) string {
	if entry.Value == nil {
		return "<nil>"
	}

	return *entry.Value
}

// stepNames extracts the deduplicated step sequence of a run.
func stepNames(run model.EnvRun) []string {
	names := []string{}

	for _, step := range run.Steps {
		if len(names) == 0 || names[len(names)-1] != step.Name {
			names = append(names, step.Name)
		}
	}

	return names
}

func TestEnvViewUnconfigured(t *testing.T) {
	t.Parallel()

	fixture := newEnvFixture(t, healthyProber())
	ctx := context.Background()

	view, err := fixture.env.View(ctx, "tn-api")
	requireNoError(t, err, "View(tn-api)")
	requireEqual(t, "View(tn-api).Configured", view.Configured, false)
	requireEqual(t, "View(tn-api).Reason", view.Reason, model.EnvReasonNotConfigured)
	requireEqual(t, "View(tn-api) entries", len(view.Entries), 0)

	_, err = fixture.env.Stage(ctx, "tn-api", "A", "b", false, testEnvActor)
	requireIs(t, "Stage(tn-api)", err, ErrEnvNotConfigured)

	disabled := NewEnv(EnvConfig{
		Services: store.NewServiceStore(fixture.db), Store: store.NewEnvStore(fixture.db),
		Audit: NewAudit(store.NewServiceStore(fixture.db), fixture.audits),
	})

	view, err = disabled.View(ctx, testEnvServiceID)
	requireNoError(t, err, "View(disabled)")
	requireEqual(t, "View(disabled).Configured", view.Configured, false)
	requireEqual(t, "View(disabled).Reason", view.Reason, model.EnvReasonKeyMissing)
	requireEqual(t, "Enabled()", disabled.Enabled(), false)

	_, err = disabled.Stage(ctx, testEnvServiceID, "A", "b", false, testEnvActor)
	requireIs(t, "Stage(disabled)", err, ErrEnvKeyMissing)

	_, err = fixture.env.View(ctx, "nope")
	requireIs(t, "View(unknown)", err, store.ErrServiceNotFound)
}

func TestEnvStageValidation(t *testing.T) {
	t.Parallel()

	fixture := newEnvFixture(t, healthyProber())

	tests := []struct {
		name    string
		key     string
		value   string
		wantErr error
	}{
		{name: "lowercase key", key: "db_url", value: "x", wantErr: ErrEnvKeyNameInvalid},
		{name: "leading digit", key: "1KEY", value: "x", wantErr: ErrEnvKeyNameInvalid},
		{name: "dash", key: "DB-URL", value: "x", wantErr: ErrEnvKeyNameInvalid},
		{name: "empty key", key: "", value: "x", wantErr: ErrEnvKeyNameInvalid},
		{name: "newline value", key: "A", value: "line\nbreak", wantErr: ErrEnvValueInvalid},
		{name: "carriage return", key: "A", value: "line\rbreak", wantErr: ErrEnvValueInvalid},
		{name: "invalid utf8", key: "A", value: "\xff\xfe", wantErr: ErrEnvValueInvalid},
		{name: "too long", key: "A", value: strings.Repeat("x", envMaxValueBytes+1), wantErr: ErrEnvValueInvalid},
		{name: "valid", key: "_OK_1", value: "spaces and = signs \"quoted\"", wantErr: nil},
		{name: "empty value", key: "EMPTY", value: "", wantErr: nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := fixture.env.Stage(context.Background(), testEnvServiceID, tt.key, tt.value, false, testEnvActor)
			requireIs(t, "Stage()", err, tt.wantErr)
		})
	}
}

func TestEnvStageStates(t *testing.T) {
	t.Parallel()

	fixture := newEnvFixture(t, healthyProber())

	fixture.stage(t, "KEEP", "1", false)
	fixture.stage(t, "EDIT", "old", false)
	fixture.stage(t, "DROP", "x", true)
	fixture.apply(t, "baseline")

	requireEqual(t, "restage same value", fixture.stage(t, "KEEP", "1", false).State, model.EnvStateApplied)
	requireEqual(t, "EDIT state", fixture.stage(t, "EDIT", "new", false).State, model.EnvStateChanged)
	requireEqual(t, "flip secret state", fixture.stage(t, "KEEP", "1", true).State, model.EnvStateChanged)
	requireEqual(t, "NEW state", fixture.stage(t, "NEW", "n", false).State, model.EnvStateAdded)
	requireEqual(t, "restage NEW", fixture.stage(t, "NEW", "n2", false).State, model.EnvStateAdded)

	fixture.remove(t, "DROP")
	fixture.remove(t, "DROP") // idempotent
	fixture.remove(t, "NEW")

	err := fixture.env.Remove(context.Background(), testEnvServiceID, "MISSING", testEnvActor)
	requireIs(t, "Remove(MISSING)", err, store.ErrEnvEntryNotFound)

	requireEqual(t, "re-add removed", fixture.stage(t, "DROP", "y", true).State, model.EnvStateChanged)
	fixture.remove(t, "DROP")

	diff := fixture.diff(t)
	requireEqual(t, "added", strings.Join(diff.Added, ","), "")
	requireEqual(t, "changed", strings.Join(diff.Changed, ","), "EDIT,KEEP")
	requireEqual(t, "removed", strings.Join(diff.Removed, ","), "DROP")
	requireEqual(t, "pending", fixture.view(t).PendingCount, 3)
}

func TestEnvStageAppliedValueCancelsPending(t *testing.T) {
	t.Parallel()

	fixture := newEnvFixture(t, healthyProber())

	fixture.stage(t, "EDIT", "old", false)
	fixture.stage(t, "DROP", "x", true)
	fixture.apply(t, "baseline")

	// A pending change goes back to applied when the applied value returns.
	requireEqual(t, "EDIT changed", fixture.stage(t, "EDIT", "new", false).State, model.EnvStateChanged)
	requireEqual(t, "EDIT undone", fixture.stage(t, "EDIT", "old", false).State, model.EnvStateApplied)

	// So does a pending removal — the UI's undo re-stages the applied value.
	fixture.remove(t, "DROP")
	requireEqual(t, "DROP undone", fixture.stage(t, "DROP", "x", true).State, model.EnvStateApplied)

	// A different flag on the same value is still a change.
	requireEqual(t, "DROP flag flip", fixture.stage(t, "DROP", "x", false).State, model.EnvStateChanged)

	requireEqual(t, "pending", fixture.view(t).PendingCount, 1)
	requireEqual(t, "undo audited", fixture.auditActions(t)[1].Detail, "unstaged DROP")
}

func TestEnvDiscardRestoresApplied(t *testing.T) {
	t.Parallel()

	fixture := newEnvFixture(t, healthyProber())
	ctx := context.Background()

	fixture.stage(t, "KEEP", "1", false)
	fixture.stage(t, "EDIT", "old", false)
	fixture.stage(t, "DROP", "x", true)
	fixture.apply(t, "baseline")

	fixture.stage(t, "EDIT", "new", false)
	fixture.stage(t, "KEEP", "1", true)
	fixture.stage(t, "NEW", "n", false)
	fixture.remove(t, "DROP")

	requireNoError(t, fixture.env.Discard(ctx, testEnvServiceID, testEnvActor), "Discard()")

	view := fixture.view(t)
	requireEqual(t, "pending after discard", view.PendingCount, 0)
	requireEqual(t, "entries after discard", len(view.Entries), 3)

	for _, entry := range view.Entries {
		requireEqual(t, entry.Key+" state", entry.State, model.EnvStateApplied)
	}

	requireEqual(t, "EDIT value", valueOf(entryByKey(t, view, "EDIT")), "old")
	requireEqual(t, "KEEP secret", entryByKey(t, view, "KEEP").IsSecret, false)
	requireEqual(t, "DROP secret", entryByKey(t, view, "DROP").IsSecret, true)

	// A second discard with nothing pending is a quiet no-op.
	requireNoError(t, fixture.env.Discard(ctx, testEnvServiceID, testEnvActor), "Discard(again)")
	requireEqual(t, "newest audit", fixture.auditActions(t)[0].Action, model.AuditEnvDiscard)
}

func TestEnvViewMasksSecrets(t *testing.T) {
	t.Parallel()

	fixture := newEnvFixture(t, healthyProber())
	fixture.stage(t, "PLAIN", "visible", false)
	fixture.stage(t, "API_TOKEN", "hidden", true)

	view := fixture.view(t)
	requireEqual(t, "configured", view.Configured, true)
	requireEqual(t, "target path", view.Target.Path, fixture.target)
	requireEqual(t, "pending", view.PendingCount, 2)

	plain := entryByKey(t, view, "PLAIN")
	requireEqual(t, "PLAIN masked", plain.Masked, false)
	requireEqual(t, "PLAIN value", valueOf(plain), "visible")

	token := entryByKey(t, view, "API_TOKEN")
	requireEqual(t, "API_TOKEN masked", token.Masked, true)
	requireEqual(t, "API_TOKEN value", valueOf(token), "<nil>")
	requireEqual(t, "API_TOKEN is_secret", token.IsSecret, true)

	encoded, err := json.Marshal(view)
	requireNoError(t, err, "json.Marshal(view)")
	requireEqual(t, "view JSON leaks secret", strings.Contains(string(encoded), "hidden"), false)
}

func TestEnvRevealRateLimitAndAudit(t *testing.T) {
	t.Parallel()

	fixture := newEnvFixture(t, healthyProber())
	fixture.stage(t, "API_TOKEN", "hidden", true)

	ctx := context.Background()

	for i := range 3 {
		value, err := fixture.env.Reveal(ctx, testEnvServiceID, "API_TOKEN", testEnvActor)
		requireNoError(t, err, "Reveal()")
		requireEqual(t, "Reveal #"+string(rune('1'+i)), value, "hidden")
	}

	_, err := fixture.env.Reveal(ctx, testEnvServiceID, "API_TOKEN", testEnvActor)
	requireIs(t, "4th Reveal()", err, ErrEnvRevealRateLimited)

	_, err = fixture.env.Reveal(ctx, testEnvServiceID, "API_TOKEN", "other-admin")
	requireNoError(t, err, "Reveal(other actor)")

	_, err = fixture.env.Reveal(ctx, testEnvServiceID, "NOPE", "third")
	requireIs(t, "Reveal(unknown)", err, store.ErrEnvEntryNotFound)

	reveals := 0

	for _, entry := range fixture.auditActions(t) {
		if entry.Action != model.AuditEnvReveal {
			continue
		}

		reveals++

		requireEqual(t, "reveal detail names key", strings.Contains(entry.Detail, "API_TOKEN"), true)
		requireEqual(t, "reveal detail leaks value", strings.Contains(entry.Detail, "hidden"), false)
	}

	requireEqual(t, "reveal audit rows (4 success + 1 limited)", reveals, 5)
}

func TestEnvImport(t *testing.T) {
	t.Parallel()

	fixture := newEnvFixture(t, healthyProber())
	ctx := context.Background()

	fixture.writeTarget(t, strings.Join([]string{
		"# comment", "", "export PORT=8080", "DATABASE_URL=postgres://u@db/app", "PUBLIC_URL=https://example.test",
		"API_KEY=abc", "lowercase=skip", "NOEQUALS", "MONGO_DSN=mongodb://x", "PORT=9090", "PRIVATE_PEM=---",
	}, "\n")+"\n")

	result, err := fixture.env.Import(ctx, testEnvServiceID, false, testEnvActor)
	requireNoError(t, err, "Import()")
	requireEqual(t, "imported", result.Imported, 6)
	requireEqual(t, "skipped", result.Skipped, 2)
	requireEqual(t, "secret guessed", result.SecretGuessed, 4)

	view := fixture.view(t)
	requireEqual(t, "pending", view.PendingCount, 0)
	requireEqual(t, "applied revision set", view.AppliedRevision != nil, true)
	requireEqual(t, "PORT value", valueOf(entryByKey(t, view, "PORT")), "9090")

	wantSecret := map[string]bool{
		"PORT": false, "DATABASE_URL": true, "PUBLIC_URL": false, testEnvAPIKey: true,
		"MONGO_DSN": true, "PRIVATE_PEM": true,
	}

	for key, want := range wantSecret {
		entry := entryByKey(t, view, key)
		requireEqual(t, key+" is_secret", entry.IsSecret, want)
		requireEqual(t, key+" state", entry.State, model.EnvStateApplied)
	}

	_, err = fixture.env.Import(ctx, testEnvServiceID, false, testEnvActor)
	requireIs(t, "second Import()", err, ErrEnvHasEntries)

	_, err = fixture.env.Import(ctx, testEnvServiceID, true, testEnvActor)
	requireNoError(t, err, "Import(replace)")
	requireEqual(t, "newest audit", fixture.auditActions(t)[0].Action, model.AuditEnvImport)
}

func TestGuessSecret(t *testing.T) {
	t.Parallel()

	tests := []struct {
		key   string
		value string
		want  bool
	}{
		{key: testEnvAPIKey, value: "x", want: true},
		{key: "JWT_SECRET", value: "x", want: true},
		{key: "AUTH_TOKEN", value: "x", want: true},
		{key: "DB_PASSWORD", value: "x", want: true},
		{key: "SMTP_PASS", value: "x", want: true},
		{key: "SENTRY_DSN", value: "x", want: true},
		{key: "PRIVATE_KEY_PATH", value: "x", want: true},
		{key: "DATABASE_URL", value: "postgres://user@host/db", want: true},
		{key: "REDIS_URI", value: "redis://:x@host:6379", want: true},
		{key: "PUBLIC_URL", value: "https://example.test/path@2x", want: false},
		{key: "PORT", value: "8080", want: false},
		{key: "NODE_ENV", value: "production", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			t.Parallel()

			requireEqual(t, "guessSecret("+tt.key+")", guessSecret(tt.key, tt.value), tt.want)
		})
	}
}

func TestRequiredKeys(t *testing.T) {
	t.Parallel()

	got := requiredKeys([]string{
		testEnvInvalidKey, "INVALID other:API_KEY: bad format", "OK env-svc", "INVALID garbage", "  INVALID a:B:",
	})

	for _, key := range []string{"DB_URL", testEnvAPIKey, "B"} {
		requireEqual(t, "requiredKeys has "+key, got[key], true)
	}

	requireEqual(t, "requiredKeys size", len(got), 3)
}
