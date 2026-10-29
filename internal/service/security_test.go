package service

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/oralecarlangelo/touchgrass/internal/model"
	"github.com/oralecarlangelo/touchgrass/internal/store"
)

func TestSecurityEvaluateDeduplicatesOpenEpisode(t *testing.T) {
	t.Parallel()
	security, db := testSecurity(t)
	evaluation := testSecurityEvaluation(true, true)
	first, err := security.Evaluate(context.Background(), evaluation)
	if err != nil {
		t.Fatal(err)
	}
	second, err := security.Evaluate(context.Background(), evaluation)
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != second.ID || second.OccurrenceCount != 2 {
		t.Errorf("episodes = (%+v, %+v), want one finding with two occurrences", first, second)
	}
	findings, err := store.NewSecurityStore(db).Findings(context.Background(), model.SecurityFindingQuery{})
	if err != nil || len(findings) != 1 {
		t.Fatalf("findings = %+v, %v", findings, err)
	}
}

func TestSecurityEvaluateCreatesRecovery(t *testing.T) {
	t.Parallel()
	security, _ := testSecurity(t)
	if _, err := security.Evaluate(context.Background(), testSecurityEvaluation(true, true)); err != nil {
		t.Fatal(err)
	}
	recovered, err := security.Evaluate(context.Background(), testSecurityEvaluation(false, true))
	if err != nil {
		t.Fatal(err)
	}
	if recovered.Status != model.SecurityFindingStatusResolved || recovered.ResolvedBy != "collector" {
		t.Errorf("recovered = %+v, want collector resolution", recovered)
	}
}

func TestSecurityEvaluateDoesNotResolveOnPartialCoverage(t *testing.T) {
	t.Parallel()
	security, _ := testSecurity(t)
	opened, err := security.Evaluate(context.Background(), testSecurityEvaluation(true, true))
	if err != nil {
		t.Fatal(err)
	}
	partial, err := security.Evaluate(context.Background(), testSecurityEvaluation(false, false))
	if err != nil {
		t.Fatal(err)
	}
	if partial.ID != 0 {
		t.Errorf("partial result = %+v, want no transition", partial)
	}
	findings, err := security.Findings(context.Background(), model.SecurityFindingQuery{Status: model.SecurityFindingStatusOpen})
	if err != nil || len(findings) != 1 || findings[0].ID != opened.ID {
		t.Errorf("open findings = %+v, %v", findings, err)
	}
}

func TestSecurityEvaluateReopensNewEpisode(t *testing.T) {
	t.Parallel()
	security, _ := testSecurity(t)
	first, _ := security.Evaluate(context.Background(), testSecurityEvaluation(true, true))
	_, _ = security.Evaluate(context.Background(), testSecurityEvaluation(false, true))
	second, err := security.Evaluate(context.Background(), testSecurityEvaluation(true, true))
	if err != nil {
		t.Fatal(err)
	}
	if second.ID == first.ID || second.Episode != 2 {
		t.Errorf("reopened = %+v after %+v, want episode 2", second, first)
	}
}

func TestSecuritySuppressRequiresActorAndReason(t *testing.T) {
	t.Parallel()
	security, _ := testSecurity(t)
	finding, _ := security.Evaluate(context.Background(), testSecurityEvaluation(true, true))
	if _, err := security.Suppress(context.Background(), finding.ID, model.FindingSuppression{}); err == nil {
		t.Fatal("Suppress() error = nil, want actor/reason validation")
	}
}

func TestCriticalFindingCreatesOneSecurityNotification(t *testing.T) {
	t.Parallel()
	security, db := testSecurity(t)
	evaluation := testSecurityEvaluation(true, true)
	if _, err := security.Evaluate(context.Background(), evaluation); err != nil {
		t.Fatal(err)
	}
	if _, err := security.Evaluate(context.Background(), evaluation); err != nil {
		t.Fatal(err)
	}
	notes, err := store.NewNotificationStore(db).List(context.Background(), "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(notes) != 1 || notes[0].Kind != model.NotificationSecurity {
		t.Errorf("notifications = %+v, want one security notification", notes)
	}
}

func testSecurity(t *testing.T) (*Security, *store.DB) {
	t.Helper()
	db := openInventoryDB(t)
	security := NewSecurity(SecurityConfig{
		Store: store.NewSecurityStore(db), Notifications: store.NewNotificationStore(db),
		Emit: func(model.Event) {}, Logger: slog.New(slog.DiscardHandler),
	})
	return security, db
}

func testSecurityEvaluation(detected, coverage bool) model.SecurityEvaluation {
	return model.SecurityEvaluation{BaselineFingerprint: "baseline", Observation: model.SecurityObservation{
		ContainerID: "container-id", RuleID: "posture.privileged", Category: model.SecurityCategoryPosture,
		Severity: model.SecuritySeverityCritical, Confidence: model.SecurityConfidenceHigh,
		Detected: detected, CoverageComplete: coverage, Evidence: map[string]string{"privileged": "true"},
		ObservedAt: time.Now().UTC(),
	}}
}
