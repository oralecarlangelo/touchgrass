package service

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/oralecarlangelo/touchgrass/internal/docker"
	"github.com/oralecarlangelo/touchgrass/internal/model"
	"github.com/oralecarlangelo/touchgrass/internal/store"
)

const defaultSecurityDiffInterval = 10 * time.Minute

// DiffScannerConfig wires bounded writable-layer monitoring.
type DiffScannerConfig struct {
	Inventory processInventory
	Docker    docker.DiffLister
	Security  *Security
	Policies  *store.SecurityStore
	Interval  time.Duration
	Workers   int
	Logger    *slog.Logger
}

// DiffScanner periodically correlates writable-layer and process metadata.
type DiffScanner struct {
	inventory processInventory
	docker    docker.DiffLister
	security  *Security
	policies  *store.SecurityStore
	interval  time.Duration
	workers   int
	logger    *slog.Logger
}

// NewDiffScanner builds a bounded diff scanner.
func NewDiffScanner(cfg DiffScannerConfig) *DiffScanner {
	interval := cfg.Interval
	if interval <= 0 {
		interval = defaultSecurityDiffInterval
	}
	workers := cfg.Workers
	if workers <= 0 {
		workers = defaultSecurityWorkers
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &DiffScanner{
		inventory: cfg.Inventory, docker: cfg.Docker, security: cfg.Security,
		policies: cfg.Policies, interval: interval, workers: workers, logger: logger,
	}
}

// Run scans immediately and periodically until the shared context stops.
func (s *DiffScanner) Run(ctx context.Context) {
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

func (s *DiffScanner) scanAndLog(ctx context.Context) {
	if err := s.Scan(ctx); err != nil && ctx.Err() == nil {
		s.logger.Warn("container layer scan degraded", "error_category", "security_diff_scan")
	}
}

// Scan performs one bounded scan of current running containers.
func (s *DiffScanner) Scan(ctx context.Context) error {
	activation, err := s.policies.Activation(ctx)
	if err != nil {
		return fmt.Errorf("reading diff security activation: %w", err)
	}
	if activation.ActivatedAt == nil {
		return nil
	}
	instances, err := s.inventory.Containers(ctx, model.ContainerQuery{State: "running", Limit: 1000})
	if err != nil {
		return fmt.Errorf("listing running containers for layer scan: %w", err)
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

func (s *DiffScanner) scanContainer(ctx context.Context, instance model.ContainerInstance) error {
	policy, err := s.policies.Policy(ctx, instance.ServiceID)
	if err != nil {
		return err
	}
	changes, diffErr := s.docker.Diff(ctx, instance.ID)
	complete := diffErr == nil
	if diffErr != nil {
		changes = []docker.LayerChange{}
		s.logger.Warn("container layer collection failed", "container", instance.Name,
			"error_category", "docker_diff_unavailable")
	}
	diffObservations := EvaluateLayerChanges(instance.ID, changes, policy, complete)
	processObservations := []model.SecurityObservation{}
	if processLister, ok := s.docker.(docker.ProcessLister); ok {
		snapshot, processErr := processLister.Processes(ctx, instance.ID)
		if processErr != nil {
			snapshot = docker.ProcessSnapshot{Processes: []docker.Process{}, Complete: false}
		}
		processObservations = EvaluateProcesses(instance.ID, snapshot, policy, len(snapshot.Processes), false)
	}
	observations := append(diffObservations, CorrelateProcessAndDiff(instance.ID, processObservations, diffObservations)...)
	for _, observation := range observations {
		observation.ObservedAt = time.Now().UTC()
		if _, err := s.security.Evaluate(ctx, model.SecurityEvaluation{
			Observation: observation, BaselineFingerprint: instance.ConfigurationFingerprint,
			ServiceID: instance.ServiceID,
		}); err != nil {
			return fmt.Errorf("evaluating layer security for container %q: %w", instance.ID, err)
		}
	}
	return nil
}

// EvaluateLayerChanges produces deterministic aggregate diff observations.
func EvaluateLayerChanges(
	containerID string,
	changes []docker.LayerChange,
	policy model.SecurityPolicy,
	coverage bool,
) []model.SecurityObservation {
	userLocalPath := ""
	addedExecutable := ""
	runtimePath := ""
	counts := map[docker.LayerChangeKind]int{}
	for _, change := range changes {
		counts[change.Kind]++
		if pathAllowed(change.Path, policy.AllowedWritablePaths) {
			continue
		}
		if userLocalPath == "" && (change.Kind == docker.LayerChangeAdded || change.Kind == docker.LayerChangeModified) &&
			pathWithin(change.Path, "/usr/local/bin") {
			userLocalPath = change.Path
		}
		if addedExecutable == "" && change.Kind == docker.LayerChangeAdded && executableLocation(change.Path) {
			addedExecutable = change.Path
		}
		if runtimePath == "" && (change.Kind == docker.LayerChangeModified || change.Kind == docker.LayerChangeDeleted) &&
			runtimeSensitivePath(change.Path) {
			runtimePath = change.Path
		}
	}
	countEvidence := map[string]string{
		"added_count":    fmt.Sprintf("%d", counts[docker.LayerChangeAdded]),
		"modified_count": fmt.Sprintf("%d", counts[docker.LayerChangeModified]),
		"deleted_count":  fmt.Sprintf("%d", counts[docker.LayerChangeDeleted]),
	}
	return []model.SecurityObservation{
		diffObservation(containerID, "diff.user_local_executable", model.SecuritySeverityHigh,
			userLocalPath != "", coverage, mergeEvidence(countEvidence, "path", userLocalPath)),
		diffObservation(containerID, "diff.added_executable", model.SecuritySeverityHigh,
			addedExecutable != "", coverage, mergeEvidence(countEvidence, "path", addedExecutable)),
		diffObservation(containerID, "diff.runtime_modification", model.SecuritySeverityHigh,
			runtimePath != "", coverage, mergeEvidence(countEvidence, "path", runtimePath)),
	}
}

// CorrelateProcessAndDiff emits one explicit critical rule when the same added
// executable is observed running in the same scan window.
func CorrelateProcessAndDiff(
	containerID string,
	processObservations, diffObservations []model.SecurityObservation,
) []model.SecurityObservation {
	processPath, processCoverage := evidenceForRule(processObservations, "process.high_risk_path", "executable")
	diffPath, diffCoverage := evidenceForRule(diffObservations, "diff.added_executable", "path")
	detected := processPath != "" && processPath == diffPath
	evidence := map[string]string{}
	if detected {
		evidence["path"] = processPath
		evidence["contributing_rules"] = "diff.added_executable,process.high_risk_path"
	}
	return []model.SecurityObservation{diffObservation(
		containerID, "correlation.added_executable_running", model.SecuritySeverityCritical,
		detected, processCoverage && diffCoverage, evidence,
	)}
}

func evidenceForRule(values []model.SecurityObservation, rule, key string) (string, bool) {
	for _, value := range values {
		if value.RuleID == rule {
			if value.Detected {
				return value.Evidence[key], value.CoverageComplete
			}
			return "", value.CoverageComplete
		}
	}
	return "", false
}

func diffObservation(containerID, rule string, severity model.SecuritySeverity, detected, coverage bool,
	evidence map[string]string) model.SecurityObservation {
	return model.SecurityObservation{
		ContainerID: containerID, RuleID: rule, Category: model.SecurityCategoryDrift,
		Severity: severity, Confidence: model.SecurityConfidenceHigh,
		Detected: detected, CoverageComplete: coverage, Evidence: evidence,
	}
}

func pathAllowed(value string, allowed []string) bool {
	for _, prefix := range allowed {
		if pathWithin(value, prefix) {
			return true
		}
	}
	return false
}

func pathWithin(value, prefix string) bool {
	prefix = strings.TrimSuffix(prefix, "/")
	return value == prefix || strings.HasPrefix(value, prefix+"/")
}

func executableLocation(value string) bool {
	for _, prefix := range []string{"/tmp", "/var/tmp", "/dev/shm", "/bin", "/sbin", "/usr/bin", "/usr/sbin", "/usr/local/bin"} {
		if pathWithin(value, prefix) {
			return true
		}
	}
	return false
}

func runtimeSensitivePath(value string) bool {
	for _, prefix := range []string{"/etc", "/bin", "/sbin", "/usr/bin", "/usr/sbin", "/usr/local/bin", "/opt", "/app"} {
		if pathWithin(value, prefix) {
			return true
		}
	}
	return false
}

func mergeEvidence(base map[string]string, key, value string) map[string]string {
	result := make(map[string]string, len(base)+1)
	for itemKey, itemValue := range base {
		result[itemKey] = itemValue
	}
	result[key] = value
	return result
}
