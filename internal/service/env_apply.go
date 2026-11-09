package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
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

// errEnvProbeTimeout reports a probe that never turned healthy.
var errEnvProbeTimeout = errors.New("probe did not become healthy before the timeout")

// envRun is one in-flight apply with its mutable state.
type envRun struct {
	target      envTarget
	run         model.EnvRun
	actor       string
	summary     string
	entries     []model.EnvEntry
	diff        model.EnvDiff
	tempPath    string
	hadPrevious bool
	wrote       bool
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
	e.mutex.Lock()
	defer e.mutex.Unlock()

	_, running := e.running[serviceID]

	return running
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

// renderAndValidate renders the candidate, writes it beside the target,
// and runs the validator. A validation failure deletes the candidate.
func (e *Env) renderAndValidate(ctx context.Context, run *envRun) error {
	rendered, err := e.render(run.entries)
	if err != nil {
		return fmt.Errorf("render: %w", err)
	}

	defer secret.Zero(rendered.data)

	run.tempPath = envTempPath(run.target.cfg.Target.Path, run.run.ID)
	e.step(ctx, run, model.EnvStepRender, fmt.Sprintf("rendered %d keys (sha256 %s)", len(rendered.keys), rendered.sha))

	cfg := run.target.cfg

	if err := e.host.WriteFile(ctx, run.tempPath, rendered.data, cfg.Target.Mode, cfg.Target.Owner, cfg.Sudo); err != nil {
		e.logger.Error("env candidate write failed", "service", run.target.def.ID, "error", err)

		return errors.New("render: writing the candidate file failed")
	}

	run.wrote = true

	if cfg.ValidateCmd == "" {
		e.step(ctx, run, model.EnvStepValidate, "no validate_cmd configured; skipped")

		return nil
	}

	lines, err := e.runValidator(ctx, run)
	validation := model.EnvValidation{ServiceID: run.target.def.ID, OK: err == nil, Lines: lines, At: time.Now()}

	if storeErr := e.store.UpsertValidation(ctx, validation); storeErr != nil {
		e.logger.Error("env validation record failed", "service", run.target.def.ID, "error", storeErr)
	}

	if err != nil {
		if removeErr := e.host.Remove(ctx, run.tempPath, cfg.Sudo); removeErr != nil {
			e.logger.Warn("env candidate cleanup failed", "service", run.target.def.ID, "error", removeErr)
		}

		run.wrote = false

		return fmt.Errorf("validate: env_validation_failed: %d lines", len(lines))
	}

	e.step(ctx, run, model.EnvStepValidate, fmt.Sprintf("validation passed (%d lines)", len(lines)))

	return nil
}

// runValidator executes validate_cmd against the candidate and collects
// bounded output lines.
func (e *Env) runValidator(ctx context.Context, run *envRun) ([]string, error) {
	cfg := run.target.cfg
	lines := []string{}
	collect := func(line string) {
		if len(lines) >= envMaxValidationLines {
			return
		}

		if len(line) > envMaxValidationLineLen {
			line = line[:envMaxValidationLineLen]
		}

		lines = append(lines, line)
	}

	validateCtx, cancel := context.WithTimeout(ctx, e.validateTimeout)
	defer cancel()

	env := []string{
		"TOUCHGRASS_ENV_CANDIDATE=" + run.tempPath,
		"TOUCHGRASS_ENV_TARGET=" + cfg.Target.Path,
		"TOUCHGRASS_ENV_SERVICE=" + run.target.def.ID,
	}

	if err := e.host.Run(validateCtx, cfg.ValidateCmd+" "+run.tempPath, env, cfg.Sudo, collect); err != nil {
		e.logger.Warn("env validation failed", "service", run.target.def.ID, "error", err)

		return lines, err
	}

	return lines, nil
}

// writeRestartProbe swaps the file in, restarts the service, and probes it.
func (e *Env) writeRestartProbe(ctx context.Context, run *envRun) error {
	cfg := run.target.cfg

	hadPrevious, err := e.host.Replace(ctx, run.tempPath, cfg.Target.Path, cfg.Sudo)
	if err != nil {
		e.logger.Error("env file replace failed", "service", run.target.def.ID, "error", err)

		return errors.New("write: replacing the target file failed")
	}

	run.hadPrevious = hadPrevious
	run.wrote = false
	e.step(ctx, run, model.EnvStepWrite, "wrote target file (previous kept as .bak)")

	if err := e.restart(ctx, run); err != nil {
		return err
	}

	return e.probe(ctx, run)
}

// restart runs restart_cmd with streamed lines.
func (e *Env) restart(ctx context.Context, run *envRun) error {
	cfg := run.target.cfg
	if cfg.RestartCmd == "" {
		e.step(ctx, run, model.EnvStepRestart, "no restart_cmd configured; skipped")

		return nil
	}

	restartCtx, cancel := context.WithTimeout(ctx, e.restartTimeout)
	defer cancel()

	e.step(ctx, run, model.EnvStepRestart, "restarting")

	err := e.host.Run(restartCtx, cfg.RestartCmd, nil, cfg.Sudo, func(line string) {
		e.step(ctx, run, model.EnvStepRestart, line)
	})
	if err != nil {
		e.logger.Error("env restart failed", "service", run.target.def.ID, "error", err)

		return errors.New("restart: restart command failed")
	}

	return nil
}

// probe polls probe_url until healthy or the timeout elapses.
func (e *Env) probe(ctx context.Context, run *envRun) error {
	cfg := run.target.cfg
	if cfg.ProbeURL == "" || e.prober == nil {
		e.step(ctx, run, model.EnvStepProbe, "no probe_url configured; skipped")

		return nil
	}

	timeout := e.probeTimeout
	if cfg.ProbeTimeoutSecs > 0 {
		timeout = time.Duration(cfg.ProbeTimeoutSecs) * time.Second
	}

	probeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	e.step(ctx, run, model.EnvStepProbe, "probing "+cfg.ProbeURL)

	ticker := time.NewTicker(e.probeInterval)
	defer ticker.Stop()

	for {
		result, err := e.prober.Check(probeCtx, cfg.ProbeURL)
		if err == nil && result.Healthy {
			e.step(ctx, run, model.EnvStepProbe, "healthy")

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

	rendered, err := e.render(run.entries)
	if err != nil {
		e.fail(ctx, run, fmt.Errorf("record: %w", err))

		return
	}

	secret.Zero(rendered.data)

	revisionID, err := e.store.InsertRevision(ctx, model.EnvRevision{
		ServiceID: serviceID, CreatedBy: run.actor, Summary: run.summary,
		RenderedSHA256: rendered.sha, Applied: true,
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
	e.step(ctx, run, model.EnvStepDone, fmt.Sprintf("applied as revision %d", revisionID))

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
	cfg := run.target.cfg

	e.logger.Error("env apply failed", "service", serviceID, "run", run.run.ID, "error", cause)

	if run.wrote {
		if err := e.host.Remove(ctx, run.tempPath, cfg.Sudo); err != nil {
			e.logger.Warn("env candidate cleanup failed", "service", serviceID, "error", err)
		}
	}

	if e.swapped(run) {
		e.restoreAndRestart(ctx, run)
	}

	finished := time.Now()
	run.run.State = model.EnvRunFailed
	run.run.FinishedAt = &finished
	e.step(ctx, run, model.EnvStepFailed, cause.Error())

	detail := "apply failed (" + cause.Error() + ") " + describeDiff(run.diff)
	e.record(ctx, serviceID, run.actor, model.AuditEnvApplyFailed, model.AuditFailure, detail)
}

// swapped reports whether the target file was already replaced.
func (e *Env) swapped(run *envRun) bool {
	for _, step := range run.run.Steps {
		if step.Step == model.EnvStepWrite {
			return true
		}
	}

	return false
}

// restoreAndRestart puts the previous file back and reruns restart_cmd.
func (e *Env) restoreAndRestart(ctx context.Context, run *envRun) {
	serviceID := run.target.def.ID
	cfg := run.target.cfg

	if err := e.host.Restore(ctx, cfg.Target.Path, run.hadPrevious, cfg.Sudo); err != nil {
		e.logger.Error("env file restore failed", "service", serviceID, "error", err)
		e.step(ctx, run, model.EnvStepWrite, "restoring the previous file failed")

		return
	}

	e.step(ctx, run, model.EnvStepWrite, "restored previous file")

	if cfg.RestartCmd == "" {
		return
	}

	restartCtx, cancel := context.WithTimeout(ctx, e.restartTimeout)
	defer cancel()

	err := e.host.Run(restartCtx, cfg.RestartCmd, nil, cfg.Sudo, func(line string) {
		e.step(ctx, run, model.EnvStepRestart, line)
	})
	if err != nil {
		e.logger.Error("env restart after restore failed", "service", serviceID, "error", err)
		e.step(ctx, run, model.EnvStepRestart, "restart after restore failed")

		return
	}

	e.step(ctx, run, model.EnvStepRestart, "restarted on previous file")
}

// step appends to the run log, persists it, and emits the SSE event.
func (e *Env) step(ctx context.Context, run *envRun, step, message string) {
	at := time.Now()

	if len(run.run.Steps) < envMaxRunSteps {
		run.run.Steps = append(run.run.Steps, model.EnvRunStep{Step: step, Message: message, At: at})
	}

	writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), envPostRunTimeout)
	defer cancel()

	if err := e.store.UpdateRun(writeCtx, run.run); err != nil && !errors.Is(err, store.ErrEnvRunNotFound) {
		e.logger.Warn("env run update failed", "service", run.target.def.ID, "error", err)
	}

	e.emit(model.Event{
		Type: model.EventEnvApply, ServiceID: run.target.def.ID, RunID: run.run.ID,
		Step: step, Message: message, At: at,
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
