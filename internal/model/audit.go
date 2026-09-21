package model

import (
	"time"
)

// Audit actions recorded in the append-only log.
const (
	AuditCutover  = "cutover"
	AuditRollback = "rollback"
	AuditLogin    = "login"
	AuditDeploy   = "deploy"
)

// Audit results recorded in the append-only log.
const (
	AuditSuccess = "success"
	AuditFailure = "failure"
)

// Audit is one immutable action entry.
type Audit struct {
	ID        int64     `json:"id"`
	ServiceID *string   `json:"service_id"`
	Actor     string    `json:"actor"`
	Action    string    `json:"action"`
	Result    string    `json:"result"`
	Detail    string    `json:"detail"`
	CreatedAt time.Time `json:"created_at"`
}

// AuditRecord carries new-entry input. An empty ServiceID records a
// global action such as login.
type AuditRecord struct {
	ServiceID string
	Actor     string
	Action    string
	Result    string
	Detail    string
}
