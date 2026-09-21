package store

import (
	"context"
	"errors"
	"testing"

	"github.com/oralecarlangelo/touchgrass/internal/model"
)

func TestRuleSeeded(t *testing.T) {
	t.Parallel()

	db := openTestDB(t)

	rules, err := NewRuleStore(db).ListByService(context.Background(), testServiceAPI)
	if err != nil {
		t.Fatalf("ListByService() error = %v, want nil", err)
	}

	if len(rules) != 1 || rules[0].Metric != model.MetricMem || rules[0].Threshold != 85 {
		t.Fatalf("seeded rules = %+v, want one mem>85 rule", rules)
	}
}

func TestRuleCreateDelete(t *testing.T) {
	t.Parallel()

	db := openTestDB(t)
	ctx := context.Background()
	rules := NewRuleStore(db)

	created, err := rules.Create(ctx, model.RuleCreate{
		ServiceID: testServiceAPI, Metric: model.MetricCPU, Threshold: 90, DurationSecs: 60,
	})
	if err != nil {
		t.Fatalf("Create() error = %v, want nil", err)
	}

	if created.ID == 0 || !created.Enabled {
		t.Errorf("Create() = %+v, want id and enabled", created)
	}

	if err := rules.Delete(ctx, created.ID); err != nil {
		t.Fatalf("Delete() error = %v, want nil", err)
	}

	if err := rules.Delete(ctx, created.ID); !errors.Is(err, ErrRuleNotFound) {
		t.Errorf("Delete() error = %v, want ErrRuleNotFound", err)
	}
}

func TestRuleAllEnabled(t *testing.T) {
	t.Parallel()

	db := openTestDB(t)

	rules, err := NewRuleStore(db).AllEnabled(context.Background())
	if err != nil {
		t.Fatalf("AllEnabled() error = %v, want nil", err)
	}

	if len(rules) != 3 {
		t.Errorf("AllEnabled() = %d rules, want 3 seeded", len(rules))
	}
}
