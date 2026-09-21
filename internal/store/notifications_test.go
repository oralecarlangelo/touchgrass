package store

import (
	"context"
	"testing"
	"time"

	"github.com/oralecarlangelo/touchgrass/internal/model"
)

func TestNotificationInsert(t *testing.T) {
	t.Parallel()

	db := openTestDB(t)
	notifications := NewNotificationStore(db)

	first, second := seedNotifications(t, notifications)

	if first.ID == 0 || second.ID == 0 || first.ID == second.ID {
		t.Fatalf("ids = (%d, %d), want distinct non-zero", first.ID, second.ID)
	}

	if first.ReadAt != nil {
		t.Errorf("ReadAt = %v, want nil (unread)", first.ReadAt)
	}
}

func TestNotificationList(t *testing.T) {
	t.Parallel()

	db := openTestDB(t)
	ctx := context.Background()
	notifications := NewNotificationStore(db)

	first, second := seedNotifications(t, notifications)

	got, err := notifications.List(ctx, "", 10)
	if err != nil {
		t.Fatalf("List() error = %v, want nil", err)
	}

	if len(got) != 2 || got[0].ID != second.ID {
		t.Fatalf("List() newest-first violated: %+v", got)
	}

	filtered, err := notifications.List(ctx, testServiceAPI, 10)
	if err != nil {
		t.Fatalf("List(service) error = %v, want nil", err)
	}

	if len(filtered) != 1 || filtered[0].ID != first.ID {
		t.Errorf("List(service) = %+v, want the one scoped note", filtered)
	}
}

func TestNotificationTrim(t *testing.T) {
	t.Parallel()

	db := openTestDB(t)
	ctx := context.Background()
	notifications := NewNotificationStore(db)

	seedNotifications(t, notifications)

	trimmed, err := notifications.TrimBefore(ctx, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("TrimBefore() error = %v, want nil", err)
	}

	if trimmed != 2 {
		t.Errorf("TrimBefore() = %d, want 2", trimmed)
	}
}

// seedNotifications inserts two notes on different services.
func seedNotifications(
	t *testing.T,
	notifications *NotificationStore,
) (model.Notification, model.Notification) {
	t.Helper()

	ctx := context.Background()

	first, err := notifications.Insert(ctx, testServiceAPI, "alert_breach", "mem high", "above 85%")
	if err != nil {
		t.Fatalf("Insert() error = %v, want nil", err)
	}

	second, err := notifications.Insert(ctx, "tn-fe", "alert_breach", "cpu high", "above 90%")
	if err != nil {
		t.Fatalf("Insert() error = %v, want nil", err)
	}

	return first, second
}

func TestNotificationReadState(t *testing.T) {
	t.Parallel()

	db := openTestDB(t)
	ctx := context.Background()
	notifications := NewNotificationStore(db)

	first, err := notifications.Insert(ctx, testServiceAPI, "alert_breach", "mem high", "above 85%")
	if err != nil {
		t.Fatalf("Insert() error = %v, want nil", err)
	}

	if _, err := notifications.Insert(ctx, "tn-fe", "alert_breach", "cpu high", "above 90%"); err != nil {
		t.Fatalf("Insert() error = %v, want nil", err)
	}

	unread := countUnread(t, notifications, "")

	if unread != 2 {
		t.Errorf("CountUnread() = %d, want 2", unread)
	}

	if err := notifications.MarkRead(ctx, first.ID); err != nil {
		t.Fatalf("MarkRead() error = %v, want nil", err)
	}

	if unread := countUnread(t, notifications, ""); unread != 1 {
		t.Errorf("CountUnread() = %d, want 1 after MarkRead", unread)
	}

	marked, err := notifications.MarkAllRead(ctx, "")
	if err != nil {
		t.Fatalf("MarkAllRead() error = %v, want nil", err)
	}

	if marked != 1 {
		t.Errorf("MarkAllRead() = %d, want 1", marked)
	}

	if unread := countUnread(t, notifications, ""); unread != 0 {
		t.Errorf("CountUnread() = %d, want 0 after MarkAllRead", unread)
	}
}

// countUnread returns the unread count or fails the test.
func countUnread(t *testing.T, notifications *NotificationStore, serviceID string) int64 {
	t.Helper()

	unread, err := notifications.CountUnread(context.Background(), serviceID)
	if err != nil {
		t.Fatalf("CountUnread() error = %v, want nil", err)
	}

	return unread
}
