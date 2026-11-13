package http

import (
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	nethttp "net/http"

	"github.com/oralecarlangelo/touchgrass/internal/authz"
	"github.com/oralecarlangelo/touchgrass/internal/model"
	"github.com/oralecarlangelo/touchgrass/internal/testfixture"
)

// matrixReach is the outcome of a request that got past its guard.
const matrixReach = "reach"

// matrixOutcomes is what one route answers an account of one role:
// with all services, scoped and in scope, scoped and out of scope.
type matrixOutcomes struct {
	allServices string
	inScope     string
	outOfScope  string
}

// loadRouteMatrix reads a checked-in route matrix: one tab-separated
// line per route, comments starting with #.
func loadRouteMatrix(t *testing.T, name string) map[string]matrixOutcomes {
	t.Helper()

	//nolint:gosec // G304: name is one of the two matrix files this test names itself.
	body, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("reading the route matrix: %v", err)
	}

	matrix := map[string]matrixOutcomes{}

	for line := range strings.SplitSeq(string(body), "\n") {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		cells := strings.Split(line, "\t")
		if len(cells) != 4 {
			t.Fatalf("%s: line %q has %d columns, want 4", name, line, len(cells))
		}

		if _, dup := matrix[cells[0]]; dup {
			t.Fatalf("%s lists %s twice", name, cells[0])
		}

		matrix[cells[0]] = matrixOutcomes{allServices: cells[1], inScope: cells[2], outOfScope: cells[3]}
	}

	return matrix
}

// observeRoute calls one route as cookie's account for serviceID and
// reports matrixReach or the status the guard answered.
func observeRoute(
	t *testing.T,
	handler nethttp.Handler,
	cookie *nethttp.Cookie,
	rt route,
	serviceID string,
	resourceID int64,
) string {
	t.Helper()

	status, reached, _ := call(t, handler, cookie, rt.method, routeTarget(rt, serviceID, resourceID), routeBody(rt, serviceID))
	if reached {
		return matrixReach
	}

	return strconv.Itoa(status)
}

// The route matrices list, for a viewer and for a developer, every
// route the role is allowed and every route it is refused: with all
// services, in scope, and out of scope. The viewer's file is frozen at
// v0.6.0; the developer's is the reviewed statement of what that role
// may do. A route missing from a file, a stale line, or any outcome
// that differs fails here.
func TestRoleRouteMatrices(t *testing.T) {
	t.Parallel()

	fixture := newScopedFixture(t)
	handler := guardHarness(fixture.server)
	granted, other := testfixture.GenericGranted, testfixture.GenericOther

	roles := []struct {
		role   model.Role
		file   string
		all    *nethttp.Cookie
		scoped *nethttp.Cookie
	}{
		{
			role: model.RoleViewer, file: "access_viewer_v060.tsv",
			all: viewerCookie(t, fixture.server), scoped: fixture.cookieFor(t, testScopedUsername),
		},
		{
			role: model.RoleDeveloper, file: "access_developer.tsv",
			all: fixture.cookieFor(t, testFleetDevUsername), scoped: fixture.cookieFor(t, testDevUsername),
		},
	}

	for _, tt := range roles {
		t.Run(string(tt.role), func(t *testing.T) {
			t.Parallel()

			want := loadRouteMatrix(t, tt.file)
			seen := []string{}

			for _, rt := range fixture.server.routes() {
				if rt.kind == guardPublic {
					continue
				}

				name := routeName(rt)
				seen = append(seen, name)

				expected, listed := want[name]
				if !listed {
					t.Errorf("%s is not in testdata/%s: state what a %s may do with it", name, tt.file, tt.role)

					continue
				}

				got := matrixOutcomes{
					allServices: observeRoute(t, handler, tt.all, rt, other, fixture.other[rt.resource]),
					inScope:     observeRoute(t, handler, tt.scoped, rt, granted, fixture.granted[rt.resource]),
					outOfScope:  observeRoute(t, handler, tt.scoped, rt, other, fixture.other[rt.resource]),
				}

				if got != expected {
					t.Errorf("%s as a %s = %+v, want %+v", name, tt.role, got, expected)
				}
			}

			for name := range want {
				if !slices.Contains(seen, name) {
					t.Errorf("testdata/%s lists %s, which is not in the route table", tt.file, name)
				}
			}
		})
	}
}

// developerGains are the only routes a developer reaches that a viewer
// does not. Everything else in the two matrices must be identical, so
// the developer file cannot drift into granting something unreviewed.
var developerGains = []string{
	"POST /api/services/{id}/deploys",
	"POST /api/services/{id}/cutover",
	"POST /api/services/{id}/rollback",
	"POST /api/services/{id}/deploy",
	"POST /api/alerts/rules",
	"DELETE /api/alerts/rules/{id}",
	"POST /api/issues/rules",
	"DELETE /api/issues/rules/{id}",
	"POST /api/notifications/{id}/read",
	"POST /api/notifications/read",
	// S33: a developer reaches the two stage routes as a proposer. The
	// handler then refuses every secret key; see env_proposal_test.go.
	"PUT /api/services/{id}/env/entries/{key}",
	"DELETE /api/services/{id}/env/entries/{key}",
	// Schema migrations: a developer reads the status and the run list
	// of a service in scope. Setup, commands and run output stay admin.
	"GET /api/services/{id}/migrations",
	"GET /api/services/{id}/migrations/runs",
}

func TestDeveloperMatrixDiffersFromViewerOnlyByItsOperations(t *testing.T) {
	t.Parallel()

	viewer := loadRouteMatrix(t, "access_viewer_v060.tsv")
	developer := loadRouteMatrix(t, "access_developer.tsv")

	for name, dev := range developer {
		base, ok := viewer[name]
		if !ok {
			t.Errorf("%s is in the developer matrix but not the viewer's", name)

			continue
		}

		if !slices.Contains(developerGains, name) {
			if dev != base {
				t.Errorf("%s: developer %+v differs from viewer %+v, and it is not a developer operation", name, dev, base)
			}

			continue
		}

		if base.allServices == matrixReach || base.inScope == matrixReach {
			t.Errorf("%s is listed as a developer gain, but a viewer already reaches it: %+v", name, base)
		}

		if dev.allServices != matrixReach || dev.inScope != matrixReach {
			t.Errorf("%s: developer %+v, want it reached with all services and in scope", name, dev)
		}

		if dev.outOfScope == matrixReach {
			t.Errorf("%s: a scoped developer reaches it for a service out of scope", name)
		}
	}

	for _, name := range developerGains {
		if _, ok := developer[name]; !ok {
			t.Errorf("developer gain %s is not in the developer matrix", name)
		}
	}
}

// Whatever its scope, a developer is refused every route that applies,
// discards, imports, reveals or rolls back env, sets up or runs schema
// migrations and seeds, or changes keys, users, services, the host,
// security or databases. The
// list is written out so that re-guarding one of them cannot pass
// unnoticed.
func TestDeveloperIsRefusedEveryAdminOnlyRoute(t *testing.T) {
	t.Parallel()

	fixture := newScopedFixture(t)
	handler := guardHarness(fixture.server)
	granted := testfixture.GenericGranted

	adminOnly := viewerRefused()
	adminOnly = slices.DeleteFunc(adminOnly, func(name string) bool { return slices.Contains(developerGains, name) })

	if len(adminOnly) != 37 {
		t.Fatalf("admin-only routes after the developer's fourteen = %d, want 37", len(adminOnly))
	}

	cookies := map[string]*nethttp.Cookie{
		"all-services developer": fixture.cookieFor(t, testFleetDevUsername),
		"scoped developer":       fixture.cookieFor(t, testDevUsername),
	}

	for _, rt := range fixture.server.routes() {
		if !slices.Contains(adminOnly, routeName(rt)) {
			continue
		}

		if authz.RoleHolds(model.RoleDeveloper, rt.capability) {
			t.Errorf("%s: the developer role holds %s", routeName(rt), rt.capability)
		}

		for label, cookie := range cookies {
			status, reached, _ := call(t, handler, cookie, rt.method,
				routeTarget(rt, granted, fixture.granted[rt.resource]), routeBody(rt, granted))
			if reached || status != nethttp.StatusForbidden {
				t.Errorf("%s as %s: status %d reached=%t, want 403", routeName(rt), label, status, reached)
			}
		}
	}
}
