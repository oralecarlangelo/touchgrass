package service

import (
	"context"
	"errors"
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

func TestSecurityCollectorFailureCreatesOneEpisodeAndRecovery(t *testing.T) {
	t.Parallel()
	db := openInventoryDB(t)
	security := NewSecurity(SecurityConfig{
		Store: store.NewSecurityStore(db), Notifications: store.NewNotificationStore(db),
		Cursors: store.NewCursorStore(db), Emit: func(model.Event) {}, Logger: slog.New(slog.DiscardHandler),
	})
	ctx := context.Background()
	if _, err := store.NewSecurityStore(db).Activate(ctx, "admin"); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := security.RecordCollector(ctx, "security_process", false, "docker_top_unavailable", 30*time.Second); err != nil {
			t.Fatal(err)
		}
	}
	findings, err := security.Findings(ctx, model.SecurityFindingQuery{Status: model.SecurityFindingStatusOpen})
	if err != nil || len(findings) != 1 || findings[0].OccurrenceCount != 2 {
		t.Fatalf("collector findings=%+v err=%v", findings, err)
	}
	if err := security.RecordCollector(ctx, "security_process", true, "", 30*time.Second); err != nil {
		t.Fatal(err)
	}
	if err := security.RecordCollector(ctx, "security_process", true, "", 30*time.Second); err != nil {
		t.Fatal(err)
	}
	resolved, err := security.Findings(ctx, model.SecurityFindingQuery{Status: model.SecurityFindingStatusResolved})
	if err != nil || len(resolved) != 1 {
		t.Fatalf("resolved collector findings=%+v err=%v", resolved, err)
	}
}

func TestSecurityCollectorEvaluationFailureCannotResolve(t *testing.T) {
	t.Parallel()
	security, db := testSecurity(t)
	ctx := context.Background()
	if _, err := store.NewSecurityStore(db).Activate(ctx, "admin"); err != nil {
		t.Fatal(err)
	}
	if err := security.RecordCollector(ctx, "security_diff", false, "docker_diff_unavailable", 10*time.Minute); err != nil {
		t.Fatal(err)
	}
	failing := NewSecurity(SecurityConfig{
		Store: store.NewSecurityStore(db), Cursors: failingCursorWriter{},
		Emit: func(model.Event) {}, Logger: slog.New(slog.DiscardHandler),
	})
	if err := failing.RecordCollector(ctx, "security_diff", true, "", 10*time.Minute); err == nil {
		t.Fatal("RecordCollector() error=nil, want cursor failure")
	}
	open, err := security.Findings(ctx, model.SecurityFindingQuery{Status: model.SecurityFindingStatusOpen})
	if err != nil || len(open) != 1 {
		t.Fatalf("open collector finding=%+v err=%v, want preserved", open, err)
	}
}

type failingCursorWriter struct{}

func (failingCursorWriter) Get(context.Context, string) (model.CollectorCursor, error) {
	return model.CollectorCursor{}, store.ErrCollectorCursorNotFound
}

func (failingCursorWriter) Upsert(context.Context, model.CollectorCursor) error {
	return errors.New("cursor unavailable")
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
