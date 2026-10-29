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

func TestLoginVerifiesManagedUsers(t *testing.T) {
	t.Parallel()

	server, _ := fullTestServer(t)

	login := func(body string) int {
		req := httptest.NewRequestWithContext(
			t.Context(),
			nethttp.MethodPost,
			"/api/auth/login",
			strings.NewReader(body),
		)
		rec := httptest.NewRecorder()

		server.handler.ServeHTTP(rec, req)

		res := rec.Result()

		if err := res.Body.Close(); err != nil {
			t.Fatalf("closing body: %v", err)
		}

		return res.StatusCode
	}

	tests := []struct {
		name   string
		body   string
		status int
	}{
		{name: "named admin", body: `{"username":"admin","password":"test-password"}`, status: nethttp.StatusNoContent},
		{name: "password only", body: `{"password":"test-password"}`, status: nethttp.StatusNoContent},
		{name: "named viewer", body: `{"username":"viewer","password":"viewer-password-1"}`, status: nethttp.StatusNoContent},
		{name: "wrong password", body: `{"username":"admin","password":"wrong"}`, status: nethttp.StatusUnauthorized},
		{name: "unknown user", body: `{"username":"nobody","password":"test-password"}`, status: nethttp.StatusUnauthorized},
		{name: "empty password", body: `{"username":"admin","password":""}`, status: nethttp.StatusUnauthorized},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if status := login(tt.body); status != tt.status {
				t.Errorf("login status = %d, want %d", status, tt.status)
			}
		})
	}
}

func TestLoginRejectsDisabledUser(t *testing.T) {
	t.Parallel()

	server, _ := fullTestServer(t)

	viewer, err := server.users.Lookup(t.Context(), testViewerUsername)
	if err != nil {
		t.Fatalf("Lookup() error = %v, want nil", err)
	}

	if _, err := server.users.SetDisabled(t.Context(), viewer.ID, true, defaultLoginUsername); err != nil {
		t.Fatalf("SetDisabled() error = %v, want nil", err)
	}

	req := httptest.NewRequestWithContext(
		t.Context(),
		nethttp.MethodPost,
		"/api/auth/login",
		strings.NewReader(`{"username":"viewer","password":"viewer-password-1"}`),
	)
	rec := httptest.NewRecorder()

	server.handler.ServeHTTP(rec, req)

	res := rec.Result()

	if err := res.Body.Close(); err != nil {
		t.Fatalf("closing body: %v", err)
	}

	if res.StatusCode != nethttp.StatusUnauthorized {
		t.Errorf("disabled login status = %d, want 401", res.StatusCode)
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

	if entries[0].Actor != defaultLoginUsername || entries[0].ServiceID != nil || entries[0].CreatedAt.IsZero() {
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

	if me.Username != defaultLoginUsername || me.Role != defaultLoginUsername {
		t.Errorf("me = %+v, want admin identity", me)
	}
}

func TestHandleMeReportsViewer(t *testing.T) {
	t.Parallel()

	server, _ := fullTestServer(t)
	cookie := viewerCookie(t, server)

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

	if !me.Authenticated || me.Username != testViewerUsername || me.Role != "viewer" {
		t.Errorf("me = %+v, want the viewer identity", me)
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

func TestViewerForbiddenMatrix(t *testing.T) {
	t.Parallel()

	server, _ := fullTestServer(t)
	cookie := viewerCookie(t, server)

	// Every mutating route must reject viewers before touching its
	// handler, so unknown ids still answer 403 instead of 404.
	tests := []struct {
		name   string
		method string
		target string
	}{
		{name: "create service", method: nethttp.MethodPost, target: testServicesPath},
		{name: "delete service", method: nethttp.MethodDelete, target: "/api/services/tn-api"},
		{name: "record deploy", method: nethttp.MethodPost, target: testDeploysPath},
		{name: "cutover", method: nethttp.MethodPost, target: testCutoverPath},
		{name: "rollback", method: nethttp.MethodPost, target: "/api/services/tn-api/rollback"},
		{name: "deploy", method: nethttp.MethodPost, target: "/api/services/tn-api/deploy"},
		{name: "create alert rule", method: nethttp.MethodPost, target: "/api/alerts/rules"},
		{name: "delete alert rule", method: nethttp.MethodDelete, target: "/api/alerts/rules/1"},
		{name: "mark notification read", method: nethttp.MethodPost, target: "/api/notifications/1/read"},
		{name: "mark notifications read", method: nethttp.MethodPost, target: "/api/notifications/read"},
		{name: "create key", method: nethttp.MethodPost, target: "/api/keys"},
		{name: "revoke key", method: nethttp.MethodPost, target: "/api/keys/1/revoke"},
		{name: "create issue rule", method: nethttp.MethodPost, target: "/api/issues/rules"},
		{name: "delete issue rule", method: nethttp.MethodDelete, target: "/api/issues/rules/1"},
		{name: "prune images", method: nethttp.MethodPost, target: "/api/docker/images/prune"},
		{name: "start backup", method: nethttp.MethodPost, target: "/api/databases/backups"},
		{name: "verify backup", method: nethttp.MethodPost, target: "/api/databases/backups/x.dump/verify"},
		{name: "restore", method: nethttp.MethodPost, target: "/api/databases/restore"},
		{name: "list users", method: nethttp.MethodGet, target: "/api/users"},
		{name: "create user", method: nethttp.MethodPost, target: "/api/users"},
		{name: "reset password", method: nethttp.MethodPost, target: "/api/users/1/password"},
		{name: "change role", method: nethttp.MethodPost, target: "/api/users/1/role"},
		{name: "disable user", method: nethttp.MethodPost, target: "/api/users/1/disable"},
		{name: "enable user", method: nethttp.MethodPost, target: "/api/users/1/enable"},
		{name: "delete user", method: nethttp.MethodDelete, target: "/api/users/1"},
		{name: "acknowledge security finding", method: nethttp.MethodPost, target: "/api/security/findings/1/acknowledge"},
		{name: "resolve security finding", method: nethttp.MethodPost, target: "/api/security/findings/1/resolve"},
		{name: "suppress security finding", method: nethttp.MethodPost, target: "/api/security/findings/1/suppress"},
		{name: "create security policy", method: nethttp.MethodPost, target: "/api/security/policies"},
		{name: "update security policy", method: nethttp.MethodPatch, target: "/api/security/policies/1"},
		{name: "delete security policy", method: nethttp.MethodDelete, target: "/api/security/policies/1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			req := httptest.NewRequestWithContext(t.Context(), tt.method, tt.target, nil)
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

			if res.StatusCode != nethttp.StatusForbidden {
				t.Fatalf("%s %s status = %d, want 403", tt.method, tt.target, res.StatusCode)
			}

			var got errorResponse

			if err := json.Unmarshal(body, &got); err != nil {
				t.Fatalf("decoding body: %v", err)
			}

			if got.Code != "forbidden" {
				t.Errorf("%s %s code = %q, want forbidden", tt.method, tt.target, got.Code)
			}
		})
	}
}

func TestViewerReadsStayOpen(t *testing.T) {
	t.Parallel()

	server, _ := fullTestServer(t)
	cookie := viewerCookie(t, server)

	tests := []struct {
		name   string
		method string
		target string
		status int
	}{
		{name: "services", method: nethttp.MethodGet, target: testServicesPath, status: nethttp.StatusOK},
		{name: "logs", method: nethttp.MethodGet, target: "/api/logs?service_id=tn-api", status: nethttp.StatusOK},
		{name: "log stats", method: nethttp.MethodGet, target: "/api/logs/stats?service_id=tn-api", status: nethttp.StatusOK},
		{name: "issues", method: nethttp.MethodGet, target: "/api/issues?service_id=tn-api", status: nethttp.StatusOK},
		{name: "occurrences", method: nethttp.MethodGet, target: "/api/issues/occurrences?service_id=tn-api", status: nethttp.StatusOK},
		{name: "deploys", method: nethttp.MethodGet, target: testDeploysPath, status: nethttp.StatusOK},
		{name: "metrics", method: nethttp.MethodGet, target: "/api/services/tn-api/metrics", status: nethttp.StatusOK},
		{name: "notifications", method: nethttp.MethodGet, target: "/api/notifications", status: nethttp.StatusOK},
		{name: "audit", method: nethttp.MethodGet, target: "/api/audit", status: nethttp.StatusOK},
		{name: "keys", method: nethttp.MethodGet, target: "/api/keys?service_id=tn-api", status: nethttp.StatusOK},
		{name: "images", method: nethttp.MethodGet, target: "/api/docker/images", status: nethttp.StatusOK},
		{name: "system", method: nethttp.MethodGet, target: "/api/system", status: nethttp.StatusOK},
		{name: "fleet", method: nethttp.MethodGet, target: "/api/system/containers", status: nethttp.StatusOK},
		{name: "security summary", method: nethttp.MethodGet, target: "/api/security/summary", status: nethttp.StatusOK},
		{name: "security findings", method: nethttp.MethodGet, target: "/api/security/findings", status: nethttp.StatusOK},
		{name: "security policies", method: nethttp.MethodGet, target: "/api/security/policies", status: nethttp.StatusOK},
		{name: "me", method: nethttp.MethodGet, target: "/api/auth/me", status: nethttp.StatusOK},
		{name: "logout", method: nethttp.MethodPost, target: "/api/auth/logout", status: nethttp.StatusNoContent},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			session := cookie

			if tt.name == "logout" {
				// Logout kills its session, so it mints its own
				// instead of sharing the table cookie.
				session = viewerCookie(t, server)
			}

			req := httptest.NewRequestWithContext(t.Context(), tt.method, tt.target, nil)
			req.AddCookie(session)

			rec := httptest.NewRecorder()

			server.handler.ServeHTTP(rec, req)

			res := rec.Result()

			if err := res.Body.Close(); err != nil {
				t.Fatalf("closing body: %v", err)
			}

			if res.StatusCode != tt.status {
				t.Errorf("%s %s status = %d, want %d", tt.method, tt.target, res.StatusCode, tt.status)
			}
		})
	}
}

func TestDisabledSessionDiesImmediately(t *testing.T) {
	t.Parallel()

	server, _ := fullTestServer(t)
	cookie := viewerCookie(t, server)

	viewer, err := server.users.Lookup(t.Context(), testViewerUsername)
	if err != nil {
		t.Fatalf("Lookup() error = %v, want nil", err)
	}

	if _, err := server.users.SetDisabled(t.Context(), viewer.ID, true, defaultLoginUsername); err != nil {
		t.Fatalf("SetDisabled() error = %v, want nil", err)
	}

	req := httptest.NewRequestWithContext(t.Context(), nethttp.MethodGet, "/api/auth/me", nil)
	req.AddCookie(cookie)

	rec := httptest.NewRecorder()

	server.handler.ServeHTTP(rec, req)

	res := rec.Result()

	if err := res.Body.Close(); err != nil {
		t.Fatalf("closing body: %v", err)
	}

	if res.StatusCode != nethttp.StatusUnauthorized {
		t.Errorf("me after disable status = %d, want 401", res.StatusCode)
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
		{name: "services denied", method: nethttp.MethodGet, target: testServicesPath, authed: false, expectedStatus: nethttp.StatusUnauthorized},
		{name: "services allowed", method: nethttp.MethodGet, target: testServicesPath, authed: true, expectedStatus: nethttp.StatusOK},
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
