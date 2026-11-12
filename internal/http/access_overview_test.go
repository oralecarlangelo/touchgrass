package http

import (
	"slices"
	"testing"

	nethttp "net/http"

	"github.com/oralecarlangelo/touchgrass/internal/authz"
	"github.com/oralecarlangelo/touchgrass/internal/model"
)

const (
	testAccessMatrixPath = "/api/access/matrix"
	// testLabelScopedViewer names the viewer granted one service.
	testLabelScopedViewer = "scoped viewer"
)

// The matrix route answers what the policy package holds, row for row:
// there is no second table behind it.
func TestAccessMatrixIsThePolicy(t *testing.T) {
	t.Parallel()

	fixture := newScopedFixture(t)

	status, _, body := call(t, fixture.server.handler, authCookie(t, fixture.server), nethttp.MethodGet, testAccessMatrixPath, "")
	if status != nethttp.StatusOK {
		t.Fatalf("admin: status %d (%s), want 200", status, body)
	}

	got := decodeBodyInto[accessMatrixResponse](t, body)

	wantRoles := []string{}
	for _, role := range authz.Roles() {
		wantRoles = append(wantRoles, string(role))
	}

	if !slices.Equal(got.Roles, wantRoles) {
		t.Errorf("roles = %v, want %v", got.Roles, wantRoles)
	}

	policy := authz.Matrix()
	if len(got.Capabilities) != len(policy) || len(policy) != len(authz.Capabilities()) {
		t.Fatalf("capabilities = %d, policy rows = %d, want %d", len(got.Capabilities), len(policy), len(authz.Capabilities()))
	}

	for i, row := range got.Capabilities {
		checkMatrixRow(t, row, policy[i])
	}

	stage := got.Capabilities[slices.IndexFunc(got.Capabilities, func(row accessMatrixRow) bool {
		return row.Capability == string(authz.EnvStage)
	})]

	if stage.Limits[string(model.RoleDeveloper)] == "" || stage.Limits[string(model.RoleAdmin)] != "" {
		t.Errorf("env.stage limits = %v, want one for a developer and none for an admin", stage.Limits)
	}
}

// checkMatrixRow holds one row of the payload to the policy's row and
// to what the guards decide for every role.
func checkMatrixRow(t *testing.T, row accessMatrixRow, want authz.MatrixRow) {
	t.Helper()

	capability := authz.Capability(row.Capability)

	if capability != want.Capability || row.Scope != authz.ScopeOf(capability).String() || row.Summary != want.Summary {
		t.Errorf("row = %+v, want %s in scope %s with the policy's summary", row, want.Capability, want.Scope)
	}

	if row.Summary == "" || row.Limits == nil {
		t.Errorf("%s: summary %q, limits %v; want a summary and a limits object", row.Capability, row.Summary, row.Limits)
	}

	if len(row.Roles) != len(authz.Roles()) {
		t.Errorf("%s lists %d roles, want %d", row.Capability, len(row.Roles), len(authz.Roles()))
	}

	for _, role := range authz.Roles() {
		if row.Roles[string(role)] != authz.RoleHolds(role, capability) {
			t.Errorf("%s for %s = %t, the guards decide %t",
				row.Capability, role, row.Roles[string(role)], authz.RoleHolds(role, capability))
		}
	}
}

// Only an admin reads the matrix, through the real handler.
func TestAccessMatrixIsAdminOnly(t *testing.T) {
	t.Parallel()

	fixture := newScopedFixture(t)

	callers := map[string]*nethttp.Cookie{
		testLabelFleetViewer:  viewerCookie(t, fixture.server),
		testLabelScopedViewer: fixture.cookieFor(t, testScopedUsername),
		testLabelFleetDev:     fixture.cookieFor(t, testFleetDevUsername),
		testLabelScopedDev:    fixture.cookieFor(t, testDevUsername),
	}

	for label, cookie := range callers {
		status, _, body := call(t, fixture.server.handler, cookie, nethttp.MethodGet, testAccessMatrixPath, "")
		if status != nethttp.StatusForbidden || body != `{"error":"`+errForbiddenMessage+`","code":"`+codeForbidden+`"}`+"\n" {
			t.Errorf("%s: status %d body %q, want the plain 403", label, status, body)
		}
	}

	if status, _, _ := call(t, fixture.server.handler, nil, nethttp.MethodGet, testAccessMatrixPath, ""); status != nethttp.StatusUnauthorized {
		t.Errorf("no session: status %d, want 401", status)
	}
}
