package http

import (
	"context"
	"encoding/json"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	nethttp "net/http"

	"github.com/oralecarlangelo/touchgrass/internal/model"
	"github.com/oralecarlangelo/touchgrass/internal/store"
)

func TestHashPassword(t *testing.T) {
	t.Parallel()

	hash, err := HashPassword("correct horse")
	if err != nil {
		t.Fatalf("HashPassword() error = %v, want nil", err)
	}

	auth := NewAuthenticator(hash, false)

	if _, ok := auth.Login("correct horse"); !ok {
		t.Error("Login(correct) ok = false, want true")
	}

	if _, ok := auth.Login("wrong horse"); ok {
		t.Error("Login(wrong) ok = true, want false")
	}

	if _, err := HashPassword(""); err == nil {
		t.Error("HashPassword(empty) error = nil, want required-password error")
	}

	if _, ok := NewAuthenticator(nil, false).Login("correct horse"); ok {
		t.Error("Login(nil hash) ok = true, want false")
	}

	if _, ok := NewAuthenticator([]byte("not-a-bcrypt-hash"), false).Login("correct horse"); ok {
		t.Error("Login(invalid hash) ok = true, want false")
	}
}

// loginAuditEntries returns the audit entries recorded by login attempts.
func loginAuditEntries(t *testing.T, db *store.DB) []model.Audit {
	t.Helper()

	entries, err := store.NewAuditStore(db).List(context.Background(), "", 10)
	if err != nil {
		t.Fatalf("List() error = %v, want nil", err)
	}

	return entries
}

func TestHandleLoginSuccess(t *testing.T) {
	t.Parallel()

	server, db := fullTestServer(t)

	req := httptest.NewRequestWithContext(
		t.Context(),
		nethttp.MethodPost,
		"/api/auth/login",
		strings.NewReader(`{"password":"test-password"}`),
	)
	rec := httptest.NewRecorder()

	server.handler.ServeHTTP(rec, req)

	res := rec.Result()

	if err := res.Body.Close(); err != nil {
		t.Fatalf("closing body: %v", err)
	}

	if res.StatusCode != nethttp.StatusNoContent {
		t.Fatalf("status = %d, want 204", res.StatusCode)
	}

	cookies := res.Cookies()
	if len(cookies) != 1 {
		t.Fatalf("cookies = %d, want 1", len(cookies))
	}

	cookie := cookies[0]
	if !cookie.HttpOnly || cookie.SameSite != nethttp.SameSiteLaxMode || cookie.Path != "/" {
		t.Errorf("cookie = %+v, want HttpOnly Lax Path=/", cookie)
	}

	if cookie.Secure {
		t.Error("cookie Secure set without opt-in")
	}

	entries := loginAuditEntries(t, db)

	if len(entries) != 1 || entries[0].Action != model.AuditLogin || entries[0].Result != model.AuditSuccess {
		t.Fatalf("audit = %+v, want one successful login", entries)
	}
}

func TestHandleLoginRejectsWrongPassword(t *testing.T) {
	t.Parallel()

	server, db := fullTestServer(t)

	req := httptest.NewRequestWithContext(
		t.Context(),
		nethttp.MethodPost,
		"/api/auth/login",
		strings.NewReader(`{"password":"wrong"}`),
	)
	rec := httptest.NewRecorder()

	server.handler.ServeHTTP(rec, req)

	res := rec.Result()

	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("reading body: %v", err)
	}

	if err := res.Body.Close(); err != nil {
		t.Fatalf("closing body: %v", err)
	}

	if res.StatusCode != nethttp.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", res.StatusCode)
	}

	var got errorResponse

	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decoding body: %v", err)
	}

	if got.Code != "unauthorized" {
		t.Errorf("code = %q, want unauthorized", got.Code)
	}

	entries := loginAuditEntries(t, db)

	if len(entries) != 1 || entries[0].Action != model.AuditLogin || entries[0].Result != model.AuditFailure {
		t.Fatalf("audit = %+v, want one failed login", entries)
	}

	if entries[0].Actor != "admin" || entries[0].ServiceID != nil || entries[0].CreatedAt.IsZero() {
		t.Errorf("entry = %+v, want admin actor, global scope, timestamp", entries[0])
	}
}

func TestHandleMeRequiresAuth(t *testing.T) {
	t.Parallel()

	server, _ := fullTestServer(t)

	req := httptest.NewRequestWithContext(t.Context(), nethttp.MethodGet, "/api/auth/me", nil)
	rec := httptest.NewRecorder()

	server.handler.ServeHTTP(rec, req)

	if res := rec.Result(); res.StatusCode != nethttp.StatusUnauthorized {
		t.Fatalf("anonymous me status = %d, want 401", res.StatusCode)
	}

	if err := rec.Result().Body.Close(); err != nil {
		t.Fatalf("closing body: %v", err)
	}
}

func TestHandleMeReportsSession(t *testing.T) {
	t.Parallel()

	server, _ := fullTestServer(t)
	cookie := authCookie(t, server)

	req := httptest.NewRequestWithContext(t.Context(), nethttp.MethodGet, "/api/auth/me", nil)
	req.AddCookie(cookie)

	rec := httptest.NewRecorder()

	server.handler.ServeHTTP(rec, req)

	res := rec.Result()

	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("reading body: %v", err)
	}

	if err := res.Body.Close(); err != nil {
		t.Fatalf("closing body: %v", err)
	}

	if res.StatusCode != nethttp.StatusOK {
		t.Fatalf("me status = %d, want 200", res.StatusCode)
	}

	var me meResponse

	if err := json.Unmarshal(body, &me); err != nil {
		t.Fatalf("decoding body: %v", err)
	}

	if !me.Authenticated {
		t.Error("authenticated = false, want true")
	}
}

func TestHandleLogoutClearsSession(t *testing.T) {
	t.Parallel()

	server, _ := fullTestServer(t)
	cookie := authCookie(t, server)

	req := httptest.NewRequestWithContext(t.Context(), nethttp.MethodPost, "/api/auth/logout", nil)
	req.AddCookie(cookie)

	rec := httptest.NewRecorder()

	server.handler.ServeHTTP(rec, req)

	res := rec.Result()

	if err := res.Body.Close(); err != nil {
		t.Fatalf("closing body: %v", err)
	}

	if res.StatusCode != nethttp.StatusNoContent {
		t.Fatalf("logout status = %d, want 204", res.StatusCode)
	}

	cleared := false

	for _, c := range res.Cookies() {
		if c.MaxAge < 0 {
			cleared = true
		}
	}

	if !cleared {
		t.Error("logout set no clearing cookie")
	}

	req = httptest.NewRequestWithContext(t.Context(), nethttp.MethodGet, "/api/auth/me", nil)
	req.AddCookie(cookie)

	rec = httptest.NewRecorder()

	server.handler.ServeHTTP(rec, req)

	if res := rec.Result(); res.StatusCode != nethttp.StatusUnauthorized {
		t.Fatalf("me after logout status = %d, want 401", res.StatusCode)
	}

	if err := rec.Result().Body.Close(); err != nil {
		t.Fatalf("closing body: %v", err)
	}
}

func TestLoginRateLimited(t *testing.T) {
	t.Parallel()

	server, _ := fullTestServer(t)

	login := func(password string) int {
		req := httptest.NewRequestWithContext(
			t.Context(),
			nethttp.MethodPost,
			"/api/auth/login",
			strings.NewReader(`{"password":"`+password+`"}`),
		)
		rec := httptest.NewRecorder()

		server.handler.ServeHTTP(rec, req)

		res := rec.Result()

		if err := res.Body.Close(); err != nil {
			t.Fatalf("closing body: %v", err)
		}

		return res.StatusCode
	}

	for i := range 5 {
		if status := login("wrong"); status != nethttp.StatusUnauthorized {
			t.Fatalf("attempt %d status = %d, want 401", i+1, status)
		}
	}

	if status := login("wrong"); status != nethttp.StatusTooManyRequests {
		t.Fatalf("attempt 6 status = %d, want 429", status)
	}

	if status := login("test-password"); status != nethttp.StatusTooManyRequests {
		t.Errorf("correct password while limited status = %d, want 429", status)
	}
}

func TestMiddlewareMatrix(t *testing.T) {
	t.Parallel()

	server, _ := fullTestServer(t)

	tests := []struct {
		name           string
		method         string
		target         string
		authed         bool
		expectedStatus int
	}{
		{name: "health public", method: nethttp.MethodGet, target: testHealthPath, authed: false, expectedStatus: nethttp.StatusOK},
		{name: "spa public", method: nethttp.MethodGet, target: "/", authed: false, expectedStatus: nethttp.StatusOK},
		{name: "login public", method: nethttp.MethodPost, target: "/api/auth/login", authed: false, expectedStatus: nethttp.StatusBadRequest},
		{name: "services denied", method: nethttp.MethodGet, target: "/api/services", authed: false, expectedStatus: nethttp.StatusUnauthorized},
		{name: "services allowed", method: nethttp.MethodGet, target: "/api/services", authed: true, expectedStatus: nethttp.StatusOK},
		{name: "events denied", method: nethttp.MethodGet, target: "/api/events", authed: false, expectedStatus: nethttp.StatusUnauthorized},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			req := httptest.NewRequestWithContext(t.Context(), tt.method, tt.target, nil)

			if tt.authed {
				req.AddCookie(authCookie(t, server))
			}

			rec := httptest.NewRecorder()

			server.handler.ServeHTTP(rec, req)

			res := rec.Result()

			if err := res.Body.Close(); err != nil {
				t.Fatalf("closing body: %v", err)
			}

			if res.StatusCode != tt.expectedStatus {
				t.Errorf("%s %s status = %d, want %d", tt.method, tt.target, res.StatusCode, tt.expectedStatus)
			}
		})
	}
}
