package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/oralecarlangelo/touchgrass/internal/model"
)

// notificationsWhat names the rows in query errors.
const notificationsWhat = "notifications"

// NotificationStore persists in-app notifications.
type NotificationStore struct {
	db *DB
}

// NewNotificationStore builds a NotificationStore on db.
func NewNotificationStore(db *DB) *NotificationStore {
	return &NotificationStore{db: db}
}

// Insert records a notification and returns it with id and timestamp.
func (s *NotificationStore) Insert(
	ctx context.Context,
	serviceID, kind, title, body string,
) (model.Notification, error) {
	now := time.Now()

	res, err := s.db.sql.ExecContext(ctx,
		"INSERT INTO notifications (service_id, kind, title, body, created_at) VALUES (?, ?, ?, ?, ?)",
		nullString(serviceID), kind, title, body, formatTime(now),
	)
	if err != nil {
		return model.Notification{}, fmt.Errorf("inserting notification: %w", err)
	}

	id, err := res.LastInsertId()
	if err != nil {
		return model.Notification{}, fmt.Errorf("reading notification id: %w", err)
	}

	return model.Notification{
		ID:        id,
		ServiceID: serviceID,
		Kind:      kind,
		Title:     title,
		Body:      body,
		CreatedAt: now,
	}, nil
}

// List returns the newest notifications first, bounded by limit. An empty
// serviceID selects every row, including the ones that belong to no
// service; otherwise it selects one service.
func (s *NotificationStore) List(
	ctx context.Context,
	serviceID string,
	limit int,
) ([]model.Notification, error) {
	query := `SELECT id, COALESCE(service_id, ''), kind, title, body, created_at, read_at
	 FROM notifications ORDER BY id DESC LIMIT ?`
	args := []any{limit}

	if serviceID != "" {
		query = `SELECT id, service_id, kind, title, body, created_at, read_at
		 FROM notifications WHERE service_id = ? ORDER BY id DESC LIMIT ?`
		args = []any{serviceID, limit}
	}

	return queryRows(ctx, s.db.sql, rowQuery[model.Notification]{
		what: notificationsWhat, query: query, args: args, scan: scanNotification,
	})
}

// ListForServices returns the newest notifications of the listed
// services first, bounded by limit. Rows that belong to no service are
// never included, and an empty list selects nothing.
func (s *NotificationStore) ListForServices(
	ctx context.Context,
	serviceIDs []string,
	limit int,
) ([]model.Notification, error) {
	if len(serviceIDs) == 0 {
		return []model.Notification{}, nil
	}

	clause, args := inClause(serviceIDs)

	return queryRows(ctx, s.db.sql, rowQuery[model.Notification]{
		what: notificationsWhat,
		query: `SELECT id, service_id, kind, title, body, created_at, read_at
		 FROM notifications WHERE service_id IN ` + clause + ` ORDER BY id DESC LIMIT ?`,
		args: append(args, limit),
		scan: scanNotification,
	})
}

// scanNotification maps one notifications row.
func scanNotification(row scanner) (model.Notification, error) {
	var (
		notification model.Notification
		createdAt    string
		readAt       sql.NullString
	)

	if err := row.Scan(
		&notification.ID, &notification.ServiceID, &notification.Kind,
		&notification.Title, &notification.Body, &createdAt, &readAt,
	); err != nil {
		return model.Notification{}, fmt.Errorf("scanning notification: %w", err)
	}

	parsedAt, err := parseTime(createdAt, "created_at")
	if err != nil {
		return model.Notification{}, err
	}

	notification.CreatedAt = parsedAt

	notification.ReadAt, err = parseNullTime(readAt, "read_at")
	if err != nil {
		return model.Notification{}, err
	}

	return notification, nil
}

func nullString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

// MarkRead stamps one notification read and reports the service it
// belongs to ("" for a row with no service) and whether this call
// changed it. An unknown id and an already-read row are a no-op
// success with marked false.
func (s *NotificationStore) MarkRead(ctx context.Context, id int64) (serviceID string, marked bool, err error) {
	err = s.db.sql.QueryRowContext(ctx,
		`UPDATE notifications SET read_at = ? WHERE id = ? AND read_at IS NULL
		 RETURNING COALESCE(service_id, '')`,
		formatTime(time.Now()), id,
	).Scan(&serviceID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}

	if err != nil {
		return "", false, fmt.Errorf("marking notification read: %w", err)
	}

	return serviceID, true, nil
}

// MarkAllRead stamps every unread notification read and reports how
// many rows it changed per service; rows with no service count under
// "". An empty serviceID selects every row; otherwise one service.
func (s *NotificationStore) MarkAllRead(ctx context.Context, serviceID string) (map[string]int64, error) {
	query := "UPDATE notifications SET read_at = ? WHERE read_at IS NULL"
	args := []any{formatTime(time.Now())}

	if serviceID != "" {
		query += " AND service_id = ?"

		args = append(args, serviceID)
	}

	return s.markReturning(ctx, query, args)
}

// markReturning runs a mark-read UPDATE and counts the changed rows per
// service.
func (s *NotificationStore) markReturning(ctx context.Context, query string, args []any) (map[string]int64, error) {
	rows, err := s.db.sql.QueryContext(ctx, query+" RETURNING COALESCE(service_id, '')", args...)
	if err != nil {
		return nil, fmt.Errorf("marking notifications read: %w", err)
	}

	defer func() {
		_ = rows.Close()
	}()

	marked := map[string]int64{}

	for rows.Next() {
		var serviceID string

		if err := rows.Scan(&serviceID); err != nil {
			return nil, fmt.Errorf("scanning read notification: %w", err)
		}

		marked[serviceID]++
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("marking notifications read: %w", err)
	}

	return marked, nil
}

// CountUnread counts unread notifications, optionally for one service.
func (s *NotificationStore) CountUnread(ctx context.Context, serviceID string) (int64, error) {
	query := "SELECT COUNT(*) FROM notifications WHERE read_at IS NULL"
	args := []any{}

	if serviceID != "" {
		query += " AND service_id = ?"

		args = append(args, serviceID)
	}

	var count int64

	if err := s.db.sql.QueryRowContext(ctx, query, args...).Scan(&count); err != nil {
		return 0, fmt.Errorf("counting unread notifications: %w", err)
	}

	return count, nil
}

// MarkAllReadForServices stamps every unread notification of the listed
// services read and reports how many rows it changed per service. An
// empty list marks nothing.
func (s *NotificationStore) MarkAllReadForServices(ctx context.Context, serviceIDs []string) (map[string]int64, error) {
	if len(serviceIDs) == 0 {
		return map[string]int64{}, nil
	}

	clause, args := inClause(serviceIDs)

	// clause is only "(?, ?, …)" placeholders; the ids are bound arguments.
	query := "UPDATE notifications SET read_at = ? WHERE read_at IS NULL AND service_id IN " + clause

	return s.markReturning(ctx, query, append([]any{formatTime(time.Now())}, args...))
}

// CountUnreadForServices counts the unread notifications of the listed
// services. An empty list counts nothing.
func (s *NotificationStore) CountUnreadForServices(ctx context.Context, serviceIDs []string) (int64, error) {
	if len(serviceIDs) == 0 {
		return 0, nil
	}

	clause, args := inClause(serviceIDs)

	// clause is only "(?, ?, …)" placeholders; the ids are bound arguments.
	query := "SELECT COUNT(*) FROM notifications WHERE read_at IS NULL AND service_id IN " + clause

	var count int64

	if err := s.db.sql.QueryRowContext(ctx, query, args...).Scan(&count); err != nil {
		return 0, fmt.Errorf("counting unread notifications: %w", err)
	}

	return count, nil
}

// TrimBefore deletes notifications older than cutoff and reports the count.
func (s *NotificationStore) TrimBefore(ctx context.Context, cutoff time.Time) (int64, error) {
	res, err := s.db.sql.ExecContext(
		ctx,
		"DELETE FROM notifications WHERE created_at < ?",
		formatTime(cutoff),
	)
	if err != nil {
		return 0, fmt.Errorf("trimming notifications: %w", err)
	}

	trimmed, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("counting trimmed notifications: %w", err)
	}

	return trimmed, nil
}
