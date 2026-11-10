package http

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	nethttp "net/http"

	"github.com/oralecarlangelo/touchgrass/internal/model"
	"github.com/oralecarlangelo/touchgrass/internal/store"
	"github.com/oralecarlangelo/touchgrass/internal/testfixture"
)

// Fixture accounts and bodies for the access tests.
const (
	testDevName        = "dev"
	testNewbieName     = "newbie"
	testViewerRoleBody = `{"role":"viewer"}`
)

// createTestUser stores one user and returns its id path.
func createTestUser(t *testing.T, server *Server, username, role string) string {
	t.Helper()

	status, body := doRequest(t, server, nethttp.MethodPost, "/api/users",
		`{"username":"`+username+`","password":"correct-horse-1","role":"`+role+`"}`)

	if status != nethttp.StatusCreated {
		t.Fatalf("create status = %d, want 201 (body: %s)", status, body)
	}

	var created userResponse

	if err := json.Unmarshal(body, &created); err != nil {
		t.Fatalf("decoding created: %v", err)
	}

	return "/api/users/" + strconv.FormatInt(created.ID, 10)
}

func TestHandleUsersListAndCreate(t *testing.T) {
	t.Parallel()

	server, _ := fullTestServer(t)

	status, body := doRequest(t, server, nethttp.MethodGet, "/api/users", "")

	if status != nethttp.StatusOK {
		t.Fatalf("list status = %d, want 200", status)
	}

	var listed usersResponse

	if err := json.Unmarshal(body, &listed); err != nil {
		t.Fatalf("decoding list: %v", err)
	}

	if len(listed.Users) != 2 {
		t.Fatalf("users = %d, want the 2 seeded rows", len(listed.Users))
	}

	if strings.Contains(string(body), "password_hash") {
		t.Fatalf("list body leaks a hash: %s", body)
	}

	status, body = doRequest(t, server, nethttp.MethodPost, "/api/users",
		`{"username":"bob","password":"correct-horse-1","role":"viewer"}`)

	if status != nethttp.StatusCreated {
		t.Fatalf("create status = %d, want 201 (body: %s)", status, body)
	}

	if strings.Contains(string(body), "password_hash") {
		t.Fatalf("create body leaks a hash: %s", body)
	}

	var created userResponse

	if err := json.Unmarshal(body, &created); err != nil {
		t.Fatalf("decoding created: %v", err)
	}

	if created.Username != "bob" || created.Role != "viewer" || created.Disabled {
		t.Fatalf("created = %+v, want bob the enabled viewer", created)
	}
}

func TestHandleUserRoleAndDisable(t *testing.T) {
	t.Parallel()

	server, _ := fullTestServer(t)
	id := createTestUser(t, server, "bob", "viewer")

	status, body := doRequest(t, server, nethttp.MethodPost, id+"/role", `{"role":"admin"}`)

	if status != nethttp.StatusOK {
		t.Fatalf("role status = %d, want 200 (body: %s)", status, body)
	}

	var promoted userResponse

	if err := json.Unmarshal(body, &promoted); err != nil {
		t.Fatalf("decoding promoted: %v", err)
	}

	if promoted.Role != defaultLoginUsername {
		t.Errorf("promoted = %+v, want the admin role", promoted)
	}

	status, body = doRequest(t, server, nethttp.MethodPost, id+"/disable", "")

	if status != nethttp.StatusOK {
		t.Fatalf("disable status = %d, want 200 (body: %s)", status, body)
	}

	var disabled userResponse

	if err := json.Unmarshal(body, &disabled); err != nil {
		t.Fatalf("decoding disabled: %v", err)
	}

	if !disabled.Disabled {
		t.Errorf("disabled = %+v, want disabled true", disabled)
	}

	status, _ = doRequest(t, server, nethttp.MethodPost, id+"/enable", "")

	if status != nethttp.StatusOK {
		t.Fatalf("enable status = %d, want 200", status)
	}
}

func TestHandleUserPasswordAndDelete(t *testing.T) {
	t.Parallel()

	server, db := fullTestServer(t)
	id := createTestUser(t, server, "bob", "viewer")

	status, _ := doRequest(t, server, nethttp.MethodPost, id+"/password", `{"password":"brand-new-horse-9"}`)

	if status != nethttp.StatusNoContent {
		t.Fatalf("password status = %d, want 204", status)
	}

	status, _ = doRequest(t, server, nethttp.MethodDelete, id, "")

	if status != nethttp.StatusNoContent {
		t.Fatalf("delete status = %d, want 204", status)
	}

	entries, err := store.NewAuditStore(db).List(context.Background(), "", 20)
	if err != nil {
		t.Fatalf("List() error = %v, want nil", err)
	}

	actions := map[string]bool{}

	for _, entry := range entries {
		actions[entry.Action] = true

		if entry.Actor != defaultLoginUsername {
			t.Errorf("audit actor = %q, want the session user", entry.Actor)
		}
	}

	for _, action := range []string{model.AuditUserCreate, model.AuditUserUpdate, model.AuditUserDelete} {
		if !actions[action] {
			t.Errorf("audit misses action %q", action)
		}
	}
}

func TestHandleCreateUserRejects(t *testing.T) {
	t.Parallel()

	server, _ := fullTestServer(t)

	tests := []struct {
		name   string
		body   string
		status int
		code   string
	}{
		{name: "duplicate", body: `{"username":"admin","password":"correct-horse-1","role":"viewer"}`, status: nethttp.StatusConflict, code: "conflict"},
		{name: "bad role", body: `{"username":"zed","password":"correct-horse-1","role":"owner"}`, status: nethttp.StatusBadRequest, code: testInvalidRequest},
		{name: "short password", body: `{"username":"zed","password":"short","role":"viewer"}`, status: nethttp.StatusBadRequest, code: testInvalidRequest},
		{name: "bad username", body: `{"username":"Zed!","password":"correct-horse-1","role":"viewer"}`, status: nethttp.StatusBadRequest, code: testInvalidRequest},
		{name: "malformed", body: testMalformedJSON, status: nethttp.StatusBadRequest, code: testInvalidRequest},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			status, body := doRequest(t, server, nethttp.MethodPost, "/api/users", tt.body)

			if status != tt.status {
				t.Fatalf("create status = %d, want %d (body: %s)", status, tt.status, body)
			}

			var got errorResponse

			if err := json.Unmarshal(body, &got); err != nil {
				t.Fatalf("decoding body: %v", err)
			}

			if got.Code != tt.code {
				t.Errorf("code = %q, want %q", got.Code, tt.code)
			}
		})
	}
}

func TestHandleUsersUnknownID(t *testing.T) {
	t.Parallel()

	server, _ := fullTestServer(t)

	tests := []struct {
		name   string
		method string
		target string
		body   string
		status int
	}{
		{name: "bad id shape", method: nethttp.MethodPost, target: "/api/users/abc/role", body: `{"role":"viewer"}`, status: nethttp.StatusBadRequest},
		{name: "unknown role", method: nethttp.MethodPost, target: "/api/users/9999/role", body: `{"role":"viewer"}`, status: nethttp.StatusNotFound},
		{name: "unknown password", method: nethttp.MethodPost, target: "/api/users/9999/password", body: `{"password":"correct-horse-1"}`, status: nethttp.StatusNotFound},
		{name: "unknown disable", method: nethttp.MethodPost, target: "/api/users/9999/disable", body: "", status: nethttp.StatusNotFound},
		{name: "unknown delete", method: nethttp.MethodDelete, target: "/api/users/9999", body: "", status: nethttp.StatusNotFound},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			status, body := doRequest(t, server, tt.method, tt.target, tt.body)

			if status != tt.status {
				t.Errorf("%s %s status = %d, want %d (body: %s)", tt.method, tt.target, status, tt.status, body)
			}
		})
	}
}

// userByName finds one user in a list response.
func userByName(t *testing.T, body []byte, username string) userResponse {
	t.Helper()

	var listed usersResponse

	if err := json.Unmarshal(body, &listed); err != nil {
		t.Fatalf("decoding list: %v", err)
	}

	for _, user := range listed.Users {
		if user.Username == username {
			return user
		}
	}

	t.Fatalf("user %s is not in %s", username, body)

	return userResponse{}
}

// createScopedUser creates a viewer scoped to the granted generic
// service through the API and returns the response.
func createScopedUser(t *testing.T, server *Server, username string) userResponse {
	t.Helper()

	status, body := doRequest(t, server, nethttp.MethodPost, usersRoute,
		`{"username":"`+username+`","password":"correct-horse-1","role":"viewer","all_services":false,`+
			`"services":["`+testfixture.GenericGranted+`"]}`)
	if status != nethttp.StatusCreated {
		t.Fatalf("create scoped = %d %s, want 201", status, body)
	}

	var created userResponse

	if err := json.Unmarshal(body, &created); err != nil {
		t.Fatalf("decoding created: %v", err)
	}

	return created
}

func TestHandleCreateUserWithScope(t *testing.T) {
	t.Parallel()

	fixture := newScopedFixture(t)
	created := createScopedUser(t, fixture.server, testDevName)

	if created.AllServices || len(created.Services) != 1 || created.Services[0] != testfixture.GenericGranted {
		t.Errorf("created = %+v, want a viewer scoped to %s", created, testfixture.GenericGranted)
	}

	if !created.MustChangePassword {
		t.Errorf("created = %+v, want a temporary password", created)
	}

	_, body := doRequest(t, fixture.server, nethttp.MethodGet, usersRoute, "")

	if seeded := userByName(t, body, testViewerUsername); !seeded.AllServices || seeded.Services == nil {
		t.Errorf("all-services user = %+v, want all_services and an empty list", seeded)
	}

	// A body from before scopes existed still creates an all-services user.
	legacy := createTestUser(t, fixture.server, "legacy", "viewer")

	_, body = doRequest(t, fixture.server, nethttp.MethodGet, usersRoute, "")
	if listed := userByName(t, body, "legacy"); !listed.AllServices || legacy == "" {
		t.Errorf("legacy create = %+v, want all services", listed)
	}
}

func TestHandleUserAccessRoundTrip(t *testing.T) {
	t.Parallel()

	fixture := newScopedFixture(t)
	server := fixture.server
	granted, other := testfixture.GenericGranted, testfixture.GenericOther

	created := createScopedUser(t, server, testDevName)
	path := usersRoute + "/" + strconv.FormatInt(created.ID, 10) + "/access"

	status, body := doRequest(t, server, nethttp.MethodPut, path,
		`{"role":"viewer","all_services":false,"services":["`+other+`","`+granted+`"]}`)
	if status != nethttp.StatusOK {
		t.Fatalf("set access = %d %s, want 200", status, body)
	}

	_, body = doRequest(t, server, nethttp.MethodGet, usersRoute, "")

	listed := userByName(t, body, testDevName)
	if listed.AllServices || len(listed.Services) != 2 || listed.Services[0] != granted || listed.Services[1] != other {
		t.Errorf("listed = %+v, want both services, sorted", listed)
	}

	status, body = doRequest(t, server, nethttp.MethodPut, path, `{"role":"admin","all_services":true}`)
	if status != nethttp.StatusOK {
		t.Fatalf("promote = %d %s, want 200", status, body)
	}

	_, body = doRequest(t, server, nethttp.MethodGet, "/api/audit?limit=50", "")

	for _, want := range []string{
		"set access of user dev: role admin, all services",
		"set access of user dev: role viewer, services [" + granted + ", " + other + "]",
		"created user dev (role viewer, services [" + granted + "])",
	} {
		if !strings.Contains(string(body), want) {
			t.Errorf("audit = %s, want %q", body, want)
		}
	}
}

func TestHandleUserAccessRejects(t *testing.T) {
	t.Parallel()

	fixture := newScopedFixture(t)
	server := fixture.server
	granted := testfixture.GenericGranted

	viewer, err := server.users.Lookup(t.Context(), testViewerUsername)
	if err != nil {
		t.Fatalf("Lookup(viewer) error = %v, want nil", err)
	}

	admin, err := server.users.Lookup(t.Context(), defaultLoginUsername)
	if err != nil {
		t.Fatalf("Lookup(admin) error = %v, want nil", err)
	}

	viewerPath := usersRoute + "/" + strconv.FormatInt(viewer.ID, 10) + "/access"
	adminPath := usersRoute + "/" + strconv.FormatInt(admin.ID, 10) + "/access"

	tests := []struct {
		name   string
		method string
		target string
		body   string
		status int
		code   string
	}{
		{
			name: "reserved developer role", method: nethttp.MethodPut, target: viewerPath,
			body: `{"role":"developer","all_services":true}`, status: nethttp.StatusBadRequest, code: testInvalidRequest,
		},
		{
			name: "unknown role", method: nethttp.MethodPut, target: viewerPath,
			body: `{"role":"owner","all_services":true}`, status: nethttp.StatusBadRequest, code: testInvalidRequest,
		},
		{
			name: "service that does not exist", method: nethttp.MethodPut, target: viewerPath,
			body:   `{"role":"viewer","all_services":false,"services":["no-such-service"]}`,
			status: nethttp.StatusBadRequest, code: testInvalidRequest,
		},
		{
			name: "scope left unsaid", method: nethttp.MethodPut, target: viewerPath,
			body: `{"role":"viewer","services":["` + granted + `"]}`, status: nethttp.StatusBadRequest, code: testInvalidRequest,
		},
		{
			name: "all services with a list", method: nethttp.MethodPut, target: viewerPath,
			body:   `{"role":"viewer","all_services":true,"services":["` + granted + `"]}`,
			status: nethttp.StatusBadRequest, code: testInvalidRequest,
		},
		{
			name: "malformed body", method: nethttp.MethodPut, target: viewerPath,
			body: testMalformedJSON, status: nethttp.StatusBadRequest, code: testInvalidRequest,
		},
		{
			name: "scoped admin", method: nethttp.MethodPut, target: viewerPath,
			body:   `{"role":"admin","all_services":false,"services":["` + granted + `"]}`,
			status: nethttp.StatusConflict, code: "conflict",
		},
		{
			name: "last admin to a scoped viewer", method: nethttp.MethodPut, target: adminPath,
			body:   `{"role":"viewer","all_services":false,"services":["` + granted + `"]}`,
			status: nethttp.StatusConflict, code: "conflict",
		},
		{
			name: "last admin through the role route", method: nethttp.MethodPost,
			target: usersRoute + "/" + strconv.FormatInt(admin.ID, 10) + "/role",
			body:   testViewerRoleBody, status: nethttp.StatusConflict, code: "conflict",
		},
		{
			name: "unknown user", method: nethttp.MethodPut, target: usersRoute + "/99999/access",
			body: `{"role":"viewer","all_services":true}`, status: nethttp.StatusNotFound, code: codeNotFound,
		},
		{
			name: "create with the developer role", method: nethttp.MethodPost, target: usersRoute,
			body:   `{"username":"dev-a","password":"correct-horse-1","role":"developer"}`,
			status: nethttp.StatusBadRequest, code: testInvalidRequest,
		},
		{
			name: "create with a list but no scope flag", method: nethttp.MethodPost, target: usersRoute,
			body:   `{"username":"dev-b","password":"correct-horse-1","role":"viewer","services":["` + granted + `"]}`,
			status: nethttp.StatusBadRequest, code: testInvalidRequest,
		},
		{
			name: "create a scoped admin", method: nethttp.MethodPost, target: usersRoute,
			body:   `{"username":"dev-c","password":"correct-horse-1","role":"admin","all_services":false}`,
			status: nethttp.StatusConflict, code: "conflict",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			status, body := doRequest(t, server, tt.method, tt.target, tt.body)
			if status != tt.status {
				t.Fatalf("%s %s = %d %s, want %d", tt.method, tt.target, status, body, tt.status)
			}

			if got := errorCode(t, body); got != tt.code {
				t.Errorf("code = %q, want %q", got, tt.code)
			}
		})
	}
}

// A non-admin cannot change anyone's access, their own included, by
// the access route or by smuggling fields into the password route.
func TestNonAdminCannotChangeAccess(t *testing.T) {
	t.Parallel()

	fixture := newScopedFixture(t)
	server := fixture.server
	cookie := fixture.cookieFor(t, testScopedUsername)

	self, err := server.users.Lookup(t.Context(), testScopedUsername)
	if err != nil {
		t.Fatalf("Lookup() error = %v, want nil", err)
	}

	id := strconv.FormatInt(self.ID, 10)

	for _, attempt := range []struct{ method, target, body string }{
		{nethttp.MethodPut, usersRoute + "/" + id + "/access", `{"role":"admin","all_services":true}`},
		{nethttp.MethodPost, usersRoute + "/" + id + "/role", `{"role":"admin"}`},
		{nethttp.MethodPost, usersRoute + "/" + id + "/password", `{"password":"another-horse-12"}`},
		{nethttp.MethodPost, usersRoute, `{"username":"twin","password":"correct-horse-1","role":"admin"}`},
		{nethttp.MethodGet, usersRoute, ""},
	} {
		status, body := requestAs(t, server, cookie, attempt.method, attempt.target, attempt.body)
		if status != nethttp.StatusForbidden || errorCode(t, body) != "forbidden" {
			t.Errorf("%s %s = %d %s, want 403 forbidden", attempt.method, attempt.target, status, body)
		}
	}

	status, body := requestAs(t, server, cookie, nethttp.MethodPost, "/api/auth/password",
		`{"current_password":"`+testViewerPassword+`","new_password":"brand-new-horse-9",`+
			`"role":"admin","all_services":true,"services":["`+testfixture.GenericOther+`"]}`)
	if status != nethttp.StatusNoContent {
		t.Fatalf("change own password = %d %s, want 204", status, body)
	}

	after, err := server.users.Lookup(t.Context(), testScopedUsername)
	if err != nil {
		t.Fatalf("Lookup() after error = %v, want nil", err)
	}

	if after.Role != model.RoleViewer || after.AllServices ||
		len(after.Services) != 1 || after.Services[0] != testfixture.GenericGranted {
		t.Errorf("access after the password change = %+v, want it untouched", after)
	}
}

// newbieOnATemporaryPassword creates a viewer scoped to the granted
// service and returns its user path and first session.
func newbieOnATemporaryPassword(t *testing.T, server *Server) (string, *nethttp.Cookie) {
	t.Helper()

	created := createScopedUser(t, server, testNewbieName)

	return usersRoute + "/" + strconv.FormatInt(created.ID, 10),
		loginCookie(t, server, `{"username":"`+testNewbieName+`","password":"correct-horse-1"}`)
}

// expectPasswordGate fails unless cookie is locked to changing its
// password.
func expectPasswordGate(t *testing.T, server *Server, cookie *nethttp.Cookie, label string) {
	t.Helper()

	status, body := requestAs(t, server, cookie, nethttp.MethodGet, testServicesPath, "")
	if status != nethttp.StatusForbidden || errorCode(t, body) != codePasswordChangeRequired {
		t.Fatalf("%s: services = %d %s, want 403 %s", label, status, body, codePasswordChangeRequired)
	}
}

// A new account starts on a temporary password and sees its scope only
// after replacing it.
func TestNewAccountMustChangeItsPassword(t *testing.T) {
	t.Parallel()

	fixture := newScopedFixture(t)
	server := fixture.server
	_, session := newbieOnATemporaryPassword(t, server)

	expectPasswordGate(t, server, session, "new account")

	status, body := requestAs(t, server, session, nethttp.MethodPost, "/api/auth/password",
		changePasswordBody("correct-horse-1", "chosen-by-newbie-1"))
	if status != nethttp.StatusNoContent {
		t.Fatalf("first password change = %d %s, want 204", status, body)
	}

	status, body = requestAs(t, server, session, nethttp.MethodGet, testServicesPath, "")
	if status != nethttp.StatusOK || !strings.Contains(string(body), testfixture.GenericGranted) {
		t.Fatalf("services after the change = %d %s, want the granted service", status, body)
	}

	if strings.Contains(string(body), testfixture.GenericOther) {
		t.Errorf("services after the change = %s, want nothing out of scope", body)
	}
}

// An admin reset leaves a temporary password and ends every session
// the user had (D-13).
func TestAdminResetEndsSessionsAndForcesAChange(t *testing.T) {
	t.Parallel()

	fixture := newScopedFixture(t)
	server := fixture.server
	path, first := newbieOnATemporaryPassword(t, server)

	if status, body := requestAs(t, server, first, nethttp.MethodPost, "/api/auth/password",
		changePasswordBody("correct-horse-1", "chosen-by-newbie-1")); status != nethttp.StatusNoContent {
		t.Fatalf("first password change = %d %s, want 204", status, body)
	}

	second := loginCookie(t, server, `{"username":"`+testNewbieName+`","password":"chosen-by-newbie-1"}`)

	if status, body := doRequest(t, server, nethttp.MethodPost, path+"/password",
		`{"password":"reset-by-admin-12"}`); status != nethttp.StatusNoContent {
		t.Fatalf("admin reset = %d %s, want 204", status, body)
	}

	for label, cookie := range map[string]*nethttp.Cookie{"first": first, "second": second} {
		if status, _ := requestAs(t, server, cookie, nethttp.MethodGet, "/api/auth/me", ""); status != nethttp.StatusUnauthorized {
			t.Errorf("%s session after the admin reset = %d, want 401", label, status)
		}
	}

	third := loginCookie(t, server, `{"username":"`+testNewbieName+`","password":"reset-by-admin-12"}`)
	expectPasswordGate(t, server, third, "after the admin reset")

	_, body := doRequest(t, server, nethttp.MethodGet, usersRoute, "")
	if listed := userByName(t, body, testNewbieName); !listed.MustChangePassword {
		t.Errorf("listed = %+v, want must_change_password set after the reset", listed)
	}
}
