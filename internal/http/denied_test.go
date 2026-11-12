package http

import (
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"testing"

	nethttp "net/http"

	"github.com/oralecarlangelo/touchgrass/internal/authz"
	"github.com/oralecarlangelo/touchgrass/internal/model"
	"github.com/oralecarlangelo/touchgrass/internal/testfixture"
)

// testLeakMarker is planted in query strings, bodies and headers of
// refused requests; it must never reach the log.
const testLeakMarker = "hunter2-do-not-log"

// deniedLine is the denial log line a refused request must write. An
// empty serviceID means the field must be absent.
type deniedLine struct {
	user       string
	role       model.Role
	capability authz.Capability
	serviceID  string
	method     string
	route      string
	status     int
	reason     authz.Refusal
}

// fields renders the line as the log attributes it must have.
func (l deniedLine) fields() map[string]any {
	fields := map[string]any{
		deniedKeyUser:       l.user,
		deniedKeyRole:       string(l.role),
		deniedKeyCapability: string(l.capability),
		deniedKeyMethod:     l.method,
		deniedKeyRoute:      l.route,
		deniedKeyStatus:     int64(l.status),
		deniedKeyReason:     string(l.reason),
	}

	if l.serviceID != "" {
		fields[deniedKeyServiceID] = l.serviceID
	}

	return fields
}

// checkDeniedLine holds one captured record to the line wanted: the
// same fields, no others, and nothing the request carried.
func checkDeniedLine(t *testing.T, record capturedRecord, want deniedLine, secrets ...string) {
	t.Helper()

	if record.message != deniedLogMessage {
		t.Errorf("message = %q, want %q", record.message, deniedLogMessage)
	}

	wanted := want.fields()

	for key, value := range wanted {
		if record.attrs[key] != value {
			t.Errorf("%s = %v, want %v", key, record.attrs[key], value)
		}
	}

	for key, value := range record.attrs {
		if _, expected := wanted[key]; !expected {
			t.Errorf("field %s = %v is present, want it absent", key, value)
		}

		for _, secret := range secrets {
			if strings.Contains(fmt.Sprint(value), secret) {
				t.Errorf("field %s = %v carries request data", key, value)
			}
		}
	}
}

// deniedCase is one refused request and the line it must write.
type deniedCase struct {
	name   string
	cookie *nethttp.Cookie
	target string
	body   string
	want   deniedLine
}

// deniedCookies are the sessions the denial cases are called with.
type deniedCookies struct {
	scopedViewer *nethttp.Cookie
	fleetViewer  *nethttp.Cookie
	scopedDev    *nethttp.Cookie
	unassigned   *nethttp.Cookie
}

// deniedCases lists one refusal per reason and per way a route finds
// its service.
func deniedCases(fixture scopedFixture, cookies deniedCookies) []deniedCase {
	granted, other := testfixture.GenericGranted, testfixture.GenericOther
	otherIssue := strconv.FormatInt(fixture.other[model.ResourceIssue], 10)

	return []deniedCase{
		{
			name: "service out of scope in the path", cookie: cookies.scopedViewer,
			target: "/api/services/" + other + "/env?token=" + testLeakMarker,
			want: deniedLine{
				user: testScopedUsername, role: model.RoleViewer, capability: authz.EnvReadKeys, serviceID: other,
				method: nethttp.MethodGet, route: "/api/services/{id}/env",
				status: nethttp.StatusForbidden, reason: authz.RefusalService,
			},
		},
		{
			name: "role lacks the capability in scope", cookie: cookies.scopedViewer,
			target: "/api/services/" + granted + "/deploy",
			want: deniedLine{
				user: testScopedUsername, role: model.RoleViewer, capability: authz.DeployTrigger, serviceID: granted,
				method: nethttp.MethodPost, route: "/api/services/{id}/deploy",
				status: nethttp.StatusForbidden, reason: authz.RefusalRole,
			},
		},
		{
			name: "fleet read as a scoped account", cookie: cookies.scopedViewer, target: "/api/system",
			want: deniedLine{
				user: testScopedUsername, role: model.RoleViewer, capability: authz.HostRead,
				method: nethttp.MethodGet, route: "/api/system",
				status: nethttp.StatusForbidden, reason: authz.RefusalAllServices,
			},
		},
		{
			name: "admin-only route as an all-services viewer", cookie: cookies.fleetViewer, target: testAccessMatrixPath,
			want: deniedLine{
				user: testViewerUsername, role: model.RoleViewer, capability: authz.UsersManage,
				method: nethttp.MethodGet, route: testAccessMatrixPath,
				status: nethttp.StatusForbidden, reason: authz.RefusalRole,
			},
		},
		{
			name: "service out of scope in a query", cookie: cookies.scopedViewer,
			target: "/api/logs?service_id=" + other + "&q=" + testLeakMarker,
			want: deniedLine{
				user: testScopedUsername, role: model.RoleViewer, capability: authz.LogsRead, serviceID: other,
				method: nethttp.MethodGet, route: "/api/logs",
				status: nethttp.StatusForbidden, reason: authz.RefusalService,
			},
		},
		{
			name: "service out of scope in a body", cookie: cookies.scopedDev,
			target: "/api/alerts/rules", body: `{"service_id":"` + other + `","note":"` + testLeakMarker + `"}`,
			want: deniedLine{
				user: testDevUsername, role: model.RoleDeveloper, capability: authz.AlertsManage, serviceID: other,
				method: nethttp.MethodPost, route: "/api/alerts/rules",
				status: nethttp.StatusForbidden, reason: authz.RefusalService,
			},
		},
		{
			name: "role lacks a body-keyed capability", cookie: cookies.scopedViewer,
			target: testKeysPath, body: `{"service_id":"` + granted + `","note":"` + testLeakMarker + `"}`,
			want: deniedLine{
				user: testScopedUsername, role: model.RoleViewer, capability: authz.KeysManage,
				method: nethttp.MethodPost, route: testKeysPath,
				status: nethttp.StatusForbidden, reason: authz.RefusalRole,
			},
		},
		{
			name: "account with no services", cookie: cookies.unassigned, target: "/api/issues/" + otherIssue,
			want: deniedLine{
				user: testEmptyUsername, role: model.RoleViewer, capability: authz.IssuesRead,
				method: nethttp.MethodGet, route: "/api/issues/{id}",
				status: nethttp.StatusForbidden, reason: authz.RefusalService,
			},
		},
		{
			name: "resource of a service out of scope", cookie: cookies.scopedViewer, target: "/api/issues/" + otherIssue,
			want: deniedLine{
				user: testScopedUsername, role: model.RoleViewer, capability: authz.IssuesRead, serviceID: other,
				method: nethttp.MethodGet, route: "/api/issues/{id}",
				status: nethttp.StatusNotFound, reason: reasonResourceOutOfScope,
			},
		},
		{
			name: "resource that does not exist", cookie: cookies.scopedViewer,
			target: "/api/issues/" + strconv.FormatInt(testMissingID, 10),
			want: deniedLine{
				user: testScopedUsername, role: model.RoleViewer, capability: authz.IssuesRead,
				method: nethttp.MethodGet, route: "/api/issues/{id}",
				status: nethttp.StatusNotFound, reason: reasonResourceNotFound,
			},
		},
	}
}

// Every refusal by a guard writes one line that says who, as what, for
// which capability, on which service, through which route and why, and
// nothing that came from the request itself.
//
//nolint:paralleltest // the subtests share one log capture and run in order.
func TestDeniedRequestsAreLoggedWithAReason(t *testing.T) {
	fixture := newScopedFixture(t)
	records := []capturedRecord{}
	fixture.server.logger = slog.New(captureHandler{records: &records})
	handler := guardHarness(fixture.server)

	cookies := deniedCookies{
		scopedViewer: fixture.cookieFor(t, testScopedUsername),
		fleetViewer:  viewerCookie(t, fixture.server),
		scopedDev:    fixture.cookieFor(t, testDevUsername),
		unassigned:   fixture.cookieFor(t, testEmptyUsername),
	}

	for _, tt := range deniedCases(fixture, cookies) {
		t.Run(tt.name, func(t *testing.T) {
			records = records[:0]

			req := newTestRequest(t, tt.want.method, tt.target, strings.NewReader(tt.body))
			req.AddCookie(tt.cookie)
			req.Header.Set("X-Api-Token", testLeakMarker)

			rec := newRecorder()
			handler.ServeHTTP(rec, req)

			if rec.Code != tt.want.status {
				t.Fatalf("status = %d (%s), want %d", rec.Code, rec.Body.String(), tt.want.status)
			}

			if len(records) != 1 {
				t.Fatalf("log records = %+v, want one line", records)
			}

			checkDeniedLine(t, records[0], tt.want, testLeakMarker, tt.cookie.Value)
		})
	}

	t.Run("an allowed request logs nothing", func(t *testing.T) {
		records = records[:0]

		status, reached, _ := call(t, handler, cookies.scopedViewer, nethttp.MethodGet,
			"/api/logs?service_id="+testfixture.GenericGranted, "")
		if !reached || len(records) != 0 {
			t.Errorf("status %d reached=%t with %d log records, want reached and none", status, reached, len(records))
		}
	})
}
