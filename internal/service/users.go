package service

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/oralecarlangelo/touchgrass/internal/model"
	"github.com/oralecarlangelo/touchgrass/internal/store"
)

const (
	// bootstrapAdmin is the username seeded from the admin password env.
	bootstrapAdmin = "admin"
	// minPasswordLength rejects short passwords on managed accounts.
	minPasswordLength = 12
	// maxPasswordLength is bcrypt's input limit in bytes.
	maxPasswordLength = 72
	// maxUsernameLength bounds usernames for audit readability.
	maxUsernameLength = 64
)

// usernamePattern constrains managed usernames to the service-id shape.
var usernamePattern = regexp.MustCompile(`^[a-z0-9-]+$`)

// CreateUserInput carries new-account fields.
type CreateUserInput struct {
	Username string
	Password string
	Role     model.Role
}

// Users manages login accounts: validation, bcrypt hashing, the
// last-admin guard, and auditing. Callers never see password hashes
// except Verify and Lookup, which need them for comparison.
type Users struct {
	users *store.UserStore
	audit *Audit
}

// NewUsers builds a Users service.
func NewUsers(users *store.UserStore, audit *Audit) *Users {
	return &Users{users: users, audit: audit}
}

// Create validates and stores one user.
func (u *Users) Create(
	ctx context.Context,
	input CreateUserInput,
	actor string,
) (model.User, error) {
	if err := checkUserInput(ctx, u.users, input, actor); err != nil {
		return model.User{}, err
	}

	hash, err := hashPassword(input.Password)
	if err != nil {
		return model.User{}, err
	}

	created, err := u.users.Create(ctx, model.User{
		Username: input.Username, PasswordHash: hash, Role: input.Role,
	})
	if err != nil {
		return model.User{}, fmt.Errorf("creating user %q: %w", input.Username, err)
	}

	if _, err := u.audit.Record(ctx, model.AuditRecord{
		Actor: actor, Action: model.AuditUserCreate, Result: model.AuditSuccess,
		Detail: "created user " + created.Username + " (" + string(created.Role) + ")",
	}); err != nil {
		return model.User{}, fmt.Errorf("auditing user create: %w", err)
	}

	return created, nil
}

// List returns users oldest first.
func (u *Users) List(ctx context.Context) ([]model.User, error) {
	return u.users.List(ctx, maxListLimit)
}

// Verify checks a username/password pair for login. Unknown users,
// disabled accounts, and wrong passwords all report ErrUnauthorized
// so callers cannot distinguish them.
func (u *Users) Verify(ctx context.Context, username, password string) (model.User, error) {
	user, err := u.users.ByUsername(ctx, username)
	if err != nil {
		if errors.Is(err, store.ErrUserNotFound) {
			return model.User{}, ErrUnauthorized
		}

		return model.User{}, fmt.Errorf("looking up user: %w", err)
	}

	if user.Disabled() {
		return model.User{}, ErrUnauthorized
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)); err != nil {
		return model.User{}, ErrUnauthorized
	}

	return user, nil
}

// Lookup returns one enabled user for session validation. Unknown and
// disabled accounts report ErrUnauthorized so sessions die the moment
// an account is deleted or disabled.
func (u *Users) Lookup(ctx context.Context, username string) (model.User, error) {
	user, err := u.users.ByUsername(ctx, username)
	if err != nil {
		if errors.Is(err, store.ErrUserNotFound) {
			return model.User{}, ErrUnauthorized
		}

		return model.User{}, fmt.Errorf("looking up user: %w", err)
	}

	if user.Disabled() {
		return model.User{}, ErrUnauthorized
	}

	return user, nil
}

// SetRole changes one user's role.
func (u *Users) SetRole(
	ctx context.Context,
	id int64,
	role model.Role,
	actor string,
) (model.User, error) {
	if actor == "" {
		return model.User{}, fmt.Errorf("%w: actor is required", ErrInvalidInput)
	}

	if role != model.RoleAdmin && role != model.RoleViewer {
		return model.User{}, fmt.Errorf("%w: role %q (want admin or viewer)", ErrInvalidInput, role)
	}

	user, err := u.users.ByID(ctx, id)
	if err != nil {
		return model.User{}, err
	}

	if user.Role == model.RoleAdmin && role == model.RoleViewer {
		if err := u.requireAnotherAdmin(ctx, user); err != nil {
			return model.User{}, err
		}
	}

	user.Role = role

	if err := u.users.Update(ctx, user); err != nil {
		return model.User{}, err
	}

	if _, err := u.audit.Record(ctx, model.AuditRecord{
		Actor: actor, Action: model.AuditUserUpdate, Result: model.AuditSuccess,
		Detail: "changed role of user " + user.Username + " to " + string(role),
	}); err != nil {
		return model.User{}, fmt.Errorf("auditing user update: %w", err)
	}

	return user, nil
}

// SetPassword resets one user's password.
func (u *Users) SetPassword(
	ctx context.Context,
	id int64,
	password, actor string,
) error {
	if actor == "" {
		return fmt.Errorf("%w: actor is required", ErrInvalidInput)
	}

	if err := checkPassword(password); err != nil {
		return err
	}

	user, err := u.users.ByID(ctx, id)
	if err != nil {
		return err
	}

	hash, err := hashPassword(password)
	if err != nil {
		return err
	}

	user.PasswordHash = hash

	if err := u.users.Update(ctx, user); err != nil {
		return err
	}

	if _, err := u.audit.Record(ctx, model.AuditRecord{
		Actor: actor, Action: model.AuditUserUpdate, Result: model.AuditSuccess,
		Detail: "reset password of user " + user.Username,
	}); err != nil {
		return fmt.Errorf("auditing user update: %w", err)
	}

	return nil
}

// SetDisabled locks or unlocks one account.
func (u *Users) SetDisabled(
	ctx context.Context,
	id int64,
	disabled bool,
	actor string,
) (model.User, error) {
	if actor == "" {
		return model.User{}, fmt.Errorf("%w: actor is required", ErrInvalidInput)
	}

	user, err := u.users.ByID(ctx, id)
	if err != nil {
		return model.User{}, err
	}

	if disabled && !user.Disabled() {
		if err := u.requireAnotherAdmin(ctx, user); err != nil {
			return model.User{}, err
		}
	}

	if disabled {
		now := time.Now()
		user.DisabledAt = &now
	} else {
		user.DisabledAt = nil
	}

	if err := u.users.Update(ctx, user); err != nil {
		return model.User{}, err
	}

	verb := "enabled"
	if disabled {
		verb = "disabled"
	}

	if _, err := u.audit.Record(ctx, model.AuditRecord{
		Actor: actor, Action: model.AuditUserUpdate, Result: model.AuditSuccess,
		Detail: verb + " user " + user.Username,
	}); err != nil {
		return model.User{}, fmt.Errorf("auditing user update: %w", err)
	}

	return user, nil
}

// Delete removes one user. Audit history keeps the username as text.
func (u *Users) Delete(ctx context.Context, id int64, actor string) error {
	if actor == "" {
		return fmt.Errorf("%w: actor is required", ErrInvalidInput)
	}

	user, err := u.users.ByID(ctx, id)
	if err != nil {
		return err
	}

	if err := u.requireAnotherAdmin(ctx, user); err != nil {
		return err
	}

	if err := u.users.Delete(ctx, id); err != nil {
		return err
	}

	if _, err := u.audit.Record(ctx, model.AuditRecord{
		Actor: actor, Action: model.AuditUserDelete, Result: model.AuditSuccess,
		Detail: "deleted user " + user.Username,
	}); err != nil {
		return fmt.Errorf("auditing user delete: %w", err)
	}

	return nil
}

// EnsureAdmin guarantees a usable admin account: when no enabled
// admin exists it creates (or repairs) the bootstrap admin from the
// configured password and reports seeded true. Otherwise it leaves
// every account untouched and reports seeded false, so later password
// rotations happen through user management, not the environment.
func (u *Users) EnsureAdmin(ctx context.Context, password string) (bool, error) {
	count, err := u.users.CountEnabledAdmins(ctx)
	if err != nil {
		return false, err
	}

	if count > 0 {
		return false, nil
	}

	if err := checkPassword(password); err != nil {
		return false, err
	}

	hash, err := hashPassword(password)
	if err != nil {
		return false, err
	}

	existing, err := u.users.ByUsername(ctx, bootstrapAdmin)
	if err != nil && !errors.Is(err, store.ErrUserNotFound) {
		return false, fmt.Errorf("looking up admin: %w", err)
	}

	if errors.Is(err, store.ErrUserNotFound) {
		if _, err := u.users.Create(ctx, model.User{
			Username: bootstrapAdmin, PasswordHash: hash, Role: model.RoleAdmin,
		}); err != nil {
			return false, fmt.Errorf("seeding admin: %w", err)
		}

		return true, nil
	}

	existing.PasswordHash = hash
	existing.Role = model.RoleAdmin
	existing.DisabledAt = nil

	if err := u.users.Update(ctx, existing); err != nil {
		return false, fmt.Errorf("repairing admin: %w", err)
	}

	return true, nil
}

// requireAnotherAdmin rejects changes that would leave no enabled
// admin. Non-admin and already-disabled accounts pass through.
func (u *Users) requireAnotherAdmin(ctx context.Context, user model.User) error {
	if user.Role != model.RoleAdmin || user.Disabled() {
		return nil
	}

	count, err := u.users.CountEnabledAdmins(ctx)
	if err != nil {
		return err
	}

	if count <= 1 {
		return fmt.Errorf("%w: cannot remove the last enabled admin", ErrConflict)
	}

	return nil
}

// checkUserInput validates the creation envelope: actor, username
// shape and uniqueness, role, and password strength.
func checkUserInput(
	ctx context.Context,
	users *store.UserStore,
	input CreateUserInput,
	actor string,
) error {
	if actor == "" {
		return fmt.Errorf("%w: actor is required", ErrInvalidInput)
	}

	if len(input.Username) == 0 || len(input.Username) > maxUsernameLength ||
		!usernamePattern.MatchString(input.Username) {
		return fmt.Errorf("%w: username %q (want 1-64 chars matching ^[a-z0-9-]+$)", ErrInvalidInput, input.Username)
	}

	if _, err := users.ByUsername(ctx, input.Username); err == nil {
		return fmt.Errorf("%w: username %q already exists", ErrConflict, input.Username)
	} else if !errors.Is(err, store.ErrUserNotFound) {
		return fmt.Errorf("checking username: %w", err)
	}

	if input.Role != model.RoleAdmin && input.Role != model.RoleViewer {
		return fmt.Errorf("%w: role %q (want admin or viewer)", ErrInvalidInput, input.Role)
	}

	return checkPassword(input.Password)
}

// checkPassword enforces the managed-account password policy.
func checkPassword(password string) error {
	if len(password) < minPasswordLength {
		return fmt.Errorf("%w: password is too short (want at least %d characters)", ErrInvalidInput, minPasswordLength)
	}

	if len(password) > maxPasswordLength {
		return fmt.Errorf("%w: password is too long (want at most %d bytes)", ErrInvalidInput, maxPasswordLength)
	}

	return nil
}

// hashPassword hashes a validated password with bcrypt.
func hashPassword(password string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", fmt.Errorf("hashing password: %w", err)
	}

	return string(hash), nil
}
