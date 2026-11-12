package service

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/oralecarlangelo/touchgrass/internal/authz"
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
	// decoySecretBytes is the size of the value behind decoyHash.
	decoySecretBytes = 32
)

// usernamePattern constrains managed usernames to the service-id shape.
var usernamePattern = regexp.MustCompile(`^[a-z0-9-]+$`)

// CreateUserInput carries new-account fields. The scope is explicit:
// AllServices, or the listed Services.
type CreateUserInput struct {
	Username    string
	Password    string
	Role        model.Role
	AllServices bool
	Services    []string
}

// Users manages login accounts: validation, bcrypt hashing, roles and
// service scope, the last-admin guard, and auditing. Callers never see password hashes
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

// Create validates and stores one user with its role and scope. The
// password is temporary: the account must replace it at first login.
func (u *Users) Create(
	ctx context.Context,
	input CreateUserInput,
	actor string,
) (model.User, error) {
	if err := checkUserInput(ctx, u.users, input, actor); err != nil {
		return model.User{}, err
	}

	access, err := u.checkAccess(ctx, model.UserAccess{
		Role: input.Role, AllServices: input.AllServices, Services: input.Services,
	})
	if err != nil {
		return model.User{}, err
	}

	hash, err := hashPassword(input.Password)
	if err != nil {
		return model.User{}, err
	}

	created, err := u.users.Create(ctx, model.User{
		Username: input.Username, PasswordHash: hash, Role: access.Role,
		AllServices: access.AllServices, Services: access.Services, MustChangePassword: true,
	}, actor)
	if err != nil {
		return model.User{}, fmt.Errorf("creating user %q: %w", input.Username, err)
	}

	if _, err := u.audit.Record(ctx, model.AuditRecord{
		Actor: actor, Action: model.AuditUserCreate, Result: model.AuditSuccess,
		Detail: "created user " + created.Username + " (" + describeAccess(access) + ")",
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
			spendPasswordCheck(password)

			return model.User{}, ErrUnauthorized
		}

		return model.User{}, fmt.Errorf("looking up user: %w", err)
	}

	// The hash is compared before the disabled flag is read, so a
	// disabled account takes as long to refuse as an enabled one.
	mismatch := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password))

	if user.Disabled() || mismatch != nil {
		return model.User{}, ErrUnauthorized
	}

	return user, nil
}

// decoyHash is a bcrypt hash of a random value nobody knows, at the
// cost new passwords are hashed with.
var decoyHash = sync.OnceValue(func() []byte {
	secret := make([]byte, decoySecretBytes)

	if _, err := rand.Read(secret); err != nil {
		return nil
	}

	hash, err := bcrypt.GenerateFromPassword(secret, bcrypt.DefaultCost)
	if err != nil {
		return nil
	}

	return hash
})

// spendPasswordCheck does the work of checking password against a
// hash that cannot match. A login for a username that does not exist
// then takes about as long as one with a wrong password, so the
// response time does not say which usernames exist.
func spendPasswordCheck(password string) {
	//nolint:errcheck,gosec // the comparison cannot succeed; only the time it takes matters.
	bcrypt.CompareHashAndPassword(decoyHash(), []byte(password))
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

// SetRole changes one user's role and keeps their scope, except that a
// user promoted to admin always gets every service.
func (u *Users) SetRole(
	ctx context.Context,
	id int64,
	role model.Role,
	actor string,
) (model.User, error) {
	if actor == "" {
		return model.User{}, fmt.Errorf("%w: actor is required", ErrInvalidInput)
	}

	if err := checkRole(role); err != nil {
		return model.User{}, err
	}

	user, err := u.users.ByID(ctx, id)
	if err != nil {
		return model.User{}, err
	}

	access := model.UserAccess{Role: role, AllServices: user.AllServices, Services: user.Services}
	if role == model.RoleAdmin {
		access.AllServices = true
		access.Services = []string{}
	}

	detail := "changed role of user " + user.Username + " to " + string(role)
	if role == model.RoleAdmin && !user.AllServices {
		detail += " (scope widened to all services)"
	}

	return u.applyAccess(ctx, user, access, actor, detail)
}

// SetAccess replaces one user's role and scope together. An unknown
// role or service is invalid input; scoping an admin, or taking the
// admin role from the last enabled admin, is a conflict.
func (u *Users) SetAccess(
	ctx context.Context,
	id int64,
	access model.UserAccess,
	actor string,
) (model.User, error) {
	if actor == "" {
		return model.User{}, fmt.Errorf("%w: actor is required", ErrInvalidInput)
	}

	checked, err := u.checkAccess(ctx, access)
	if err != nil {
		return model.User{}, err
	}

	user, err := u.users.ByID(ctx, id)
	if err != nil {
		return model.User{}, err
	}

	return u.applyAccess(ctx, user, checked, actor,
		"set access of user "+user.Username+": "+describeAccess(checked))
}

// applyAccess stores a validated role and scope behind the last-admin
// guard and audits it.
func (u *Users) applyAccess(
	ctx context.Context,
	user model.User,
	access model.UserAccess,
	actor, detail string,
) (model.User, error) {
	if user.Role == model.RoleAdmin && access.Role != model.RoleAdmin {
		if err := u.requireAnotherAdmin(ctx, user); err != nil {
			return model.User{}, err
		}
	}

	services, err := u.users.SetAccess(ctx, user.ID, access, actor)
	if err != nil {
		return model.User{}, err
	}

	user.Role = access.Role
	user.AllServices = access.AllServices
	user.Services = services

	if _, err := u.audit.Record(ctx, model.AuditRecord{
		Actor: actor, Action: model.AuditUserUpdate, Result: model.AuditSuccess, Detail: detail,
	}); err != nil {
		return model.User{}, fmt.Errorf("auditing user update: %w", err)
	}

	return user, nil
}

// checkAccess validates a role and scope and returns it normalized:
// services sorted and de-duplicated, and empty for an all-services
// scope.
func (u *Users) checkAccess(ctx context.Context, access model.UserAccess) (model.UserAccess, error) {
	if err := checkRole(access.Role); err != nil {
		return model.UserAccess{}, err
	}

	if access.AllServices && len(access.Services) > 0 {
		return model.UserAccess{}, fmt.Errorf(
			"%w: choose all services or a list of services, not both", ErrInvalidInput)
	}

	if access.Role == model.RoleAdmin && !access.AllServices {
		return model.UserAccess{}, fmt.Errorf("%w: an admin always has every service", ErrConflict)
	}

	services := slices.Clone(access.Services)
	if services == nil {
		services = []string{}
	}

	slices.Sort(services)
	services = slices.Compact(services)

	missing, err := u.users.MissingServices(ctx, services)
	if err != nil {
		return model.UserAccess{}, fmt.Errorf("checking services: %w", err)
	}

	if len(missing) > 0 {
		return model.UserAccess{}, fmt.Errorf("%w: unknown service %q", ErrInvalidInput, missing[0])
	}

	return model.UserAccess{Role: access.Role, AllServices: access.AllServices, Services: services}, nil
}

// SetPassword resets one user's password to a temporary one chosen by
// an admin: the account must replace it at its next login. It returns
// the user so the caller can end that user's sessions.
func (u *Users) SetPassword(
	ctx context.Context,
	id int64,
	password, actor string,
) (model.User, error) {
	if actor == "" {
		return model.User{}, fmt.Errorf("%w: actor is required", ErrInvalidInput)
	}

	if err := checkPassword(password); err != nil {
		return model.User{}, err
	}

	user, err := u.users.ByID(ctx, id)
	if err != nil {
		return model.User{}, err
	}

	hash, err := hashPassword(password)
	if err != nil {
		return model.User{}, err
	}

	user.PasswordHash = hash
	user.MustChangePassword = true

	if err := u.users.Update(ctx, user); err != nil {
		return model.User{}, err
	}

	u.discardInitialPassword(user.Username)

	if _, err := u.audit.Record(ctx, model.AuditRecord{
		Actor: actor, Action: model.AuditUserUpdate, Result: model.AuditSuccess,
		Detail: "reset password of user " + user.Username + " to a temporary one",
	}); err != nil {
		return model.User{}, fmt.Errorf("auditing user update: %w", err)
	}

	return user, nil
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

// Delete removes one user and returns the removed account. Audit
// history keeps the username as text.
func (u *Users) Delete(ctx context.Context, id int64, actor string) (model.User, error) {
	if actor == "" {
		return model.User{}, fmt.Errorf("%w: actor is required", ErrInvalidInput)
	}

	user, err := u.users.ByID(ctx, id)
	if err != nil {
		return model.User{}, err
	}

	if err := u.requireAnotherAdmin(ctx, user); err != nil {
		return model.User{}, err
	}

	if err := u.users.Delete(ctx, id); err != nil {
		return model.User{}, err
	}

	if _, err := u.audit.Record(ctx, model.AuditRecord{
		Actor: actor, Action: model.AuditUserDelete, Result: model.AuditSuccess,
		Detail: "deleted user " + user.Username,
	}); err != nil {
		return model.User{}, fmt.Errorf("auditing user delete: %w", err)
	}

	return user, nil
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
// that name into an enabled admin with every service, with the given
// password. The scope is reset first: a row that was a scoped viewer
// cannot become an admin while it is still scoped.
func (u *Users) seedAdmin(ctx context.Context, password string, mustChange bool) error {
	hash, err := hashPassword(password)
	if err != nil {
		return err
	}

	existing, err := u.users.ByUsername(ctx, bootstrapAdmin)
	if errors.Is(err, store.ErrUserNotFound) {
		if _, err := u.users.Create(ctx, model.User{
			Username: bootstrapAdmin, PasswordHash: hash, Role: model.RoleAdmin,
			AllServices: true, MustChangePassword: mustChange,
		}, bootstrapAdmin); err != nil {
			return fmt.Errorf("seeding admin: %w", err)
		}

		return nil
	}

	if err != nil {
		return fmt.Errorf("looking up admin: %w", err)
	}

	if _, err := u.users.SetAccess(ctx, existing.ID, model.UserAccess{
		Role: model.RoleAdmin, AllServices: true, Services: []string{},
	}, bootstrapAdmin); err != nil {
		return fmt.Errorf("repairing admin access: %w", err)
	}

	existing.PasswordHash = hash
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

	if err := checkRole(input.Role); err != nil {
		return err
	}

	return checkPassword(input.Password)
}

// checkRole rejects a role no account may be given.
func checkRole(role model.Role) error {
	if !authz.Assignable(role) {
		return fmt.Errorf("%w: role %q (want admin, developer, or viewer)", ErrInvalidInput, role)
	}

	return nil
}

// describeAccess renders a role and scope for audit details.
func describeAccess(access model.UserAccess) string {
	if access.AllServices {
		return "role " + string(access.Role) + ", all services"
	}

	return "role " + string(access.Role) + ", services [" + strings.Join(access.Services, ", ") + "]"
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
