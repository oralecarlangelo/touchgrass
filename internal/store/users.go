package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/oralecarlangelo/touchgrass/internal/model"
)

// ErrUserNotFound reports an unknown user id or username.
var ErrUserNotFound = errors.New("store: user not found")

// UserStore persists login accounts.
type UserStore struct {
	db *DB
}

// NewUserStore builds a UserStore on db.
func NewUserStore(db *DB) *UserStore {
	return &UserStore{db: db}
}

// Create stores one user and returns it with its id.
func (s *UserStore) Create(ctx context.Context, user model.User) (model.User, error) {
	if user.CreatedAt.IsZero() {
		user.CreatedAt = time.Now()
	}

	res, err := s.db.sql.ExecContext(ctx,
		`INSERT INTO users (username, password_hash, role, disabled_at, created_at)
		 VALUES (?, ?, ?, ?, ?)`,
		user.Username, user.PasswordHash, string(user.Role),
		formatNullTime(user.DisabledAt), formatTime(user.CreatedAt),
	)
	if err != nil {
		return model.User{}, fmt.Errorf("creating user: %w", err)
	}

	id, err := res.LastInsertId()
	if err != nil {
		return model.User{}, fmt.Errorf("reading user id: %w", err)
	}

	user.ID = id

	return user, nil
}

// ByID returns one user by id.
func (s *UserStore) ByID(ctx context.Context, id int64) (model.User, error) {
	row := s.db.sql.QueryRowContext(ctx,
		`SELECT id, username, password_hash, role, disabled_at, created_at
		 FROM users WHERE id = ?`,
		id,
	)

	user, err := scanUser(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return model.User{}, ErrUserNotFound
		}

		return model.User{}, err
	}

	return user, nil
}

// ByUsername returns one user by username.
func (s *UserStore) ByUsername(ctx context.Context, username string) (model.User, error) {
	row := s.db.sql.QueryRowContext(ctx,
		`SELECT id, username, password_hash, role, disabled_at, created_at
		 FROM users WHERE username = ?`,
		username,
	)

	user, err := scanUser(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return model.User{}, ErrUserNotFound
		}

		return model.User{}, err
	}

	return user, nil
}

// List returns users oldest first, bounded by limit.
func (s *UserStore) List(ctx context.Context, limit int) ([]model.User, error) {
	rows, err := s.db.sql.QueryContext(ctx,
		`SELECT id, username, password_hash, role, disabled_at, created_at
		 FROM users ORDER BY id ASC LIMIT ?`,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("querying users: %w", err)
	}

	defer func() {
		_ = rows.Close()
	}()

	users := []model.User{}

	for rows.Next() {
		user, err := scanUser(rows)
		if err != nil {
			return nil, err
		}

		users = append(users, user)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating users: %w", err)
	}

	return users, nil
}

// Update rewrites one user's mutable columns.
func (s *UserStore) Update(ctx context.Context, user model.User) error {
	res, err := s.db.sql.ExecContext(ctx,
		`UPDATE users SET password_hash = ?, role = ?, disabled_at = ?
		 WHERE id = ?`,
		user.PasswordHash, string(user.Role),
		formatNullTime(user.DisabledAt), user.ID,
	)
	if err != nil {
		return fmt.Errorf("updating user: %w", err)
	}

	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("counting updated users: %w", err)
	}

	if affected == 0 {
		return fmt.Errorf("%w: %d", ErrUserNotFound, user.ID)
	}

	return nil
}

// Delete removes one user. Audit history keeps the username as text;
// it is never rewritten.
func (s *UserStore) Delete(ctx context.Context, id int64) error {
	res, err := s.db.sql.ExecContext(ctx, "DELETE FROM users WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("deleting user: %w", err)
	}

	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("counting deleted users: %w", err)
	}

	if affected == 0 {
		return fmt.Errorf("%w: %d", ErrUserNotFound, id)
	}

	return nil
}

// CountEnabledAdmins reports how many admin accounts are not disabled.
func (s *UserStore) CountEnabledAdmins(ctx context.Context) (int, error) {
	var count int

	if err := s.db.sql.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM users WHERE role = 'admin' AND disabled_at IS NULL`,
	).Scan(&count); err != nil {
		return 0, fmt.Errorf("counting enabled admins: %w", err)
	}

	return count, nil
}

// scanUser maps one users row.
func scanUser(row scanner) (model.User, error) {
	var (
		user       model.User
		role       string
		disabledAt sql.NullString
		createdAt  string
	)

	if err := row.Scan(
		&user.ID,
		&user.Username,
		&user.PasswordHash,
		&role,
		&disabledAt,
		&createdAt,
	); err != nil {
		return model.User{}, fmt.Errorf("scanning user: %w", err)
	}

	user.Role = model.Role(role)

	disabled, err := parseNullTime(disabledAt, "disabled_at")
	if err != nil {
		return model.User{}, err
	}

	user.DisabledAt = disabled

	created, err := parseTime(createdAt, "created_at")
	if err != nil {
		return model.User{}, err
	}

	user.CreatedAt = created

	return user, nil
}
