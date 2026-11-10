package http

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"log/slog"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	nethttp "net/http"

	"github.com/oralecarlangelo/touchgrass/internal/authz"
	"github.com/oralecarlangelo/touchgrass/internal/model"
	"github.com/oralecarlangelo/touchgrass/internal/store"
	"github.com/oralecarlangelo/touchgrass/internal/testfixture"
)

// Scoped fixture logins. All share the harness viewer password.
const (
	// testScopedUsername is a viewer granted testfixture.GenericGranted.
	testScopedUsername = "scoped"
	// testEmptyUsername is a viewer granted no service at all.
	testEmptyUsername = "unassigned"
	// testReservedUsername carries the reserved developer role.
	testReservedUsername = "reserved"
	// testReachedHeader marks a response written by a route's handler,
	// as opposed to its guard.
	testReachedHeader = "X-Test-Reached"
	// testMissingID is a resource id no fixture row has.
	testMissingID = int64(1) << 40
)

// legacyAdminOnly is every route requireAdmin wrapped before the route
// table existed. It is frozen here so the table can be proved to treat
// an all-services viewer exactly as the old gate did.
var legacyAdminOnly = []string{
	"POST /api/services",
	"DELETE /api/services/{id}",
	"POST /api/services/{id}/deploys",
	"POST /api/services/{id}/cutover",
	"POST /api/services/{id}/rollback",
	"POST /api/services/{id}/deploy",
	"POST /api/alerts/rules",
	"DELETE /api/alerts/rules/{id}",
	"POST /api/notifications/{id}/read",
	"POST /api/notifications/read",
	"GET /api/users",
	"POST /api/users",
	"POST /api/users/{id}/password",
	"POST /api/users/{id}/role",
	"POST /api/users/{id}/disable",
	"POST /api/users/{id}/enable",
	"DELETE /api/users/{id}",
	"POST /api/keys",
	"POST /api/keys/{id}/revoke",
	"POST /api/issues/rules",
	"DELETE /api/issues/rules/{id}",
	"POST /api/docker/images/prune",
	"POST /api/system/containers/{id}/approve",
	"POST /api/system/containers/{id}/ignore",
	"POST /api/security/findings/{id}/acknowledge",
	"POST /api/security/findings/{id}/resolve",
	"POST /api/security/findings/{id}/suppress",
	"POST /api/security/policies",
	"PATCH /api/security/policies/{id}",
	"DELETE /api/security/policies/{id}",
	"POST /api/databases/backups",
	"POST /api/databases/backups/{name}/verify",
	"POST /api/databases/restore",
	"PUT /api/services/{id}/env/entries/{key}",
	"DELETE /api/services/{id}/env/entries/{key}",
	"POST /api/services/{id}/env/entries/{key}/reveal",
	"POST /api/services/{id}/env/discard",
	"POST /api/services/{id}/env/import",
	"POST /api/services/{id}/env/apply",
	"POST /api/services/{id}/env/revisions/{rev}/rollback",
}

// adminOnlySince31 are the routes S31 made admin-only: the onboarding
// suggestion (D-11) and the new access route.
var adminOnlySince31 = []string{
	"GET /api/onboarding/suggest",
	"PUT /api/users/{id}/access",
}

// scopedFixture is a server holding the generic two-service fixture,
// one row of every resource kind in each, and the scoped logins.
type scopedFixture struct {
	server  *Server
	db      *store.DB
	granted map[model.ResourceKind]int64
	other   map[model.ResourceKind]int64
}

// newScopedFixture builds the scoped harness.
func newScopedFixture(t *testing.T) scopedFixture {
	t.Helper()

	server, db := fullTestServer(t)
	ctx := context.Background()
	services := store.NewServiceStore(db)

	for _, service := range testfixture.GenericServices() {
		if err := services.Create(ctx, service); err != nil {
			t.Fatalf("Create(%s) error = %v, want nil", service.ID, err)
		}
	}

	hash, err := hashViewerPassword()
	if err != nil {
		t.Fatalf("hashing viewer password: %v", err)
	}

	users := store.NewUserStore(db)

	for _, user := range []model.User{
		{
			Username: testScopedUsername, PasswordHash: string(hash), Role: model.RoleViewer,
			Services: []string{testfixture.GenericGranted},
		},
		{Username: testEmptyUsername, PasswordHash: string(hash), Role: model.RoleViewer},
		{Username: testReservedUsername, PasswordHash: string(hash), Role: model.RoleDeveloper, AllServices: true},
	} {
		if _, err := users.Create(ctx, user, defaultLoginUsername); err != nil {
			t.Fatalf("Create(%s) error = %v, want nil", user.Username, err)
		}
	}

	return scopedFixture{
		server:  server,
		db:      db,
		granted: seedOwnedRows(t, db, testfixture.GenericGranted),
		other:   seedOwnedRows(t, db, testfixture.GenericOther),
	}
}

// cookieFor logs in one fixture user.
func (f scopedFixture) cookieFor(t *testing.T, username string) *nethttp.Cookie {
	t.Helper()

	return loginCookie(t, f.server, `{"username":"`+username+`","password":"`+testViewerPassword+`"}`)
}

// seedOwnedRows stores one row of every resource kind for serviceID,
// plus an audit row and a deploy, and returns the resource ids.
func seedOwnedRows(t *testing.T, db *store.DB, serviceID string) map[model.ResourceKind]int64 {
	t.Helper()

	ctx := context.Background()
	ids := map[model.ResourceKind]int64{}

	occurrence, err := store.NewIssueStore(db).GroupOccurrence(ctx, serviceID, "fp-"+serviceID, "boom in "+serviceID, "v1",
		model.Occurrence{ServiceID: serviceID, Type: "Error", Message: "boom in " + serviceID, CreatedAt: time.Now()})
	if err != nil || occurrence.IssueID == nil {
		t.Fatalf("GroupOccurrence(%s) = %+v, %v, want an issue", serviceID, occurrence, err)
	}

	ids[model.ResourceIssue] = *occurrence.IssueID

	logs := store.NewLogStore(db)

	if _, err := logs.InsertBatch(ctx, []model.LogLine{
		{ServiceID: serviceID, Container: "c1", Stream: "stdout", Line: "hello from " + serviceID, Ts: time.Now()},
	}); err != nil {
		t.Fatalf("InsertBatch(%s) error = %v, want nil", serviceID, err)
	}

	lines, err := logs.Search(ctx, store.LogFilter{ServiceID: serviceID, Limit: 1})
	if err != nil || len(lines) != 1 {
		t.Fatalf("Search(%s) = %+v, %v, want one line", serviceID, lines, err)
	}

	ids[model.ResourceLogLine] = lines[0].ID

	rule, err := store.NewRuleStore(db).Create(ctx, testfixture.DefaultRule(serviceID))
	if err != nil {
		t.Fatalf("Create(alert rule) error = %v, want nil", err)
	}

	ids[model.ResourceAlertRule] = rule.ID

	issueRule, err := store.NewIssueRuleStore(db).Create(ctx, model.IssueRule{
		ServiceID: serviceID, Kind: "new_issue", Threshold: 1, WindowSecs: 60, Enabled: true,
	})
	if err != nil {
		t.Fatalf("Create(issue rule) error = %v, want nil", err)
	}

	ids[model.ResourceIssueRule] = issueRule.ID

	key, err := store.NewKeyStore(db).Create(ctx, model.APIKey{
		ServiceID: serviceID, KeyHash: "hash-" + serviceID, KeyPrefix: "tg_" + serviceID[:4], SampleRate: 1,
	})
	if err != nil {
		t.Fatalf("Create(key) error = %v, want nil", err)
	}

	ids[model.ResourceAPIKey] = key.ID

	notification, err := store.NewNotificationStore(db).Insert(ctx, serviceID, "alert", "alert on "+serviceID, "body")
	if err != nil {
		t.Fatalf("Insert(notification) error = %v, want nil", err)
	}

	ids[model.ResourceNotification] = notification.ID

	owner := serviceID

	if _, err := store.NewAuditStore(db).Insert(ctx, model.Audit{
		ServiceID: &owner, Actor: defaultLoginUsername, Action: model.AuditDeploy,
		Result: model.AuditSuccess, Detail: "deployed " + serviceID,
	}); err != nil {
		t.Fatalf("Insert(audit) error = %v, want nil", err)
	}

	return ids
}

// guardHarness serves the real route table behind the real middleware
// and guards, with every handler swapped for a marker. A response that
// carries testReachedHeader got past its guard; nothing has side
// effects, so every route can be called as every kind of user.
func guardHarness(server *Server) nethttp.Handler {
	mux := nethttp.NewServeMux()

	for _, rt := range server.routes() {
		rt.handler = func(w nethttp.ResponseWriter, _ *nethttp.Request) {
			w.Header().Set(testReachedHeader, "1")
			w.WriteHeader(nethttp.StatusNoContent)
		}

		mux.HandleFunc(rt.method+" "+rt.pattern, server.guard(rt))
	}

	return server.auth.Middleware(slog.New(slog.DiscardHandler), mux)
}

// routeTarget fills a route pattern's placeholders. serviceID fills
// {id} on service-keyed routes and resourceID fills it on the rest.
func routeTarget(rt route, serviceID string, resourceID int64) string {
	id := strconv.FormatInt(resourceID, 10)
	if rt.kind == guardServicePath || strings.HasPrefix(rt.pattern, "/api/services/{id}") {
		id = serviceID
	}

	target := strings.NewReplacer(
		"{id}", id, "{key}", "PORT", "{run_id}", "run-1", "{rev}", "1", "{name}", "x.dump",
	).Replace(rt.pattern)

	if rt.kind == guardServiceQuery && serviceID != "" {
		target += "?" + serviceIDParam + "=" + serviceID
	}

	return target
}

// routeBody is the body a guardServiceBody route is called with.
func routeBody(rt route, serviceID string) string {
	if rt.kind != guardServiceBody {
		return ""
	}

	return `{"` + serviceIDParam + `":"` + serviceID + `"}`
}

// call performs one request against handler and reports the status and
// whether the route's handler was reached.
func call(
	t *testing.T,
	handler nethttp.Handler,
	cookie *nethttp.Cookie,
	method, target, body string,
) (int, bool, string) {
	t.Helper()

	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}

	req := newTestRequest(t, method, target, reader)

	if cookie != nil {
		req.AddCookie(cookie)
	}

	rec := newRecorder()
	handler.ServeHTTP(rec, req)

	return rec.Code, rec.Header().Get(testReachedHeader) != "", rec.Body.String()
}

func routeName(rt route) string {
	return rt.method + " " + rt.pattern
}

func TestRouteTableIsConsistent(t *testing.T) {
	t.Parallel()

	server, _ := fullTestServer(t)
	seen := map[string]bool{}

	for _, rt := range server.routes() {
		name := routeName(rt)

		if seen[name] {
			t.Errorf("%s is declared twice", name)
		}

		seen[name] = true

		if !strings.HasPrefix(rt.pattern, "/api/") {
			t.Errorf("%s is in the API route table but is not under /api/", name)
		}

		if rt.handler == nil {
			t.Errorf("%s has no handler", name)
		}

		scope := authz.ScopeOf(rt.capability)

		// Each guard kind only makes sense with one scope class: a
		// mismatch would let a route check the wrong thing.
		wantScope := map[guardKind]authz.Scope{
			guardPublic:       authz.ScopeUnknown,
			guardSession:      authz.ScopeSelf,
			guardFleet:        authz.ScopeFleet,
			guardServicePath:  authz.ScopeService,
			guardServiceQuery: authz.ScopeService,
			guardServiceBody:  authz.ScopeService,
			guardResource:     authz.ScopeService,
		}

		want, known := wantScope[rt.kind]
		if !known {
			t.Errorf("%s has guard kind %d, want a declared kind", name, rt.kind)

			continue
		}

		if scope != want {
			t.Errorf("%s: capability %q has scope %s, want %s for its guard kind", name, rt.capability, scope, want)
		}

		if (rt.kind == guardResource) != (rt.resource != model.ResourceUnknown) {
			t.Errorf("%s: resource kind %d does not match its guard kind", name, rt.resource)
		}

		if rt.listScoped && rt.kind != guardServiceQuery {
			t.Errorf("%s is listScoped but is not keyed by service_id", name)
		}

		if rt.kind == guardPublic != isPublic(rt.pattern) {
			t.Errorf("%s: route table and auth middleware disagree on whether it is public", name)
		}
	}

	if len(seen) != 93 {
		t.Errorf("route table has %d routes, want 93 (the 92 before S31 plus PUT /api/users/{id}/access)", len(seen))
	}
}

// muxRegistration is one Handle or HandleFunc call found in the
// package source. literal is empty when the pattern is not a literal.
type muxRegistration struct {
	position string
	literal  string
}

// muxRegistrations parses the package's non-test files and returns
// every Handle and HandleFunc call.
func muxRegistrations(t *testing.T) []muxRegistration {
	t.Helper()

	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("listing package files: %v", err)
	}

	found := []muxRegistration{}
	fileSet := token.NewFileSet()

	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}

		parsed, err := parser.ParseFile(fileSet, name, nil, 0)
		if err != nil {
			t.Fatalf("parsing %s: %v", name, err)
		}

		ast.Inspect(parsed, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok || len(call.Args) == 0 {
				return true
			}

			selector, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || (selector.Sel.Name != "HandleFunc" && selector.Sel.Name != "Handle") {
				return true
			}

			registration := muxRegistration{position: fileSet.Position(call.Pos()).String()}

			if literal, ok := call.Args[0].(*ast.BasicLit); ok {
				registration.literal = literal.Value
			}

			found = append(found, registration)

			return true
		})
	}

	return found
}

// No /api/ route may be registered outside the route table: a literal
// pattern handed straight to a mux would have no guard.
func TestNoRouteIsRegisteredOutsideTheTable(t *testing.T) {
	t.Parallel()

	registrations := muxRegistrations(t)
	tableLoops := 0

	for _, registration := range registrations {
		if registration.literal == "" {
			tableLoops++

			continue
		}

		if strings.Contains(registration.literal, "/api") {
			t.Errorf("%s registers %s directly on a mux; declare it in routes() so it gets a guard",
				registration.position, registration.literal)
		}
	}

	// The only registration without a literal pattern is the loop over
	// the route table; the literals are /docs, /docs/ and the two SPA
	// fallbacks. A new registration has to be looked at.
	if tableLoops != 1 || len(registrations) != 5 {
		t.Errorf("found %d mux registrations (%d without a literal pattern), want 5 and 1: %+v",
			len(registrations), tableLoops, registrations)
	}
}

func TestEveryRouteIsGuarded(t *testing.T) {
	t.Parallel()

	fixture := newScopedFixture(t)
	handler := guardHarness(fixture.server)
	reserved := fixture.cookieFor(t, testReservedUsername)

	for _, rt := range fixture.server.routes() {
		t.Run(routeName(rt), func(t *testing.T) {
			t.Parallel()

			target := routeTarget(rt, testfixture.GenericGranted, fixture.granted[rt.resource])
			body := routeBody(rt, testfixture.GenericGranted)

			status, reached, _ := call(t, handler, nil, rt.method, target, body)

			if rt.kind == guardPublic {
				if !reached {
					t.Errorf("public route without a session: status %d, want it to reach its handler", status)
				}

				return
			}

			if reached || status != nethttp.StatusUnauthorized {
				t.Errorf("without a session: status %d reached=%t, want 401", status, reached)
			}

			// An account whose role holds nothing is refused everywhere,
			// its own session routes included: nothing is open by default.
			status, reached, response := call(t, handler, reserved, rt.method, target, body)
			if reached || status != nethttp.StatusForbidden || !strings.Contains(response, `"code":"forbidden"`) {
				t.Errorf("role with no capabilities: status %d reached=%t body %s, want 403 forbidden",
					status, reached, response)
			}
		})
	}
}

// An all-services viewer is refused exactly the routes requireAdmin
// refused, plus the two S31 made admin-only. An admin reaches all.
func TestAllServicesAccountsMatchTheLegacyGate(t *testing.T) {
	t.Parallel()

	fixture := newScopedFixture(t)
	handler := guardHarness(fixture.server)
	viewer := viewerCookie(t, fixture.server)
	admin := authCookie(t, fixture.server)

	adminOnly := append(slices.Clone(legacyAdminOnly), adminOnlySince31...)

	for _, name := range adminOnly {
		if !slices.ContainsFunc(fixture.server.routes(), func(rt route) bool { return routeName(rt) == name }) {
			t.Errorf("admin-only route %s is no longer in the route table", name)
		}
	}

	for _, rt := range fixture.server.routes() {
		if rt.kind == guardPublic {
			continue
		}

		t.Run(routeName(rt), func(t *testing.T) {
			t.Parallel()

			target := routeTarget(rt, testfixture.GenericOther, fixture.other[rt.resource])
			body := routeBody(rt, testfixture.GenericOther)

			status, reached, _ := call(t, handler, admin, rt.method, target, body)
			if !reached {
				t.Errorf("admin: status %d, want the handler reached", status)
			}

			wantReached := !slices.Contains(adminOnly, routeName(rt))

			status, reached, _ = call(t, handler, viewer, rt.method, target, body)
			if reached != wantReached {
				t.Errorf("all-services viewer: status %d reached=%t, want reached=%t", status, reached, wantReached)
			}

			if !wantReached && status != nethttp.StatusForbidden {
				t.Errorf("all-services viewer: status %d, want 403", status)
			}
		})
	}
}

// scopedExpectation is what the scoped viewer must get from one route
// for one target.
type scopedExpectation struct {
	label   string
	target  string
	body    string
	status  int
	reached bool
}

// scopedExpectations derives, from a route's declaration alone, what a
// viewer granted only GenericGranted must get from it.
func scopedExpectations(fixture scopedFixture, rt route) []scopedExpectation {
	granted, other, unknown := testfixture.GenericGranted, testfixture.GenericOther, "no-such-service"
	holds := authz.RoleHolds(model.RoleViewer, rt.capability)

	forbidden := func(label, serviceID string, resourceID int64) scopedExpectation {
		return scopedExpectation{
			label: label, target: routeTarget(rt, serviceID, resourceID), body: routeBody(rt, serviceID),
			status: nethttp.StatusForbidden,
		}
	}

	inScope := scopedExpectation{
		label: "in scope", target: routeTarget(rt, granted, fixture.granted[rt.resource]),
		body: routeBody(rt, granted), status: nethttp.StatusNoContent, reached: true,
	}

	if !holds {
		inScope.status, inScope.reached = nethttp.StatusForbidden, false
	}

	switch rt.kind {
	case guardFleet:
		return []scopedExpectation{forbidden("fleet route", granted, fixture.granted[rt.resource])}
	case guardServicePath, guardServiceBody:
		return []scopedExpectation{inScope, forbidden("out of scope", other, 0), forbidden("unknown service", unknown, 0)}
	case guardServiceQuery:
		absent := scopedExpectation{label: "no service_id", target: routeTarget(rt, "", 0)}

		switch {
		case !holds:
			absent.status = nethttp.StatusForbidden
		case rt.listScoped:
			absent.status, absent.reached = nethttp.StatusNoContent, true
		default:
			absent.status = nethttp.StatusBadRequest
		}

		return []scopedExpectation{
			inScope, absent, forbidden("out of scope", other, 0), forbidden("unknown service", unknown, 0),
		}
	case guardResource:
		if !holds {
			return []scopedExpectation{inScope, forbidden("out of scope", other, fixture.other[rt.resource])}
		}

		return []scopedExpectation{
			inScope,
			{label: "out of scope", target: routeTarget(rt, other, fixture.other[rt.resource]), status: nethttp.StatusNotFound},
			{label: "missing id", target: routeTarget(rt, other, testMissingID), status: nethttp.StatusNotFound},
			{label: "malformed id", target: strings.Replace(rt.pattern, "{id}", "abc", 1), status: nethttp.StatusBadRequest},
		}
	case guardSession:
		return []scopedExpectation{{
			label: "own session", target: rt.pattern, status: nethttp.StatusNoContent, reached: true,
		}}
	case guardPublic, guardUnknown:
		return []scopedExpectation{}
	default:
		return []scopedExpectation{}
	}
}

// The scoped-viewer matrix: every route, called in scope, out of scope,
// with an unknown service, and with a foreign or missing resource id.
func TestScopedViewerMatrix(t *testing.T) {
	t.Parallel()

	fixture := newScopedFixture(t)
	handler := guardHarness(fixture.server)
	cookie := fixture.cookieFor(t, testScopedUsername)

	for _, rt := range fixture.server.routes() {
		for _, want := range scopedExpectations(fixture, rt) {
			t.Run(routeName(rt)+"/"+want.label, func(t *testing.T) {
				t.Parallel()

				status, reached, body := call(t, handler, cookie, rt.method, want.target, want.body)
				if status != want.status || reached != want.reached {
					t.Errorf("%s %s = %d reached=%t (%s), want %d reached=%t",
						rt.method, want.target, status, reached, strings.TrimSpace(body), want.status, want.reached)
				}
			})
		}
	}
}

// A service out of scope and a service that does not exist answer the
// same bytes, and so do a foreign and a missing resource id: nothing
// tells a scoped user which ids exist.
func TestScopedDenialsDoNotRevealWhatExists(t *testing.T) {
	t.Parallel()

	fixture := newScopedFixture(t)
	cookie := fixture.cookieFor(t, testScopedUsername)
	handler := fixture.server.handler

	pairs := []struct {
		name    string
		exists  string
		missing string
		status  int
	}{
		{
			name:   "service in a path",
			exists: "/api/services/" + testfixture.GenericOther + "/env", missing: "/api/services/no-such-service/env",
			status: nethttp.StatusForbidden,
		},
		{
			name:   "service in a query",
			exists: "/api/logs?service_id=" + testfixture.GenericOther, missing: "/api/logs?service_id=no-such-service",
			status: nethttp.StatusForbidden,
		},
		{
			name:    "issue id",
			exists:  "/api/issues/" + strconv.FormatInt(fixture.other[model.ResourceIssue], 10),
			missing: "/api/issues/" + strconv.FormatInt(testMissingID, 10),
			status:  nethttp.StatusNotFound,
		},
		{
			name:    "log line id",
			exists:  "/api/logs/" + strconv.FormatInt(fixture.other[model.ResourceLogLine], 10),
			missing: "/api/logs/" + strconv.FormatInt(testMissingID, 10),
			status:  nethttp.StatusNotFound,
		},
	}

	for _, tt := range pairs {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			existsStatus, _, existsBody := call(t, handler, cookie, nethttp.MethodGet, tt.exists, "")
			missingStatus, _, missingBody := call(t, handler, cookie, nethttp.MethodGet, tt.missing, "")

			if existsStatus != tt.status || missingStatus != tt.status {
				t.Errorf("statuses = %d and %d, want %d for both", existsStatus, missingStatus, tt.status)
			}

			if existsBody != missingBody {
				t.Errorf("bodies differ: %q for an id that exists, %q for one that does not", existsBody, missingBody)
			}
		})
	}
}

// A viewer granted no service reaches only their own session routes
// and the three lists, which come back empty.
func TestViewerWithNoServices(t *testing.T) {
	t.Parallel()

	fixture := newScopedFixture(t)
	handler := guardHarness(fixture.server)
	cookie := fixture.cookieFor(t, testEmptyUsername)

	for _, rt := range fixture.server.routes() {
		if rt.kind == guardPublic {
			continue
		}

		t.Run(routeName(rt), func(t *testing.T) {
			t.Parallel()

			wantReached := rt.kind == guardSession ||
				(rt.listScoped && authz.RoleHolds(model.RoleViewer, rt.capability))

			target := routeTarget(rt, "", fixture.granted[rt.resource])

			status, reached, _ := call(t, handler, cookie, rt.method, target, routeBody(rt, testfixture.GenericGranted))
			if reached != wantReached {
				t.Errorf("status %d reached=%t, want reached=%t", status, reached, wantReached)
			}
		})
	}
}
