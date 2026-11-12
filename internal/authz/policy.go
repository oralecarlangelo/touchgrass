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
// reads: running what an admin configured for a service, tending its
// alerts, and proposing env changes. Every entry is service-scoped, so
// a developer never gains a fleet-level mutation whatever their scope.
//
// EnvStage is a narrower right for a developer than for an admin. The
// capability only opens the stage routes; a caller without EnvApply is
// a proposer there and may not touch a secret key or another user's
// pending change (service.Env.Stage). Applying stays with EnvApply.
var developerOperations = []Capability{
	DeployTrigger,
	DeployRecord,
	AlertsManage,
	NotificationsAck,
	EnvStage,
}

// grants is the one role-to-capability table. Enforcement, the session
// payload the UI reads, and the documented matrix all come from it. A
// role with no entry holds nothing.
var grants = map[model.Role]map[Capability]bool{
	model.RoleViewer:    grantSet(viewerGrants),
	model.RoleDeveloper: grantSet(slices.Concat(viewerGrants, developerOperations)),
	model.RoleAdmin:     grantSet(Capabilities()),
}

// limits are the grants that are narrower than the capability's name
// says, with the sentence the access overview shows beside them. A
// limit is enforced where the capability is used, not here; this is
// only its description, kept next to the grant so the two are reviewed
// together.
var limits = map[model.Role]map[Capability]string{
	model.RoleDeveloper: {
		EnvStage: "Proposes only: may add a key and change or remove a non-secret key. " +
			"May not touch a secret key or another user's pending change. An admin applies.",
		DeployRecord: "Always recorded under their own name.",
	},
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
