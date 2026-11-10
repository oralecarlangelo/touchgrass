package authz_test

import (
	"bufio"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/oralecarlangelo/touchgrass/internal/authz"
	"github.com/oralecarlangelo/touchgrass/internal/model"
)

// Fixture service ids. They are generic on purpose: the policy must not
// know any deployment's names.
const (
	testGranted = "billing-worker"
	testOther   = "docs-site"
)

// matrixRow is one line of the checked-in access matrix.
type matrixRow struct {
	capability authz.Capability
	scope      string
	holds      map[model.Role]bool
}

// matrixRoles are the role columns of the matrix, in file order.
var matrixRoles = []model.Role{model.RoleViewer, model.RoleDeveloper, model.RoleAdmin}

// loadMatrix parses testdata/matrix.tsv.
func loadMatrix(t *testing.T) []matrixRow {
	t.Helper()

	file, err := os.Open("testdata/matrix.tsv")
	if err != nil {
		t.Fatalf("opening matrix: %v", err)
	}

	defer func() {
		if err := file.Close(); err != nil {
			t.Errorf("closing matrix: %v", err)
		}
	}()

	rows := []matrixRow{}
	scanner := bufio.NewScanner(file)

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		fields := strings.Split(line, "\t")
		if len(fields) != 2+len(matrixRoles) {
			t.Fatalf("matrix line %q has %d fields, want %d", line, len(fields), 2+len(matrixRoles))
		}

		row := matrixRow{capability: authz.Capability(fields[0]), scope: fields[1], holds: map[model.Role]bool{}}

		for index, role := range matrixRoles {
			switch fields[2+index] {
			case "yes":
				row.holds[role] = true
			case "no":
				row.holds[role] = false
			default:
				t.Fatalf("matrix line %q: cell %q, want yes or no", line, fields[2+index])
			}
		}

		rows = append(rows, row)
	}

	if err := scanner.Err(); err != nil {
		t.Fatalf("reading matrix: %v", err)
	}

	return rows
}

func TestMatrixListsEveryCapability(t *testing.T) {
	t.Parallel()

	rows := loadMatrix(t)
	listed := []authz.Capability{}

	for _, row := range rows {
		listed = append(listed, row.capability)

		if got := authz.ScopeOf(row.capability).String(); got != row.scope {
			t.Errorf("ScopeOf(%s) = %s, want %s", row.capability, got, row.scope)
		}
	}

	if !slices.Equal(listed, authz.Capabilities()) {
		t.Errorf("matrix capabilities = %v, want %v (same order as the policy)", listed, authz.Capabilities())
	}
}

func TestRoleHoldsMatchesMatrix(t *testing.T) {
	t.Parallel()

	for _, row := range loadMatrix(t) {
		for _, role := range matrixRoles {
			if got := authz.RoleHolds(role, row.capability); got != row.holds[role] {
				t.Errorf("RoleHolds(%s, %s) = %t, want %t", role, row.capability, got, row.holds[role])
			}
		}
	}
}

// subjectShape is one scope a role can be combined with.
type subjectShape struct {
	name        string
	allServices bool
	services    []string
}

// subjectShapes covers every scope a subject can have.
var subjectShapes = []subjectShape{
	{name: "all services", allServices: true, services: []string{}},
	{name: "granted one service", allServices: false, services: []string{testGranted}},
	{name: "granted nothing", allServices: false, services: []string{}},
}

// wantCan is the rule the matrix documents: the role must hold the
// capability, a fleet capability also needs the all-services scope, and
// a service capability needs the service to be in scope.
func wantCan(row matrixRow, role model.Role, shape subjectShape, serviceID string) bool {
	if !row.holds[role] {
		return false
	}

	switch row.scope {
	case "self":
		return true
	case "fleet":
		return shape.allServices
	default:
		return serviceID != "" && (shape.allServices || slices.Contains(shape.services, serviceID))
	}
}

func TestSubjectCanMatchesMatrix(t *testing.T) {
	t.Parallel()

	roles := append(slices.Clone(matrixRoles), model.RoleUnknown, model.Role(""), model.Role("owner"))
	serviceIDs := []string{testGranted, testOther, ""}

	for _, row := range loadMatrix(t) {
		for _, role := range roles {
			for _, shape := range subjectShapes {
				subject := authz.Subject{
					Username: "someone", Role: role, AllServices: shape.allServices, Services: shape.services,
				}

				for _, serviceID := range serviceIDs {
					want := wantCan(row, role, shape, serviceID)

					if got := subject.Can(row.capability, serviceID); got != want {
						t.Errorf("%s/%s Can(%s, %q) = %t, want %t",
							role, shape.name, row.capability, serviceID, got, want)
					}
				}

				wantFleet := row.holds[role] && shape.allServices

				if got := subject.CanFleet(row.capability); got != wantFleet {
					t.Errorf("%s/%s CanFleet(%s) = %t, want %t", role, shape.name, row.capability, got, wantFleet)
				}

				wantHolds := wantCan(row, role, shape, testGranted)

				if got := subject.Holds(row.capability); got != wantHolds {
					t.Errorf("%s/%s Holds(%s) = %t, want %t", role, shape.name, row.capability, got, wantHolds)
				}
			}
		}
	}
}

func TestUnknownCapabilityIsDenied(t *testing.T) {
	t.Parallel()

	admin := authz.Subject{Username: "root", Role: model.RoleAdmin, AllServices: true, Services: []string{}}
	unknown := authz.Capability("services.destroy_everything")

	if admin.Can(unknown, testGranted) || admin.CanFleet(unknown) || admin.Holds(unknown) {
		t.Error("an admin holds a capability that is not in the policy, want denied")
	}

	if got := authz.ScopeOf(unknown); got != authz.ScopeUnknown {
		t.Errorf("ScopeOf(unknown) = %v, want ScopeUnknown", got)
	}
}

func TestZeroSubjectHoldsNothing(t *testing.T) {
	t.Parallel()

	var subject authz.Subject

	for _, capability := range authz.Capabilities() {
		if subject.Can(capability, testGranted) || subject.CanFleet(capability) || subject.Holds(capability) {
			t.Errorf("zero Subject holds %s, want nothing", capability)
		}
	}

	if all, ids := subject.VisibleServices(); all || len(ids) != 0 {
		t.Errorf("zero Subject VisibleServices() = %t, %v, want false and none", all, ids)
	}
}

func TestAssignable(t *testing.T) {
	t.Parallel()

	tests := []struct {
		role model.Role
		want bool
	}{
		{role: model.RoleAdmin, want: true},
		{role: model.RoleViewer, want: true},
		{role: model.RoleDeveloper, want: false},
		{role: model.RoleUnknown, want: false},
		{role: model.Role(""), want: false},
	}

	for _, tt := range tests {
		if got := authz.Assignable(tt.role); got != tt.want {
			t.Errorf("Assignable(%q) = %t, want %t", tt.role, got, tt.want)
		}
	}
}

func TestVisibleServices(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		subject authz.Subject
		wantAll bool
		wantIDs []string
	}{
		{
			name:    "all services viewer",
			subject: authz.Subject{Role: model.RoleViewer, AllServices: true, Services: []string{}},
			wantAll: true, wantIDs: []string{},
		},
		{
			name:    "scoped viewer",
			subject: authz.Subject{Role: model.RoleViewer, AllServices: false, Services: []string{testGranted}},
			wantAll: false, wantIDs: []string{testGranted},
		},
		{
			name:    "viewer with no services",
			subject: authz.Subject{Role: model.RoleViewer, AllServices: false, Services: []string{}},
			wantAll: false, wantIDs: []string{},
		},
		{
			name:    "reserved role sees nothing even with a scope",
			subject: authz.Subject{Role: model.RoleDeveloper, AllServices: true, Services: []string{testGranted}},
			wantAll: false, wantIDs: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			all, ids := tt.subject.VisibleServices()
			if all != tt.wantAll || !slices.Equal(ids, tt.wantIDs) {
				t.Errorf("VisibleServices() = %t, %v, want %t, %v", all, ids, tt.wantAll, tt.wantIDs)
			}
		})
	}
}

func TestCapabilitiesOmitsFleetForScopedSubject(t *testing.T) {
	t.Parallel()

	scoped := authz.Subject{Role: model.RoleViewer, AllServices: false, Services: []string{testGranted}}

	for _, capability := range scoped.Capabilities() {
		if authz.ScopeOf(capability) == authz.ScopeFleet {
			t.Errorf("scoped viewer Capabilities() includes fleet capability %s", capability)
		}
	}

	if !slices.Contains(scoped.Capabilities(), authz.LogsRead) {
		t.Error("scoped viewer Capabilities() lacks logs.read, want it")
	}

	everywhere := authz.Subject{Role: model.RoleViewer, AllServices: true, Services: []string{}}
	if !slices.Contains(everywhere.Capabilities(), authz.HostRead) {
		t.Error("all-services viewer Capabilities() lacks host.read, want it")
	}
}
