package store

import (
	"context"
	"testing"
	"time"

	"github.com/oralecarlangelo/touchgrass/internal/model"
)

func TestDeployRecord(t *testing.T) {
	t.Parallel()

	db := openTestDB(t)
	ctx := context.Background()
	deploys := NewDeployStore(db)

	started := time.Now().Add(-time.Minute).Truncate(time.Second)
	finished := time.Now().Truncate(time.Second)
	duration := int64(60)

	id, err := deploys.Record(ctx, model.Deploy{
		ServiceID: testServiceAPI, SHA: "abc123", Actor: "carl",
		Type: model.DeployManual, Outcome: model.DeploySuccess,
		StartedAt: &started, FinishedAt: &finished, DurationSecs: &duration,
		Notes: "scripted cutover",
	})
	if err != nil {
		t.Fatalf("Record() error = %v, want nil", err)
	}

	if id == 0 {
		t.Fatal("Record() id = 0, want non-zero")
	}

	got, err := deploys.ListByService(ctx, testServiceAPI, 10)
	if err != nil {
		t.Fatalf("ListByService() error = %v, want nil", err)
	}

	if len(got) != 1 {
		t.Fatalf("ListByService() = %d entries, want 1", len(got))
	}

	entry := got[0]
	if entry.SHA != "abc123" || entry.Actor != "carl" || entry.Notes != "scripted cutover" {
		t.Errorf("entry = %+v, want recorded fields", entry)
	}

	if entry.StartedAt == nil || entry.DurationSecs == nil || *entry.DurationSecs != 60 {
		t.Errorf("entry times = %+v, want parsed timestamps and duration", entry)
	}

	if entry.DowntimeSecs != nil {
		t.Errorf("downtime = %v, want nil (lands in Sprint 6)", entry.DowntimeSecs)
	}

	if entry.CreatedAt.IsZero() {
		t.Error("created_at is zero, want timestamp")
	}
}

func TestDeployProbeRoundTrip(t *testing.T) {
	t.Parallel()

	db := openTestDB(t)
	ctx := context.Background()
	deploys := NewDeployStore(db)

	now := time.Now().Truncate(time.Second)
	samples := []model.DeployProbe{
		{ServiceID: testServiceAPI, OK: false, StatusCode: 500, LatencyMs: 12, SampledAt: now},
		{ServiceID: testServiceAPI, OK: true, StatusCode: 200, LatencyMs: 3, SampledAt: now.Add(time.Second)},
		{ServiceID: "tn-fe", OK: false, StatusCode: 503, LatencyMs: 9, SampledAt: now},
	}

	for i, sample := range samples {
		if _, err := deploys.InsertProbe(ctx, sample); err != nil {
			t.Fatalf("InsertProbe(%d) error = %v, want nil", i, err)
		}
	}

	failed, err := deploys.FailedProbeCount(ctx, testServiceAPI, now.Add(-time.Minute), now.Add(time.Minute))
	if err != nil {
		t.Fatalf("FailedProbeCount() error = %v, want nil", err)
	}

	if failed != 1 {
		t.Errorf("FailedProbeCount() = %d, want 1", failed)
	}

	trimmed, err := deploys.TrimProbes(ctx, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("TrimProbes() error = %v, want nil", err)
	}

	if trimmed != 3 {
		t.Errorf("TrimProbes() = %d, want 3", trimmed)
	}
}

func TestDeployListEmpty(t *testing.T) {
	t.Parallel()

	db := openTestDB(t)

	empty, err := NewDeployStore(db).ListByService(context.Background(), "tn-fe", 10)
	if err != nil {
		t.Fatalf("ListByService() error = %v, want nil", err)
	}

	if len(empty) != 0 {
		t.Errorf("tn-fe history = %d entries, want 0", len(empty))
	}
}
