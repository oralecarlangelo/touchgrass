package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/oralecarlangelo/touchgrass/internal/model"
)

// ErrUserNotFound reports an unknown user id or username.
var ErrUserNotFound = errors.New("store: user not found")

// userColumns is the users projection every read shares.
const userColumns = `id, username, password_hash, role, all_services, must_change_password, disabled_at, created_at`

// UserStore persists login accounts and their service grants.
type UserStore struct {
	db *DB
}

// NewUserStore builds a UserStore on db.
func NewUserStore(db *DB) *UserStore {
	return &UserStore{db: db}
}

// Create stores one user with its scope and returns it with its id.
// user.Services become grants attributed to grantedBy; they must be
// empty when user.AllServices is set.
func (s *UserStore) Create(ctx context.Context, user model.User, grantedBy string) (model.User, error) {
	if user.CreatedAt.IsZero() {
		user.CreatedAt = time.Now()
	}

	if user.AllServices && len(user.Services) > 0 {
		return model.User{}, errors.New("creating user: an all-services user takes no service list")
	}

	tx, err := s.db.sql.BeginTx(ctx, nil)
	if err != nil {
		return model.User{}, fmt.Errorf("beginning user create: %w", err)
	}

	// Rollback is best-effort: after a successful commit it reports
	// sql.ErrTxDone, which must not mask the result.
	defer func() {
		_ = tx.Rollback()
	}()

	res, err := tx.ExecContext(ctx,
		`INSERT INTO users (username, password_hash, role, all_services, must_change_password, disabled_at, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		user.Username, user.PasswordHash, string(user.Role), user.AllServices, user.MustChangePassword,
		formatNullTime(user.DisabledAt), formatTime(user.CreatedAt),
	)
	if err != nil {
		return model.User{}, fmt.Errorf("creating user: %w", err)
	}

	id, err := res.LastInsertId()
	if err != nil {
		return model.User{}, fmt.Errorf("reading user id: %w", err)
	}

	services := normalizeServiceIDs(user.Services)

	for _, serviceID := range services {
		if err := insertGrant(ctx, tx, id, serviceID, grantedBy); err != nil {
			return model.User{}, err
		}
	}

	if err := tx.Commit(); err != nil {
		return model.User{}, fmt.Errorf("committing user create: %w", err)
	}

	user.ID = id
	user.Services = services

	return user, nil
}

// ByID returns one user by id.
func (s *UserStore) ByID(ctx context.Context, id int64) (model.User, error) {
	return s.one(ctx, `SELECT `+userColumns+` FROM users WHERE id = ?`, id)
}

// ByUsername returns one user by username.
func (s *UserStore) ByUsername(ctx context.Context, username string) (model.User, error) {
	return s.one(ctx, `SELECT `+userColumns+` FROM users WHERE username = ?`, username)
}

// one reads a single user and, for a scoped one, its grants.
func (s *UserStore) one(ctx context.Context, query string, arg any) (model.User, error) {
	user, err := scanUser(s.db.sql.QueryRowContext(ctx, query, arg))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return model.User{}, ErrUserNotFound
		}

		return model.User{}, err
	}

	if user.AllServices {
		return user, nil
	}

	services, err := queryRows(ctx, s.db.sql, rowQuery[string]{
		what:  "user services",
		query: `SELECT service_id FROM user_services WHERE user_id = ? ORDER BY service_id`,
		args:  []any{user.ID},
		scan:  scanServiceID,
	})
	if err != nil {
		return model.User{}, err
	}

	user.Services = services

	return user, nil
}

// List returns users oldest first, bounded by limit, with their grants.
func (s *UserStore) List(ctx context.Context, limit int) ([]model.User, error) {
	users, err := queryRows(ctx, s.db.sql, rowQuery[model.User]{
		what:  "users",
		query: `SELECT ` + userColumns + ` FROM users ORDER BY id ASC LIMIT ?`,
		args:  []any{limit},
		scan:  scanUser,
	})
	if err != nil {
		return nil, err
	}

	grants, err := queryRows(ctx, s.db.sql, rowQuery[userGrant]{
		what:  "user services",
		query: `SELECT user_id, service_id FROM user_services ORDER BY user_id, service_id`,
		args:  []any{},
		scan:  scanUserGrant,
	})
	if err != nil {
		return nil, err
	}

	byUser := map[int64][]string{}

	for _, grant := range grants {
		byUser[grant.userID] = append(byUser[grant.userID], grant.serviceID)
	}

	for index := range users {
		if services, ok := byUser[users[index].ID]; ok && !users[index].AllServices {
			users[index].Services = services
		}
	}

	return users, nil
}

// Update rewrites one user's password and lock columns. Role and scope
// change only through SetAccess, so every access change takes one path.
func (s *UserStore) Update(ctx context.Context, user model.User) error {
	res, err := s.db.sql.ExecContext(ctx,
		`UPDATE users SET password_hash = ?, must_change_password = ?, disabled_at = ?
		 WHERE id = ?`,
		user.PasswordHash, user.MustChangePassword,
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

// SetAccess replaces one user's role and scope in one transaction and
// returns the stored service list. Grants that stay keep who granted
// them and when; new ones are attributed to grantedBy. An all-services
// scope clears every grant.
func (s *UserStore) SetAccess(
	ctx context.Context,
	id int64,
	access model.UserAccess,
	grantedBy string,
) ([]string, error) {
	wanted := normalizeServiceIDs(access.Services)

	if access.AllServices {
		wanted = []string{}
	}

	tx, err := s.db.sql.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("beginning access change: %w", err)
	}

	// Rollback is best-effort: after a successful commit it reports
	// sql.ErrTxDone, which must not mask the result.
	defer func() {
		_ = tx.Rollback()
	}()

	res, err := tx.ExecContext(ctx,
		`UPDATE users SET role = ?, all_services = ? WHERE id = ?`,
		string(access.Role), access.AllServices, id,
	)
	if err != nil {
		return nil, fmt.Errorf("updating user access: %w", err)
	}

	affected, err := res.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("counting updated users: %w", err)
	}

	if affected == 0 {
		return nil, fmt.Errorf("%w: %d", ErrUserNotFound, id)
	}

	if err := replaceGrants(ctx, tx, id, wanted, grantedBy); err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("committing access change: %w", err)
	}

	return wanted, nil
}

// MissingServices returns the ids that name no service, in input order.
func (s *UserStore) MissingServices(ctx context.Context, serviceIDs []string) ([]string, error) {
	if len(serviceIDs) == 0 {
		return []string{}, nil
	}

	clause, args := inClause(serviceIDs)

	known, err := queryRows(ctx, s.db.sql, rowQuery[string]{
		what:  "services",
		query: `SELECT id FROM services WHERE id IN ` + clause,
		args:  args,
		scan:  scanServiceID,
	})
	if err != nil {
		return nil, err
	}

	missing := []string{}

	for _, serviceID := range serviceIDs {
		if !slices.Contains(known, serviceID) && !slices.Contains(missing, serviceID) {
			missing = append(missing, serviceID)
		}
	}

	return missing, nil
}

// Delete removes one user; their grants cascade. Audit history keeps
// the username as text; it is never rewritten.
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

// userGrant is one user_services row.
type userGrant struct {
	userID    int64
	serviceID string
}

// replaceGrants makes a user's grants exactly wanted, touching only the
// rows that differ.
func replaceGrants(ctx context.Context, tx *sql.Tx, userID int64, wanted []string, grantedBy string) error {
	rows, err := tx.QueryContext(ctx, `SELECT service_id FROM user_services WHERE user_id = ?`, userID)
	if err != nil {
		return fmt.Errorf("querying user services: %w", err)
	}

	existing := []string{}

	for rows.Next() {
		serviceID, err := scanServiceID(rows)
		if err != nil {
			_ = rows.Close()

			return err
		}

		existing = append(existing, serviceID)
	}

	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return fmt.Errorf("iterating user services: %w", err)
	}

	for _, serviceID := range existing {
		if slices.Contains(wanted, serviceID) {
			continue
		}

		if _, err := tx.ExecContext(ctx,
			`DELETE FROM user_services WHERE user_id = ? AND service_id = ?`, userID, serviceID,
		); err != nil {
			return fmt.Errorf("revoking service %q: %w", serviceID, err)
		}
	}

	for _, serviceID := range wanted {
		if slices.Contains(existing, serviceID) {
			continue
		}

		if err := insertGrant(ctx, tx, userID, serviceID, grantedBy); err != nil {
			return err
		}
	}

	return nil
}

// insertGrant stores one grant.
func insertGrant(ctx context.Context, tx *sql.Tx, userID int64, serviceID, grantedBy string) error {
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO user_services (user_id, service_id, granted_by, granted_at) VALUES (?, ?, ?, ?)`,
		userID, serviceID, grantedBy, formatTime(time.Now()),
	); err != nil {
		return fmt.Errorf("granting service %q: %w", serviceID, err)
	}

	return nil
}

// normalizeServiceIDs returns ids sorted and de-duplicated, never nil.
func normalizeServiceIDs(serviceIDs []string) []string {
	normalized := slices.Clone(serviceIDs)
	if normalized == nil {
		normalized = []string{}
	}

	slices.Sort(normalized)

	return slices.Compact(normalized)
}

// inClause renders "(?, ?, …)" and its arguments for a non-empty list.
func inClause(values []string) (string, []any) {
	marks := make([]string, 0, len(values))
	args := make([]any, 0, len(values))

	for _, value := range values {
		marks = append(marks, "?")
		args = append(args, value)
	}

	return "(" + strings.Join(marks, ", ") + ")", args
}

// scanServiceID maps one service id column.
func scanServiceID(row scanner) (string, error) {
	var serviceID string

	if err := row.Scan(&serviceID); err != nil {
		return "", fmt.Errorf("scanning service id: %w", err)
	}

	return serviceID, nil
}

// scanUserGrant maps one user_services row.
func scanUserGrant(row scanner) (userGrant, error) {
	var grant userGrant

	if err := row.Scan(&grant.userID, &grant.serviceID); err != nil {
		return userGrant{}, fmt.Errorf("scanning user service: %w", err)
	}

	return grant, nil
}

// scanUser maps one users row. Services starts empty; callers that
// need a scoped user's grants load them separately.
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
		&user.AllServices,
		&user.MustChangePassword,
		&disabledAt,
		&createdAt,
	); err != nil {
		return model.User{}, fmt.Errorf("scanning user: %w", err)
	}

	user.Role = model.Role(role)
	user.Services = []string{}

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
