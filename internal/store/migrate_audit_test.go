package store

import (
	"context"
	"slices"
	"testing"

	"github.com/oralecarlangelo/touchgrass/internal/model"
	"github.com/oralecarlangelo/touchgrass/internal/testfixture"
)

// migrateFromV060 builds a v0.6.0 database (migrations through 0023)
// holding the generic services, a scoped viewer with a grant, and
// audit rows with and without a service, then migrates it.
func migrateFromV060(t *testing.T) *DB {
	t.Helper()

	db := openMigratedThrough(t, "0023")
	ctx := context.Background()

	for _, service := range testfixture.GenericServices() {
		if err := NewServiceStore(db).Create(ctx, service); err != nil {
			t.Fatalf("Create(%s) error = %v, want nil", service.ID, err)
		}
	}

	execAll(t, db,
		`INSERT INTO users (username, password_hash, role, all_services)
		 VALUES ('`+testOwnerName+`', 'hash-owner', 'admin', 1)`,
		`INSERT INTO users (username, password_hash, role, all_services)
		 VALUES ('`+testReaderName+`', 'hash-reader', 'viewer', 0)`,
		`INSERT INTO user_services (user_id, service_id, granted_by, granted_at)
		 VALUES (2, '`+testfixture.GenericGranted+`', '`+testOwnerName+`', '2026-11-01T00:00:00.000Z')`,
		`INSERT INTO audit (id, service_id, actor, action, result, detail, created_at)
		 VALUES (7, '`+testfixture.GenericGranted+`', '`+testOwnerName+`', 'deploy', 'success', 'deployed', '2026-11-02T03:04:05.000Z')`,
		`INSERT INTO audit (id, service_id, actor, action, result, detail, created_at)
		 VALUES (9, NULL, '`+testReaderName+`', 'login', 'failure', 'bad password', '2026-11-03T03:04:05.000Z')`,
		`INSERT INTO audit (id, service_id, actor, action, result, detail, created_at)
		 VALUES (12, '`+testfixture.GenericOther+`', '`+testOwnerName+`', 'env.apply', 'success', 'applied', '2026-11-04T03:04:05.000Z')`,
	)

	applied, err := db.MigrateUp(ctx)
	if err != nil {
		t.Fatalf("MigrateUp() error = %v, want nil", err)
	}

	if !slices.Equal(applied, []string{"0024", "0025"}) {
		t.Fatalf("MigrateUp() applied = %v, want [0024 0025]", applied)
	}

	return db
}

func TestMigrateAuditOperatorActionsKeepsEveryRow(t *testing.T) {
	t.Parallel()

	db := migrateFromV060(t)
	ctx := context.Background()

	rows, err := NewAuditStore(db).List(ctx, "", 10)
	if err != nil {
		t.Fatalf("List() error = %v, want nil", err)
	}

	type kept struct {
		id      int64
		service string
		actor   string
		action  string
		result  string
		detail  string
		at      string
	}

	got := []kept{}

	for _, row := range rows {
		service := ""
		if row.ServiceID != nil {
			service = *row.ServiceID
		}

		got = append(got, kept{
			id: row.ID, service: service, actor: row.Actor, action: row.Action, result: row.Result,
			detail: row.Detail, at: row.CreatedAt.UTC().Format("2006-01-02T15:04:05"),
		})
	}

	want := []kept{
		{
			id: 12, service: testfixture.GenericOther, actor: testOwnerName, action: model.AuditEnvApply,
			result: model.AuditSuccess, detail: "applied", at: "2026-11-04T03:04:05",
		},
		{id: 9, actor: testReaderName, action: model.AuditLogin, result: model.AuditFailure, detail: "bad password", at: "2026-11-03T03:04:05"},
		{
			id: 7, service: testfixture.GenericGranted, actor: testOwnerName, action: model.AuditDeploy,
			result: model.AuditSuccess, detail: "deployed", at: "2026-11-02T03:04:05",
		},
	}

	if !slices.Equal(got, want) {
		t.Errorf("audit rows after 0024 = %+v, want %+v", got, want)
	}

	// The next row continues after the highest id that existed, so a
	// rebuilt table never hands out an id twice.
	next, err := NewAuditStore(db).Insert(ctx, model.Audit{
		Actor: testOwnerName, Action: model.AuditLogin, Result: model.AuditSuccess,
	})
	if err != nil || next != 13 {
		t.Errorf("Insert() after 0024 = %d, %v, want id 13", next, err)
	}
}

func TestMigrateAuditOperatorActionsLeavesAccountsAlone(t *testing.T) {
	t.Parallel()

	db := migrateFromV060(t)

	listed, err := NewUserStore(db).List(context.Background(), 10)
	if err != nil {
		t.Fatalf("List() error = %v, want nil", err)
	}

	if len(listed) != 2 {
		t.Fatalf("List() = %+v, want both accounts", listed)
	}

	for _, user := range listed {
		switch user.Username {
		case testOwnerName:
			if user.Role != model.RoleAdmin || !user.AllServices {
				t.Errorf("owner = %+v, want an all-services admin", user)
			}
		case testReaderName:
			if user.Role != model.RoleViewer || user.AllServices || !slices.Equal(user.Services, []string{testfixture.GenericGranted}) {
				t.Errorf("reader = %+v, want a viewer still granted only %s", user, testfixture.GenericGranted)
			}
		default:
			t.Errorf("unexpected account %+v", user)
		}
	}
}

func TestAuditAdmitsOperatorActions(t *testing.T) {
	t.Parallel()

	db := openTestDB(t)
	ctx := context.Background()
	audits := NewAuditStore(db)

	for _, action := range []string{
		model.AuditDeployRecord, model.AuditAlertRuleCreate, model.AuditAlertRuleDelete,
		model.AuditIssueRuleCreate, model.AuditIssueRuleDelete, model.AuditNotificationRead,
	} {
		if _, err := audits.Insert(ctx, model.Audit{Actor: "dev", Action: action, Result: model.AuditSuccess}); err != nil {
			t.Errorf("Insert(%s) error = %v, want nil", action, err)
		}
	}

	if _, err := audits.Insert(ctx, model.Audit{Actor: "dev", Action: "rule_rename", Result: model.AuditSuccess}); err == nil {
		t.Error("Insert(unknown action) error = nil, want the CHECK to refuse it")
	}
}
