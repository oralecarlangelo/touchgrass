package service

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/oralecarlangelo/touchgrass/internal/model"
	"github.com/oralecarlangelo/touchgrass/internal/store"
)

// seedGroupedIssue groups n occurrences and returns the issue id.
func seedGroupedIssue(
	t *testing.T,
	db *store.DB,
	fingerprint, release string,
	n int,
) int64 {
	t.Helper()

	issues := store.NewIssueStore(db)

	var issueID int64

	for range n {
		grouped, err := issues.GroupOccurrence(
			context.Background(), testServiceAPI, fingerprint, "boom "+fingerprint, release,
			model.Occurrence{
				ServiceID: testServiceAPI, Type: model.OccurrenceException,
				Message: "boom", Release: release,
			},
		)
		if err != nil {
			t.Fatalf("GroupOccurrence() error = %v, want nil", err)
		}

		issueID = *grouped.IssueID
	}

	return issueID
}

// seedIssueRule stores one enabled rule.
func seedIssueRule(t *testing.T, db *store.DB, kind string, threshold, window int) {
	t.Helper()

	if _, err := store.NewIssueRuleStore(db).Create(context.Background(), model.IssueRule{
		ServiceID: testServiceAPI, Kind: kind,
		Threshold: threshold, WindowSecs: window, Enabled: true,
	}); err != nil {
		t.Fatalf("Create() error = %v, want nil", err)
	}
}

// issueNotifications lists issue-kind notifications.
func issueNotifications(t *testing.T, db *store.DB) []model.Notification {
	t.Helper()

	all, err := store.NewNotificationStore(db).List(context.Background(), "", 100)
	if err != nil {
		t.Fatalf("List() error = %v, want nil", err)
	}

	issues := []model.Notification{}

	for _, note := range all {
		if note.Kind == model.NotificationIssue {
			issues = append(issues, note)
		}
	}

	return issues
}

func TestEvaluateNewIssue(t *testing.T) {
	t.Parallel()

	sampler, db := testSampler(t, stubStatsLister{})
	ctx := context.Background()

	seedGroupedIssue(t, db, "fp-fresh", "v1", 2)
	seedIssueRule(t, db, model.IssueRuleNewIssue, 0, 3600)

	if err := sampler.evaluateIssues(ctx); err != nil {
		t.Fatalf("evaluateIssues() error = %v, want nil", err)
	}

	notes := issueNotifications(t, db)
	if len(notes) != 1 {
		t.Fatalf("notifications = %d, want 1 new-issue note", len(notes))
	}

	// The latch holds: a second cycle must not refire.
	if err := sampler.evaluateIssues(ctx); err != nil {
		t.Fatalf("evaluateIssues() error = %v, want nil", err)
	}

	if notes := issueNotifications(t, db); len(notes) != 1 {
		t.Errorf("notifications = %d, want still 1 after relatch", len(notes))
	}
}

func TestEvaluateSpike(t *testing.T) {
	t.Parallel()

	sampler, db := testSampler(t, stubStatsLister{})
	ctx := context.Background()

	seedGroupedIssue(t, db, "fp-spike", "v1", 5)
	seedIssueRule(t, db, model.IssueRuleSpike, 5, 3600)

	if err := sampler.evaluateIssues(ctx); err != nil {
		t.Fatalf("evaluateIssues() error = %v, want nil", err)
	}

	if notes := issueNotifications(t, db); len(notes) != 1 {
		t.Fatalf("notifications = %d, want 1 spike note", len(notes))
	}

	// Edge-triggered: still above threshold, no refire.
	if err := sampler.evaluateIssues(ctx); err != nil {
		t.Fatalf("evaluateIssues() error = %v, want nil", err)
	}

	if notes := issueNotifications(t, db); len(notes) != 1 {
		t.Errorf("notifications = %d, want still 1 while firing", len(notes))
	}
}

func TestEvaluateSpikeBelowThreshold(t *testing.T) {
	t.Parallel()

	sampler, db := testSampler(t, stubStatsLister{})
	ctx := context.Background()

	seedGroupedIssue(t, db, "fp-quiet", "v1", 2)
	seedIssueRule(t, db, model.IssueRuleSpike, 5, 3600)

	if err := sampler.evaluateIssues(ctx); err != nil {
		t.Fatalf("evaluateIssues() error = %v, want nil", err)
	}

	if notes := issueNotifications(t, db); len(notes) != 0 {
		t.Errorf("notifications = %d, want 0 below threshold", len(notes))
	}
}

func TestTrimIssues(t *testing.T) {
	t.Parallel()

	db := openInventoryDB(t)
	sampler := NewSampler(SamplerConfig{
		Services:      store.NewServiceStore(db),
		Docker:        stubStatsLister{},
		Metrics:       store.NewMetricStore(db),
		Rules:         store.NewRuleStore(db),
		Notifications: store.NewNotificationStore(db),
		Deploys:       store.NewDeployStore(db),
		Occurrences:   store.NewOccurrenceStore(db),
		Issues:        store.NewIssueStore(db),
		IssueRules:    store.NewIssueRuleStore(db),
		Logs:          store.NewLogStore(db),
		SDKLogs:       store.NewSDKLogStore(db),
		Interval:      time.Hour,
		Retention: Retention{
			Metrics: time.Hour, Notifications: time.Hour, Deploys: time.Hour,
			Errors: time.Nanosecond, Logs: time.Nanosecond, SDKLogs: time.Hour,
		},
		Logger: slog.New(slog.DiscardHandler),
	})

	seedGroupedIssue(t, db, "fp-stale", "v1", 1)

	sampler.trim(context.Background())

	issues, err := store.NewIssueStore(db).ListByService(context.Background(), testServiceAPI, 10)
	if err != nil {
		t.Fatalf("ListByService() error = %v, want nil", err)
	}

	if len(issues) != 0 {
		t.Errorf("ListByService() = %d issues, want 0 after retention trim", len(issues))
	}
}
