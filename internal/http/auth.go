package http

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"strings"
	"sync"
	"time"

	nethttp "net/http"

	"github.com/oralecarlangelo/touchgrass/internal/model"
	"github.com/oralecarlangelo/touchgrass/internal/service"
)

// Session and rate-limit policy.
const (
	sessionCookie  = "touchgrass_session"
	maxFailures    = 5
	failureWindow  = time.Minute
	tokenBytes     = 32
	sessionExpiry  = 24 * time.Hour
	cookiePath     = "/"
	errAuthMessage = "authentication required"
)

// Login and authorization policy.
const (
	// defaultLoginUsername keeps password-only logins working: an
	// empty username means the bootstrap admin.
	defaultLoginUsername = "admin"
	// errForbiddenMessage reports a valid session without the role.
	errForbiddenMessage = "admin role required"
	// errPasswordChangeMessage reports a session still on a one-time
	// password: it may only change that password.
	errPasswordChangeMessage = "change your one-time password to continue"
	// codePasswordChangeRequired is the envelope code clients switch on
	// to show the change-password screen.
	codePasswordChangeRequired = "password_change_required"
	// unknownActor attributes requests that never passed auth.
	unknownActor = "unknown"
)

// Auth errors, translated to user-safe envelopes at the boundary.
var (
	errAuthRequired           = errors.New("missing or expired session")
	errForbidden              = errors.New("role lacks permission")
	errPasswordChangeRequired = errors.New("one-time password not yet replaced")
)

// Identity is the authenticated caller attached to request contexts.
type Identity struct {
	Username string
	Role     model.Role
	// MustChangePassword marks a session on a one-time password.
	MustChangePassword bool
}

// Admin reports full-power access.
func (i Identity) Admin() bool {
	return i.Role == model.RoleAdmin
}

// session binds a token to a username with an expiry. Role and
// disabled state always re-resolve from the store, so role changes
// and disables bite on the very next request.
type session struct {
	username string
	expires  time.Time
}

// Authenticator holds multi-user sessions and login rate limiting.
type Authenticator struct {
	users        *service.Users
	secureCookie bool
	mutex        sync.Mutex
	sessions     map[string]session
	failures     map[string][]time.Time
}

// NewAuthenticator builds an Authenticator over managed users.
func NewAuthenticator(users *service.Users, secureCookie bool) *Authenticator {
	return &Authenticator{
		users:        users,
		secureCookie: secureCookie,
		sessions:     map[string]session{},
		failures:     map[string][]time.Time{},
	}
}

// Login verifies credentials and mints a session token.
func (a *Authenticator) Login(ctx context.Context, username, password string) (string, bool) {
	user, err := a.users.Verify(ctx, username, password)
	if err != nil {
		return "", false
	}

	token := make([]byte, tokenBytes)

	if _, err := rand.Read(token); err != nil {
		return "", false
	}

	sessionID := hex.EncodeToString(token)

	a.mutex.Lock()
	a.sessions[sessionID] = session{username: user.Username, expires: time.Now().Add(sessionExpiry)}
	a.mutex.Unlock()

	return sessionID, true
}

// Authenticate validates the request cookie against the live user
// row, dropping sessions whose account vanished or was disabled.
func (a *Authenticator) Authenticate(r *nethttp.Request) (Identity, bool) {
	cookie, err := r.Cookie(sessionCookie)
	if err != nil || cookie.Value == "" {
		return Identity{}, false
	}

	a.mutex.Lock()

	sess, ok := a.sessions[cookie.Value]

	if !ok || time.Now().After(sess.expires) {
		delete(a.sessions, cookie.Value)
		a.mutex.Unlock()

		return Identity{}, false
	}

	a.mutex.Unlock()

	user, err := a.users.Lookup(r.Context(), sess.username)
	if err != nil {
		if errors.Is(err, service.ErrUnauthorized) {
			a.Logout(cookie.Value)
		}

		return Identity{}, false
	}

	return Identity{
		Username: user.Username, Role: user.Role, MustChangePassword: user.MustChangePassword,
	}, true
}

// DropOtherSessions ends every session of username except keep, so a
// password change logs out whoever else held the old password.
func (a *Authenticator) DropOtherSessions(username, keep string) {
	a.mutex.Lock()
	defer a.mutex.Unlock()

	for token, sess := range a.sessions {
		if sess.username == username && token != keep {
			delete(a.sessions, token)
		}
	}
}

// Logout drops a session token.
func (a *Authenticator) Logout(token string) {
	a.mutex.Lock()
	defer a.mutex.Unlock()

	delete(a.sessions, token)
}

// cookie builds a session cookie. Secure stays configurable because local
// development is plain HTTP; production deployments set it behind TLS.
func (a *Authenticator) cookie(token string, maxAge int) *nethttp.Cookie {
	//nolint:gosec // Secure is intentionally opt-in for localhost dev; CookieSecure enables it in prod.
	return &nethttp.Cookie{
		Name:     sessionCookie,
		Value:    token,
		Path:     cookiePath,
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   a.secureCookie,
		SameSite: nethttp.SameSiteLaxMode,
	}
}

// AttemptsExceeded reports whether ip is rate-limited.
func (a *Authenticator) AttemptsExceeded(ip string) bool {
	a.mutex.Lock()
	defer a.mutex.Unlock()

	cutoff := time.Now().Add(-failureWindow)
	recent := a.failures[ip][:0]

	for _, failure := range a.failures[ip] {
		if failure.After(cutoff) {
			recent = append(recent, failure)
		}
	}

	a.failures[ip] = recent

	return len(recent) >= maxFailures
}

// RegisterFailure records a failed login, clearing on success.
func (a *Authenticator) RegisterFailure(ip string, failed bool) {
	a.mutex.Lock()
	defer a.mutex.Unlock()

	if !failed {
		delete(a.failures, ip)

		return
	}

	a.failures[ip] = append(a.failures[ip], time.Now())
}

// Middleware rejects unauthenticated API requests outside the public
// set and attaches the caller identity to the request context.
func (a *Authenticator) Middleware(logger *slog.Logger, next nethttp.Handler) nethttp.Handler {
	return nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		if isPublic(r.URL.Path) || !strings.HasPrefix(r.URL.Path, "/api/") {
			next.ServeHTTP(w, r)

			return
		}

		identity, ok := a.Authenticate(r)
		if !ok {
			writeError(w, logger, errAuthRequired, errAuthMessage, "unauthorized", nethttp.StatusUnauthorized)

			return
		}

		if identity.MustChangePassword && !allowedOnOneTimePassword(r.URL.Path) {
			writeError(w, logger, errPasswordChangeRequired, errPasswordChangeMessage,
				codePasswordChangeRequired, nethttp.StatusForbidden)

			return
		}

		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), identityKey{}, identity)))
	})
}

// allowedOnOneTimePassword reports the only routes a session on a
// one-time password may call: see who it is, change the password, or
// leave.
func allowedOnOneTimePassword(path string) bool {
	return path == "/api/auth/me" || path == "/api/auth/password" || path == "/api/auth/logout"
}

// requireAdmin rejects authenticated callers without the admin role.
// It runs inside the auth middleware, so identity is always present.
func (s *Server) requireAdmin(next nethttp.HandlerFunc) nethttp.HandlerFunc {
	return func(w nethttp.ResponseWriter, r *nethttp.Request) {
		identity, _ := identityFrom(r)

		if !identity.Admin() {
			writeError(w, s.logger, errForbidden, errForbiddenMessage, "forbidden", nethttp.StatusForbidden)

			return
		}

		next(w, r)
	}
}

// identityKey carries the caller identity in request contexts.
type identityKey struct{}

// identityFrom returns the authenticated caller.
func identityFrom(r *nethttp.Request) (Identity, bool) {
	identity, ok := r.Context().Value(identityKey{}).(Identity)

	return identity, ok
}

// requestActor returns the username for audit attribution.
func requestActor(r *nethttp.Request) string {
	if identity, ok := identityFrom(r); ok {
		return identity.Username
	}

	return unknownActor
}

// isPublic reports API paths that need no session.
func isPublic(path string) bool {
	return path == "/api/health" || path == "/api/auth/login" ||
		path == "/api/ingest" || path == "/api/ingest/logs"
}

// clientIP returns the peer host without trusting proxy headers.
func clientIP(r *nethttp.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}

	return host
}

// loginRequest is the login body. An empty username means admin.
type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// meResponse is the session probe payload.
type meResponse struct {
	Authenticated      bool   `json:"authenticated"`
	Username           string `json:"username"`
	Role               string `json:"role"`
	MustChangePassword bool   `json:"must_change_password"`
}

// changePasswordRequest is the self-service password change body.
type changePasswordRequest struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

// handleLogin mints a session cookie.
func (s *Server) handleLogin(w nethttp.ResponseWriter, r *nethttp.Request) {
	var body loginRequest

	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, s.logger, err, "invalid JSON body", "invalid_request", nethttp.StatusBadRequest)

		return
	}

	username := body.Username
	if username == "" {
		username = defaultLoginUsername
	}

	ip := clientIP(r)

	if s.auth.AttemptsExceeded(ip) {
		s.auditLogin(r.Context(), username, model.AuditFailure, "rate-limited login from "+ip)
		writeError(w, s.logger, errAuthRequired, "too many attempts, try again later", "rate_limited", nethttp.StatusTooManyRequests)

		return
	}

	token, ok := s.auth.Login(r.Context(), username, body.Password)
	s.auth.RegisterFailure(ip, !ok)

	if !ok {
		s.auditLogin(r.Context(), username, model.AuditFailure, "failed login from "+ip)
		writeError(w, s.logger, errAuthRequired, "invalid username or password", "unauthorized", nethttp.StatusUnauthorized)

		return
	}

	s.auditLogin(r.Context(), username, model.AuditSuccess, "login from "+ip)
	nethttp.SetCookie(w, s.auth.cookie(token, int(sessionExpiry.Seconds())))
	w.WriteHeader(nethttp.StatusNoContent)
}

// auditLogin records a login attempt without logging the password.
func (s *Server) auditLogin(ctx context.Context, username, result, detail string) {
	if _, err := s.audit.Record(ctx, model.AuditRecord{
		Actor: username, Action: model.AuditLogin, Result: result, Detail: detail,
	}); err != nil {
		s.logger.Warn("login audit failed", "error", err)
	}
}

// handleLogout clears the session cookie.
func (s *Server) handleLogout(w nethttp.ResponseWriter, r *nethttp.Request) {
	if cookie, err := r.Cookie(sessionCookie); err == nil {
		s.auth.Logout(cookie.Value)
	}

	nethttp.SetCookie(w, s.auth.cookie("", -1))
	w.WriteHeader(nethttp.StatusNoContent)
}

// handleMe reports the session state.
func (s *Server) handleMe(w nethttp.ResponseWriter, r *nethttp.Request) {
	identity, _ := identityFrom(r)
	writeJSON(w, s.logger, nethttp.StatusOK, meResponse{
		Authenticated: true, Username: identity.Username, Role: string(identity.Role),
		MustChangePassword: identity.MustChangePassword,
	})
}

// handleChangePassword replaces the caller's own password. A wrong
// current password counts against the login rate limit, and success
// ends the caller's other sessions.
func (s *Server) handleChangePassword(w nethttp.ResponseWriter, r *nethttp.Request) {
	body, ok := decodeBody[changePasswordRequest](w, s.logger, r)
	if !ok {
		return
	}

	identity, _ := identityFrom(r)
	ip := clientIP(r)

	if s.auth.AttemptsExceeded(ip) {
		writeError(w, s.logger, errAuthRequired, "too many attempts, try again later", "rate_limited", nethttp.StatusTooManyRequests)

		return
	}

	err := s.users.ChangeOwnPassword(r.Context(), identity.Username, body.CurrentPassword, body.NewPassword)
	s.auth.RegisterFailure(ip, errors.Is(err, service.ErrUnauthorized))

	if errors.Is(err, service.ErrUnauthorized) {
		s.auditLogin(r.Context(), identity.Username, model.AuditFailure, "failed password change from "+ip)
		writeError(w, s.logger, err, "current password is incorrect", "invalid_current_password", nethttp.StatusForbidden)

		return
	}

	if err != nil {
		writeServiceError(w, s.logger, err, "failed to change password")

		return
	}

	if cookie, err := r.Cookie(sessionCookie); err == nil {
		s.auth.DropOtherSessions(identity.Username, cookie.Value)
	}

	w.WriteHeader(nethttp.StatusNoContent)
}
