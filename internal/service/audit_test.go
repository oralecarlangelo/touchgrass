package service

import (
	"context"
	"errors"
	"testing"

	"github.com/oralecarlangelo/touchgrass/internal/model"
	"github.com/oralecarlangelo/touchgrass/internal/store"
)

func TestAuditRecord(t *testing.T) {
	t.Parallel()

	db := openInventoryDB(t)
	ctx := context.Background()
	audit := NewAudit(store.NewServiceStore(db), store.NewAuditStore(db))

	global, err := audit.Record(ctx, model.AuditRecord{
		Actor: testAdminActor, Action: model.AuditLogin, Result: model.AuditSuccess,
		Detail: "login ok",
	})
	if err != nil {
		t.Fatalf("Record(global) error = %v, want nil", err)
	}

	scoped, err := audit.Record(ctx, model.AuditRecord{
		ServiceID: testServiceAPI, Actor: testAdminActor, Action: model.AuditRollback,
		Result: model.AuditFailure, Detail: "rollback failed",
	})
	if err != nil {
		t.Fatalf("Record(scoped) error = %v, want nil", err)
	}

	if global == 0 || scoped == 0 || global == scoped {
		t.Fatalf("ids = (%d, %d), want distinct non-zero", global, scoped)
	}
}

func TestAuditList(t *testing.T) {
	t.Parallel()

	db := openInventoryDB(t)
	ctx := context.Background()
	audit := NewAudit(store.NewServiceStore(db), store.NewAuditStore(db))

	global, err := audit.Record(ctx, model.AuditRecord{
		Actor: testAdminActor, Action: model.AuditLogin, Result: model.AuditSuccess,
	})
	if err != nil {
		t.Fatalf("Record(global) error = %v, want nil", err)
	}

	scoped, err := audit.Record(ctx, model.AuditRecord{
		ServiceID: testServiceAPI, Actor: testAdminActor, Action: model.AuditRollback,
		Result: model.AuditFailure,
	})
	if err != nil {
		t.Fatalf("Record(scoped) error = %v, want nil", err)
	}

	entries, err := audit.List(ctx, "", 10)
	if err != nil {
		t.Fatalf("List(all) error = %v, want nil", err)
	}

	if len(entries) != 2 || entries[0].ID != scoped || entries[1].ID != global {
		t.Fatalf("List(all) = %+v, want newest first", entries)
	}

	if entries[1].ServiceID != nil {
		t.Errorf("global service_id = %v, want nil", *entries[1].ServiceID)
	}

	scopedEntries, err := audit.List(ctx, testServiceAPI, 10)
	if err != nil {
		t.Fatalf("List(service) error = %v, want nil", err)
	}

	if len(scopedEntries) != 1 || scopedEntries[0].ID != scoped {
		t.Errorf("List(service) = %+v, want one scoped entry", scopedEntries)
	}

	if _, err := audit.List(ctx, testUnknownServiceID, 10); !errors.Is(err, store.ErrServiceNotFound) {
		t.Errorf("List(unknown) error = %v, want ErrServiceNotFound", err)
	}
}

func TestAuditRecordValidation(t *testing.T) {
	t.Parallel()

	db := openInventoryDB(t)
	ctx := context.Background()
	audit := NewAudit(store.NewServiceStore(db), store.NewAuditStore(db))

	base := model.AuditRecord{
		ServiceID: testServiceAPI, Actor: testAdminActor, Action: model.AuditCutover,
		Result: model.AuditSuccess, Detail: "cutover ok",
	}

	tests := []struct {
		name   string
		mutate func(*model.AuditRecord)
	}{
		{name: testUnknownServiceName, mutate: func(r *model.AuditRecord) { r.ServiceID = testUnknownServiceID }},
		{name: "bad action", mutate: func(r *model.AuditRecord) { r.Action = testBogusValue }},
		{name: "bad result", mutate: func(r *model.AuditRecord) { r.Result = testBogusValue }},
		{name: "empty actor", mutate: func(r *model.AuditRecord) { r.Actor = "" }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			record := base
			tt.mutate(&record)

			if _, err := audit.Record(ctx, record); err == nil {
				t.Errorf("Record() error = nil, want validation error")
			}
		})
	}
}
