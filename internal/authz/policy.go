package authz

import (
	"github.com/oralecarlangelo/touchgrass/internal/model"
)

// viewerGrants is what a read-only account holds.
var viewerGrants = []Capability{
	SessionSelf,
	ServiceRead,
	MetricsRead,
	DeployRead,
	LogsRead,
	IssuesRead,
	AlertsRead,
	EnvReadKeys,
	KeysRead,
	AuditRead,
	AuditReadAll,
	HostRead,
	SecurityRead,
	DatabasesRead,
}

// grants is the one role-to-capability table. Enforcement, the session
// payload the UI reads, and the documented matrix all come from it.
//
// A role with no entry holds nothing. model.RoleDeveloper is absent on
// purpose: the schema already admits the value, but its rules (deploys
// attributed to the caller, env staging limited to non-secret keys) are
// not built yet, so an account carrying it is denied everything rather
// than handed rights nobody has reviewed.
var grants = map[model.Role]map[Capability]bool{
	model.RoleViewer: grantSet(viewerGrants),
	model.RoleAdmin:  grantSet(Capabilities()),
}

// grantSet indexes a capability list.
func grantSet(capabilities []Capability) map[Capability]bool {
	set := make(map[Capability]bool, len(capabilities))

	for _, capability := range capabilities {
		set[capability] = true
	}

	return set
}

// Assignable reports whether an account may be given role today.
func Assignable(role model.Role) bool {
	_, ok := grants[role]

	return ok
}

// RoleHolds reports whether role includes capability, before any scope
// is considered.
func RoleHolds(role model.Role, capability Capability) bool {
	return grants[role][capability]
}
