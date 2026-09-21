package store

import (
	"context"
	"testing"
	"time"

	"github.com/oralecarlangelo/touchgrass/internal/model"
)

// testOccurrence builds one exception occurrence.
func testOccurrence() model.Occurrence {
	return model.Occurrence{
		ServiceID: testServiceAPI, Type: model.OccurrenceException,
		Message: "boom",
		Stack: []model.StackFrame{
			{Function: "handler", File: "app.js", Line: 10, Column: 3},
		},
		Breadcrumbs: []model.Breadcrumb{
			{At: "2026-09-21T00:00:00Z", Category: "nav", Message: "route"},
		},
		Release: "v1.2.3",
	}
}

func TestOccurrenceStoreInsert(t *testing.T) {
	t.Parallel()

	db := openTestDB(t)
	ctx := context.Background()
	occurrences := NewOccurrenceStore(db)

	created, err := occurrences.Insert(ctx, testOccurrence())
	if err != nil {
		t.Fatalf("Insert() error = %v, want nil", err)
	}

	if created.ID == 0 {
		t.Fatal("Insert() id = 0, want non-zero")
	}

	count, err := occurrences.CountByService(ctx, testServiceAPI)
	if err != nil {
		t.Fatalf("CountByService() error = %v, want nil", err)
	}

	if count != 1 {
		t.Errorf("CountByService() = %d, want 1", count)
	}
}

func TestOccurrenceStoreList(t *testing.T) {
	t.Parallel()

	db := openTestDB(t)
	ctx := context.Background()
	occurrences := NewOccurrenceStore(db)

	if _, err := occurrences.Insert(ctx, testOccurrence()); err != nil {
		t.Fatalf("Insert() error = %v, want nil", err)
	}

	listed, err := occurrences.ListByService(ctx, testServiceAPI, 10)
	if err != nil {
		t.Fatalf("ListByService() error = %v, want nil", err)
	}

	if len(listed) != 1 {
		t.Fatalf("ListByService() = %d rows, want 1", len(listed))
	}

	checkOccurrenceShape(t, listed[0])
}

// checkOccurrenceShape validates the stored report fields.
func checkOccurrenceShape(t *testing.T, got model.Occurrence) {
	t.Helper()

	if got.Message != "boom" || got.Release != "v1.2.3" || got.Type != model.OccurrenceException {
		t.Errorf("ListByService() = %+v, want the inserted report", got)
	}

	if len(got.Stack) != 1 || got.Stack[0].Function != "handler" {
		t.Errorf("ListByService() stack = %+v, want one frame", got.Stack)
	}

	if len(got.Breadcrumbs) != 1 || got.Breadcrumbs[0].Category != "nav" {
		t.Errorf("ListByService() breadcrumbs = %+v, want one crumb", got.Breadcrumbs)
	}
}

func TestOccurrenceStoreTrimBeyondCap(t *testing.T) {
	t.Parallel()

	db := openTestDB(t)
	ctx := context.Background()
	occurrences := NewOccurrenceStore(db)

	for range 5 {
		if _, err := occurrences.Insert(ctx, testOccurrence()); err != nil {
			t.Fatalf("Insert() error = %v, want nil", err)
		}
	}

	trimmed, err := occurrences.TrimBeyondCap(ctx, testServiceAPI, 3)
	if err != nil {
		t.Fatalf("TrimBeyondCap() error = %v, want nil", err)
	}

	if trimmed != 2 {
		t.Errorf("TrimBeyondCap() = %d, want 2", trimmed)
	}

	listed, err := occurrences.ListByService(ctx, testServiceAPI, 10)
	if err != nil {
		t.Fatalf("ListByService() error = %v, want nil", err)
	}

	if len(listed) != 3 {
		t.Fatalf("ListByService() = %d rows, want 3 newest", len(listed))
	}

	if listed[0].ID < listed[2].ID {
		t.Errorf("ListByService() keeps oldest, want newest first kept")
	}

	trimmed, err = occurrences.TrimBeyondCap(ctx, "tn-fe", 3)
	if err != nil {
		t.Fatalf("TrimBeyondCap() error = %v, want nil", err)
	}

	if trimmed != 0 {
		t.Errorf("TrimBeyondCap() = %d, want 0 for empty service", trimmed)
	}
}

func TestOccurrenceStoreTrimBefore(t *testing.T) {
	t.Parallel()

	db := openTestDB(t)
	ctx := context.Background()
	occurrences := NewOccurrenceStore(db)

	if _, err := occurrences.Insert(ctx, testOccurrence()); err != nil {
		t.Fatalf("Insert() error = %v, want nil", err)
	}

	trimmed, err := occurrences.TrimBefore(ctx, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("TrimBefore() error = %v, want nil", err)
	}

	if trimmed != 1 {
		t.Errorf("TrimBefore() = %d, want 1", trimmed)
	}
}
