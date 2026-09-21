package http

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"sync"
	"time"

	nethttp "net/http"

	"github.com/oralecarlangelo/touchgrass/internal/model"
	"golang.org/x/crypto/bcrypt"
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

// errAuthRequired reports a missing or expired session.
var errAuthRequired = errors.New("missing or expired session")

// Authenticator holds the admin session store and login rate limiting.
type Authenticator struct {
	password     []byte
	secureCookie bool
	mutex        sync.Mutex
	sessions     map[string]time.Time
	failures     map[string][]time.Time
}

// HashPassword hashes the configured admin password with bcrypt.
func HashPassword(password string) ([]byte, error) {
	if password == "" {
		return nil, errors.New("admin password is required")
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, fmt.Errorf("hashing admin password: %w", err)
	}

	return hash, nil
}

// NewAuthenticator builds an Authenticator for one bcrypt password hash.
func NewAuthenticator(passwordHash []byte, secureCookie bool) *Authenticator {
	return &Authenticator{
		password:     bytes.Clone(passwordHash),
		secureCookie: secureCookie,
		sessions:     map[string]time.Time{},
		failures:     map[string][]time.Time{},
	}
}

// Login verifies the password and mints a session token.
func (a *Authenticator) Login(password string) (string, bool) {
	if len(a.password) == 0 {
		return "", false
	}

	if err := bcrypt.CompareHashAndPassword(a.password, []byte(password)); err != nil {
		return "", false
	}

	token := make([]byte, tokenBytes)

	if _, err := rand.Read(token); err != nil {
		return "", false
	}

	session := hex.EncodeToString(token)

	a.mutex.Lock()
	a.sessions[session] = time.Now().Add(sessionExpiry)
	a.mutex.Unlock()

	return session, true
}

// Authenticate validates the request cookie.
func (a *Authenticator) Authenticate(r *nethttp.Request) bool {
	cookie, err := r.Cookie(sessionCookie)
	if err != nil || cookie.Value == "" {
		return false
	}

	a.mutex.Lock()
	defer a.mutex.Unlock()

	expires, ok := a.sessions[cookie.Value]
	if !ok {
		return false
	}

	if time.Now().After(expires) {
		delete(a.sessions, cookie.Value)

		return false
	}

	return true
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

// Middleware rejects unauthenticated API requests outside the public set.
func (a *Authenticator) Middleware(logger *slog.Logger, next nethttp.Handler) nethttp.Handler {
	return nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		if isPublic(r.URL.Path) || !strings.HasPrefix(r.URL.Path, "/api/") {
			next.ServeHTTP(w, r)

			return
		}

		if a.Authenticate(r) {
			next.ServeHTTP(w, r)

			return
		}

		writeError(w, logger, errAuthRequired, errAuthMessage, "unauthorized", nethttp.StatusUnauthorized)
	})
}

// isPublic reports API paths that need no session.
func isPublic(path string) bool {
	return path == "/api/health" || path == "/api/auth/login" || path == "/api/ingest"
}

// clientIP returns the peer host without trusting proxy headers.
func clientIP(r *nethttp.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}

	return host
}

// loginRequest is the login body.
type loginRequest struct {
	Password string `json:"password"`
}

// meResponse is the session probe payload.
type meResponse struct {
	Authenticated bool `json:"authenticated"`
}

// handleLogin mints a session cookie.
func (s *Server) handleLogin(w nethttp.ResponseWriter, r *nethttp.Request) {
	ip := clientIP(r)

	if s.auth.AttemptsExceeded(ip) {
		s.auditLogin(r.Context(), model.AuditFailure, "rate-limited login from "+ip)
		writeError(w, s.logger, errAuthRequired, "too many attempts, try again later", "rate_limited", nethttp.StatusTooManyRequests)

		return
	}

	var body loginRequest

	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, s.logger, err, "invalid JSON body", "invalid_request", nethttp.StatusBadRequest)

		return
	}

	token, ok := s.auth.Login(body.Password)
	s.auth.RegisterFailure(ip, !ok)

	if !ok {
		s.auditLogin(r.Context(), model.AuditFailure, "failed login from "+ip)
		writeError(w, s.logger, errAuthRequired, "invalid password", "unauthorized", nethttp.StatusUnauthorized)

		return
	}

	s.auditLogin(r.Context(), model.AuditSuccess, "login from "+ip)
	nethttp.SetCookie(w, s.auth.cookie(token, int(sessionExpiry.Seconds())))
	w.WriteHeader(nethttp.StatusNoContent)
}

// auditLogin records a login attempt without logging the password.
func (s *Server) auditLogin(ctx context.Context, result, detail string) {
	if _, err := s.audit.Record(ctx, model.AuditRecord{
		Actor: actorAdmin, Action: model.AuditLogin, Result: result, Detail: detail,
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
func (s *Server) handleMe(w nethttp.ResponseWriter, _ *nethttp.Request) {
	writeJSON(w, s.logger, nethttp.StatusOK, meResponse{Authenticated: true})
}
