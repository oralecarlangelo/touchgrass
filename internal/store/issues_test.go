package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/oralecarlangelo/touchgrass/internal/model"
)

// groupTestOccurrence groups one occurrence under fingerprint fp.
func groupTestOccurrence(
	t *testing.T,
	issues *IssueStore,
	fingerprint, release string,
) model.Occurrence {
	t.Helper()

	grouped, err := issues.GroupOccurrence(
		context.Background(), testServiceAPI, fingerprint, "boom "+fingerprint, release,
		testOccurrence(),
	)
	if err != nil {
		t.Fatalf("GroupOccurrence() error = %v, want nil", err)
	}

	return grouped
}

func TestIssueStoreGroups(t *testing.T) {
	t.Parallel()

	db := openTestDB(t)
	ctx := context.Background()
	issues := NewIssueStore(db)

	first := groupTestOccurrence(t, issues, "fp-one", "v1")
	second := groupTestOccurrence(t, issues, "fp-one", "v2")

	if first.IssueID == nil || second.IssueID == nil || *first.IssueID != *second.IssueID {
		t.Fatalf("issue ids = (%v, %v), want one shared issue", first.IssueID, second.IssueID)
	}

	other := groupTestOccurrence(t, issues, "fp-two", "v1")

	if *other.IssueID == *first.IssueID {
		t.Error("distinct fingerprints share an issue, want separate")
	}

	got, err := issues.Get(ctx, *first.IssueID)
	if err != nil {
		t.Fatalf("Get() error = %v, want nil", err)
	}

	if got.Count != 2 {
		t.Errorf("Get() count = %d, want 2", got.Count)
	}

	if len(got.Releases) != 2 || got.Releases[0] != "v1" || got.Releases[1] != "v2" {
		t.Errorf("Get() releases = %v, want [v1 v2]", got.Releases)
	}

	if got.NotifiedAt != nil {
		t.Errorf("Get() notified = %v, want nil", got.NotifiedAt)
	}

	if _, err := issues.Get(ctx, 9999); !errors.Is(err, ErrIssueNotFound) {
		t.Errorf("Get() error = %v, want ErrIssueNotFound", err)
	}
}

func TestIssueStoreList(t *testing.T) {
	t.Parallel()

	db := openTestDB(t)
	ctx := context.Background()
	issues := NewIssueStore(db)

	groupTestOccurrence(t, issues, "fp-old", "v1")

	time.Sleep(10 * time.Millisecond)
	groupTestOccurrence(t, issues, "fp-new", "v1")
	groupTestOccurrence(t, issues, "fp-new", "v1")

	listed, err := issues.ListByService(ctx, testServiceAPI, 10)
	if err != nil {
		t.Fatalf("ListByService() error = %v, want nil", err)
	}

	if len(listed) != 2 {
		t.Fatalf("ListByService() = %d issues, want 2", len(listed))
	}

	if listed[0].Fingerprint != "fp-new" || listed[0].Count != 2 {
		t.Errorf("ListByService()[0] = %+v, want fp-new newest with count 2", listed[0])
	}

	if len(listed[0].Releases) != 1 || listed[0].Releases[0] != "v1" {
		t.Errorf("ListByService()[0] releases = %v, want [v1]", listed[0].Releases)
	}

	empty, err := issues.ListByService(ctx, "tn-fe", 10)
	if err != nil {
		t.Fatalf("ListByService() error = %v, want nil", err)
	}

	if len(empty) != 0 {
		t.Errorf("ListByService() = %d issues, want 0 for empty service", len(empty))
	}
}

func TestIssueStoreNotifyAndTrim(t *testing.T) {
	t.Parallel()

	db := openTestDB(t)
	ctx := context.Background()
	issues := NewIssueStore(db)
	grouped := groupTestOccurrence(t, issues, "fp-note", "v1")

	unnotified, err := issues.UnnotifiedSince(ctx, testServiceAPI, time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatalf("UnnotifiedSince() error = %v, want nil", err)
	}

	if len(unnotified) != 1 {
		t.Fatalf("UnnotifiedSince() = %d issues, want 1", len(unnotified))
	}

	if err := issues.MarkNotified(ctx, *grouped.IssueID); err != nil {
		t.Fatalf("MarkNotified() error = %v, want nil", err)
	}

	unnotified, err = issues.UnnotifiedSince(ctx, testServiceAPI, time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatalf("UnnotifiedSince() error = %v, want nil", err)
	}

	if len(unnotified) != 0 {
		t.Errorf("UnnotifiedSince() = %d issues, want 0 after notify", len(unnotified))
	}

	trimmed, err := issues.TrimBefore(ctx, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("TrimBefore() error = %v, want nil", err)
	}

	if trimmed != 1 {
		t.Errorf("TrimBefore() = %d, want 1", trimmed)
	}

	if _, err := issues.Get(ctx, *grouped.IssueID); !errors.Is(err, ErrIssueNotFound) {
		t.Errorf("Get() error = %v, want ErrIssueNotFound after trim", err)
	}
}
