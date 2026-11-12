package authz

import (
	"slices"

	"github.com/oralecarlangelo/touchgrass/internal/model"
)

// Subject is the caller a decision is made about: a role, which decides
// what they may do, and a scope, which decides on which services. The
// zero Subject holds nothing.
type Subject struct {
	Username string
	Role     model.Role
	// AllServices is the fleet-wide scope: every service, including
	// ones created later, plus the host-level screens.
	AllServices bool
	// Services lists the service ids in scope when AllServices is false.
	Services []string
}

// Holds reports whether the subject's role includes capability on at
// least one service. It answers "may they ever", not "may they here".
func (s Subject) Holds(capability Capability) bool {
	if !RoleHolds(s.Role, capability) {
		return false
	}

	switch ScopeOf(capability) {
	case ScopeSelf:
		return true
	case ScopeFleet:
		return s.AllServices
	case ScopeService:
		return s.AllServices || len(s.Services) > 0
	case ScopeUnknown:
		return false
	default:
		return false
	}
}

// Can reports whether the subject may use capability on serviceID. A
// fleet capability ignores serviceID and needs the all-services scope;
// a service capability needs serviceID to be in scope.
func (s Subject) Can(capability Capability, serviceID string) bool {
	if !RoleHolds(s.Role, capability) {
		return false
	}

	switch ScopeOf(capability) {
	case ScopeSelf:
		return true
	case ScopeFleet:
		return s.AllServices
	case ScopeService:
		return s.inScope(serviceID)
	case ScopeUnknown:
		return false
	default:
		return false
	}
}

// CanFleet reports whether the subject may use capability across every
// service at once: the role holds it and the scope is all services.
func (s Subject) CanFleet(capability Capability) bool {
	if ScopeOf(capability) == ScopeUnknown {
		return false
	}

	return s.AllServices && RoleHolds(s.Role, capability)
}

// VisibleServices reports the services list queries may return: all of
// them, or exactly ids. A subject whose role cannot read services sees
// none.
func (s Subject) VisibleServices() (all bool, ids []string) {
	if !RoleHolds(s.Role, ServiceRead) {
		return false, []string{}
	}

	if s.AllServices {
		return true, []string{}
	}

	return false, slices.Clone(s.Services)
}

// Capabilities lists what the subject holds, in matrix order, for the
// session payload. Fleet capabilities are absent for a scoped subject,
// so a client only needs to pair the names with the service list.
func (s Subject) Capabilities() []Capability {
	held := []Capability{}

	for _, capability := range Capabilities() {
		if s.Holds(capability) {
			held = append(held, capability)
		}
	}

	return held
}

// inScope reports whether one service is in the subject's scope.
func (s Subject) inScope(serviceID string) bool {
	if serviceID == "" {
		return false
	}

	return s.AllServices || slices.Contains(s.Services, serviceID)
}

// Refusal names why a subject may not use a capability, for the denial
// log and the troubleshooting page. It never reaches the caller: a
// refused request reads the same whichever reason applied.
type Refusal string

// Refusals, from the first check that fails.
const (
	// RefusalNone means the subject is allowed.
	RefusalNone Refusal = ""
	// RefusalRole means the role does not include the capability on any
	// service. An unknown capability or role is refused for this reason.
	RefusalRole Refusal = "role"
	// RefusalAllServices means the role includes the capability, but it
	// is a fleet capability and the account is limited to a list of
	// services.
	RefusalAllServices Refusal = "all_services_required"
	// RefusalService means the role includes the capability, but the
	// service asked about is not in the account's scope (or no service
	// was named, or it does not exist).
	RefusalService Refusal = "service_out_of_scope"
)

// Refusal reports why Can(capability, serviceID) is false, or
// RefusalNone when it is true.
func (s Subject) Refusal(capability Capability, serviceID string) Refusal {
	if s.Can(capability, serviceID) {
		return RefusalNone
	}

	if !RoleHolds(s.Role, capability) {
		return RefusalRole
	}

	if ScopeOf(capability) == ScopeFleet {
		return RefusalAllServices
	}

	return RefusalService
}
