package service

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/oralecarlangelo/touchgrass/internal/model"
	"github.com/oralecarlangelo/touchgrass/internal/store"
)

// ingestReport ingests one report or fails the test.
func ingestReport(
	t *testing.T,
	ingestor *Ingestor,
	plaintext, message, release string,
) {
	t.Helper()

	report := testReport()
	report.Message = message
	report.Release = release

	sampled, err := ingestor.Ingest(context.Background(), plaintext, report)
	if err != nil {
		t.Fatalf("Ingest() error = %v, want nil", err)
	}

	if !sampled {
		t.Fatal("Ingest() sampled = false, want true at rate 1")
	}
}

func TestIngestGroupsIssues(t *testing.T) {
	t.Parallel()

	ingestor, _ := testIngestor(t, 1000)
	ctx := context.Background()
	plaintext := mintKey(t, ingestor, testServiceAPI, 1)

	ingestReport(t, ingestor, plaintext, "user 101 not found", "v1")
	ingestReport(t, ingestor, plaintext, "user 202 not found", "v1")
	ingestReport(t, ingestor, plaintext, "user 303 not found", "v2")
	ingestReport(t, ingestor, plaintext, "payment declined", "v1")

	issues, err := ingestor.Issues(ctx, testServiceAPI)
	if err != nil {
		t.Fatalf("Issues() error = %v, want nil", err)
	}

	if len(issues) != 2 {
		t.Fatalf("Issues() = %d issues, want 2", len(issues))
	}

	grouped := findCountedIssue(t, issues, 3)

	if len(grouped.Releases) != 2 {
		t.Errorf("grouped releases = %v, want [v1 v2]", grouped.Releases)
	}

	checkGroupedIssue(t, ingestor, grouped.ID)
}

// findCountedIssue returns the issue with the wanted count.
func findCountedIssue(t *testing.T, issues []model.Issue, want int64) model.Issue {
	t.Helper()

	for _, issue := range issues {
		if issue.Count == want {
			return issue
		}
	}

	t.Fatalf("Issues() = %+v, want one count-%d group", issues, want)

	return model.Issue{}
}

// checkGroupedIssue validates one grouped issue and its occurrences.
func checkGroupedIssue(t *testing.T, ingestor *Ingestor, id int64) {
	t.Helper()

	ctx := context.Background()

	got, err := ingestor.Issue(ctx, id)
	if err != nil {
		t.Fatalf("Issue() error = %v, want nil", err)
	}

	if got.Count != 3 || got.Fingerprint == "" {
		t.Errorf("Issue() = %+v, want grouped issue", got)
	}

	occurrences, err := ingestor.IssueOccurrences(ctx, id, 10)
	if err != nil {
		t.Fatalf("IssueOccurrences() error = %v, want nil", err)
	}

	if len(occurrences) != 3 {
		t.Fatalf("IssueOccurrences() = %d rows, want 3", len(occurrences))
	}

	if occurrences[0].IssueID == nil || *occurrences[0].IssueID != id {
		t.Errorf("occurrence issue = %v, want %d", occurrences[0].IssueID, id)
	}
}

func TestIssueLogs(t *testing.T) {
	t.Parallel()

	ingestor, db := testIngestor(t, 1000)
	ctx := context.Background()
	plaintext := mintKey(t, ingestor, testServiceAPI, 1)

	ingestReport(t, ingestor, plaintext, "user 101 not found", "v1")

	issues, err := ingestor.Issues(ctx, testServiceAPI)
	if err != nil {
		t.Fatalf("Issues() error = %v, want nil", err)
	}

	now := time.Now()
	lines := []model.LogLine{
		{ServiceID: testServiceAPI, Container: testBlueContainerName, Stream: model.LogStdout, Line: "before", Ts: now.Add(-30 * time.Second)},
		{ServiceID: testServiceAPI, Container: testBlueContainerName, Stream: model.LogStderr, Line: "during", Ts: now.Add(30 * time.Second)},
		{ServiceID: testServiceAPI, Container: testBlueContainerName, Stream: model.LogStdout, Line: "ancient", Ts: now.Add(-2 * time.Hour)},
	}

	if _, err := store.NewLogStore(db).InsertBatch(ctx, lines); err != nil {
		t.Fatalf("InsertBatch() error = %v, want nil", err)
	}

	got, err := ingestor.IssueLogs(ctx, issues[0].ID, 0, 10)
	if err != nil {
		t.Fatalf("IssueLogs() error = %v, want nil", err)
	}

	if len(got) != 2 || got[0].Line != "during" || got[1].Line != "before" {
		t.Errorf("IssueLogs() = %+v, want the two windowed lines newest first", got)
	}

	narrow, err := ingestor.IssueLogs(ctx, issues[0].ID, 1, 10)
	if err != nil {
		t.Fatalf("IssueLogs() error = %v, want nil", err)
	}

	if len(narrow) != 0 {
		t.Errorf("IssueLogs() narrow = %d lines, want 0 outside ±1s", len(narrow))
	}

	if _, err := ingestor.IssueLogs(ctx, 9999, 0, 10); !errors.Is(err, store.ErrIssueNotFound) {
		t.Errorf("IssueLogs() error = %v, want ErrIssueNotFound", err)
	}
}

func TestIssueReadsReject(t *testing.T) {
	t.Parallel()

	ingestor, _ := testIngestor(t, 1000)
	ctx := context.Background()

	if _, err := ingestor.Issues(ctx, testUnknownServiceID); err == nil {
		t.Error("Issues() error = nil, want unknown service error")
	}

	if _, err := ingestor.Issue(ctx, 9999); !errors.Is(err, store.ErrIssueNotFound) {
		t.Errorf("Issue() error = %v, want ErrIssueNotFound", err)
	}

	if _, err := ingestor.IssueOccurrences(ctx, 9999, 10); !errors.Is(err, store.ErrIssueNotFound) {
		t.Errorf("IssueOccurrences() error = %v, want ErrIssueNotFound", err)
	}

	if _, err := ingestor.RecentOccurrences(ctx, testUnknownServiceID, 10); err == nil {
		t.Error("RecentOccurrences() error = nil, want unknown service error")
	}
}

func TestRecentOccurrences(t *testing.T) {
	t.Parallel()

	ingestor, _ := testIngestor(t, 1000)
	ctx := context.Background()
	plaintext := mintKey(t, ingestor, testServiceAPI, 1)

	ingestReport(t, ingestor, plaintext, "first boom", "v1")
	ingestReport(t, ingestor, plaintext, "second boom", "v1")

	got, err := ingestor.RecentOccurrences(ctx, testServiceAPI, 10)
	if err != nil {
		t.Fatalf("RecentOccurrences() error = %v, want nil", err)
	}

	if len(got) != 2 || got[0].Message != "second boom" || got[1].Message != "first boom" {
		t.Fatalf("RecentOccurrences() = %+v, want newest first", got)
	}

	one, err := ingestor.RecentOccurrences(ctx, testServiceAPI, 1)
	if err != nil {
		t.Fatalf("RecentOccurrences() error = %v, want nil", err)
	}

	if len(one) != 1 || one[0].Message != "second boom" {
		t.Errorf("limited = %+v, want newest one", one)
	}
}

func TestIssueRulesRoundTrip(t *testing.T) {
	t.Parallel()

	ingestor, _ := testIngestor(t, 1000)
	ctx := context.Background()

	created, err := ingestor.CreateIssueRule(ctx, model.IssueRuleCreate{
		ServiceID: testServiceAPI, Kind: model.IssueRuleSpike, Threshold: 10, WindowSecs: 300,
	})
	if err != nil {
		t.Fatalf("CreateIssueRule() error = %v, want nil", err)
	}

	if !created.Enabled || created.Threshold != 10 {
		t.Errorf("CreateIssueRule() = %+v, want enabled spike", created)
	}

	rules, err := ingestor.IssueRules(ctx, testServiceAPI)
	if err != nil {
		t.Fatalf("IssueRules() error = %v, want nil", err)
	}

	if len(rules) != 1 {
		t.Fatalf("IssueRules() = %d rules, want 1", len(rules))
	}

	if err := ingestor.DeleteIssueRule(ctx, created.ID); err != nil {
		t.Fatalf("DeleteIssueRule() error = %v, want nil", err)
	}

	if err := ingestor.DeleteIssueRule(ctx, created.ID); !errors.Is(err, store.ErrIssueRuleNotFound) {
		t.Errorf("DeleteIssueRule() error = %v, want ErrIssueRuleNotFound", err)
	}
}

func TestCreateIssueRuleRejects(t *testing.T) {
	t.Parallel()

	ingestor, _ := testIngestor(t, 1000)
	ctx := context.Background()

	tests := []struct {
		name   string
		create model.IssueRuleCreate
	}{
		{
			name:   "unknown service",
			create: model.IssueRuleCreate{ServiceID: testUnknownServiceID, Kind: model.IssueRuleSpike, Threshold: 5, WindowSecs: 60},
		},
		{
			name:   "bad kind",
			create: model.IssueRuleCreate{ServiceID: testServiceAPI, Kind: "flaky", Threshold: 5, WindowSecs: 60},
		},
		{
			name:   "spike needs threshold",
			create: model.IssueRuleCreate{ServiceID: testServiceAPI, Kind: model.IssueRuleSpike, WindowSecs: 60},
		},
		{
			name:   "window needs seconds",
			create: model.IssueRuleCreate{ServiceID: testServiceAPI, Kind: model.IssueRuleNewIssue},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if _, err := ingestor.CreateIssueRule(ctx, tt.create); err == nil {
				t.Error("CreateIssueRule() error = nil, want error")
			}
		})
	}

	created, err := ingestor.CreateIssueRule(ctx, model.IssueRuleCreate{
		ServiceID: testServiceAPI, Kind: model.IssueRuleNewIssue, Threshold: 99, WindowSecs: 60,
	})
	if err != nil {
		t.Fatalf("CreateIssueRule() error = %v, want nil", err)
	}

	if created.Threshold != 0 {
		t.Errorf("new-issue threshold = %d, want normalized 0", created.Threshold)
	}
}

// TestStormGroupsUnderCap is the S8 validation: a synthetic error storm
// groups into few issues and stays under the write-time cap.
func TestStormGroupsUnderCap(t *testing.T) {
	t.Parallel()

	ingestor, _ := testIngestor(t, 100000)
	ctx := context.Background()
	plaintext := mintKey(t, ingestor, testServiceAPI, 1)

	bugs := []string{"user %d not found", "order %d timeout", "payment %d declined"}
	releases := []string{"v1", "v2"}

	for i := range 300 {
		ingestReport(t, ingestor, plaintext, fmt.Sprintf(bugs[i%3], i), releases[i%2])
	}

	issues, err := ingestor.Issues(ctx, testServiceAPI)
	if err != nil {
		t.Fatalf("Issues() error = %v, want nil", err)
	}

	if len(issues) != 3 {
		t.Fatalf("Issues() = %d issues, want 3 from the storm", len(issues))
	}

	for _, issue := range issues {
		if issue.Count != 100 {
			t.Errorf("issue %q count = %d, want 100", issue.Title, issue.Count)
		}

		if len(issue.Releases) != 2 {
			t.Errorf("issue %q releases = %v, want [v1 v2]", issue.Title, issue.Releases)
		}
	}

	capped, cappedDB := testIngestor(t, 50)
	cappedPlaintext := mintKey(t, capped, testServiceAPI, 1)

	for i := range 300 {
		ingestReport(t, capped, cappedPlaintext, fmt.Sprintf(bugs[i%3], i), releases[i%2])
	}

	count, err := store.NewOccurrenceStore(cappedDB).CountByService(ctx, testServiceAPI)
	if err != nil {
		t.Fatalf("CountByService() error = %v, want nil", err)
	}

	if count > 50 {
		t.Errorf("CountByService() = %d, want under the 50-row cap", count)
	}

	cappedIssues, err := capped.Issues(ctx, testServiceAPI)
	if err != nil {
		t.Fatalf("Issues() error = %v, want nil", err)
	}

	if len(cappedIssues) != 3 {
		t.Errorf("Issues() = %d, want 3 groups surviving the cap", len(cappedIssues))
	}
}
