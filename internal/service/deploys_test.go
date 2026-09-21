package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/oralecarlangelo/touchgrass/internal/model"
	"github.com/oralecarlangelo/touchgrass/internal/store"
)

func TestRecordHistory(t *testing.T) {
	t.Parallel()

	db := openInventoryDB(t)
	ctx := context.Background()
	deploys := NewDeploys(store.NewServiceStore(db), store.NewDeployStore(db))

	started := time.Now().Add(-time.Minute).Truncate(time.Second)
	finished := time.Now().Truncate(time.Second)

	id, err := deploys.Record(ctx, model.DeployRecord{
		ServiceID: testServiceAPI, SHA: "abc123", Actor: "carl",
		Type: model.DeployManual, Outcome: model.DeploySuccess,
		StartedAt: &started, FinishedAt: &finished, Notes: "scripted",
	})
	if err != nil {
		t.Fatalf("Record() error = %v, want nil", err)
	}

	if id == 0 {
		t.Fatal("Record() id = 0, want non-zero")
	}

	history, err := deploys.History(ctx, testServiceAPI, 10)
	if err != nil {
		t.Fatalf("History() error = %v, want nil", err)
	}

	if len(history) != 1 || history[0].DurationSecs == nil || *history[0].DurationSecs != 60 {
		t.Errorf("history = %+v, want one entry with 60s duration", history)
	}

	if _, err := deploys.History(ctx, testUnknownServiceID, 10); !errors.Is(err, store.ErrServiceNotFound) {
		t.Errorf("History() error = %v, want ErrServiceNotFound", err)
	}
}

func TestRecordValidation(t *testing.T) {
	t.Parallel()

	db := openInventoryDB(t)
	ctx := context.Background()
	deploys := NewDeploys(store.NewServiceStore(db), store.NewDeployStore(db))

	now := time.Now()
	earlier := now.Add(-time.Minute)

	base := model.DeployRecord{
		ServiceID: testServiceAPI, SHA: "abc", Actor: "carl",
		Type: model.DeployManual, Outcome: model.DeploySuccess,
	}

	tests := []struct {
		name   string
		mutate func(*model.DeployRecord)
	}{
		{name: testUnknownServiceName, mutate: func(r *model.DeployRecord) { r.ServiceID = testUnknownServiceID }},
		{name: "bad type", mutate: func(r *model.DeployRecord) { r.Type = testBogusValue }},
		{name: "bad outcome", mutate: func(r *model.DeployRecord) { r.Outcome = testBogusValue }},
		{name: "empty sha", mutate: func(r *model.DeployRecord) { r.SHA = "" }},
		{name: "empty actor", mutate: func(r *model.DeployRecord) { r.Actor = "" }},
		{
			name:   "finished before started",
			mutate: func(r *model.DeployRecord) { r.StartedAt = &now; r.FinishedAt = &earlier },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			record := base
			tt.mutate(&record)

			if _, err := deploys.Record(ctx, record); err == nil {
				t.Errorf("Record() error = nil, want validation error")
			}
		})
	}
}
