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
