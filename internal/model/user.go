package model

import (
	"time"
)

// Role is a user's privilege level. Admins hold full power; viewers
// are read-only. What each role may do lives in internal/authz.
type Role string

// Supported user roles.
const (
	RoleUnknown Role = "unknown"
	RoleAdmin   Role = "admin"
	RoleViewer  Role = "viewer"
	// RoleDeveloper reads like a viewer and may also deploy and tend
	// the alerts of the services in its scope.
	RoleDeveloper Role = "developer"
)

// User is one login account. PasswordHash never leaves the store layer.
type User struct {
	ID           int64
	Username     string
	PasswordHash string
	Role         Role
	// MustChangePassword marks a one-time password: the account may only
	// change its password until it has.
	MustChangePassword bool
	// AllServices is the fleet-wide scope: every service, present and
	// future, plus the host-level screens. An admin always has it.
	AllServices bool
	// Services lists the granted service ids, sorted, when AllServices
	// is false. It is empty otherwise.
	Services   []string
	DisabledAt *time.Time
	CreatedAt  time.Time
}

// Disabled reports whether the account is locked out.
func (u User) Disabled() bool {
	return u.DisabledAt != nil
}

// UserAccess is a user's role and scope, set together.
type UserAccess struct {
	Role Role
	// AllServices selects every service; Services must then be empty.
	AllServices bool
	// Services lists the granted service ids when AllServices is false.
	Services []string
}
