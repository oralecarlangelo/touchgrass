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

func TestProcessScannerRuleMatrix(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		processes []docker.Process
		policy    model.SecurityPolicy
		baseline  int
		sustained bool
		rule      string
		detected  bool
	}{
		{name: "kernel-like command", processes: []docker.Process{{PID: 8, ParentPID: 1, User: "root", Command: "[kworker/0:1]"}}, rule: "process.kernel_like", detected: true},
		{name: "high-risk path", processes: []docker.Process{{PID: 8, ParentPID: 1, User: "app", Command: "/dev/shm/update"}}, rule: "process.high_risk_path", detected: true},
		{name: "allowed path", processes: []docker.Process{{PID: 8, ParentPID: 1, User: "app", Command: "/tmp/health"}}, policy: model.SecurityPolicy{AllowedExecutablePaths: []string{"/tmp/health"}}, rule: "process.high_risk_path", detected: false},
		{name: "parent child mismatch", processes: []docker.Process{{PID: 7, ParentPID: 1, User: "app", Command: "app"}, {PID: 8, ParentPID: 7, User: "root", Command: "helper"}}, rule: "process.parent_child_mismatch", detected: true},
		{name: "sustained pid spike", processes: make([]docker.Process, 40), baseline: 10, sustained: true, rule: "process.pid_spike", detected: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := observationByRule(EvaluateProcesses("container", docker.ProcessSnapshot{Processes: test.processes, Complete: true}, test.policy, test.baseline, test.sustained), test.rule)
			if got.RuleID == "" || got.Detected != test.detected {
				t.Fatalf("rule %q = %+v, want detected=%v", test.rule, got, test.detected)
			}
		})
	}
}

func TestProcessScannerFailurePreservesFinding(t *testing.T) {
	t.Parallel()
	security, db := testSecurity(t)
	securityStore := store.NewSecurityStore(db)
	if _, err := securityStore.Activate(context.Background(), "admin"); err != nil {
		t.Fatal(err)
	}
	if _, err := security.Evaluate(context.Background(), model.SecurityEvaluation{Observation: model.SecurityObservation{
		ContainerID: "container", RuleID: "process.high_risk_path", Category: model.SecurityCategoryProcess,
		Severity: model.SecuritySeverityHigh, Confidence: model.SecurityConfidenceHigh,
		Detected: true, CoverageComplete: true, Evidence: map[string]string{"command": "/tmp/update"},
	}}); err != nil {
		t.Fatal(err)
	}
	scanner := NewProcessScanner(ProcessScannerConfig{
		Inventory: processInventoryStub{containers: []model.ContainerInstance{{ID: "container", State: "running", Present: true}}},
		Docker:    processListerStub{err: errors.New("top unavailable")}, Security: security,
		Policies: securityStore, Interval: time.Minute, Workers: 1, Logger: slog.New(slog.DiscardHandler),
	})
	if err := scanner.Scan(context.Background()); err != nil {
		t.Fatalf("Scan() error = %v, want degraded success", err)
	}
	findings, err := security.Findings(context.Background(), model.SecurityFindingQuery{Status: model.SecurityFindingStatusOpen})
	if err != nil {
		t.Fatal(err)
	}
	if !containsFindingRule(findings, "process.high_risk_path") ||
		!containsFindingRule(findings, "collector.security_process.failure") {
		t.Fatalf("open findings = %+v; want preserved process episode and collector incident", findings)
	}
}

func containsFindingRule(findings []model.SecurityFinding, rule string) bool {
	for _, finding := range findings {
		if finding.RuleID == rule {
			return true
		}
	}
	return false
}

func TestProcessScannerStopsOnCancellation(t *testing.T) {
	t.Parallel()
	security, db := testSecurity(t)
	if _, err := store.NewSecurityStore(db).Activate(context.Background(), "admin"); err != nil {
		t.Fatal(err)
	}
	scanner := NewProcessScanner(ProcessScannerConfig{
		Inventory: processInventoryStub{}, Docker: processListerStub{}, Security: security,
		Policies: store.NewSecurityStore(db), Interval: time.Hour, Workers: 1,
		Logger: slog.New(slog.DiscardHandler),
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		scanner.Run(ctx)
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("ProcessScanner.Run() did not stop after cancellation")
	}
}

type processInventoryStub struct{ containers []model.ContainerInstance }

func (s processInventoryStub) Containers(context.Context, model.ContainerQuery) ([]model.ContainerInstance, error) {
	return append([]model.ContainerInstance{}, s.containers...), nil
}

type processListerStub struct {
	snapshot docker.ProcessSnapshot
	err      error
}

func (s processListerStub) Processes(context.Context, string) (docker.ProcessSnapshot, error) {
	return s.snapshot, s.err
}
