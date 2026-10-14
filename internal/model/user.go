package model

import (
	"time"
)

// Role is a user's privilege level. Admins hold full power; viewers
// are read-only.
type Role string

// Supported user roles.
const (
	RoleUnknown Role = "unknown"
	RoleAdmin   Role = "admin"
	RoleViewer  Role = "viewer"
)

// User is one login account. PasswordHash never leaves the store layer.
type User struct {
	ID           int64
	Username     string
	PasswordHash string
	Role         Role
	DisabledAt   *time.Time
	CreatedAt    time.Time
}

// Disabled reports whether the account is locked out.
func (u User) Disabled() bool {
	return u.DisabledAt != nil
}
