package model

import (
	"time"
)

// Audit actions recorded in the append-only log.
const (
	AuditCutover              = "cutover"
	AuditRollback             = "rollback"
	AuditLogin                = "login"
	AuditDeploy               = "deploy"
	AuditServiceCreate        = "service_create"
	AuditServiceDelete        = "service_delete"
	AuditUserCreate           = "user_create"
	AuditUserUpdate           = "user_update"
	AuditUserDelete           = "user_delete"
	AuditContainerApprove     = "container_approve"
	AuditContainerIgnore      = "container_ignore"
	AuditSecurityAcknowledge  = "security_acknowledge"
	AuditSecurityResolve      = "security_resolve"
	AuditSecuritySuppress     = "security_suppress"
	AuditSecurityPolicyCreate = "security_policy_create"
	AuditSecurityPolicyUpdate = "security_policy_update"
	AuditSecurityPolicyDelete = "security_policy_delete"
	AuditEnvStage             = "env.stage"
	AuditEnvDiscard           = "env.discard"
	AuditEnvImport            = "env.import"
	AuditEnvReveal            = "env.reveal"
	AuditEnvApply             = "env.apply"
	AuditEnvApplyFailed       = "env.apply_failed"
	AuditEnvRollback          = "env.rollback"
	AuditDeployRecord         = "deploy_record"
	AuditAlertRuleCreate      = "alert_rule_create"
	AuditAlertRuleDelete      = "alert_rule_delete"
	AuditIssueRuleCreate      = "issue_rule_create"
	AuditIssueRuleDelete      = "issue_rule_delete"
	AuditNotificationRead     = "notification_read"
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
