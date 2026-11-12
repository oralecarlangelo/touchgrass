// Package authz is the touchgrass authorization policy: which role may
// do what, and on which services. It is pure (no HTTP, no SQL), so the
// whole policy is one table that one test checks exhaustively. The HTTP
// layer asks it; it never decides anything itself.
package authz

// Capability names one thing a user may do.
type Capability string

// Scope classes say where a capability applies.
type Scope int

// Supported scope classes.
const (
	// ScopeUnknown marks a capability that is not in the policy.
	ScopeUnknown Scope = iota
	// ScopeSelf applies to the caller's own account.
	ScopeSelf
	// ScopeService needs the service to be in the user's scope.
	ScopeService
	// ScopeFleet needs the user's scope to be every service.
	ScopeFleet
)

// String names the scope class as the docs and the matrix spell it.
func (s Scope) String() string {
	switch s {
	case ScopeSelf:
		return "self"
	case ScopeService:
		return "service"
	case ScopeFleet:
		return "fleet"
	case ScopeUnknown:
		return "unknown"
	default:
		return "unknown"
	}
}

// Capabilities, grouped as the access matrix lists them.
const (
	// SessionSelf covers the session probe, logout, the live event
	// stream, and changing one's own password.
	SessionSelf Capability = "session.self"

	// ServiceRead covers a service's list entry, health, and containers.
	ServiceRead Capability = "service.read"
	// MetricsRead covers service metrics and per-service performance.
	MetricsRead Capability = "metrics.read"

	// DeployRead covers deploy history.
	DeployRead Capability = "deploy.read"
	// DeployTrigger covers deploy, cutover, and rollback.
	DeployTrigger Capability = "deploy.trigger"
	// DeployRecord covers recording a manual deploy.
	DeployRecord Capability = "deploy.record"

	// LogsRead covers log search, stats, line context, and issue logs.
	LogsRead Capability = "logs.read"
	// IssuesRead covers issues, occurrences, and issue detail.
	IssuesRead Capability = "issues.read"

	// AlertsRead covers alert rules, issue rules, and notifications.
	AlertsRead Capability = "alerts.read"
	// AlertsManage covers creating and deleting alert and issue rules.
	AlertsManage Capability = "alerts.manage"
	// NotificationsAck covers marking notifications read.
	NotificationsAck Capability = "notifications.ack"

	// EnvReadKeys covers the env view: key names, flags, state,
	// non-secret values, diff, revisions, and runs.
	EnvReadKeys Capability = "env.read_keys"
	// EnvStage covers staging or removing an entry.
	EnvStage Capability = "env.stage"
	// EnvDiscard covers dropping all pending changes.
	EnvDiscard Capability = "env.discard"
	// EnvImport covers importing the file from the host.
	EnvImport Capability = "env.import"
	// EnvApply covers applying, and rolling back to a revision.
	EnvApply Capability = "env.apply"
	// EnvReveal covers decrypting one secret value.
	EnvReveal Capability = "env.reveal"

	// KeysRead covers listing SDK keys (prefix only).
	KeysRead Capability = "keys.read"
	// KeysManage covers minting and revoking SDK keys.
	KeysManage Capability = "keys.manage"

	// AuditRead covers the audit rows of a service.
	AuditRead Capability = "audit.read"
	// AuditReadAll covers rows that belong to no service: logins, user
	// management, and host actions.
	AuditReadAll Capability = "audit.read_all"

	// HostRead covers the system snapshot, host history, container
	// inventory, images, collector status, and global performance.
	HostRead Capability = "host.read"
	// HostManage covers image prune and container approve and ignore.
	HostManage Capability = "host.manage"

	// SecurityRead covers the security summary, findings, and policies.
	SecurityRead Capability = "security.read"
	// SecurityManage covers finding transitions and policy changes.
	SecurityManage Capability = "security.manage"

	// DatabasesRead covers database health, the backup list, and jobs.
	DatabasesRead Capability = "databases.read"
	// DatabasesBackup covers starting and verifying a backup.
	DatabasesBackup Capability = "databases.backup"
	// DatabasesRestore covers a restore.
	DatabasesRestore Capability = "databases.restore"

	// ServicesManage covers creating and deleting a service, and the
	// onboarding suggestion that probes a container.
	ServicesManage Capability = "services.manage"
	// UsersManage covers all user and access management, and reading
	// the access matrix.
	UsersManage Capability = "users.manage"
)

// scopes is the scope class of every capability, in matrix order, with
// the one-line summary the access overview shows an admin.
var scopes = []struct {
	capability Capability
	scope      Scope
	summary    string
}{
	{capability: SessionSelf, scope: ScopeSelf, summary: "Sign out, read the own session, follow the live event stream, change the own password"},
	{capability: ServiceRead, scope: ScopeService, summary: "See a service in the list, with its health and containers"},
	{capability: MetricsRead, scope: ScopeService, summary: "Read a service's metrics and performance"},
	{capability: DeployRead, scope: ScopeService, summary: "Read deploy history"},
	{capability: DeployTrigger, scope: ScopeService, summary: "Deploy, cut over and roll back"},
	{capability: DeployRecord, scope: ScopeService, summary: "Record a manual deploy"},
	{capability: LogsRead, scope: ScopeService, summary: "Search logs, read log statistics and the lines around one"},
	{capability: IssuesRead, scope: ScopeService, summary: "Read issues and their occurrences"},
	{capability: AlertsRead, scope: ScopeService, summary: "Read alert rules, issue rules and notifications"},
	{capability: AlertsManage, scope: ScopeService, summary: "Create and delete alert rules and issue rules"},
	{capability: NotificationsAck, scope: ScopeService, summary: "Mark notifications read"},
	{capability: EnvReadKeys, scope: ScopeService, summary: "Read env key names, flags, non-secret values, the diff, revisions and runs"},
	{capability: EnvStage, scope: ScopeService, summary: "Stage or remove an env entry"},
	{capability: EnvDiscard, scope: ScopeService, summary: "Drop all pending env changes"},
	{capability: EnvImport, scope: ScopeService, summary: "Import the env file from the host"},
	{capability: EnvApply, scope: ScopeService, summary: "Apply pending env changes, and roll back to a revision"},
	{capability: EnvReveal, scope: ScopeService, summary: "Decrypt one secret env value"},
	{capability: KeysRead, scope: ScopeService, summary: "List SDK keys (prefix only)"},
	{capability: KeysManage, scope: ScopeService, summary: "Mint and revoke SDK keys"},
	{capability: AuditRead, scope: ScopeService, summary: "Read the audit rows of a service"},
	{capability: AuditReadAll, scope: ScopeFleet, summary: "Read audit rows that belong to no service: logins, user management, host actions"},
	{capability: HostRead, scope: ScopeFleet, summary: "Read the host: system snapshot and history, containers, images, collectors, fleet-wide performance"},
	{capability: HostManage, scope: ScopeFleet, summary: "Prune images; approve and ignore containers"},
	{capability: SecurityRead, scope: ScopeFleet, summary: "Read the security summary, findings and policies"},
	{capability: SecurityManage, scope: ScopeFleet, summary: "Acknowledge, resolve and suppress findings; change security policies"},
	{capability: DatabasesRead, scope: ScopeFleet, summary: "Read database health, the backup list and jobs"},
	{capability: DatabasesBackup, scope: ScopeFleet, summary: "Start and verify a backup"},
	{capability: DatabasesRestore, scope: ScopeFleet, summary: "Restore a database from a backup"},
	{capability: ServicesManage, scope: ScopeFleet, summary: "Create and delete services; ask for an onboarding suggestion"},
	{capability: UsersManage, scope: ScopeFleet, summary: "Manage users and their access; read the access matrix"},
}

// Capabilities returns every capability in matrix order.
func Capabilities() []Capability {
	all := make([]Capability, 0, len(scopes))

	for _, entry := range scopes {
		all = append(all, entry.capability)
	}

	return all
}

// ScopeOf returns a capability's scope class, or ScopeUnknown for a
// name that is not in the policy.
func ScopeOf(capability Capability) Scope {
	for _, entry := range scopes {
		if entry.capability == capability {
			return entry.scope
		}
	}

	return ScopeUnknown
}
