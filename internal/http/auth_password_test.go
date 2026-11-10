package http

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	nethttp "net/http"

	"github.com/oralecarlangelo/touchgrass/internal/store"
)

const (
	testAdminPassword = "test-password"
	testNewPassword   = "a-fresh-password-1"
	usersRoute        = "/api/users"
	changeOwnRoute    = "/api/auth/password"
)

// requireOneTimePassword flags the harness admin as holding a
// one-time password, the way a generated first boot leaves it.
func requireOneTimePassword(t *testing.T, db *store.DB) {
	t.Helper()

	users := store.NewUserStore(db)

	admin, err := users.ByUsername(context.Background(), defaultLoginUsername)
	if err != nil {
		t.Fatalf("ByUsername(admin) error = %v, want nil", err)
	}

	admin.MustChangePassword = true

	if err := users.Update(context.Background(), admin); err != nil {
		t.Fatalf("Update(admin) error = %v, want nil", err)
	}
}

// errorCode extracts the envelope code from an error body.
func errorCode(t *testing.T, body []byte) string {
	t.Helper()

	var envelope struct {
		Code string `json:"code"`
	}

	if err := json.Unmarshal(body, &envelope); err != nil {
		t.Fatalf("decoding error envelope %q: %v", body, err)
	}

	return envelope.Code
}

func changePasswordBody(current, next string) string {
	return `{"current_password":"` + current + `","new_password":"` + next + `"}`
}

func TestOneTimePasswordLocksTheSessionUntilChanged(t *testing.T) {
	t.Parallel()

	server, db := fullTestServer(t)
	requireOneTimePassword(t, db)

	cookie := authCookie(t, server)

	status, body := requestAs(t, server, cookie, nethttp.MethodGet, "/api/auth/me", "")
	if status != nethttp.StatusOK || !strings.Contains(string(body), `"must_change_password":true`) {
		t.Fatalf("me = %d %s, want 200 reporting must_change_password", status, body)
	}

	blocked := []struct{ method, path string }{
		{nethttp.MethodGet, "/api/services"},
		{nethttp.MethodGet, usersRoute},
		{nethttp.MethodPost, usersRoute},
		{nethttp.MethodGet, "/api/events"},
		{nethttp.MethodGet, "/api/audit"},
	}

	for _, route := range blocked {
		status, body := requestAs(t, server, cookie, route.method, route.path, "")
		if status != nethttp.StatusForbidden || errorCode(t, body) != codePasswordChangeRequired {
			t.Errorf("%s %s = %d %s, want 403 %s", route.method, route.path, status, body, codePasswordChangeRequired)
		}
	}

	status, body = requestAs(t, server, cookie, nethttp.MethodPost, changeOwnRoute,
		changePasswordBody("not-the-password", testNewPassword))
	if status != nethttp.StatusForbidden || errorCode(t, body) != "invalid_current_password" {
		t.Fatalf("wrong current = %d %s, want 403 invalid_current_password", status, body)
	}

	status, body = requestAs(t, server, cookie, nethttp.MethodPost, changeOwnRoute,
		changePasswordBody(testAdminPassword, testNewPassword))
	if status != nethttp.StatusNoContent {
		t.Fatalf("change = %d %s, want 204", status, body)
	}

	status, body = requestAs(t, server, cookie, nethttp.MethodGet, "/api/auth/me", "")
	if status != nethttp.StatusOK || !strings.Contains(string(body), `"must_change_password":false`) {
		t.Fatalf("me after change = %d %s, want the restriction lifted", status, body)
	}

	if status, body := requestAs(t, server, cookie, nethttp.MethodGet, usersRoute, ""); status != nethttp.StatusOK {
		t.Errorf("users after change = %d %s, want 200 on the same session", status, body)
	}
}

func TestChangePasswordValidatesAndEndsOtherSessions(t *testing.T) {
	t.Parallel()

	server, _ := fullTestServer(t)

	kept := viewerCookie(t, server)
	other := viewerCookie(t, server)
	admin := authCookie(t, server)

	rejects := []struct {
		name string
		body string
		want int
	}{
		{name: "same password", body: changePasswordBody(testViewerPassword, testViewerPassword), want: nethttp.StatusBadRequest},
		{name: "too short", body: changePasswordBody(testViewerPassword, "short"), want: nethttp.StatusBadRequest},
		{name: "not json", body: `{`, want: nethttp.StatusBadRequest},
	}

	for _, tt := range rejects {
		status, body := requestAs(t, server, kept, nethttp.MethodPost, changeOwnRoute, tt.body)
		if status != tt.want {
			t.Errorf("%s = %d %s, want %d", tt.name, status, body, tt.want)
		}
	}

	status, body := requestAs(t, server, kept, nethttp.MethodPost, changeOwnRoute,
		changePasswordBody(testViewerPassword, testNewPassword))
	if status != nethttp.StatusNoContent {
		t.Fatalf("viewer change = %d %s, want 204 (any role may change its own password)", status, body)
	}

	if status, _ := requestAs(t, server, kept, nethttp.MethodGet, "/api/auth/me", ""); status != nethttp.StatusOK {
		t.Errorf("changing session = %d, want it kept", status)
	}

	if status, _ := requestAs(t, server, other, nethttp.MethodGet, "/api/auth/me", ""); status != nethttp.StatusUnauthorized {
		t.Errorf("other session of the same user = %d, want 401 after the change", status)
	}

	if status, _ := requestAs(t, server, admin, nethttp.MethodGet, "/api/auth/me", ""); status != nethttp.StatusOK {
		t.Errorf("another user's session = %d, want it untouched", status)
	}

	if _, ok := server.auth.Login(t.Context(), testViewerUsername, testViewerPassword); ok {
		t.Error("login with the old password succeeded, want it refused")
	}

	if _, ok := server.auth.Login(t.Context(), testViewerUsername, testNewPassword); !ok {
		t.Error("login with the new password failed, want it accepted")
	}
}

func TestChangePasswordRequiresASession(t *testing.T) {
	t.Parallel()

	server, _ := fullTestServer(t)

	stranger := &nethttp.Cookie{
		Name: sessionCookie, Value: "not-a-session", HttpOnly: true, Secure: true, SameSite: nethttp.SameSiteLaxMode,
	}

	status, _ := requestAs(t, server, stranger, nethttp.MethodPost, changeOwnRoute,
		changePasswordBody(testAdminPassword, testNewPassword))
	if status != nethttp.StatusUnauthorized {
		t.Errorf("change without a session = %d, want 401", status)
	}
}
