package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/oralecarlangelo/touchgrass/internal/model"
)

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
		serviceID, kind, title, body, formatTime(now),
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
// serviceID selects every service; otherwise it selects one service.
func (s *NotificationStore) List(
	ctx context.Context,
	serviceID string,
	limit int,
) ([]model.Notification, error) {
	query := `SELECT id, service_id, kind, title, body, created_at, read_at
	 FROM notifications ORDER BY id DESC LIMIT ?`
	args := []any{limit}

	if serviceID != "" {
		query = `SELECT id, service_id, kind, title, body, created_at, read_at
		 FROM notifications WHERE service_id = ? ORDER BY id DESC LIMIT ?`
		args = []any{serviceID, limit}
	}

	rows, err := s.db.sql.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("querying notifications: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()

	notifications := []model.Notification{}

	for rows.Next() {
		var (
			notification model.Notification
			createdAt    string
			readAt       sql.NullString
		)

		if err := rows.Scan(
			&notification.ID, &notification.ServiceID, &notification.Kind,
			&notification.Title, &notification.Body, &createdAt, &readAt,
		); err != nil {
			return nil, fmt.Errorf("scanning notification: %w", err)
		}

		parsedAt, err := parseTime(createdAt, "created_at")
		if err != nil {
			return nil, err
		}

		notification.CreatedAt = parsedAt

		notification.ReadAt, err = parseNullTime(readAt, "read_at")
		if err != nil {
			return nil, err
		}

		notifications = append(notifications, notification)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating notifications: %w", err)
	}

	return notifications, nil
}

// MarkRead stamps one notification read. Unknown ids are a no-op success.
func (s *NotificationStore) MarkRead(ctx context.Context, id int64) error {
	if _, err := s.db.sql.ExecContext(ctx,
		"UPDATE notifications SET read_at = ? WHERE id = ? AND read_at IS NULL",
		formatTime(time.Now()), id,
	); err != nil {
		return fmt.Errorf("marking notification read: %w", err)
	}

	return nil
}

// MarkAllRead stamps every unread notification read and reports the count.
// An empty serviceID selects every service; otherwise one service.
func (s *NotificationStore) MarkAllRead(ctx context.Context, serviceID string) (int64, error) {
	query := "UPDATE notifications SET read_at = ? WHERE read_at IS NULL"
	args := []any{formatTime(time.Now())}

	if serviceID != "" {
		query += " AND service_id = ?"

		args = append(args, serviceID)
	}

	res, err := s.db.sql.ExecContext(ctx, query, args...)
	if err != nil {
		return 0, fmt.Errorf("marking notifications read: %w", err)
	}

	marked, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("counting read notifications: %w", err)
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
