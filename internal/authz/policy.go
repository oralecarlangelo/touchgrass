package authz

import (
	"slices"

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

// developerOperations is what a developer holds on top of a viewer's
// reads: running what an admin configured for a service, and tending
// its alerts. Every entry is service-scoped, so a developer never gains
// a fleet-level mutation whatever their scope.
//
// EnvStage is absent on purpose. Staging has rules of its own (a
// non-admin must not touch a secret key) that are not built yet, so the
// role is denied it rather than handed a right nobody has reviewed.
var developerOperations = []Capability{
	DeployTrigger,
	DeployRecord,
	AlertsManage,
	NotificationsAck,
}

// grants is the one role-to-capability table. Enforcement, the session
// payload the UI reads, and the documented matrix all come from it. A
// role with no entry holds nothing.
var grants = map[model.Role]map[Capability]bool{
	model.RoleViewer:    grantSet(viewerGrants),
	model.RoleDeveloper: grantSet(slices.Concat(viewerGrants, developerOperations)),
	model.RoleAdmin:     grantSet(Capabilities()),
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
