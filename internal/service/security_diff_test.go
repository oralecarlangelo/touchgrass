package service

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/oralecarlangelo/touchgrass/internal/docker"
	"github.com/oralecarlangelo/touchgrass/internal/model"
	"github.com/oralecarlangelo/touchgrass/internal/store"
)

func TestDiffScannerFlagsUserLocalExecutable(t *testing.T) {
	t.Parallel()
	got := observationByRule(EvaluateLayerChanges("c", []docker.LayerChange{{Kind: docker.LayerChangeAdded, Path: "/usr/local/bin/agent"}}, model.SecurityPolicy{}, true), "diff.user_local_executable")
	if !got.Detected || got.Severity != model.SecuritySeverityHigh {
		t.Fatalf("observation = %+v, want high detection", got)
	}
}

func TestDiffScannerAllowsConfiguredCache(t *testing.T) {
	t.Parallel()
	got := observationByRule(EvaluateLayerChanges("c", []docker.LayerChange{{Kind: docker.LayerChangeModified, Path: "/var/cache/app/item"}}, model.SecurityPolicy{AllowedWritablePaths: []string{"/var/cache/app"}}, true), "diff.runtime_modification")
	if got.Detected {
		t.Fatalf("observation = %+v, want configured cache allowed", got)
	}
}

func TestDiffScannerFlagsRuntimeModification(t *testing.T) {
	t.Parallel()
	got := observationByRule(EvaluateLayerChanges("c", []docker.LayerChange{{Kind: docker.LayerChangeModified, Path: "/etc/ssh/sshd_config"}}, model.SecurityPolicy{}, true), "diff.runtime_modification")
	if !got.Detected {
		t.Fatalf("observation = %+v, want runtime modification", got)
	}
}

func TestCorrelateAddedExecutableAndRunningProcessCritical(t *testing.T) {
	t.Parallel()
	process := []model.SecurityObservation{{RuleID: "process.high_risk_path", Detected: true, CoverageComplete: true, Evidence: map[string]string{"executable": "/tmp/update"}}}
	diff := []model.SecurityObservation{{RuleID: "diff.added_executable", Detected: true, CoverageComplete: true, Evidence: map[string]string{"path": "/tmp/update"}}}
	got := observationByRule(CorrelateProcessAndDiff("c", process, diff), "correlation.added_executable_running")
	if !got.Detected || got.Severity != model.SecuritySeverityCritical {
		t.Fatalf("correlation = %+v, want critical", got)
	}
}

func TestCorrelationExplainsContributingRules(t *testing.T) {
	t.Parallel()
	process := []model.SecurityObservation{{RuleID: "process.high_risk_path", Detected: true, CoverageComplete: true, Evidence: map[string]string{"executable": "/tmp/update"}}}
	diff := []model.SecurityObservation{{RuleID: "diff.added_executable", Detected: true, CoverageComplete: true, Evidence: map[string]string{"path": "/tmp/update"}}}
	got := observationByRule(CorrelateProcessAndDiff("c", process, diff), "correlation.added_executable_running")
	if got.Evidence["contributing_rules"] != "diff.added_executable,process.high_risk_path" {
		t.Fatalf("contributing rules = %q, want deterministic ordered rules", got.Evidence["contributing_rules"])
	}
}

func TestDiffFailurePreservesPriorFinding(t *testing.T) {
	t.Parallel()
	security, db := testSecurity(t)
	securityStore := store.NewSecurityStore(db)
	if _, err := securityStore.Activate(context.Background(), "admin"); err != nil {
		t.Fatal(err)
	}
	if _, err := security.Evaluate(context.Background(), model.SecurityEvaluation{Observation: model.SecurityObservation{
		ContainerID: "container", RuleID: "diff.runtime_modification", Category: model.SecurityCategoryDrift,
		Severity: model.SecuritySeverityHigh, Confidence: model.SecurityConfidenceHigh,
		Detected: true, CoverageComplete: true, Evidence: map[string]string{"path": "/etc/app.conf"},
	}}); err != nil {
		t.Fatal(err)
	}
	scanner := NewDiffScanner(DiffScannerConfig{
		Inventory: processInventoryStub{containers: []model.ContainerInstance{{ID: "container", State: "running", Present: true}}},
		Docker:    diffListerStub{err: errors.New("diff unavailable")}, Security: security,
		Policies: securityStore, Interval: time.Minute, Workers: 1, Logger: slog.New(slog.DiscardHandler),
	})
	if err := scanner.Scan(context.Background()); err != nil {
		t.Fatalf("Scan() error = %v, want degraded success", err)
	}
	findings, err := security.Findings(context.Background(), model.SecurityFindingQuery{Status: model.SecurityFindingStatusOpen})
	if err != nil {
		t.Fatal(err)
	}
	if !containsFindingRule(findings, "diff.runtime_modification") ||
		!containsFindingRule(findings, "collector.security_diff.failure") {
		t.Fatalf("open findings = %+v; want preserved diff episode and collector incident", findings)
	}
}

type diffListerStub struct {
	changes []docker.LayerChange
	err     error
}

func (s diffListerStub) Diff(context.Context, string) ([]docker.LayerChange, error) {
	return append([]docker.LayerChange{}, s.changes...), s.err
}
