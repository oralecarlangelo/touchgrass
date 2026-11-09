package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/oralecarlangelo/touchgrass/internal/model"
	"github.com/oralecarlangelo/touchgrass/internal/secret"
	"github.com/oralecarlangelo/touchgrass/internal/store"
)

// Apply run limits.
const (
	envRunIDBytes           = 16
	envMaxValidationLines   = 200
	envMaxValidationLineLen = 1024
	envMaxRunSteps          = 500
	envPostRunTimeout       = 30 * time.Second
)

// Apply failure causes, kept short and value-free for run records.
var (
	errEnvProbeTimeout     = errors.New("probe did not become healthy before the timeout")
	errEnvValidationFailed = errors.New("env_validation_failed")
	errEnvWriteCmdFailed   = errors.New("write command failed")
	errEnvCandidateWrite   = errors.New("writing the candidate file failed")
	errEnvReplaceFailed    = errors.New("replacing the target file failed")
	errEnvRestartFailed    = errors.New("restart command failed")
	errEnvNoRevision       = errors.New("no applied revision to restore")
)

// envRun is one in-flight apply with its mutable state.
type envRun struct {
	target      envTarget
	run         model.EnvRun
	actor       string
	summary     string
	entries     []model.EnvEntry
	diff        model.EnvDiff
	rendered    []byte
	sha         string
	tempPath    string
	privateDir  string
	hadPrevious bool
	wrote       bool
	swapped     bool
}

// Apply validates the request and starts an async apply run.
func (e *Env) Apply(ctx context.Context, serviceID, confirm, summary, actor string) (string, error) {
	target, err := e.resolve(ctx, serviceID)
	if err != nil {
		return "", err
	}

	if e.Active(serviceID) {
		return "", fmt.Errorf("%w: %q", ErrEnvApplyInProgress, serviceID)
	}

	entries, err := e.store.ListEntries(ctx, serviceID)
	if err != nil {
		return "", err
	}

	diff := diffEntries(entries)
	if len(diff.Added)+len(diff.Changed)+len(diff.Removed) == 0 {
		return "", fmt.Errorf("%w: %q", ErrEnvNothingToApply, serviceID)
	}

	if confirm != target.def.Name {
		return "", fmt.Errorf("%w: type the service name to confirm", ErrEnvConfirmationMismatch)
	}

	return e.launch(ctx, target, entries, diff, summary, actor)
}

// Rollback stages a revision's snapshot as pending changes and applies it.
func (e *Env) Rollback(ctx context.Context, serviceID string, revisionID int64, confirm, actor string) (string, error) {
	target, err := e.resolve(ctx, serviceID)
	if err != nil {
		return "", err
	}

	if e.Active(serviceID) {
		return "", fmt.Errorf("%w: %q", ErrEnvApplyInProgress, serviceID)
	}

	if confirm != target.def.Name {
		return "", fmt.Errorf("%w: type the service name to confirm", ErrEnvConfirmationMismatch)
	}

	revision, err := e.store.GetRevision(ctx, serviceID, revisionID)
	if err != nil {
		return "", err
	}

	snapshot, err := e.store.RevisionEntries(ctx, revision.ID)
	if err != nil {
		return "", err
	}

	current, err := e.store.ListEntries(ctx, serviceID)
	if err != nil {
		return "", err
	}

	staged, err := e.stageSnapshot(serviceID, current, snapshot, actor)
	if err != nil {
		return "", err
	}

	diff := diffEntries(staged)
	if len(diff.Added)+len(diff.Changed)+len(diff.Removed) == 0 {
		return "", fmt.Errorf("%w: revision %d is already applied", ErrEnvNothingToApply, revision.ID)
	}

	if err := e.store.ReplaceEntries(ctx, serviceID, staged); err != nil {
		return "", err
	}

	summary := fmt.Sprintf("rollback to revision %d", revision.ID)
	e.record(ctx, serviceID, actor, model.AuditEnvRollback, model.AuditSuccess, summary+" "+describeDiff(diff))

	return e.launch(ctx, target, staged, diff, summary, actor)
}

// stageSnapshot rewrites the working set so that applying it reproduces
// the snapshot: new keys are added, differing values changed, extra keys
// removed. Snapshot ciphertexts carry over because the AAD is unchanged.
func (e *Env) stageSnapshot(
	serviceID string,
	current []model.EnvEntry,
	snapshot []model.EnvRevisionEntry,
	actor string,
) ([]model.EnvEntry, error) {
	byKey := make(map[string]model.EnvEntry, len(current))
	for _, entry := range current {
		byKey[entry.Key] = entry
	}

	now := time.Now()
	staged := make([]model.EnvEntry, 0, len(snapshot)+len(current))
	seen := map[string]bool{}

	for _, want := range snapshot {
		seen[want.Key] = true
		entry := model.EnvEntry{
			ServiceID: serviceID, Key: want.Key, Ciphertext: want.Ciphertext, Nonce: want.Nonce,
			KeyID: want.KeyID, IsSecret: want.IsSecret, State: model.EnvStateAdded, UpdatedBy: actor, UpdatedAt: now,
		}

		have, exists := byKey[want.Key]
		if exists {
			same, err := e.sameCiphertext(have, want)
			if err != nil {
				return nil, err
			}

			switch {
			case have.State == model.EnvStateAdded:
				entry.State = model.EnvStateAdded
			case same && have.IsSecret == want.IsSecret:
				entry.State = model.EnvStateApplied
				entry.UpdatedBy = have.UpdatedBy
				entry.UpdatedAt = have.UpdatedAt
			default:
				entry.State = model.EnvStateChanged
			}
		}

		staged = append(staged, entry)
	}

	for _, have := range current {
		if seen[have.Key] || have.State == model.EnvStateAdded {
			continue
		}

		have.State = model.EnvStateRemoved
		have.UpdatedBy = actor
		have.UpdatedAt = now
		staged = append(staged, have)
	}

	return staged, nil
}

// sameCiphertext reports whether a working entry and a snapshot entry hold
// the same plaintext.
func (e *Env) sameCiphertext(have model.EnvEntry, want model.EnvRevisionEntry) (bool, error) {
	left, err := e.decrypt(have.ServiceID, have.Key, have.Ciphertext, have.Nonce)
	if err != nil {
		return false, err
	}

	defer secret.Zero(left)

	right, err := e.decrypt(have.ServiceID, want.Key, want.Ciphertext, want.Nonce)
	if err != nil {
		return false, err
	}

	defer secret.Zero(right)

	return string(left) == string(right), nil
}

// launch claims the service, records the run, and starts execution.
func (e *Env) launch(
	ctx context.Context,
	target envTarget,
	entries []model.EnvEntry,
	diff model.EnvDiff,
	summary, actor string,
) (string, error) {
	runID, err := newRunID()
	if err != nil {
		return "", err
	}

	if err := e.claim(target.def.ID, runID); err != nil {
		return "", err
	}

	run := model.EnvRun{
		ID: runID, ServiceID: target.def.ID, State: model.EnvRunRunning, StartedAt: time.Now(),
		Steps: []model.EnvRunStep{},
	}

	if err := e.store.InsertRun(ctx, run); err != nil {
		e.release(target.def.ID, runID)

		return "", err
	}

	go e.execute(ctx, &envRun{
		target: target, run: run, actor: actor, summary: summary, entries: entries, diff: diff,
	})

	return runID, nil
}

// newRunID mints a random run identifier.
func newRunID() (string, error) {
	raw := make([]byte, envRunIDBytes)

	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generating run id: %w", err)
	}

	return hex.EncodeToString(raw), nil
}

// claim marks a service as applying, rejecting concurrent runs.
func (e *Env) claim(serviceID, runID string) error {
	e.mutex.Lock()
	defer e.mutex.Unlock()

	if _, running := e.running[serviceID]; running {
		return fmt.Errorf("%w: %q", ErrEnvApplyInProgress, serviceID)
	}

	e.running[serviceID] = runID
	e.done[runID] = make(chan struct{})

	return nil
}

// release clears the running mark and wakes waiters.
func (e *Env) release(serviceID, runID string) {
	e.mutex.Lock()
	defer e.mutex.Unlock()

	delete(e.running, serviceID)

	if done, ok := e.done[runID]; ok {
		close(done)
		delete(e.done, runID)
	}
}

// Active reports whether an apply run is in flight for a service.
func (e *Env) Active(serviceID string) bool {
	_, running := e.activeRun(serviceID)

	return running
}

// activeRun returns the in-flight run id for a service.
func (e *Env) activeRun(serviceID string) (string, bool) {
	e.mutex.Lock()
	defer e.mutex.Unlock()

	runID, running := e.running[serviceID]

	return runID, running
}

// Done returns a channel closed when the run finishes. Unknown or
// finished runs yield an already-closed channel.
func (e *Env) Done(runID string) <-chan struct{} {
	e.mutex.Lock()
	defer e.mutex.Unlock()

	if done, ok := e.done[runID]; ok {
		return done
	}

	closed := make(chan struct{})
	close(closed)

	return closed
}

// Run returns one run with its step log.
func (e *Env) Run(ctx context.Context, serviceID, runID string) (model.EnvRun, error) {
	if _, err := e.services.Get(ctx, serviceID); err != nil {
		return model.EnvRun{}, err
	}

	return e.store.GetRun(ctx, serviceID, runID)
}

// AwaitRun blocks until the run finishes or ctx ends, then returns it.
func (e *Env) AwaitRun(ctx context.Context, serviceID, runID string) (model.EnvRun, error) {
	select {
	case <-ctx.Done():
		return model.EnvRun{}, fmt.Errorf("waiting for env run: %w", ctx.Err())
	case <-e.Done(runID):
	}

	return e.Run(ctx, serviceID, runID)
}

// Reconcile fails runs left running by a previous process. Call once at boot.
func (e *Env) Reconcile(ctx context.Context) error {
	failed, err := e.store.FailRunningRuns(ctx, time.Now(), "touchgrass restarted during the run")
	if err != nil {
		return err
	}

	if failed > 0 {
		e.logger.Warn("failed orphaned env runs", "count", failed)
	}

	return nil
}

// execute drives one apply run end to end.
func (e *Env) execute(ctx context.Context, run *envRun) {
	defer e.release(run.target.def.ID, run.run.ID)
	defer e.cleanup(ctx, run)

	err := e.renderAndValidate(ctx, run)
	if err == nil {
		err = e.writeRestartProbe(ctx, run)
	}

	postCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), envPostRunTimeout)
	defer cancel()

	if err != nil {
		e.fail(postCtx, run, err)

		return
	}

	e.succeed(postCtx, run)
}

// cleanup zeroes the rendered file and removes any leftover candidate.
func (e *Env) cleanup(ctx context.Context, run *envRun) {
	secret.Zero(run.rendered)

	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), envPostRunTimeout)
	defer cancel()

	if run.wrote {
		if err := e.host.Remove(cleanupCtx, run.tempPath, run.target.cfg.Sudo); err != nil {
			e.logger.Warn("env candidate cleanup failed", "service", run.target.def.ID, "error", err)
		}

		run.wrote = false
	}

	if run.privateDir != "" {
		if err := os.RemoveAll(run.privateDir); err != nil {
			e.logger.Warn("env private candidate cleanup failed", "service", run.target.def.ID, "error", err)
		}

		run.privateDir = ""
	}
}

// renderAndValidate renders the candidate, places it where the validator
// can read it, and runs the validator. A validation failure deletes the
// candidate and records the output.
func (e *Env) renderAndValidate(ctx context.Context, run *envRun) error {
	rendered, err := e.render(run.entries)
	if err != nil {
		return fmt.Errorf("render: %w", err)
	}

	run.rendered = rendered.data
	run.sha = rendered.sha
	cfg := run.target.cfg

	if err := e.placeCandidate(ctx, run); err != nil {
		e.logger.Error("env candidate write failed", "service", run.target.def.ID, "error", err)

		return fmt.Errorf("render: %w", errEnvCandidateWrite)
	}

	e.progress(ctx, run, model.EnvStepRender, model.EnvRunSucceeded,
		fmt.Sprintf("rendered %d keys (sha256 %s)", len(rendered.keys), rendered.sha))

	if cfg.ValidateCmd == "" {
		e.progress(ctx, run, model.EnvStepValidate, model.EnvRunSucceeded, "no validate_cmd configured; skipped")

		return nil
	}

	e.progress(ctx, run, model.EnvStepValidate, model.EnvRunRunning, "validating candidate")

	lines, err := e.runValidator(ctx, run)
	e.recordValidation(ctx, run.target.def.ID, err == nil, lines)

	if err != nil {
		run.run.ValidationLines = lines
		e.cleanup(ctx, run)

		return fmt.Errorf("validate: %w: %d lines", errEnvValidationFailed, len(lines))
	}

	e.progress(ctx, run, model.EnvStepValidate, model.EnvRunSucceeded,
		fmt.Sprintf("validation passed (%d lines)", len(lines)))

	return nil
}

// placeCandidate writes the rendered file where the next step needs it:
// beside the target when touchgrass installs it, or in a private temp
// dir when write_cmd owns the install and only the validator reads it.
func (e *Env) placeCandidate(ctx context.Context, run *envRun) error {
	cfg := run.target.cfg

	if cfg.WriteCmd == "" {
		run.tempPath = envTempPath(cfg.Target.Path, run.run.ID)

		if err := e.host.WriteFile(ctx, run.tempPath, run.rendered, cfg.Target.Mode, cfg.Target.Owner, cfg.Sudo); err != nil {
			return err
		}

		run.wrote = true

		return nil
	}

	if cfg.ValidateCmd == "" {
		return nil
	}

	dir, err := os.MkdirTemp("", "touchgrass-env-")
	if err != nil {
		return fmt.Errorf("creating private candidate dir: %w", err)
	}

	run.privateDir = dir
	run.tempPath = filepath.Join(dir, filepath.Base(cfg.Target.Path))

	if err := os.WriteFile(run.tempPath, run.rendered, defaultEnvFileMode); err != nil {
		return fmt.Errorf("writing private candidate: %w", err)
	}

	return nil
}

// recordValidation stores the validator outcome, logging on failure.
func (e *Env) recordValidation(ctx context.Context, serviceID string, ok bool, lines []string) {
	validation := model.EnvValidation{ServiceID: serviceID, OK: ok, Lines: lines, At: time.Now()}

	if err := e.store.UpsertValidation(ctx, validation); err != nil {
		e.logger.Error("env validation record failed", "service", serviceID, "error", err)
	}
}

// lineCollector gathers bounded command output.
type lineCollector struct {
	lines []string
}

// collect appends one line within the caps.
func (c *lineCollector) collect(line string) {
	if len(c.lines) >= envMaxValidationLines {
		return
	}

	if len(line) > envMaxValidationLineLen {
		line = line[:envMaxValidationLineLen]
	}

	c.lines = append(c.lines, line)
}

// runEnv builds the variables every env command receives.
func runEnv(run *envRun) []string {
	return []string{
		"TOUCHGRASS_ENV_CANDIDATE=" + run.tempPath,
		"TOUCHGRASS_ENV_TARGET=" + run.target.cfg.Target.Path,
		"TOUCHGRASS_ENV_SERVICE=" + run.target.def.ID,
		"TOUCHGRASS_ENV_RUN=" + run.run.ID,
	}
}

// runValidator executes validate_cmd against the candidate and collects
// bounded output lines.
func (e *Env) runValidator(ctx context.Context, run *envRun) ([]string, error) {
	cfg := run.target.cfg
	collector := &lineCollector{lines: []string{}}

	validateCtx, cancel := context.WithTimeout(ctx, e.validateTimeout)
	defer cancel()

	err := e.host.Run(validateCtx, cfg.ValidateCmd+" "+run.tempPath, nil, runEnv(run), cfg.Sudo, collector.collect)
	if err != nil {
		e.logger.Warn("env validation failed", "service", run.target.def.ID, "error", err)

		return collector.lines, err
	}

	return collector.lines, nil
}

// writeRestartProbe installs the file, restarts the service, and probes it.
func (e *Env) writeRestartProbe(ctx context.Context, run *envRun) error {
	if err := e.install(ctx, run); err != nil {
		return err
	}

	run.swapped = true

	if err := e.restart(ctx, run); err != nil {
		return err
	}

	return e.probe(ctx, run)
}

// install swaps the candidate in, either by touchgrass's atomic rename
// (keeping one .bak) or by piping the rendered file to write_cmd.
func (e *Env) install(ctx context.Context, run *envRun) error {
	cfg := run.target.cfg

	if cfg.WriteCmd != "" {
		e.progress(ctx, run, model.EnvStepWrite, model.EnvRunRunning, "running write command")

		lines, err := e.pipeWrite(ctx, run, run.rendered)
		if err != nil {
			e.logger.Error("env write command failed", "service", run.target.def.ID, "error", err)
			run.run.ValidationLines = lines
			e.recordValidation(ctx, run.target.def.ID, false, lines)

			return fmt.Errorf("write: %w", errEnvWriteCmdFailed)
		}

		e.progress(ctx, run, model.EnvStepWrite, model.EnvRunSucceeded, "write command installed the file")

		return nil
	}

	hadPrevious, err := e.host.Replace(ctx, run.tempPath, cfg.Target.Path, cfg.Sudo)
	if err != nil {
		e.logger.Error("env file replace failed", "service", run.target.def.ID, "error", err)

		return fmt.Errorf("write: %w", errEnvReplaceFailed)
	}

	run.hadPrevious = hadPrevious
	run.wrote = false
	e.progress(ctx, run, model.EnvStepWrite, model.EnvRunSucceeded, "wrote target file (previous kept as .bak)")

	return nil
}

// pipeWrite streams content to write_cmd and returns its output lines.
func (e *Env) pipeWrite(ctx context.Context, run *envRun, content []byte) ([]string, error) {
	cfg := run.target.cfg
	collector := &lineCollector{lines: []string{}}

	writeCtx, cancel := context.WithTimeout(ctx, e.validateTimeout)
	defer cancel()

	if err := e.host.Run(writeCtx, cfg.WriteCmd, content, runEnv(run), cfg.Sudo, collector.collect); err != nil {
		return collector.lines, err
	}

	return collector.lines, nil
}

// restart runs restart_cmd with streamed lines.
func (e *Env) restart(ctx context.Context, run *envRun) error {
	cfg := run.target.cfg
	if cfg.RestartCmd == "" {
		e.progress(ctx, run, model.EnvStepRestart, model.EnvRunSucceeded, "no restart_cmd configured; skipped")

		return nil
	}

	restartCtx, cancel := context.WithTimeout(ctx, e.restartTimeout)
	defer cancel()

	e.progress(ctx, run, model.EnvStepRestart, model.EnvRunRunning, "restarting")

	err := e.host.Run(restartCtx, cfg.RestartCmd, nil, runEnv(run), cfg.Sudo, func(line string) {
		e.progress(ctx, run, model.EnvStepRestart, model.EnvRunRunning, line)
	})
	if err != nil {
		e.logger.Error("env restart failed", "service", run.target.def.ID, "error", err)

		return fmt.Errorf("restart: %w", errEnvRestartFailed)
	}

	e.progress(ctx, run, model.EnvStepRestart, model.EnvRunSucceeded, "restarted")

	return nil
}

// probe polls probe_url until healthy or the timeout elapses.
func (e *Env) probe(ctx context.Context, run *envRun) error {
	cfg := run.target.cfg
	if cfg.ProbeURL == "" || e.prober == nil {
		e.progress(ctx, run, model.EnvStepProbe, model.EnvRunSucceeded, "no probe_url configured; skipped")

		return nil
	}

	timeout := e.probeTimeout
	if cfg.ProbeTimeoutSecs > 0 {
		timeout = time.Duration(cfg.ProbeTimeoutSecs) * time.Second
	}

	probeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	e.progress(ctx, run, model.EnvStepProbe, model.EnvRunRunning, "probing "+cfg.ProbeURL)

	ticker := time.NewTicker(e.probeInterval)
	defer ticker.Stop()

	for {
		result, err := e.prober.Check(probeCtx, cfg.ProbeURL)
		if err == nil && result.Healthy {
			e.progress(ctx, run, model.EnvStepProbe, model.EnvRunSucceeded, "healthy")

			return nil
		}

		select {
		case <-probeCtx.Done():
			return fmt.Errorf("probe: %w", errEnvProbeTimeout)
		case <-ticker.C:
		}
	}
}

// succeed snapshots the applied set, promotes pending entries, and audits.
func (e *Env) succeed(ctx context.Context, run *envRun) {
	serviceID := run.target.def.ID

	revisionID, err := e.store.InsertRevision(ctx, model.EnvRevision{
		ServiceID: serviceID, CreatedBy: run.actor, Summary: run.summary,
		RenderedSHA256: run.sha, Applied: true,
	}, snapshotEntries(run.entries))
	if err != nil {
		e.fail(ctx, run, fmt.Errorf("record: %w", err))

		return
	}

	if err := e.store.MarkApplied(ctx, serviceID, time.Now()); err != nil {
		e.fail(ctx, run, fmt.Errorf("record: %w", err))

		return
	}

	finished := time.Now()
	run.run.RevisionID = &revisionID
	run.run.State = model.EnvRunSucceeded
	run.run.FinishedAt = &finished
	e.progress(ctx, run, model.EnvStepDone, model.EnvRunSucceeded, fmt.Sprintf("applied as revision %d", revisionID))

	detail := "apply " + describeDiff(run.diff)
	if run.summary != "" {
		detail += "; " + run.summary
	}

	e.record(ctx, serviceID, run.actor, model.AuditEnvApply, model.AuditSuccess, detail)
}

// fail restores the previous file when the swap already happened, then
// records the failed run and audits it.
func (e *Env) fail(ctx context.Context, run *envRun, cause error) {
	serviceID := run.target.def.ID

	e.logger.Error("env apply failed", "service", serviceID, "run", run.run.ID, "error", cause)
	e.cleanup(ctx, run)

	if run.swapped {
		e.restoreAndRestart(ctx, run)
	}

	finished := time.Now()
	run.run.State = model.EnvRunFailed
	run.run.FinishedAt = &finished
	run.run.Error = cause.Error()

	// The step that was running is the one that failed; close it so a polled
	// record never shows a "running" step inside a failed run.
	for i := len(run.run.Steps) - 1; i >= 0; i-- {
		if run.run.Steps[i].Status == model.EnvRunRunning {
			run.run.Steps[i].Status = model.EnvRunFailed

			break
		}
	}

	e.progress(ctx, run, model.EnvStepFailed, model.EnvRunFailed, cause.Error())

	detail := "apply failed (" + cause.Error() + ") " + describeDiff(run.diff)
	e.record(ctx, serviceID, run.actor, model.AuditEnvApplyFailed, model.AuditFailure, detail)
}

// restoreAndRestart puts the previous file back and reruns restart_cmd.
func (e *Env) restoreAndRestart(ctx context.Context, run *envRun) {
	serviceID := run.target.def.ID
	cfg := run.target.cfg

	if err := e.restoreFile(ctx, run); err != nil {
		e.logger.Error("env file restore failed", "service", serviceID, "error", err)
		e.progress(ctx, run, model.EnvStepWrite, model.EnvRunFailed, "restoring the previous file failed")

		return
	}

	run.run.Restored = true
	e.progress(ctx, run, model.EnvStepWrite, model.EnvRunSucceeded, "restored previous file")

	if cfg.RestartCmd == "" {
		return
	}

	restartCtx, cancel := context.WithTimeout(ctx, e.restartTimeout)
	defer cancel()

	err := e.host.Run(restartCtx, cfg.RestartCmd, nil, runEnv(run), cfg.Sudo, func(line string) {
		e.progress(ctx, run, model.EnvStepRestart, model.EnvRunRunning, line)
	})
	if err != nil {
		e.logger.Error("env restart after restore failed", "service", serviceID, "error", err)
		e.progress(ctx, run, model.EnvStepRestart, model.EnvRunFailed, "restart after restore failed")

		return
	}

	e.progress(ctx, run, model.EnvStepRestart, model.EnvRunSucceeded, "restarted on previous file")
}

// restoreFile puts the previous file back: the .bak when touchgrass
// installed the file, or the applied revision re-rendered through
// write_cmd when the command owns the install.
func (e *Env) restoreFile(ctx context.Context, run *envRun) error {
	cfg := run.target.cfg

	if cfg.WriteCmd == "" {
		return e.host.Restore(ctx, cfg.Target.Path, run.hadPrevious, cfg.Sudo)
	}

	snapshot, err := e.appliedSnapshot(ctx, run.target.def.ID)
	if err != nil {
		return err
	}

	if len(snapshot) == 0 {
		return errEnvNoRevision
	}

	previous := make([]model.EnvEntry, 0, len(snapshot))

	for key, entry := range snapshot {
		previous = append(previous, model.EnvEntry{
			ServiceID: run.target.def.ID, Key: key, Ciphertext: entry.Ciphertext, Nonce: entry.Nonce,
			KeyID: entry.KeyID, IsSecret: entry.IsSecret, State: model.EnvStateApplied,
		})
	}

	rendered, err := e.render(previous)
	if err != nil {
		return err
	}

	defer secret.Zero(rendered.data)

	if _, err := e.pipeWrite(ctx, run, rendered.data); err != nil {
		return err
	}

	return nil
}

// progress appends to the run log, persists it, and emits the SSE event.
func (e *Env) progress(ctx context.Context, run *envRun, name, status, message string) {
	at := time.Now()

	if len(run.run.Steps) < envMaxRunSteps {
		run.run.Steps = append(run.run.Steps, model.EnvRunStep{Name: name, Status: status, Message: message, At: at})
	}

	writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), envPostRunTimeout)
	defer cancel()

	if err := e.store.UpdateRun(writeCtx, run.run); err != nil && !errors.Is(err, store.ErrEnvRunNotFound) {
		e.logger.Warn("env run update failed", "service", run.target.def.ID, "error", err)
	}

	e.emit(model.Event{
		Type: model.EventEnvApply, ServiceID: run.target.def.ID, RunID: run.run.ID,
		Step: name, Message: message, At: at,
	})
}

// describeDiff renders key names grouped by change for audit details.
func describeDiff(diff model.EnvDiff) string {
	return fmt.Sprintf("added [%s] changed [%s] removed [%s]",
		strings.Join(diff.Added, ","), strings.Join(diff.Changed, ","), strings.Join(diff.Removed, ","))
}

// Revisions lists revisions newest first with key-level diffs against
// their predecessors.
func (e *Env) Revisions(ctx context.Context, serviceID string) ([]model.EnvRevisionView, error) {
	if _, err := e.resolve(ctx, serviceID); err != nil {
		return nil, err
	}

	revisions, err := e.store.ListRevisions(ctx, serviceID, envRevisionListLimit+1)
	if err != nil {
		return nil, err
	}

	snapshots := make([][]model.EnvRevisionEntry, len(revisions))

	for i, revision := range revisions {
		snapshots[i], err = e.store.RevisionEntries(ctx, revision.ID)
		if err != nil {
			return nil, err
		}
	}

	views := make([]model.EnvRevisionView, 0, len(revisions))

	for i, revision := range revisions {
		if i >= envRevisionListLimit {
			break
		}

		var previous []model.EnvRevisionEntry
		if i+1 < len(snapshots) {
			previous = snapshots[i+1]
		}

		diff, err := e.snapshotDiff(serviceID, previous, snapshots[i])
		if err != nil {
			return nil, err
		}

		views = append(views, model.EnvRevisionView{
			ID: revision.ID, CreatedAt: revision.CreatedAt, CreatedBy: revision.CreatedBy, Summary: revision.Summary,
			Added: diff.Added, Changed: diff.Changed, Removed: diff.Removed,
			RenderedSHA256: revision.RenderedSHA256, Applied: revision.Applied,
		})
	}

	return views, nil
}

// snapshotDiff compares two snapshots by decrypted value.
func (e *Env) snapshotDiff(
	serviceID string,
	previous, current []model.EnvRevisionEntry,
) (model.EnvDiff, error) {
	diff := model.EnvDiff{Added: []string{}, Changed: []string{}, Removed: []string{}}
	before := make(map[string]model.EnvRevisionEntry, len(previous))

	for _, entry := range previous {
		before[entry.Key] = entry
	}

	seen := map[string]bool{}

	for _, entry := range current {
		seen[entry.Key] = true

		old, ok := before[entry.Key]
		if !ok {
			diff.Added = append(diff.Added, entry.Key)

			continue
		}

		same, err := e.sameSnapshotValue(serviceID, old, entry)
		if err != nil {
			return model.EnvDiff{}, err
		}

		if !same || old.IsSecret != entry.IsSecret {
			diff.Changed = append(diff.Changed, entry.Key)
		}
	}

	for _, entry := range previous {
		if !seen[entry.Key] {
			diff.Removed = append(diff.Removed, entry.Key)
		}
	}

	return diff, nil
}

// sameSnapshotValue decrypts two snapshot entries and compares them.
func (e *Env) sameSnapshotValue(serviceID string, left, right model.EnvRevisionEntry) (bool, error) {
	a, err := e.decrypt(serviceID, left.Key, left.Ciphertext, left.Nonce)
	if err != nil {
		return false, err
	}

	defer secret.Zero(a)

	b, err := e.decrypt(serviceID, right.Key, right.Ciphertext, right.Nonce)
	if err != nil {
		return false, err
	}

	defer secret.Zero(b)

	return string(a) == string(b), nil
}
