package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/oralecarlangelo/touchgrass/internal/model"
	"github.com/oralecarlangelo/touchgrass/internal/testfixture"
)

// seedOwnedRows stores one row of every resource kind for serviceID and
// returns their ids.
func seedOwnedRows(t *testing.T, db *DB, serviceID string) map[model.ResourceKind]int64 {
	t.Helper()

	ctx := context.Background()
	ids := map[model.ResourceKind]int64{}

	occurrence, err := NewIssueStore(db).GroupOccurrence(ctx, serviceID, "fp-"+serviceID, "boom", "v1",
		model.Occurrence{ServiceID: serviceID, Type: "Error", Message: "boom", CreatedAt: time.Now()})
	if err != nil {
		t.Fatalf("GroupOccurrence(%s) error = %v, want nil", serviceID, err)
	}

	if occurrence.IssueID == nil {
		t.Fatalf("GroupOccurrence(%s) left no issue id", serviceID)
	}

	ids[model.ResourceIssue] = *occurrence.IssueID

	if _, err := NewLogStore(db).InsertBatch(ctx, []model.LogLine{
		{ServiceID: serviceID, Container: "c1", Stream: "stdout", Line: "hello from " + serviceID, Ts: time.Now()},
	}); err != nil {
		t.Fatalf("InsertBatch(%s) error = %v, want nil", serviceID, err)
	}

	if err := db.sql.QueryRowContext(ctx,
		`SELECT MAX(id) FROM log_lines WHERE service_id = ?`, serviceID,
	).Scan(ptr(ids, model.ResourceLogLine)); err != nil {
		t.Fatalf("reading log line id: %v", err)
	}

	rule, err := NewRuleStore(db).Create(ctx, testfixture.DefaultRule(serviceID))
	if err != nil {
		t.Fatalf("Create(alert rule) error = %v, want nil", err)
	}

	ids[model.ResourceAlertRule] = rule.ID

	issueRule, err := NewIssueRuleStore(db).Create(ctx, model.IssueRule{
		ServiceID: serviceID, Kind: "new_issue", Threshold: 1, WindowSecs: 60, Enabled: true,
	})
	if err != nil {
		t.Fatalf("Create(issue rule) error = %v, want nil", err)
	}

	ids[model.ResourceIssueRule] = issueRule.ID

	key, err := NewKeyStore(db).Create(ctx, model.APIKey{
		ServiceID: serviceID, KeyHash: "hash-" + serviceID, KeyPrefix: "tg_" + serviceID[:3], SampleRate: 1,
	})
	if err != nil {
		t.Fatalf("Create(key) error = %v, want nil", err)
	}

	ids[model.ResourceAPIKey] = key.ID

	notification, err := NewNotificationStore(db).Insert(ctx, serviceID, "alert", "title", "body")
	if err != nil {
		t.Fatalf("Insert(notification) error = %v, want nil", err)
	}

	ids[model.ResourceNotification] = notification.ID

	return ids
}

// ptr returns a scan target that stores into ids[kind].
func ptr(ids map[model.ResourceKind]int64, kind model.ResourceKind) *int64Setter {
	return &int64Setter{ids: ids, kind: kind}
}

// int64Setter scans an integer column into a map entry.
type int64Setter struct {
	ids  map[model.ResourceKind]int64
	kind model.ResourceKind
}

// Scan implements sql.Scanner.
func (s *int64Setter) Scan(value any) error {
	id, ok := value.(int64)
	if !ok {
		return errors.New("id column is not an integer")
	}

	s.ids[s.kind] = id

	return nil
}

func TestOwnerStoreServiceOf(t *testing.T) {
	t.Parallel()

	db := openTestDB(t)
	seedGenericServices(t, db)

	owners := NewOwnerStore(db)
	ctx := context.Background()

	granted := seedOwnedRows(t, db, testfixture.GenericGranted)
	other := seedOwnedRows(t, db, testfixture.GenericOther)

	kinds := []model.ResourceKind{
		model.ResourceIssue, model.ResourceLogLine, model.ResourceAlertRule,
		model.ResourceIssueRule, model.ResourceAPIKey, model.ResourceNotification,
	}

	for _, kind := range kinds {
		for serviceID, ids := range map[string]map[model.ResourceKind]int64{
			testfixture.GenericGranted: granted,
			testfixture.GenericOther:   other,
		} {
			got, err := owners.ServiceOf(ctx, kind, ids[kind])
			if err != nil || got != serviceID {
				t.Errorf("ServiceOf(kind %d, id %d) = %q, %v, want %q", kind, ids[kind], got, err, serviceID)
			}
		}

		if _, err := owners.ServiceOf(ctx, kind, 1<<40); !errors.Is(err, ErrResourceNotFound) {
			t.Errorf("ServiceOf(kind %d, missing id) error = %v, want ErrResourceNotFound", kind, err)
		}
	}

	if _, err := owners.ServiceOf(ctx, model.ResourceUnknown, 1); err == nil || errors.Is(err, ErrResourceNotFound) {
		t.Errorf("ServiceOf(unknown kind) error = %v, want a distinct failure", err)
	}

	// A notification that belongs to no service has no owner.
	global, err := NewNotificationStore(db).Insert(ctx, "", "security", "host", "body")
	if err != nil {
		t.Fatalf("Insert(global notification) error = %v, want nil", err)
	}

	if _, err := owners.ServiceOf(ctx, model.ResourceNotification, global.ID); !errors.Is(err, ErrResourceNotFound) {
		t.Errorf("ServiceOf(global notification) error = %v, want ErrResourceNotFound", err)
	}
}

func TestScopedLists(t *testing.T) {
	t.Parallel()

	db := openTestDB(t)
	seedGenericServices(t, db)

	ctx := context.Background()
	audits := NewAuditStore(db)
	notifications := NewNotificationStore(db)

	granted, other := testfixture.GenericGranted, testfixture.GenericOther

	for _, entry := range []model.Audit{
		{ServiceID: &granted, Actor: "root", Action: model.AuditDeploy, Result: model.AuditSuccess, Detail: "granted deploy"},
		{ServiceID: &other, Actor: "root", Action: model.AuditDeploy, Result: model.AuditSuccess, Detail: "other deploy"},
		{Actor: "root", Action: model.AuditLogin, Result: model.AuditSuccess, Detail: "login from 10.0.0.9"},
	} {
		if _, err := audits.Insert(ctx, entry); err != nil {
			t.Fatalf("Insert(audit) error = %v, want nil", err)
		}
	}

	scopedAudit, err := audits.ListForServices(ctx, []string{granted}, 10)
	if err != nil {
		t.Fatalf("ListForServices() error = %v, want nil", err)
	}

	if len(scopedAudit) != 1 || scopedAudit[0].Detail != "granted deploy" {
		t.Errorf("ListForServices() = %+v, want only the granted service's row", scopedAudit)
	}

	if none, err := audits.ListForServices(ctx, []string{}, 10); err != nil || len(none) != 0 {
		t.Errorf("ListForServices(no services) = %+v, %v, want nothing", none, err)
	}

	for _, serviceID := range []string{granted, granted, other, ""} {
		if _, err := notifications.Insert(ctx, serviceID, "alert", "title", "for "+serviceID); err != nil {
			t.Fatalf("Insert(notification) error = %v, want nil", err)
		}
	}

	scoped, err := notifications.ListForServices(ctx, []string{granted}, 1)
	if err != nil {
		t.Fatalf("ListForServices() error = %v, want nil", err)
	}

	if len(scoped) != 1 || scoped[0].ServiceID != granted {
		t.Errorf("ListForServices(limit 1) = %+v, want one row of the granted service", scoped)
	}

	unread, err := notifications.CountUnreadForServices(ctx, []string{granted})
	if err != nil || unread != 2 {
		t.Errorf("CountUnreadForServices() = %d, %v, want 2", unread, err)
	}

	if count, err := notifications.CountUnreadForServices(ctx, []string{}); err != nil || count != 0 {
		t.Errorf("CountUnreadForServices(no services) = %d, %v, want 0", count, err)
	}

	marked, err := notifications.MarkAllReadForServices(ctx, []string{granted})
	if err != nil || marked != 2 {
		t.Errorf("MarkAllReadForServices() = %d, %v, want 2", marked, err)
	}

	if marked, err := notifications.MarkAllReadForServices(ctx, []string{}); err != nil || marked != 0 {
		t.Errorf("MarkAllReadForServices(no services) = %d, %v, want 0", marked, err)
	}

	// The other service's row and the global row are still unread.
	if total, err := notifications.CountUnread(ctx, ""); err != nil || total != 2 {
		t.Errorf("CountUnread(all) = %d, %v, want 2 left untouched", total, err)
	}
}
