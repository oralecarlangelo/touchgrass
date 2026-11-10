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
	// bootstrapAdmin is the username seeded on first boot, from the
	// configured admin password or a generated one-time password.
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
	// initialPasswordFile is where a generated one-time admin password
	// is written; empty disables generation.
	initialPasswordFile string
}

// NewUsers builds a Users service.
func NewUsers(users *store.UserStore, audit *Audit) *Users {
	return &Users{users: users, audit: audit}
}

// WithInitialPasswordFile sets where EnsureAdmin writes a generated
// one-time admin password and returns u for chaining at wiring time.
// Without it, EnsureAdmin needs a configured password.
func (u *Users) WithInitialPasswordFile(path string) *Users {
	u.initialPasswordFile = path

	return u
}

// AdminBootstrap reports what EnsureAdmin found or did at startup.
type AdminBootstrap struct {
	// Seeded reports that the bootstrap admin was created or repaired.
	Seeded bool
	// Generated reports that the seeded password was generated rather
	// than configured; it is in PasswordFile and must be replaced at
	// first login.
	Generated bool
	// Pending reports that the bootstrap admin still holds a one-time
	// password from an earlier boot or a reset.
	Pending bool
	// PasswordFile is the generated password's file, when one exists.
	PasswordFile string
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
	user.MustChangePassword = false

	if err := u.users.Update(ctx, user); err != nil {
		return err
	}

	u.discardInitialPassword(user.Username)

	if _, err := u.audit.Record(ctx, model.AuditRecord{
		Actor: actor, Action: model.AuditUserUpdate, Result: model.AuditSuccess,
		Detail: "reset password of user " + user.Username,
	}); err != nil {
		return fmt.Errorf("auditing user update: %w", err)
	}

	return nil
}

// ChangeOwnPassword replaces the caller's own password after proving
// the current one, and lifts the one-time-password restriction. A
// wrong current password reports ErrUnauthorized.
func (u *Users) ChangeOwnPassword(ctx context.Context, username, current, next string) error {
	user, err := u.Verify(ctx, username, current)
	if err != nil {
		return err
	}

	if next == current {
		return fmt.Errorf("%w: the new password must differ from the current one", ErrInvalidInput)
	}

	if err := checkPassword(next); err != nil {
		return err
	}

	hash, err := hashPassword(next)
	if err != nil {
		return err
	}

	detail := "changed own password"
	if user.MustChangePassword {
		detail = "replaced one-time password"
	}

	user.PasswordHash = hash
	user.MustChangePassword = false

	if err := u.users.Update(ctx, user); err != nil {
		return err
	}

	u.discardInitialPassword(user.Username)

	if _, err := u.audit.Record(ctx, model.AuditRecord{
		Actor: user.Username, Action: model.AuditUserUpdate, Result: model.AuditSuccess, Detail: detail,
	}); err != nil {
		return fmt.Errorf("auditing password change: %w", err)
	}

	return nil
}

// ResetWithOneTimePassword gives one enabled account a generated
// one-time password and returns it once: the account can then only
// change its password until it has. It is the offline recovery path
// for a lost password (the `users reset-password` command).
func (u *Users) ResetWithOneTimePassword(ctx context.Context, username, actor string) (string, error) {
	if actor == "" {
		return "", fmt.Errorf("%w: actor is required", ErrInvalidInput)
	}

	user, err := u.users.ByUsername(ctx, username)
	if err != nil {
		return "", fmt.Errorf("looking up user %q: %w", username, err)
	}

	if user.Disabled() {
		return "", fmt.Errorf("%w: user %q is disabled", ErrConflict, username)
	}

	password := generatePassword()

	hash, err := hashPassword(password)
	if err != nil {
		return "", err
	}

	user.PasswordHash = hash
	user.MustChangePassword = true

	if err := u.users.Update(ctx, user); err != nil {
		return "", err
	}

	u.discardInitialPassword(user.Username)

	if _, err := u.audit.Record(ctx, model.AuditRecord{
		Actor: actor, Action: model.AuditUserUpdate, Result: model.AuditSuccess,
		Detail: "reset password of user " + user.Username + " to a one-time password",
	}); err != nil {
		return "", fmt.Errorf("auditing password reset: %w", err)
	}

	return password, nil
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

// EnsureAdmin guarantees a usable admin account. When no enabled admin
// exists it creates (or repairs) the bootstrap admin: from the
// configured password when one is set, otherwise from a generated
// one-time password written to the initial password file, which the
// admin must replace at first login. When an enabled admin exists it
// leaves every account untouched, so later rotations happen through
// user management, not the environment — except that a password
// configured while the generated one is still unused takes its place.
func (u *Users) EnsureAdmin(ctx context.Context, password string) (AdminBootstrap, error) {
	count, err := u.users.CountEnabledAdmins(ctx)
	if err != nil {
		return AdminBootstrap{}, err
	}

	if count > 0 {
		return u.reconcileBootstrap(ctx, password)
	}

	if password != "" {
		if err := checkPassword(password); err != nil {
			return AdminBootstrap{}, err
		}

		if err := u.seedAdmin(ctx, password, false); err != nil {
			return AdminBootstrap{}, err
		}

		u.discardInitialPassword(bootstrapAdmin)

		return AdminBootstrap{Seeded: true}, nil
	}

	return u.seedGeneratedAdmin(ctx)
}

// seedGeneratedAdmin writes a one-time password to the initial
// password file, then seeds the bootstrap admin with it. The file is
// written first so a crash between the two leaves no admin whose
// password nobody can read; the next boot simply generates again.
func (u *Users) seedGeneratedAdmin(ctx context.Context) (AdminBootstrap, error) {
	if u.initialPasswordFile == "" {
		return AdminBootstrap{}, fmt.Errorf(
			"%w: no admin password is configured and no initial password file is set", ErrInvalidInput)
	}

	password := generatePassword()

	if err := writeInitialPassword(u.initialPasswordFile, password); err != nil {
		return AdminBootstrap{}, err
	}

	if err := u.seedAdmin(ctx, password, true); err != nil {
		return AdminBootstrap{}, errors.Join(err, removeIfPresent(u.initialPasswordFile))
	}

	return AdminBootstrap{Seeded: true, Generated: true, PasswordFile: u.initialPasswordFile}, nil
}

// seedAdmin creates the bootstrap admin, or repairs an existing row of
// that name into an enabled admin, with the given password.
func (u *Users) seedAdmin(ctx context.Context, password string, mustChange bool) error {
	hash, err := hashPassword(password)
	if err != nil {
		return err
	}

	existing, err := u.users.ByUsername(ctx, bootstrapAdmin)
	if errors.Is(err, store.ErrUserNotFound) {
		if _, err := u.users.Create(ctx, model.User{
			Username: bootstrapAdmin, PasswordHash: hash, Role: model.RoleAdmin, MustChangePassword: mustChange,
		}); err != nil {
			return fmt.Errorf("seeding admin: %w", err)
		}

		return nil
	}

	if err != nil {
		return fmt.Errorf("looking up admin: %w", err)
	}

	existing.PasswordHash = hash
	existing.Role = model.RoleAdmin
	existing.MustChangePassword = mustChange
	existing.DisabledAt = nil

	if err := u.users.Update(ctx, existing); err != nil {
		return fmt.Errorf("repairing admin: %w", err)
	}

	return nil
}

// reconcileBootstrap handles a boot that already has an enabled admin.
// It reports a bootstrap admin still holding a one-time password,
// applies a newly configured password in its place, and removes an
// initial password file nobody needs any more.
func (u *Users) reconcileBootstrap(ctx context.Context, password string) (AdminBootstrap, error) {
	admin, err := u.users.ByUsername(ctx, bootstrapAdmin)
	if err != nil && !errors.Is(err, store.ErrUserNotFound) {
		return AdminBootstrap{}, fmt.Errorf("looking up admin: %w", err)
	}

	pending := err == nil && admin.MustChangePassword && !admin.Disabled() && admin.Role == model.RoleAdmin
	if !pending {
		u.discardInitialPassword(bootstrapAdmin)

		return AdminBootstrap{}, nil
	}

	if password == "" {
		bootstrap := AdminBootstrap{Pending: true}
		if u.initialPasswordFile != "" && fileExists(u.initialPasswordFile) {
			bootstrap.PasswordFile = u.initialPasswordFile
		}

		return bootstrap, nil
	}

	if err := checkPassword(password); err != nil {
		return AdminBootstrap{}, err
	}

	if err := u.seedAdmin(ctx, password, false); err != nil {
		return AdminBootstrap{}, err
	}

	u.discardInitialPassword(bootstrapAdmin)

	return AdminBootstrap{Seeded: true}, nil
}

// discardInitialPassword removes the initial password file once the
// bootstrap admin's password is no longer the generated one. Removal
// is best-effort: the file then holds a dead password, and failing a
// completed password change over it would help nobody.
func (u *Users) discardInitialPassword(username string) {
	if username != bootstrapAdmin || u.initialPasswordFile == "" {
		return
	}

	_ = removeIfPresent(u.initialPasswordFile)
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
