package service

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/oralecarlangelo/touchgrass/internal/docker"
	"github.com/oralecarlangelo/touchgrass/internal/model"
	"github.com/oralecarlangelo/touchgrass/internal/store"
)

const (
	defaultSecurityProcessInterval = 30 * time.Second
	defaultSecurityWorkers         = 4
)

type processInventory interface {
	Containers(context.Context, model.ContainerQuery) ([]model.ContainerInstance, error)
}

// ProcessScannerConfig wires bounded runtime-process monitoring.
type ProcessScannerConfig struct {
	Inventory processInventory
	Docker    docker.ProcessLister
	Security  *Security
	Policies  *store.SecurityStore
	Interval  time.Duration
	Workers   int
	Logger    *slog.Logger
}

// ProcessScanner periodically scans only current running containers.
type ProcessScanner struct {
	inventory processInventory
	docker    docker.ProcessLister
	security  *Security
	policies  *store.SecurityStore
	interval  time.Duration
	workers   int
	logger    *slog.Logger
	mu        sync.Mutex
	baselines map[string]int
	spikes    map[string]int
}

// NewProcessScanner builds a bounded process scanner.
func NewProcessScanner(cfg ProcessScannerConfig) *ProcessScanner {
	interval := cfg.Interval
	if interval <= 0 {
		interval = defaultSecurityProcessInterval
	}
	workers := cfg.Workers
	if workers <= 0 {
		workers = defaultSecurityWorkers
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &ProcessScanner{
		inventory: cfg.Inventory, docker: cfg.Docker, security: cfg.Security, policies: cfg.Policies,
		interval: interval, workers: workers, logger: logger,
		baselines: map[string]int{}, spikes: map[string]int{},
	}
}

// Run scans immediately and at the configured interval until cancellation.
func (s *ProcessScanner) Run(ctx context.Context) {
	if s == nil || s.inventory == nil || s.docker == nil || s.security == nil || s.policies == nil {
		return
	}
	s.scanAndLog(ctx)
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.scanAndLog(ctx)
		}
	}
}

func (s *ProcessScanner) scanAndLog(ctx context.Context) {
	if err := s.Scan(ctx); err != nil && ctx.Err() == nil {
		s.logger.Warn("container process scan degraded", "error_category", "security_process_scan")
	}
}

// Scan performs one bounded running-container process scan. Per-container top
// failures are represented as partial observations and are not fatal.
func (s *ProcessScanner) Scan(ctx context.Context) error {
	activation, err := s.policies.Activation(ctx)
	if err != nil {
		return fmt.Errorf("reading process security activation: %w", err)
	}
	if activation.ActivatedAt == nil {
		return nil
	}
	instances, err := s.inventory.Containers(ctx, model.ContainerQuery{State: "running", Limit: 1000})
	if err != nil {
		return fmt.Errorf("listing running containers for process scan: %w", err)
	}
	jobs := make(chan model.ContainerInstance)
	errs := make(chan error, len(instances))
	var group sync.WaitGroup
	for range min(s.workers, max(len(instances), 1)) {
		group.Go(func() {
			for instance := range jobs {
				if err := s.scanContainer(ctx, instance); err != nil {
					errs <- err
				}
			}
		})
	}
	for _, instance := range instances {
		if instance.State != "running" || !instance.Present {
			continue
		}
		select {
		case <-ctx.Done():
			close(jobs)
			group.Wait()
			return ctx.Err()
		case jobs <- instance:
		}
	}
	close(jobs)
	group.Wait()
	close(errs)
	for err := range errs {
		return err
	}
	return nil
}

func (s *ProcessScanner) scanContainer(ctx context.Context, instance model.ContainerInstance) error {
	policy, err := s.policies.Policy(ctx, instance.ServiceID)
	if err != nil {
		return err
	}
	snapshot, scanErr := s.docker.Processes(ctx, instance.ID)
	if scanErr != nil {
		snapshot = docker.ProcessSnapshot{Processes: []docker.Process{}, Complete: false}
		s.logger.Warn("container process collection failed", "container", instance.Name,
			"error_category", "docker_top_unavailable")
	}
	baseline, sustained := s.observeCount(instance.ID, snapshot)
	for _, observation := range EvaluateProcesses(instance.ID, snapshot, policy, baseline, sustained) {
		observation.ObservedAt = time.Now().UTC()
		if _, err := s.security.Evaluate(ctx, model.SecurityEvaluation{
			Observation: observation, BaselineFingerprint: instance.ConfigurationFingerprint,
			ServiceID: instance.ServiceID,
		}); err != nil {
			return fmt.Errorf("evaluating process security for container %q: %w", instance.ID, err)
		}
	}
	return nil
}

func (s *ProcessScanner) observeCount(containerID string, snapshot docker.ProcessSnapshot) (int, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	baseline, exists := s.baselines[containerID]
	if !snapshot.Complete {
		return baseline, false
	}
	if !exists {
		s.baselines[containerID] = len(snapshot.Processes)
		return len(snapshot.Processes), false
	}
	spike := baseline > 0 && len(snapshot.Processes) >= max(baseline*3, baseline+20)
	if spike {
		s.spikes[containerID]++
	} else {
		s.spikes[containerID] = 0
	}
	return baseline, s.spikes[containerID] >= 2
}

// EvaluateProcesses returns the complete process rule set for one snapshot.
func EvaluateProcesses(
	containerID string,
	snapshot docker.ProcessSnapshot,
	policy model.SecurityPolicy,
	baseline int,
	sustainedSpike bool,
) []model.SecurityObservation {
	commands := make([]string, 0, len(snapshot.Processes))
	kernelLike := false
	highRisk := ""
	parents := make(map[int]docker.Process, len(snapshot.Processes))
	for _, process := range snapshot.Processes {
		parents[process.PID] = process
		commands = append(commands, process.Command)
		if strings.HasPrefix(process.Command, "[") && strings.HasSuffix(process.Command, "]") {
			kernelLike = true
		}
		if highRisk == "" && isHighRiskExecutable(process.Command) &&
			!slices.Contains(policy.AllowedExecutablePaths, process.Command) {
			highRisk = process.Command
		}
	}
	parentMismatch := false
	for _, process := range snapshot.Processes {
		parent, ok := parents[process.ParentPID]
		if ok && isRootProcess(process.User) && !isRootProcess(parent.User) {
			parentMismatch = true
			break
		}
	}
	coverage := snapshot.Complete
	return []model.SecurityObservation{
		processObservation(containerID, "process.kernel_like", model.SecuritySeverityCritical, kernelLike, coverage,
			map[string]string{"detected": fmt.Sprintf("%t", kernelLike)}),
		processObservation(containerID, "process.high_risk_path", model.SecuritySeverityHigh, highRisk != "", coverage,
			map[string]string{"executable": highRisk}),
		processObservation(containerID, "process.parent_child_mismatch", model.SecuritySeverityHigh, parentMismatch, coverage,
			map[string]string{"detected": fmt.Sprintf("%t", parentMismatch)}),
		processObservation(containerID, "process.pid_spike", model.SecuritySeverityHigh, sustainedSpike, coverage,
			map[string]string{"process_count": fmt.Sprintf("%d", len(commands)), "baseline_count": fmt.Sprintf("%d", baseline)}),
	}
}

func processObservation(
	containerID, rule string,
	severity model.SecuritySeverity,
	detected, coverage bool,
	evidence map[string]string,
) model.SecurityObservation {
	return model.SecurityObservation{
		ContainerID: containerID, RuleID: rule, Category: model.SecurityCategoryProcess,
		Severity: severity, Confidence: model.SecurityConfidenceHigh,
		Detected: detected, CoverageComplete: coverage, Evidence: evidence,
	}
}

func isHighRiskExecutable(command string) bool {
	return strings.HasPrefix(command, "/tmp/") || strings.HasPrefix(command, "/var/tmp/") ||
		strings.HasPrefix(command, "/dev/shm/")
}

func isRootProcess(user string) bool {
	return user == "0" || strings.EqualFold(user, "root")
}
