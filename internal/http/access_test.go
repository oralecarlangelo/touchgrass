package http

import (
	"context"
	"encoding/json"
	"io"
	"slices"
	"strconv"
	"strings"
	"testing"

	nethttp "net/http"

	"github.com/oralecarlangelo/touchgrass/internal/authz"
	"github.com/oralecarlangelo/touchgrass/internal/model"
	"github.com/oralecarlangelo/touchgrass/internal/store"
	"github.com/oralecarlangelo/touchgrass/internal/testfixture"
)

// withIdentity attaches a caller to a request the way the auth
// middleware does, for tests that call a guard or handler directly.
func withIdentity(r *nethttp.Request, identity Identity) *nethttp.Request {
	return r.WithContext(context.WithValue(r.Context(), identityKey{}, identity))
}

// decodeBodyInto decodes a JSON response body.
func decodeBodyInto[T any](t *testing.T, body string) T {
	t.Helper()

	var decoded T

	if err := json.Unmarshal([]byte(body), &decoded); err != nil {
		t.Fatalf("decoding %q: %v", body, err)
	}

	return decoded
}

// The scoped viewer's in-scope reads work end to end, through the real
// handlers, and return the granted service's data.
func TestScopedViewerReadsItsService(t *testing.T) {
	t.Parallel()

	fixture := newScopedFixture(t)
	cookie := fixture.cookieFor(t, testScopedUsername)
	granted := testfixture.GenericGranted
	issue := strconv.FormatInt(fixture.granted[model.ResourceIssue], 10)
	line := strconv.FormatInt(fixture.granted[model.ResourceLogLine], 10)

	tests := []struct {
		name   string
		target string
		want   string
	}{
		{name: "logs", target: "/api/logs?service_id=" + granted, want: "hello from " + granted},
		{name: "log stats", target: "/api/logs/stats?service_id=" + granted, want: ""},
		{name: "log context", target: "/api/logs/" + line, want: "hello from " + granted},
		{name: "issues", target: "/api/issues?service_id=" + granted, want: "boom in " + granted},
		{name: "occurrences", target: "/api/issues/occurrences?service_id=" + granted, want: "boom in " + granted},
		{name: "issue detail", target: "/api/issues/" + issue, want: "boom in " + granted},
		{name: "issue logs", target: "/api/issues/" + issue + "/logs", want: ""},
		{name: "deploys", target: "/api/services/" + granted + "/deploys", want: ""},
		{name: "metrics", target: "/api/services/" + granted + "/metrics", want: ""},
		{name: "performance", target: "/api/services/" + granted + "/performance/summary", want: ""},
		{name: "env", target: "/api/services/" + granted + "/env", want: ""},
		{name: "alert rules", target: "/api/alerts/rules?service_id=" + granted, want: `"service_id":"` + granted},
		{name: "issue rules", target: "/api/issues/rules?service_id=" + granted, want: `"service_id":"` + granted},
		{name: "keys", target: "/api/keys?service_id=" + granted, want: "tg_bill"},
		{name: "audit of the service", target: "/api/audit?service_id=" + granted, want: "deployed " + granted},
		{name: "notifications of the service", target: "/api/notifications?service_id=" + granted, want: "alert on " + granted},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			status, _, body := call(t, fixture.server.handler, cookie, nethttp.MethodGet, tt.target, "")
			if status != nethttp.StatusOK {
				t.Fatalf("GET %s = %d (%s), want 200", tt.target, status, strings.TrimSpace(body))
			}

			if !strings.Contains(body, tt.want) {
				t.Errorf("GET %s body = %s, want it to contain %q", tt.target, body, tt.want)
			}

			if strings.Contains(body, testfixture.GenericOther) {
				t.Errorf("GET %s body mentions the service out of scope: %s", tt.target, body)
			}
		})
	}
}

// seedGlobalNotification stores a notification that belongs to no
// service, as host findings do.
func seedGlobalNotification(t *testing.T, db *store.DB) {
	t.Helper()

	if _, err := store.NewNotificationStore(db).Insert(t.Context(), "", "security", "host finding", "body"); err != nil {
		t.Fatalf("Insert(global notification) error = %v, want nil", err)
	}
}

// getAs fetches target as cookie and decodes the 200 response.
func getAs[T any](t *testing.T, fixture scopedFixture, cookie *nethttp.Cookie, target string) (T, string) {
	t.Helper()

	status, _, body := call(t, fixture.server.handler, cookie, nethttp.MethodGet, target, "")
	if status != nethttp.StatusOK {
		t.Fatalf("GET %s = %d %s, want 200", target, status, body)
	}

	return decodeBodyInto[T](t, body), body
}

// The three lists are bounded by the scope: a scoped viewer's list
// holds their services' rows and nothing else, and counts agree.
func TestScopedListsHoldOnlyTheScope(t *testing.T) {
	t.Parallel()

	fixture := newScopedFixture(t)
	seedGlobalNotification(t, fixture.db)

	granted := testfixture.GenericGranted
	scoped := fixture.cookieFor(t, testScopedUsername)

	services, body := getAs[servicesResponse](t, fixture, scoped, testServicesPath)
	if len(services.Services) != 1 || services.Services[0].ID != granted {
		t.Errorf("scoped services = %s, want only %s", body, granted)
	}

	notifications, body := getAs[notificationsResponse](t, fixture, scoped, "/api/notifications")
	if len(notifications.Notifications) != 1 || notifications.Notifications[0].ServiceID != granted {
		t.Errorf("scoped notifications = %s, want one row of %s", body, granted)
	}

	if notifications.Unread != 1 {
		t.Errorf("scoped unread = %d, want 1: the count covers the same rows as the list", notifications.Unread)
	}

	audit, body := getAs[auditResponse](t, fixture, scoped, "/api/audit")
	if len(audit.Audit) != 1 || audit.Audit[0].ServiceID == nil || *audit.Audit[0].ServiceID != granted {
		t.Errorf("scoped audit = %s, want only the row of %s", body, granted)
	}

	if strings.Contains(body, "login") || strings.Contains(body, testfixture.GenericOther) {
		t.Errorf("scoped audit leaks a global or foreign row: %s", body)
	}

	// The limit applies after the scope, not before it.
	if limited, body := getAs[auditResponse](t, fixture, scoped, "/api/audit?limit=1"); len(limited.Audit) != 1 {
		t.Errorf("scoped audit with limit 1 = %s, want the one in-scope row", body)
	}
}

func TestListsAreEmptyForAViewerWithNoServices(t *testing.T) {
	t.Parallel()

	fixture := newScopedFixture(t)
	seedGlobalNotification(t, fixture.db)

	cookie := fixture.cookieFor(t, testEmptyUsername)

	for _, target := range []string{testServicesPath, "/api/notifications", "/api/audit"} {
		status, _, body := call(t, fixture.server.handler, cookie, nethttp.MethodGet, target, "")
		if status != nethttp.StatusOK {
			t.Errorf("GET %s = %d %s, want 200", target, status, body)
		}

		for _, leaked := range []string{testfixture.GenericGranted, testfixture.GenericOther, "login", "host finding"} {
			if strings.Contains(body, leaked) {
				t.Errorf("GET %s leaks %q to a viewer with no services: %s", target, leaked, body)
			}
		}
	}
}

// An all-services viewer still sees everything, global rows included.
func TestListsAreWholeForAnAllServicesViewer(t *testing.T) {
	t.Parallel()

	fixture := newScopedFixture(t)
	seedGlobalNotification(t, fixture.db)

	viewer := viewerCookie(t, fixture.server)

	_, body := getAs[auditResponse](t, fixture, viewer, "/api/audit")
	if !strings.Contains(body, "login from") || !strings.Contains(body, "deployed "+testfixture.GenericOther) {
		t.Errorf("all-services viewer audit = %s, want logins and every service", body)
	}

	notifications, body := getAs[notificationsResponse](t, fixture, viewer, "/api/notifications")
	if len(notifications.Notifications) != 3 || notifications.Unread != 3 {
		t.Errorf("all-services viewer notifications = %s, want all 3 rows unread", body)
	}

	if services, _ := getAs[servicesResponse](t, fixture, viewer, testServicesPath); len(services.Services) != 5 {
		t.Errorf("all-services viewer services = %d, want all 5", len(services.Services))
	}
}

// Mark-all-read without a service touches only the caller's scope. No
// role can reach that branch yet (only admins may mark read, and an
// admin is never scoped), so the handler is called directly as the
// scoped operator a later sprint will allow.
func TestMarkNotificationsReadStaysInScope(t *testing.T) {
	t.Parallel()

	fixture := newScopedFixture(t)
	notifications := store.NewNotificationStore(fixture.db)

	seedGlobalNotification(t, fixture.db)

	req := withIdentity(
		newTestRequest(t, nethttp.MethodPost, "/api/notifications/read", nil),
		Identity{Username: "operator", Role: model.RoleAdmin, Services: []string{testfixture.GenericGranted}},
	)
	rec := newRecorder()

	fixture.server.handleMarkNotificationsRead(rec, req)

	if marked := decodeBodyInto[markedResponse](t, rec.Body.String()); rec.Code != nethttp.StatusOK || marked.Marked != 1 {
		t.Fatalf("mark read = %d %s, want 200 and 1 marked", rec.Code, rec.Body.String())
	}

	unread, err := notifications.CountUnread(t.Context(), "")
	if err != nil || unread != 2 {
		t.Errorf("unread after a scoped mark-all = %d, %v, want the foreign and the global row left", unread, err)
	}
}

func TestHandleMeReportsScope(t *testing.T) {
	t.Parallel()

	fixture := newScopedFixture(t)

	tests := []struct {
		name        string
		cookie      *nethttp.Cookie
		role        string
		allServices bool
		services    []string
		holds       []authz.Capability
		doesNotHold []authz.Capability
	}{
		{
			name: "an admin", cookie: authCookie(t, fixture.server), role: string(model.RoleAdmin), allServices: true, services: []string{},
			holds: authz.Capabilities(), doesNotHold: []authz.Capability{},
		},
		{
			name: "all-services viewer", cookie: viewerCookie(t, fixture.server), role: string(model.RoleViewer),
			allServices: true, services: []string{},
			holds:       []authz.Capability{authz.SessionSelf, authz.LogsRead, authz.HostRead, authz.AuditReadAll},
			doesNotHold: []authz.Capability{authz.DeployTrigger, authz.UsersManage, authz.ServicesManage},
		},
		{
			name: "scoped viewer", cookie: fixture.cookieFor(t, testScopedUsername), role: string(model.RoleViewer),
			allServices: false, services: []string{testfixture.GenericGranted},
			holds:       []authz.Capability{authz.SessionSelf, authz.LogsRead, authz.EnvReadKeys, authz.AuditRead},
			doesNotHold: []authz.Capability{authz.HostRead, authz.AuditReadAll, authz.SecurityRead, authz.DeployTrigger},
		},
		{
			name: "viewer with no services", cookie: fixture.cookieFor(t, testEmptyUsername), role: string(model.RoleViewer),
			allServices: false, services: []string{},
			holds:       []authz.Capability{authz.SessionSelf},
			doesNotHold: []authz.Capability{authz.LogsRead, authz.ServiceRead, authz.HostRead},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			status, _, body := call(t, fixture.server.handler, tt.cookie, nethttp.MethodGet, "/api/auth/me", "")
			if status != nethttp.StatusOK {
				t.Fatalf("me = %d %s, want 200", status, body)
			}

			if !strings.Contains(body, `"services":[`) || !strings.Contains(body, `"capabilities":[`) {
				t.Errorf("me body = %s, want services and capabilities as arrays", body)
			}

			got := decodeBodyInto[meResponse](t, body)
			if got.Role != tt.role || got.AllServices != tt.allServices || !slices.Equal(got.Services, tt.services) {
				t.Errorf("me = %+v, want role %s, all_services %t, services %v", got, tt.role, tt.allServices, tt.services)
			}

			for _, capability := range tt.holds {
				if !slices.Contains(got.Capabilities, capability) {
					t.Errorf("me capabilities lack %s", capability)
				}
			}

			for _, capability := range tt.doesNotHold {
				if slices.Contains(got.Capabilities, capability) {
					t.Errorf("me capabilities include %s, want it absent", capability)
				}
			}
		})
	}
}

// A guard that reads the service from the body checks it and hands the
// body on untouched. No scoped role holds a body-keyed capability yet,
// so the guard is exercised with a read capability the viewer has.
func TestGuardServiceBody(t *testing.T) {
	t.Parallel()

	server, _ := fullTestServer(t)
	scoped := Identity{Username: testScopedUsername, Role: model.RoleViewer, Services: []string{testfixture.GenericGranted}}
	inScope := `{"service_id":"` + testfixture.GenericGranted + `","metric":"cpu"}`

	tests := []struct {
		name     string
		identity Identity
		body     string
		status   int
		wantSeen string
	}{
		{name: "service in scope", identity: scoped, body: inScope, status: nethttp.StatusNoContent, wantSeen: inScope},
		{
			name: "service out of scope", identity: scoped,
			body: `{"service_id":"` + testfixture.GenericOther + `"}`, status: nethttp.StatusForbidden,
		},
		{name: "no service named", identity: scoped, body: `{"metric":"cpu"}`, status: nethttp.StatusForbidden},
		{name: "malformed body", identity: scoped, body: testMalformedJSON, status: nethttp.StatusBadRequest},
		{
			name:     "role without the capability is refused before the body is read",
			identity: Identity{Username: testReservedUsername, Role: model.RoleDeveloper, AllServices: true},
			body:     testMalformedJSON, status: nethttp.StatusForbidden,
		},
		{
			name:     "all services skips the body",
			identity: Identity{Username: testViewerUsername, Role: model.RoleViewer, AllServices: true},
			body:     testMalformedJSON, status: nethttp.StatusNoContent, wantSeen: testMalformedJSON,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var seen string

			guarded := server.guard(route{
				method: nethttp.MethodPost, pattern: "/api/test", kind: guardServiceBody, capability: authz.LogsRead,
				handler: func(w nethttp.ResponseWriter, r *nethttp.Request) {
					raw, err := io.ReadAll(r.Body)
					if err != nil {
						t.Errorf("handler reading body: %v", err)
					}

					seen = string(raw)

					w.WriteHeader(nethttp.StatusNoContent)
				},
			})

			rec := newRecorder()
			guarded(rec, withIdentity(newTestRequest(t, nethttp.MethodPost, "/api/test", strings.NewReader(tt.body)), tt.identity))

			if rec.Code != tt.status || seen != tt.wantSeen {
				t.Errorf("status %d, handler saw %q; want %d and %q", rec.Code, seen, tt.status, tt.wantSeen)
			}
		})
	}
}

// A route with no declared guard kind is refused for everyone.
func TestGuardRefusesAnUndeclaredKind(t *testing.T) {
	t.Parallel()

	server, _ := fullTestServer(t)
	guarded := server.guard(route{
		method: nethttp.MethodGet, pattern: "/api/test", capability: authz.SessionSelf,
		handler: func(nethttp.ResponseWriter, *nethttp.Request) { t.Error("handler reached through an undeclared guard") },
	})

	rec := newRecorder()
	guarded(rec, withIdentity(newTestRequest(t, nethttp.MethodGet, "/api/test", nil),
		Identity{Username: defaultLoginUsername, Role: model.RoleAdmin, AllServices: true}))

	if rec.Code != nethttp.StatusForbidden {
		t.Errorf("status = %d, want 403", rec.Code)
	}
}
