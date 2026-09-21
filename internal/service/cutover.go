package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/oralecarlangelo/touchgrass/internal/docker"
	"github.com/oralecarlangelo/touchgrass/internal/model"
	"github.com/oralecarlangelo/touchgrass/internal/probe"
	"github.com/oralecarlangelo/touchgrass/internal/store"
	"github.com/samber/oops"
)

// TargetAuto resolves to the idle color.
const TargetAuto = "auto"

const unknownSHA = "unknown"

// postOpTimeout bounds post-run lookups and health verification.
const postOpTimeout = 30 * time.Second

// defaultProbeInterval samples the public URL every second during runs.
const defaultProbeInterval = time.Second

// Runner executes a deploy script, streaming output lines to emit.
type Runner func(ctx context.Context, dir, script string, args []string, emit func(string)) error

// CutoverConfig wires a Cutover.
type CutoverConfig struct {
	Services      *store.ServiceStore
	Docker        docker.Lister
	Deploys       *Deploys
	Probes        *store.DeployStore
	Notifications *store.NotificationStore
	Audit         *Audit
	Prober        Prober
	Emit          func(model.Event)
	Run           Runner
	Timeout       time.Duration
	ProbeInterval time.Duration
	Logger        *slog.Logger
}

// Cutover is the generic deploy engine: one async run/verify/record path
// for blue-green cutovers, rollbacks, and recreate deploys. Strategy
// differences end at resolution; execution is strategy-agnostic.
type Cutover struct {
	services      *store.ServiceStore
	docker        docker.Lister
	deploys       *Deploys
	probes        *store.DeployStore
	notifications *store.NotificationStore
	audit         *Audit
	prober        Prober
	emit          func(model.Event)
	run           Runner
	timeout       time.Duration
	probeInterval time.Duration
	logger        *slog.Logger
	mutex         sync.Mutex
	runs          map[string]bool
}

// operation is one resolved async run. Resolution is per-strategy;
// everything downstream only sees these strategy-agnostic fields.
type operation struct {
	def          model.Service
	script       string
	args         []string
	shaService   string
	healthURL    string
	publicURL    string
	target       string
	actor        string
	deployType   string
	verifyHealth bool
}

// NewCutover builds a Cutover. A nil Run defaults to runScript and a
// non-positive ProbeInterval defaults to one second.
func NewCutover(cfg CutoverConfig) *Cutover {
	run := cfg.Run
	if run == nil {
		run = runScript
	}

	probeInterval := cfg.ProbeInterval
	if probeInterval <= 0 {
		probeInterval = defaultProbeInterval
	}

	return &Cutover{
		services:      cfg.Services,
		docker:        cfg.Docker,
		deploys:       cfg.Deploys,
		probes:        cfg.Probes,
		notifications: cfg.Notifications,
		audit:         cfg.Audit,
		prober:        cfg.Prober,
		emit:          cfg.Emit,
		run:           run,
		timeout:       cfg.Timeout,
		probeInterval: probeInterval,
		logger:        cfg.Logger,
		runs:          map[string]bool{},
	}
}

// Start validates and begins an async cutover, returning the resolved target.
func (c *Cutover) Start(ctx context.Context, serviceID, target, actor string) (string, error) {
	def, err := c.services.Get(ctx, serviceID)
	if err != nil {
		return "", err
	}

	if def.Strategy != model.StrategyBlueGreen {
		return "", fmt.Errorf("%w: cutover requires a blue-green service", ErrInvalidInput)
	}

	cfg, err := decodeBlueGreen(def)
	if err != nil {
		c.logger.Error("cutover misconfigured", "service", def.ID, "error", err)

		return "", fmt.Errorf("%w: service %q cutover config is invalid", ErrInvalidInput, def.ID)
	}

	return c.begin(ctx, def, cfg, target, actor)
}

// StartDeploy validates and begins an async recreate deploy, returning the
// deployed compose service name.
func (c *Cutover) StartDeploy(ctx context.Context, serviceID, actor string) (string, error) {
	def, err := c.services.Get(ctx, serviceID)
	if err != nil {
		return "", err
	}

	if def.Strategy != model.StrategyRecreate {
		return "", fmt.Errorf("%w: deploy requires a recreate service", ErrInvalidInput)
	}

	cfg, err := decodeRecreate(def)
	if err != nil {
		c.logger.Error("deploy misconfigured", "service", def.ID, "error", err)

		return "", fmt.Errorf("%w: service %q deploy config is invalid", ErrInvalidInput, def.ID)
	}

	return c.beginDeploy(ctx, def, cfg, actor)
}

// StartRollback validates and begins an async rollback, returning the
// resolved target: the opposite live color for blue-green services, the
// compose service name for recreate services.
func (c *Cutover) StartRollback(ctx context.Context, serviceID, actor string) (string, error) {
	def, err := c.services.Get(ctx, serviceID)
	if err != nil {
		return "", err
	}

	switch def.Strategy {
	case model.StrategyBlueGreen:
		cfg, err := decodeBlueGreen(def)
		if err != nil {
			c.logger.Error("rollback misconfigured", "service", def.ID, "error", err)

			return "", fmt.Errorf("%w: service %q rollback config is invalid", ErrInvalidInput, def.ID)
		}

		return c.beginRollback(ctx, def, cfg, actor)
	case model.StrategyRecreate:
		cfg, err := decodeRecreate(def)
		if err != nil {
			c.logger.Error("rollback misconfigured", "service", def.ID, "error", err)

			return "", fmt.Errorf("%w: service %q rollback config is invalid", ErrInvalidInput, def.ID)
		}

		return c.beginRecreateRollback(ctx, def, cfg, actor)
	case model.StrategyUnknown:
		return "", fmt.Errorf("%w: service %q has unknown strategy", ErrInvalidInput, def.ID)
	default:
		return "", fmt.Errorf("%w: service %q has unknown strategy %q", ErrInvalidInput, def.ID, def.Strategy)
	}
}

// begin guards, resolves, and launches a cutover run.
func (c *Cutover) begin(
	ctx context.Context,
	def model.Service,
	cfg model.BlueGreenConfig,
	target, actor string,
) (string, error) {
	resolved, err := c.resolve(def, cfg, target)
	if err != nil {
		return "", err
	}

	if err := c.claimRun(def.ID); err != nil {
		return "", err
	}

	//nolint:gosec // Cutover intentionally outlives the request; handlers pass a detached context.
	go c.execute(ctx, operation{
		def:        def,
		script:     cfg.CutoverScript,
		args:       cutoverArgs(def, cfg, resolved),
		shaService: colorService(cfg, resolved),
		publicURL:  cfg.PublicURL,
		target:     resolved,
		actor:      actor,
		deployType: model.DeployCutover,
	})

	return resolved, nil
}

// beginRollback guards, resolves, and launches a blue-green rollback run.
func (c *Cutover) beginRollback(
	ctx context.Context,
	def model.Service,
	cfg model.BlueGreenConfig,
	actor string,
) (string, error) {
	resolved, err := c.resolveRollback(def, cfg)
	if err != nil {
		return "", err
	}

	if err := c.claimRun(def.ID); err != nil {
		return "", err
	}

	//nolint:gosec // Rollback intentionally outlives the request; handlers pass a detached context.
	go c.execute(ctx, operation{
		def:          def,
		script:       cfg.CutoverScript,
		args:         cutoverArgs(def, cfg, resolved),
		shaService:   colorService(cfg, resolved),
		healthURL:    colorURL(cfg, resolved),
		publicURL:    cfg.PublicURL,
		target:       resolved,
		actor:        actor,
		deployType:   model.DeployRollback,
		verifyHealth: true,
	})

	return resolved, nil
}

// beginDeploy guards, resolves, and launches a recreate deploy run.
func (c *Cutover) beginDeploy(
	ctx context.Context,
	def model.Service,
	cfg model.RecreateConfig,
	actor string,
) (string, error) {
	if err := resolveRecreate(def, cfg, cfg.DeployScript, "deploy"); err != nil {
		return "", err
	}

	return c.launchRecreate(ctx, recreateLaunch{
		def: def, cfg: cfg, script: cfg.DeployScript,
		deployType: model.DeployDeploy, actor: actor,
	})
}

// beginRecreateRollback guards, resolves, and launches a recreate rollback.
func (c *Cutover) beginRecreateRollback(
	ctx context.Context,
	def model.Service,
	cfg model.RecreateConfig,
	actor string,
) (string, error) {
	if err := resolveRecreate(def, cfg, cfg.RollbackScript, "rollback"); err != nil {
		return "", err
	}

	return c.launchRecreate(ctx, recreateLaunch{
		def: def, cfg: cfg, script: cfg.RollbackScript,
		deployType: model.DeployRollback, actor: actor,
	})
}

// recreateLaunch carries one resolved recreate run.
type recreateLaunch struct {
	def        model.Service
	cfg        model.RecreateConfig
	script     string
	deployType string
	actor      string
}

// launchRecreate claims the run and starts the async recreate operation.
func (c *Cutover) launchRecreate(ctx context.Context, launch recreateLaunch) (string, error) {
	if err := c.claimRun(launch.def.ID); err != nil {
		return "", err
	}

	//nolint:gosec // Recreate runs intentionally outlive the request; handlers pass a detached context.
	go c.execute(ctx, operation{
		def:          launch.def,
		script:       launch.script,
		args:         recreateArgs(launch.def, launch.cfg),
		shaService:   launch.cfg.Service,
		healthURL:    launch.cfg.HealthURL,
		publicURL:    launch.cfg.PublicURL,
		target:       launch.cfg.Service,
		actor:        launch.actor,
		deployType:   launch.deployType,
		verifyHealth: true,
	})

	return launch.cfg.Service, nil
}

// claimRun marks a service as running, rejecting concurrent operations.
func (c *Cutover) claimRun(serviceID string) error {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	if c.runs[serviceID] {
		return fmt.Errorf("%w: deploy already running for %q", ErrConflict, serviceID)
	}

	c.runs[serviceID] = true

	return nil
}

// releaseRun clears a service's running mark.
func (c *Cutover) releaseRun(serviceID string) {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	delete(c.runs, serviceID)
}

// resolve validates the target and script, resolving auto to idle.
func (c *Cutover) resolve(def model.Service, cfg model.BlueGreenConfig, target string) (string, error) {
	switch target {
	case "", TargetAuto:
		target = TargetAuto
	case colorBlue, colorGreen:
	default:
		return "", fmt.Errorf("%w: target %q (want auto, blue, or green)", ErrInvalidInput, target)
	}

	if cfg.CutoverScript == "" {
		return "", fmt.Errorf("%w: cutover is not configured for %q (set cutover_script)", ErrInvalidInput, def.ID)
	}

	if err := checkExecutable(cfg.CutoverScript); err != nil {
		return "", fmt.Errorf("%w: cutover script %q: %w", ErrInvalidInput, cfg.CutoverScript, err)
	}

	if target != TargetAuto {
		return target, nil
	}

	live, err := probe.LiveTarget(cfg.NginxConf, cfg.Marker)
	if err != nil {
		return "", fmt.Errorf("%w: cannot auto-resolve target: live color unknown", ErrInvalidInput)
	}

	switch live {
	case cfg.BlueTarget:
		return colorGreen, nil
	case cfg.GreenTarget:
		return colorBlue, nil
	default:
		return "", fmt.Errorf(
			"%w: cannot auto-resolve target while %q is live; pass blue or green",
			ErrInvalidInput,
			live,
		)
	}
}

// resolveRollback validates the script and resolves the opposite live color.
func (c *Cutover) resolveRollback(def model.Service, cfg model.BlueGreenConfig) (string, error) {
	if cfg.CutoverScript == "" {
		return "", fmt.Errorf("%w: rollback is not configured for %q (set cutover_script)", ErrInvalidInput, def.ID)
	}

	if err := checkExecutable(cfg.CutoverScript); err != nil {
		return "", fmt.Errorf("%w: rollback script %q: %w", ErrInvalidInput, cfg.CutoverScript, err)
	}

	live, err := probe.LiveTarget(cfg.NginxConf, cfg.Marker)
	if err != nil {
		return "", fmt.Errorf("%w: cannot resolve rollback target: live color unknown", ErrInvalidInput)
	}

	switch live {
	case cfg.BlueTarget:
		return colorGreen, nil
	case cfg.GreenTarget:
		return colorBlue, nil
	default:
		return "", fmt.Errorf(
			"%w: cannot roll back while %q is live; rollback supports blue/green only",
			ErrInvalidInput,
			live,
		)
	}
}

// resolveRecreate validates the script, service name, and health URL for a
// recreate deploy or rollback. Noun names the operation in errors.
func resolveRecreate(def model.Service, cfg model.RecreateConfig, script, noun string) error {
	if script == "" {
		return fmt.Errorf("%w: %s is not configured for %q", ErrInvalidInput, noun, def.ID)
	}

	if err := checkExecutable(script); err != nil {
		return fmt.Errorf("%w: %s script %q: %w", ErrInvalidInput, noun, script, err)
	}

	if cfg.Service == "" {
		return fmt.Errorf("%w: %s requires service for %q (set service)", ErrInvalidInput, noun, def.ID)
	}

	if cfg.HealthURL == "" {
		return fmt.Errorf("%w: %s requires health_url for %q (set health_url)", ErrInvalidInput, noun, def.ID)
	}

	return nil
}

// recreateArgs builds script flags from recreate service config.
func recreateArgs(def model.Service, cfg model.RecreateConfig) []string {
	return []string{
		"--service", cfg.Service,
		"--project", def.ComposeProject,
		"--compose-dir", def.ComposeDir,
	}
}

// colorService returns the compose service name for a blue-green target.
func colorService(cfg model.BlueGreenConfig, target string) string {
	if target == colorBlue {
		return cfg.BlueService
	}

	return cfg.GreenService
}

// colorURL returns the direct health URL for a blue-green target.
func colorURL(cfg model.BlueGreenConfig, target string) string {
	if target == colorBlue {
		return cfg.BlueURL
	}

	return cfg.GreenURL
}

// deployEventTypes groups started/progress/finished SSE types.
type deployEventTypes struct {
	started  string
	progress string
	finished string
}

// deployEvents selects SSE types for a cutover or rollback.
func deployEvents(deployType string) deployEventTypes {
	if deployType == model.DeployRollback {
		return deployEventTypes{
			started:  model.EventRollbackStarted,
			progress: model.EventRollbackProgress,
			finished: model.EventRollbackFinished,
		}
	}

	return deployEventTypes{
		started:  model.EventDeployStarted,
		progress: model.EventDeployProgress,
		finished: model.EventDeployFinished,
	}
}

// auditAction maps a deploy type to its audit action.
func auditAction(deployType string) string {
	switch deployType {
	case model.DeployRollback:
		return model.AuditRollback
	case model.DeployDeploy:
		return model.AuditDeploy
	default:
		return model.AuditCutover
	}
}

// auditResult maps a deploy outcome to its audit result.
func auditResult(outcome string) string {
	if outcome == model.DeployFailure {
		return model.AuditFailure
	}

	return model.AuditSuccess
}

// execute runs the script, verifies targets, and records the outcome.
func (c *Cutover) execute(ctx context.Context, op operation) {
	defer c.releaseRun(op.def.ID)

	events := deployEvents(op.deployType)
	started := time.Now()
	c.emit(model.Event{
		Type:      events.started,
		ServiceID: op.def.ID,
		Message:   op.deployType + " to " + op.target + " started",
		At:        started,
	})

	outcome, notes, downtime := c.runAndVerify(ctx, op)
	finished := time.Now()

	postCtx, postCancel := context.WithTimeout(context.Background(), postOpTimeout)
	defer postCancel()

	sha := c.liveSHA(postCtx, op.def, op.shaService)
	c.recordDeploy(postCtx, op, sha, outcome, notes, downtime, started, finished)
	c.finishOperation(postCtx, op, events, outcome, notes, finished)
}

// runAndVerify executes the script, probes downtime, and verifies targets.
func (c *Cutover) runAndVerify(ctx context.Context, op operation) (string, string, *float64) {
	events := deployEvents(op.deployType)

	runCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	stopProbing := c.startProbeLoop(runCtx, op.def.ID, op.publicURL)

	runErr := c.run(
		runCtx,
		op.def.ComposeDir,
		op.script,
		op.args,
		func(line string) {
			c.emit(model.Event{
				Type:      events.progress,
				ServiceID: op.def.ID,
				Message:   line,
				At:        time.Now(),
			})
		},
	)
	downtime := stopProbing()

	if runErr != nil {
		wrapped := oops.
			In("deploy").
			Tags("deploy", op.deployType).
			With("service_id", op.def.ID).
			With("target", op.target).
			Wrapf(runErr, "deploy script failed")
		c.logger.Error("deploy script failed", "service", op.def.ID, "error", wrapped)

		return model.DeployFailure, runErr.Error(), downtime
	}

	if !op.verifyHealth {
		return model.DeploySuccess, "", downtime
	}

	postCtx, postCancel := context.WithTimeout(context.Background(), postOpTimeout)
	defer postCancel()

	if err := c.verifyTarget(postCtx, op); err != nil {
		c.logger.Error("post-deploy health check failed", "service", op.def.ID, "error", err)

		notes := op.deployType + " target " + op.target + " failed post-" + op.deployType + " health check"

		return model.DeployFailure, notes, downtime
	}

	return model.DeploySuccess, "", downtime
}

// startProbeLoop samples url until the returned stop func runs, recording
// each sample and reporting failed-sample seconds. An empty url disables
// probing and reports nil downtime.
func (c *Cutover) startProbeLoop(ctx context.Context, serviceID, url string) func() *float64 {
	if url == "" {
		return func() *float64 { return nil }
	}

	var failed atomic.Int64

	stop := make(chan struct{})

	var wg sync.WaitGroup

	wg.Go(func() {
		ticker := time.NewTicker(c.probeInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-stop:
				return
			case at := <-ticker.C:
				c.sampleProbe(ctx, serviceID, url, at, &failed)
			}
		}
	})

	return func() *float64 {
		close(stop)
		wg.Wait()

		secs := float64(failed.Load()) * c.probeInterval.Seconds()

		return &secs
	}
}

// sampleProbe takes one downtime sample, recording it and counting failures.
func (c *Cutover) sampleProbe(
	ctx context.Context,
	serviceID, url string,
	at time.Time,
	failed *atomic.Int64,
) {
	start := time.Now()
	result, err := c.prober.Check(ctx, url)
	latency := time.Since(start).Milliseconds()

	ok := err == nil && result.Healthy
	if !ok {
		failed.Add(1)
	}

	status := 0
	if err == nil {
		status = result.StatusCode
	}

	writeCtx, cancel := context.WithTimeout(context.Background(), postOpTimeout)
	defer cancel()

	if _, werr := c.probes.InsertProbe(writeCtx, model.DeployProbe{
		ServiceID: serviceID, OK: ok, StatusCode: status,
		LatencyMs: latency, SampledAt: at,
	}); werr != nil {
		c.logger.Warn("deploy probe sample failed", "service", serviceID, "error", werr)
	}
}

// verifyTarget probes the operation target after a successful script run.
func (c *Cutover) verifyTarget(ctx context.Context, op operation) error {
	if c.prober == nil {
		return oops.
			In("deploy").
			Tags("deploy", op.deployType).
			Code("health_check_unavailable").
			With("service_id", op.def.ID).
			Errorf("deploy health check unavailable")
	}

	result, err := c.prober.Check(ctx, op.healthURL)
	if err != nil {
		return oops.
			In("deploy").
			Tags("deploy", op.deployType, "health").
			With("service_id", op.def.ID).
			With("target", op.target).
			Wrapf(err, "post-deploy health check failed")
	}

	if !result.Healthy {
		return oops.
			In("deploy").
			Tags("deploy", op.deployType, "health").
			Code("target_unhealthy").
			With("service_id", op.def.ID).
			With("target", op.target).
			With("status_code", result.StatusCode).
			Errorf("post-deploy health check failed")
	}

	return nil
}

// recordDeploy stores one history entry for an operation.
func (c *Cutover) recordDeploy(
	ctx context.Context,
	op operation,
	sha, outcome, notes string,
	downtime *float64,
	started, finished time.Time,
) {
	if _, err := c.deploys.Record(ctx, model.DeployRecord{
		ServiceID: op.def.ID, SHA: sha, Actor: op.actor,
		Type: op.deployType, Outcome: outcome, Notes: notes,
		StartedAt: &started, FinishedAt: &finished, DowntimeSecs: downtime,
	}); err != nil {
		wrapped := oops.
			In("deploy").
			Tags("deploy", op.deployType).
			With("service_id", op.def.ID).
			Wrapf(err, "recording deploy failed")
		c.logger.Error("recording deploy failed", "service", op.def.ID, "error", wrapped)
	}
}

// finishOperation emits completion, notifies, and audits an operation.
func (c *Cutover) finishOperation(
	ctx context.Context,
	op operation,
	events deployEventTypes,
	outcome, notes string,
	finished time.Time,
) {
	c.emit(model.Event{
		Type:      events.finished,
		ServiceID: op.def.ID,
		Message:   op.deployType + " to " + op.target + ": " + outcome,
		At:        finished,
	})

	title := op.deployType + " " + outcome + ": " + op.def.ID + " → " + op.target

	if _, err := c.notifications.Insert(ctx, op.def.ID, model.NotificationDeploy, title, notes); err != nil {
		c.logger.Warn("deploy notification failed", "service", op.def.ID, "error", err)
	}

	detail := op.deployType + " to " + op.target + ": " + outcome
	if notes != "" {
		detail += "; " + notes
	}

	if _, err := c.audit.Record(ctx, model.AuditRecord{
		ServiceID: op.def.ID, Actor: op.actor, Action: auditAction(op.deployType),
		Result: auditResult(outcome), Detail: detail,
	}); err != nil {
		wrapped := oops.
			In("deploy").
			Tags("deploy", op.deployType, "audit").
			With("service_id", op.def.ID).
			Wrapf(err, "recording audit entry failed")
		c.logger.Error("recording audit entry failed", "service", op.def.ID, "error", wrapped)
	}
}

// liveSHA returns the target service's image id, or unknown.
func (c *Cutover) liveSHA(ctx context.Context, def model.Service, serviceName string) string {
	containers, err := c.docker.List(ctx)
	if err != nil {
		c.logger.Warn("live SHA lookup failed", "service", def.ID, "error", err)

		return unknownSHA
	}

	for _, cont := range containers {
		if !belongsTo(cont, def.ComposeProject, map[string]bool{serviceName: true}) {
			continue
		}

		return cont.ImageID
	}

	return unknownSHA
}

// cutoverArgs builds script flags from service config.
func cutoverArgs(def model.Service, cfg model.BlueGreenConfig, target string) []string {
	args := []string{
		"--target", target,
		"--compose-dir", def.ComposeDir,
		"--nginx-conf", cfg.NginxConf,
		"--blue-server", cfg.BlueTarget,
		"--green-server", cfg.GreenTarget,
		"--legacy-server", cfg.LegacyTarget,
		"--blue-url", cfg.BlueURL,
		"--green-url", cfg.GreenURL,
		"--public-url", cfg.PublicURL,
	}

	if cfg.ComposeFile != "" {
		args = append(args, "--compose-file", cfg.ComposeFile)
	}

	if cfg.Project != "" {
		args = append(args, "--project", cfg.Project)
	}

	if cfg.EnvFile != "" {
		args = append(args, "--env-file", cfg.EnvFile)
	}

	if cfg.Sudo {
		args = append(args, "--sudo")
	} else {
		args = append(args, "--no-sudo")
	}

	if cfg.SettleSecs > 0 {
		args = append(args, "--settle-seconds", strconv.Itoa(cfg.SettleSecs))
	}

	if cfg.HealthTimeout > 0 {
		args = append(args, "--health-timeout", strconv.Itoa(cfg.HealthTimeout))
	}

	if cfg.PublicTimeout > 0 {
		args = append(args, "--public-timeout", strconv.Itoa(cfg.PublicTimeout))
	}

	return args
}

// checkExecutable verifies path is an executable file.
func checkExecutable(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}

	if info.IsDir() {
		return errors.New("is a directory")
	}

	if info.Mode()&0o111 == 0 {
		return errors.New("is not executable")
	}

	return nil
}

// runScript executes the script with args, streaming merged output to emit.
func runScript(
	ctx context.Context,
	dir, script string,
	args []string,
	emit func(string),
) error {
	//nolint:gosec // script is an admin-configured absolute path; args are allowlisted or validated, never raw user input.
	cmd := exec.CommandContext(ctx, script, args...)
	cmd.Dir = dir

	writer := &lineWriter{emit: emit}
	cmd.Stdout = writer
	cmd.Stderr = writer

	if err := cmd.Run(); err != nil {
		writer.flush()

		return fmt.Errorf("deploy script failed: %w", err)
	}

	writer.flush()

	return nil
}

// lineWriter splits streamed output into lines.
type lineWriter struct {
	mutex sync.Mutex
	emit  func(string)
	buf   []byte
}

// Write buffers output and emits complete lines.
func (w *lineWriter) Write(data []byte) (int, error) {
	w.mutex.Lock()
	defer w.mutex.Unlock()

	w.buf = append(w.buf, data...)

	for {
		i := bytes.IndexByte(w.buf, '\n')
		if i < 0 {
			break
		}

		w.emit(string(w.buf[:i]))
		w.buf = append([]byte{}, w.buf[i+1:]...)
	}

	return len(data), nil
}

// flush emits any trailing partial line.
func (w *lineWriter) flush() {
	w.mutex.Lock()
	defer w.mutex.Unlock()

	if len(w.buf) > 0 {
		w.emit(string(w.buf))
		w.buf = nil
	}
}
