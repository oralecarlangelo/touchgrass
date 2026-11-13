package service

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/oralecarlangelo/touchgrass/internal/dbmigrate"
	"github.com/oralecarlangelo/touchgrass/internal/docker"
	"github.com/oralecarlangelo/touchgrass/internal/model"
	"github.com/oralecarlangelo/touchgrass/internal/secret"
	"github.com/oralecarlangelo/touchgrass/internal/store"
)

const (
	// migrationOutputMax is how much of a command's output is kept: the
	// tail, because that is where a failure explains itself.
	migrationOutputMax = 256 << 10
	// migrationFinishTimeout bounds the bookkeeping after a command.
	migrationFinishTimeout = 10 * time.Second
	// migrationInterrupted is stored on runs a restart cut short.
	migrationInterrupted = "touchgrass restarted while this command was running. " +
		"Its outcome is unknown: check the migration status before running it again."
)

// MigrationCommand is a request to run one migration command.
type MigrationCommand struct {
	// Kind is deploy, resolve, or seed.
	Kind string
	// Migration is the migration a resolve concerns.
	Migration string
	// Resolution is applied or rolled_back, for a resolve.
	Resolution string
	// Confirm must equal the service name: these commands write to the
	// application's database.
	Confirm string
}

// Runs lists a service's newest commands, without output.
func (m *Migrations) Runs(ctx context.Context, serviceID string) ([]model.MigrationRun, error) {
	if _, err := m.services.Get(ctx, serviceID); err != nil {
		return nil, fmt.Errorf("loading service: %w", err)
	}

	runs, err := m.runs.List(ctx, serviceID, migrationRunsLimit)
	if err != nil {
		return nil, fmt.Errorf("listing migration runs: %w", err)
	}

	return runs, nil
}

// Run returns one command of a service with its output.
func (m *Migrations) Run(ctx context.Context, serviceID string, id int64) (model.MigrationRun, error) {
	run, err := m.runs.Get(ctx, serviceID, id)
	if err != nil {
		return model.MigrationRun{}, fmt.Errorf("loading migration run: %w", err)
	}

	return run, nil
}

// Start runs one migration command inside the service's container and
// returns as soon as it is under way. One command runs per service at
// a time. The outcome lands on the run and in the audit log.
func (m *Migrations) Start(
	ctx context.Context,
	serviceID string,
	command MigrationCommand,
	actor string,
) (model.MigrationRun, error) {
	def, err := m.services.Get(ctx, serviceID)
	if err != nil {
		return model.MigrationRun{}, fmt.Errorf("loading service: %w", err)
	}

	if command.Confirm != def.Name {
		return model.MigrationRun{}, ErrMigrationConfirmation
	}

	setup, err := m.load(ctx, serviceID)
	if err != nil {
		return model.MigrationRun{}, err
	}

	scrub := setup.url

	args, err := migrationArgs(setup.tool, command)
	if err != nil {
		secret.Zero(scrub)

		return model.MigrationRun{}, err
	}

	plan, err := m.plan(ctx, def, setup, command)
	if err != nil {
		secret.Zero(scrub)

		return model.MigrationRun{}, err
	}

	prefix := setup.row.Command
	if len(prefix) == 0 {
		prefix = setup.tool.DefaultCommand()
	}

	plan.argv = append(append([]string{}, prefix...), args...)

	run := model.MigrationRun{
		ServiceID: serviceID,
		Kind:      command.Kind,
		Tool:      setup.tool.Name(),
		Detail:    strings.Join(plan.argv, " "),
		Actor:     actor,
		State:     model.MigrationRunRunning,
		Container: plan.container.Name,
		Image:     plan.container.Image,
		StartedAt: time.Now().UTC(),
	}

	runCtx, release, err := m.claim(ctx, serviceID)
	if err != nil {
		secret.Zero(scrub)

		return model.MigrationRun{}, err
	}

	id, err := m.runs.Create(ctx, run)
	if err != nil {
		release()
		secret.Zero(scrub)

		return model.MigrationRun{}, fmt.Errorf("creating migration run: %w", err)
	}

	run.ID = id

	m.group.Go(func() {
		defer release()
		defer secret.Zero(scrub)

		m.execute(runCtx, run, plan, scrub)
	})

	return run, nil
}

// migrationArgs maps a request to the tool's arguments.
func migrationArgs(tool dbmigrate.Tool, command MigrationCommand) ([]string, error) {
	var action dbmigrate.Action

	switch {
	case command.Kind == model.MigrationRunDeploy:
		action = dbmigrate.ActionDeploy
	case command.Kind == model.MigrationRunSeed:
		action = dbmigrate.ActionSeed
	case command.Kind == model.MigrationRunResolve && command.Resolution == model.MigrationResolveApplied:
		action = dbmigrate.ActionResolveApplied
	case command.Kind == model.MigrationRunResolve && command.Resolution == model.MigrationResolveRolledBack:
		action = dbmigrate.ActionResolveRolledBack
	case command.Kind == model.MigrationRunResolve:
		return nil, fmt.Errorf("%w: resolution must be applied or rolled_back", ErrInvalidInput)
	default:
		return nil, fmt.Errorf("%w: kind must be deploy, resolve, or seed", ErrInvalidInput)
	}

	args, err := tool.Command(action, command.Migration)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidInput, err)
	}

	return args, nil
}

// migrationPlan is where and what one command runs.
type migrationPlan struct {
	container docker.Container
	workdir   string
	argv      []string
}

// plan finds the container and checks that the command makes sense
// against what is there now: a resolve needs a migration that failed,
// and a seed needs a build that declares one.
func (m *Migrations) plan(
	ctx context.Context,
	def model.Service,
	setup migrationSetup,
	command MigrationCommand,
) (migrationPlan, error) {
	found, err := m.container(ctx, def, setup.row.Container)
	if err != nil {
		return migrationPlan{}, err
	}

	workdir, err := m.workdir(ctx, found, setup.row.Workdir)
	if err != nil {
		return migrationPlan{}, err
	}

	switch command.Kind {
	case model.MigrationRunResolve:
		if err := m.requireFailed(ctx, setup, command.Migration); err != nil {
			return migrationPlan{}, err
		}
	case model.MigrationRunSeed:
		source, shipped := m.readShipped(ctx, def, setup)
		if source.SeedCommand == "" {
			return migrationPlan{}, fmt.Errorf("%w: the running build declares no seed (%s)",
				ErrInvalidInput, shipped.Target)
		}
	}

	return migrationPlan{container: found, workdir: workdir}, nil
}

// requireFailed refuses a resolve of a migration the history does not
// record as failed. Resolving anything else rewrites a healthy record.
func (m *Migrations) requireFailed(ctx context.Context, setup migrationSetup, name string) error {
	records, history := m.readHistory(ctx, setup)
	if !history.OK {
		return fmt.Errorf("%w: the migration history cannot be read: %s", ErrConflict, history.Error)
	}

	for _, migration := range dbmigrate.Compare(records, nil, false) {
		if migration.Name == name && migration.State == model.MigrationFailed {
			return nil
		}
	}

	return fmt.Errorf("%w: migration %q is not recorded as failed", ErrInvalidInput, name)
}

// claim takes the service's single run slot and returns the context
// the command runs under, detached from the request that started it.
func (m *Migrations) claim(ctx context.Context, serviceID string) (context.Context, func(), error) {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	if _, busy := m.running[serviceID]; busy {
		return nil, nil, ErrMigrationBusy
	}

	runCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), m.runTimeout)
	m.running[serviceID] = cancel

	var once sync.Once

	release := func() {
		once.Do(func() {
			cancel()

			m.mutex.Lock()
			defer m.mutex.Unlock()

			delete(m.running, serviceID)
		})
	}

	return runCtx, release, nil
}

// execute runs the command to its end and records the outcome.
func (m *Migrations) execute(ctx context.Context, run model.MigrationRun, plan migrationPlan, scrub []byte) {
	output := newTailBuffer(migrationOutputMax)

	exitCode, err := m.docker.Exec(ctx, plan.container.ID, docker.ExecSpec{
		Cmd:        plan.argv,
		WorkingDir: plan.workdir,
	}, output)

	result := store.MigrationRunResult{
		State:      model.MigrationRunSucceeded,
		ExitCode:   &exitCode,
		FinishedAt: time.Now().UTC(),
	}

	text, truncated := output.Text()

	switch {
	case err != nil:
		result.State = model.MigrationRunFailed
		result.ExitCode = nil
		text += "\n[touchgrass] " + err.Error()
	case exitCode != 0:
		result.State = model.MigrationRunFailed
	}

	result.Output = redact(text, scrub)
	result.Truncated = truncated

	// The command is over; its record must land even though ctx may be.
	finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), migrationFinishTimeout)
	defer cancel()

	if err := m.runs.Finish(finishCtx, run.ID, result); err != nil {
		m.logger.Error("finishing migration run failed", "service", run.ServiceID, "run", run.ID, "error", err)
	}

	m.record(finishCtx, run.ServiceID, run.Actor, migrationAuditAction(run.Kind),
		migrationAuditResult(result.State), migrationAuditDetail(run, result))
}

// migrationAuditAction maps a run kind to its audit action.
func migrationAuditAction(kind string) string {
	switch kind {
	case model.MigrationRunSeed:
		return model.AuditSeedRun
	case model.MigrationRunResolve:
		return model.AuditMigrationResolve
	default:
		return model.AuditMigrationDeploy
	}
}

// migrationAuditResult maps a run state to an audit result.
func migrationAuditResult(state string) string {
	if state == model.MigrationRunSucceeded {
		return model.AuditSuccess
	}

	return model.AuditFailure
}

// migrationAuditDetail describes a finished run in one line.
func migrationAuditDetail(run model.MigrationRun, result store.MigrationRunResult) string {
	exit := "no exit code"
	if result.ExitCode != nil {
		exit = fmt.Sprintf("exit %d", *result.ExitCode)
	}

	return fmt.Sprintf("run %d: %s in %s (%s)", run.ID, run.Detail, run.Container, exit)
}

// Recover fails the runs a restart cut short and audits each. Call it
// once at startup, before serving.
func (m *Migrations) Recover(ctx context.Context) error {
	interrupted, err := m.runs.ListRunning(ctx)
	if err != nil {
		return fmt.Errorf("listing interrupted migration runs: %w", err)
	}

	if len(interrupted) == 0 {
		return nil
	}

	if _, err := m.runs.FailRunning(ctx, migrationInterrupted); err != nil {
		return fmt.Errorf("failing interrupted migration runs: %w", err)
	}

	for _, run := range interrupted {
		m.record(ctx, run.ServiceID, run.Actor, migrationAuditAction(run.Kind), model.AuditFailure,
			fmt.Sprintf("run %d: %s interrupted by a restart; outcome unknown", run.ID, run.Detail))
	}

	return nil
}

// Close cancels running commands and waits for their bookkeeping.
func (m *Migrations) Close() {
	m.mutex.Lock()

	for _, cancel := range m.running {
		cancel()
	}

	m.mutex.Unlock()

	m.group.Wait()
}

// tailBuffer keeps the last limit bytes written to it.
type tailBuffer struct {
	mutex     sync.Mutex
	limit     int
	data      []byte
	truncated bool
}

// newTailBuffer builds a tailBuffer holding at most limit bytes.
func newTailBuffer(limit int) *tailBuffer {
	return &tailBuffer{limit: limit, data: []byte{}}
}

// Write appends p, dropping the oldest bytes over the limit.
func (b *tailBuffer) Write(p []byte) (int, error) {
	b.mutex.Lock()
	defer b.mutex.Unlock()

	b.data = append(b.data, p...)

	// Compact only at twice the limit, so a chatty command does not
	// copy the whole tail on every write.
	if len(b.data) > 2*b.limit {
		b.data = append([]byte{}, b.data[len(b.data)-b.limit:]...)
		b.truncated = true
	}

	return len(p), nil
}

// Text returns what was kept and whether older output was dropped.
func (b *tailBuffer) Text() (string, bool) {
	b.mutex.Lock()
	defer b.mutex.Unlock()

	kept := b.data
	truncated := b.truncated

	if over := len(kept) - b.limit; over > 0 {
		kept = kept[over:]
		truncated = true
	}

	return strings.ToValidUTF8(string(kept), "?"), truncated
}
