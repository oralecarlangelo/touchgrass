package authz_test

import (
	"slices"
	"testing"

	"github.com/oralecarlangelo/touchgrass/internal/authz"
	"github.com/oralecarlangelo/touchgrass/internal/model"
)

func TestRolesAreExactlyTheAssignableRoles(t *testing.T) {
	t.Parallel()

	roles := authz.Roles()

	if !slices.Equal(roles, matrixRoles) {
		t.Errorf("Roles() = %v, want %v, the columns of testdata/matrix.tsv", roles, matrixRoles)
	}

	for _, role := range roles {
		if !authz.Assignable(role) {
			t.Errorf("Roles() lists %q, which is not assignable", role)
		}
	}

	for _, role := range []model.Role{model.RoleUnknown, "operator", ""} {
		if slices.Contains(roles, role) {
			t.Errorf("Roles() lists %q", role)
		}
	}
}

// The overview an admin reads is the table the guards use: every row,
// in order, with the grants of the checked-in matrix.
func TestMatrixMatchesTheCheckedInPolicy(t *testing.T) {
	t.Parallel()

	want := loadMatrix(t)
	got := authz.Matrix()

	if len(got) != len(want) {
		t.Fatalf("Matrix() has %d rows, want %d", len(got), len(want))
	}

	for i, row := range got {
		if row.Capability != want[i].capability || row.Scope.String() != want[i].scope {
			t.Errorf("row %d = %s (%s), want %s (%s)", i, row.Capability, row.Scope, want[i].capability, want[i].scope)
		}

		if row.Summary == "" {
			t.Errorf("%s has no summary", row.Capability)
		}

		if len(row.Grants) != len(matrixRoles) {
			t.Errorf("%s lists %d roles, want %d", row.Capability, len(row.Grants), len(matrixRoles))
		}

		for _, role := range matrixRoles {
			if row.Grants[role] != want[i].holds[role] {
				t.Errorf("%s for %s = %t, want %t", row.Capability, role, row.Grants[role], want[i].holds[role])
			}

			if row.Grants[role] != authz.RoleHolds(role, row.Capability) {
				t.Errorf("%s for %s disagrees with RoleHolds", row.Capability, role)
			}
		}

		for role, limit := range row.Limits {
			if !row.Grants[role] || limit == "" {
				t.Errorf("%s has a limit for %s, which does not hold it or is empty: %q", row.Capability, role, limit)
			}
		}
	}
}

// A developer's env.stage is narrower than its name; the overview has
// to say so, or an admin would read it as the right to change env.
func TestMatrixStatesTheDeveloperStageLimit(t *testing.T) {
	t.Parallel()

	for _, row := range authz.Matrix() {
		limit, limited := row.Limits[model.RoleDeveloper]

		if row.Capability == authz.EnvStage && !limited {
			t.Errorf("env.stage has no limit for a developer")
		}

		if _, adminLimited := row.Limits[model.RoleAdmin]; adminLimited {
			t.Errorf("%s has a limit for an admin: %q", row.Capability, limit)
		}
	}
}

// Changing what Matrix returned must not change the policy.
func TestMatrixIsACopy(t *testing.T) {
	t.Parallel()

	rows := authz.Matrix()
	rows[0].Grants[model.RoleViewer] = false

	for _, row := range rows {
		row.Grants[model.RoleViewer] = true
		row.Grants["operator"] = true
	}

	if !authz.RoleHolds(model.RoleViewer, authz.SessionSelf) || authz.RoleHolds(model.RoleViewer, authz.UsersManage) ||
		authz.RoleHolds("operator", authz.SessionSelf) {
		t.Errorf("editing Matrix() changed the policy")
	}
}

func TestRefusal(t *testing.T) {
	t.Parallel()

	scopedViewer := authz.Subject{Role: model.RoleViewer, Services: []string{testGranted}}
	fleetViewer := authz.Subject{Role: model.RoleViewer, AllServices: true}
	unassigned := authz.Subject{Role: model.RoleDeveloper}

	tests := []struct {
		name       string
		subject    authz.Subject
		capability authz.Capability
		serviceID  string
		want       authz.Refusal
	}{
		{name: "allowed in scope", subject: scopedViewer, capability: authz.LogsRead, serviceID: testGranted, want: authz.RefusalNone},
		{name: "role lacks it", subject: scopedViewer, capability: authz.DeployTrigger, serviceID: testGranted, want: authz.RefusalRole},
		{name: "role lacks it out of scope", subject: scopedViewer, capability: authz.DeployTrigger, serviceID: testOther, want: authz.RefusalRole},
		{name: "service out of scope", subject: scopedViewer, capability: authz.LogsRead, serviceID: testOther, want: authz.RefusalService},
		{name: "no service named", subject: scopedViewer, capability: authz.LogsRead, want: authz.RefusalService},
		{name: "no services assigned", subject: unassigned, capability: authz.DeployTrigger, serviceID: testGranted, want: authz.RefusalService},
		{name: "fleet read needs all services", subject: scopedViewer, capability: authz.HostRead, want: authz.RefusalAllServices},
		{name: "fleet read with all services", subject: fleetViewer, capability: authz.HostRead, want: authz.RefusalNone},
		{name: "fleet mutation as viewer", subject: fleetViewer, capability: authz.UsersManage, want: authz.RefusalRole},
		{name: "unknown capability", subject: fleetViewer, capability: "nope.nothing", want: authz.RefusalRole},
		{name: "unknown role", subject: authz.Subject{Role: "operator", AllServices: true}, capability: authz.SessionSelf, want: authz.RefusalRole},
		{name: "zero subject", subject: authz.Subject{}, capability: authz.SessionSelf, want: authz.RefusalRole},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.subject.Refusal(tt.capability, tt.serviceID); got != tt.want {
				t.Errorf("Refusal(%s, %q) = %q, want %q", tt.capability, tt.serviceID, got, tt.want)
			}
		})
	}
}

// Refusal and Can never disagree, for any role, scope and capability.
func TestRefusalIsNoneExactlyWhenCanIsTrue(t *testing.T) {
	t.Parallel()

	roles := append(slices.Clone(matrixRoles), "operator")
	scopes := []authz.Subject{{AllServices: true}, {Services: []string{testGranted}}, {}}

	for _, role := range roles {
		for _, subject := range scopes {
			subject.Role = role

			for _, capability := range authz.Capabilities() {
				for _, serviceID := range []string{testGranted, testOther, ""} {
					allowed := subject.Can(capability, serviceID)
					refusal := subject.Refusal(capability, serviceID)

					if allowed != (refusal == authz.RefusalNone) {
						t.Errorf("%s %+v %s on %q: Can = %t, Refusal = %q", role, subject, capability, serviceID, allowed, refusal)
					}
				}
			}
		}
	}
}
