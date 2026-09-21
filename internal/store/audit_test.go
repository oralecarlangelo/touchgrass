package store

import (
	"context"
	"testing"

	"github.com/oralecarlangelo/touchgrass/internal/model"
)

const testAdminActor = "admin"

func TestAuditInsert(t *testing.T) {
	t.Parallel()

	db := openTestDB(t)
	ctx := context.Background()
	audits := NewAuditStore(db)

	service := testServiceAPI

	global, err := audits.Insert(ctx, model.Audit{
		Actor: testAdminActor, Action: model.AuditLogin, Result: model.AuditSuccess,
		Detail: "login ok",
	})
	if err != nil {
		t.Fatalf("Insert(global) error = %v, want nil", err)
	}

	scoped, err := audits.Insert(ctx, model.Audit{
		ServiceID: &service, Actor: testAdminActor, Action: model.AuditCutover,
		Result: model.AuditFailure, Detail: "cutover failed",
	})
	if err != nil {
		t.Fatalf("Insert(scoped) error = %v, want nil", err)
	}

	if global == 0 || scoped == 0 || global == scoped {
		t.Fatalf("ids = (%d, %d), want distinct non-zero", global, scoped)
	}
}

// checkAuditEntries validates ids and required fields of listed entries.
func checkAuditEntries(t *testing.T, got []model.Audit, serviceID string, wantIDs []int64) {
	t.Helper()

	if len(got) != len(wantIDs) {
		t.Fatalf("List(%q) = %d entries, want %d", serviceID, len(got), len(wantIDs))
	}

	for i, id := range wantIDs {
		if got[i].ID != id {
			t.Errorf("entries[%d].id = %d, want %d", i, got[i].ID, id)
		}

		if got[i].Actor == "" || got[i].Action == "" || got[i].Result == "" {
			t.Errorf("entries[%d] = %+v, want actor/action/result", i, got[i])
		}

		if got[i].CreatedAt.IsZero() {
			t.Errorf("entries[%d] created_at is zero, want timestamp", i)
		}
	}

	if serviceID == "" && got[1].ServiceID != nil {
		t.Errorf("global service_id = %v, want nil", *got[1].ServiceID)
	}
}

func TestAuditListFilters(t *testing.T) {
	t.Parallel()

	db := openTestDB(t)
	ctx := context.Background()
	audits := NewAuditStore(db)

	service := testServiceAPI

	global, err := audits.Insert(ctx, model.Audit{
		Actor: testAdminActor, Action: model.AuditLogin, Result: model.AuditSuccess,
	})
	if err != nil {
		t.Fatalf("Insert(global) error = %v, want nil", err)
	}

	scoped, err := audits.Insert(ctx, model.Audit{
		ServiceID: &service, Actor: testAdminActor, Action: model.AuditCutover,
		Result: model.AuditFailure,
	})
	if err != nil {
		t.Fatalf("Insert(scoped) error = %v, want nil", err)
	}

	tests := []struct {
		name      string
		serviceID string
		wantIDs   []int64
	}{
		{name: "all entries newest first", serviceID: "", wantIDs: []int64{scoped, global}},
		{name: "service filter", serviceID: testServiceAPI, wantIDs: []int64{scoped}},
		{name: "unknown service empty", serviceID: "no-such-service", wantIDs: []int64{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := audits.List(ctx, tt.serviceID, 10)
			if err != nil {
				t.Fatalf("List(%q) error = %v, want nil", tt.serviceID, err)
			}

			checkAuditEntries(t, got, tt.serviceID, tt.wantIDs)
		})
	}
}
