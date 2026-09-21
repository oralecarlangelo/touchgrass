package store

import (
	"context"
	"errors"
	"testing"

	"github.com/oralecarlangelo/touchgrass/internal/model"
)

func TestIssueRuleStoreRoundTrip(t *testing.T) {
	t.Parallel()

	db := openTestDB(t)
	ctx := context.Background()
	rules := NewIssueRuleStore(db)

	created, err := rules.Create(ctx, model.IssueRule{
		ServiceID: testServiceAPI, Kind: model.IssueRuleSpike,
		Threshold: 10, WindowSecs: 300, Enabled: true,
	})
	if err != nil {
		t.Fatalf("Create() error = %v, want nil", err)
	}

	if created.ID == 0 {
		t.Fatal("Create() id = 0, want non-zero")
	}

	if _, err := rules.Create(ctx, model.IssueRule{
		ServiceID: testServiceAPI, Kind: model.IssueRuleNewIssue,
		WindowSecs: 300, Enabled: false,
	}); err != nil {
		t.Fatalf("Create() error = %v, want nil", err)
	}

	listed, err := rules.ListByService(ctx, testServiceAPI)
	if err != nil {
		t.Fatalf("ListByService() error = %v, want nil", err)
	}

	if len(listed) != 2 {
		t.Fatalf("ListByService() = %d rules, want 2", len(listed))
	}

	enabled, err := rules.ListEnabled(ctx)
	if err != nil {
		t.Fatalf("ListEnabled() error = %v, want nil", err)
	}

	if len(enabled) != 1 || enabled[0].ID != created.ID {
		t.Fatalf("ListEnabled() = %+v, want the one enabled rule", enabled)
	}

	if err := rules.Delete(ctx, created.ID); err != nil {
		t.Fatalf("Delete() error = %v, want nil", err)
	}

	if err := rules.Delete(ctx, created.ID); !errors.Is(err, ErrIssueRuleNotFound) {
		t.Errorf("Delete() error = %v, want ErrIssueRuleNotFound", err)
	}
}
