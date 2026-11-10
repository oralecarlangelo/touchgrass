package http

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strconv"

	nethttp "net/http"

	"github.com/oralecarlangelo/touchgrass/internal/authz"
	"github.com/oralecarlangelo/touchgrass/internal/model"
	"github.com/oralecarlangelo/touchgrass/internal/service"
)

// guardKind says how a route finds what it has to authorize.
type guardKind int

// Guard kinds. Every /api/ route declares exactly one.
const (
	// guardUnknown is the zero value; a route carrying it is refused.
	guardUnknown guardKind = iota
	// guardPublic needs no session: health, login, and ingest, which
	// authenticates with a per-service key instead.
	guardPublic
	// guardSession needs any signed-in user acting on their own account.
	guardSession
	// guardFleet needs a capability over every service.
	guardFleet
	// guardServicePath takes the service from the {id} path value.
	guardServicePath
	// guardServiceQuery takes the service from ?service_id=.
	guardServiceQuery
	// guardServiceBody takes the service from "service_id" in the JSON
	// body.
	guardServiceBody
	// guardResource looks up the service that owns the row named by
	// the {id} path value.
	guardResource
)

const (
	// serviceIDParam is the query parameter and body field that names
	// a service.
	serviceIDParam = "service_id"
	// maxGuardBodyBytes caps the body a guard reads to find the service.
	maxGuardBodyBytes = 1 << 20
	// codeForbidden and codeNotFound are the envelope codes guards
	// answer with.
	codeForbidden = "forbidden"
	codeNotFound  = "not_found"
	// errNotFoundMessage is the one answer for a resource id that is
	// missing and for one that belongs to a service out of scope, so an
	// id cannot be probed across services.
	errNotFoundMessage = "not found"
)

// route declares one API route: what it is, what it takes to call it,
// and how the service it concerns is found. Routes are data so that
// one test can prove none is unguarded.
type route struct {
	method     string
	pattern    string
	kind       guardKind
	capability authz.Capability
	// listScoped marks a guardServiceQuery route that also serves a
	// list across services when service_id is absent. Its handler must
	// bound that list with listScope; without the flag a missing
	// service_id is refused before the handler runs.
	listScoped bool
	// resource is the row kind a guardResource route is keyed by.
	resource model.ResourceKind
	handler  nethttp.HandlerFunc
}

// routes is the whole API surface. Order follows the product areas;
// the mux does not depend on it.
func (s *Server) routes() []route {
	return []route{
		// Liveness and session.
		{method: nethttp.MethodGet, pattern: "/api/health", kind: guardPublic, handler: s.handleHealth},
		{method: nethttp.MethodPost, pattern: "/api/auth/login", kind: guardPublic, handler: s.handleLogin},
		{method: nethttp.MethodPost, pattern: "/api/auth/logout", kind: guardSession, capability: authz.SessionSelf, handler: s.handleLogout},
		{method: nethttp.MethodGet, pattern: "/api/auth/me", kind: guardSession, capability: authz.SessionSelf, handler: s.handleMe},
		{method: nethttp.MethodPost, pattern: "/api/auth/password", kind: guardSession, capability: authz.SessionSelf, handler: s.handleChangePassword},
		// The stream is open to any session; the hub filters each event
		// by the subscriber's role and scope.
		{method: nethttp.MethodGet, pattern: "/api/events", kind: guardSession, capability: authz.SessionSelf, handler: s.handleEvents},

		// Users and access.
		{method: nethttp.MethodGet, pattern: "/api/users", kind: guardFleet, capability: authz.UsersManage, handler: s.handleUsers},
		{method: nethttp.MethodPost, pattern: "/api/users", kind: guardFleet, capability: authz.UsersManage, handler: s.handleCreateUser},
		{method: nethttp.MethodPost, pattern: "/api/users/{id}/password", kind: guardFleet, capability: authz.UsersManage, handler: s.handleUserPassword},
		{method: nethttp.MethodPost, pattern: "/api/users/{id}/role", kind: guardFleet, capability: authz.UsersManage, handler: s.handleUserRole},
		{method: nethttp.MethodPut, pattern: "/api/users/{id}/access", kind: guardFleet, capability: authz.UsersManage, handler: s.handleUserAccess},
		{method: nethttp.MethodPost, pattern: "/api/users/{id}/disable", kind: guardFleet, capability: authz.UsersManage, handler: s.handleUserDisable},
		{method: nethttp.MethodPost, pattern: "/api/users/{id}/enable", kind: guardFleet, capability: authz.UsersManage, handler: s.handleUserEnable},
		{method: nethttp.MethodDelete, pattern: "/api/users/{id}", kind: guardFleet, capability: authz.UsersManage, handler: s.handleDeleteUser},

		// Service inventory.
		{method: nethttp.MethodGet, pattern: "/api/services", kind: guardServiceQuery, capability: authz.ServiceRead, listScoped: true, handler: s.handleServices},
		{method: nethttp.MethodPost, pattern: "/api/services", kind: guardFleet, capability: authz.ServicesManage, handler: s.handleCreateService},
		{method: nethttp.MethodDelete, pattern: "/api/services/{id}", kind: guardFleet, capability: authz.ServicesManage, handler: s.handleDeleteService},
		{method: nethttp.MethodGet, pattern: "/api/onboarding/suggest", kind: guardFleet, capability: authz.ServicesManage, handler: s.handleOnboardingSuggest},

		// Metrics and performance of one service.
		{method: nethttp.MethodGet, pattern: "/api/services/{id}/metrics", kind: guardServicePath, capability: authz.MetricsRead, handler: s.handleMetrics},
		{method: nethttp.MethodGet, pattern: "/api/services/{id}/performance/summary", kind: guardServicePath, capability: authz.MetricsRead, handler: s.handlePerformanceSummary},
		{method: nethttp.MethodGet, pattern: "/api/services/{id}/performance/timeline", kind: guardServicePath, capability: authz.MetricsRead, handler: s.handlePerformanceTimeline},
		{method: nethttp.MethodGet, pattern: "/api/services/{id}/performance/routes", kind: guardServicePath, capability: authz.MetricsRead, handler: s.handlePerformanceRoutes},
		{method: nethttp.MethodGet, pattern: "/api/services/{id}/performance/postgres", kind: guardServicePath, capability: authz.MetricsRead, handler: s.handlePerformancePostgres},
		{method: nethttp.MethodGet, pattern: "/api/services/{id}/performance/redis", kind: guardServicePath, capability: authz.MetricsRead, handler: s.handlePerformanceRedis},

		// Deploys.
		{method: nethttp.MethodGet, pattern: "/api/services/{id}/deploys", kind: guardServicePath, capability: authz.DeployRead, handler: s.handleDeploys},
		{method: nethttp.MethodPost, pattern: "/api/services/{id}/deploys", kind: guardServicePath, capability: authz.DeployRecord, handler: s.handleRecordDeploy},
		{method: nethttp.MethodPost, pattern: "/api/services/{id}/cutover", kind: guardServicePath, capability: authz.DeployTrigger, handler: s.handleCutover},
		{method: nethttp.MethodPost, pattern: "/api/services/{id}/rollback", kind: guardServicePath, capability: authz.DeployTrigger, handler: s.handleRollback},
		{method: nethttp.MethodPost, pattern: "/api/services/{id}/deploy", kind: guardServicePath, capability: authz.DeployTrigger, handler: s.handleDeploy},

		// Env and secrets.
		{method: nethttp.MethodGet, pattern: "/api/services/{id}/env", kind: guardServicePath, capability: authz.EnvReadKeys, handler: s.handleEnv},
		{method: nethttp.MethodPut, pattern: "/api/services/{id}/env/entries/{key}", kind: guardServicePath, capability: authz.EnvStage, handler: s.handleEnvPut},
		{method: nethttp.MethodDelete, pattern: "/api/services/{id}/env/entries/{key}", kind: guardServicePath, capability: authz.EnvStage, handler: s.handleEnvDelete},
		{method: nethttp.MethodPost, pattern: "/api/services/{id}/env/entries/{key}/reveal", kind: guardServicePath, capability: authz.EnvReveal, handler: s.handleEnvReveal},
		{method: nethttp.MethodGet, pattern: "/api/services/{id}/env/diff", kind: guardServicePath, capability: authz.EnvReadKeys, handler: s.handleEnvDiff},
		{method: nethttp.MethodPost, pattern: "/api/services/{id}/env/discard", kind: guardServicePath, capability: authz.EnvDiscard, handler: s.handleEnvDiscard},
		{method: nethttp.MethodPost, pattern: "/api/services/{id}/env/import", kind: guardServicePath, capability: authz.EnvImport, handler: s.handleEnvImport},
		{method: nethttp.MethodPost, pattern: "/api/services/{id}/env/apply", kind: guardServicePath, capability: authz.EnvApply, handler: s.handleEnvApply},
		{method: nethttp.MethodGet, pattern: "/api/services/{id}/env/runs/{run_id}", kind: guardServicePath, capability: authz.EnvReadKeys, handler: s.handleEnvRun},
		{method: nethttp.MethodGet, pattern: "/api/services/{id}/env/revisions", kind: guardServicePath, capability: authz.EnvReadKeys, handler: s.handleEnvRevisions},
		{method: nethttp.MethodPost, pattern: "/api/services/{id}/env/revisions/{rev}/rollback", kind: guardServicePath, capability: authz.EnvApply, handler: s.handleEnvRollback},

		// Logs and issues.
		{method: nethttp.MethodGet, pattern: "/api/logs", kind: guardServiceQuery, capability: authz.LogsRead, handler: s.handleLogs},
		{method: nethttp.MethodGet, pattern: "/api/logs/stats", kind: guardServiceQuery, capability: authz.LogsRead, handler: s.handleLogStats},
		{method: nethttp.MethodGet, pattern: "/api/logs/{id}", kind: guardResource, capability: authz.LogsRead, resource: model.ResourceLogLine, handler: s.handleLogContext},
		{method: nethttp.MethodGet, pattern: "/api/issues", kind: guardServiceQuery, capability: authz.IssuesRead, handler: s.handleIssues},
		{method: nethttp.MethodGet, pattern: "/api/issues/occurrences", kind: guardServiceQuery, capability: authz.IssuesRead, handler: s.handleOccurrences},
		{method: nethttp.MethodGet, pattern: "/api/issues/{id}", kind: guardResource, capability: authz.IssuesRead, resource: model.ResourceIssue, handler: s.handleIssue},
		{method: nethttp.MethodGet, pattern: "/api/issues/{id}/logs", kind: guardResource, capability: authz.LogsRead, resource: model.ResourceIssue, handler: s.handleIssueLogs},

		// Alert rules, issue rules, and notifications.
		{method: nethttp.MethodGet, pattern: "/api/alerts/rules", kind: guardServiceQuery, capability: authz.AlertsRead, handler: s.handleRules},
		{method: nethttp.MethodPost, pattern: "/api/alerts/rules", kind: guardServiceBody, capability: authz.AlertsManage, handler: s.handleCreateRule},
		{method: nethttp.MethodDelete, pattern: "/api/alerts/rules/{id}", kind: guardResource, capability: authz.AlertsManage, resource: model.ResourceAlertRule, handler: s.handleDeleteRule},
		{method: nethttp.MethodGet, pattern: "/api/issues/rules", kind: guardServiceQuery, capability: authz.AlertsRead, handler: s.handleIssueRules},
		{method: nethttp.MethodPost, pattern: "/api/issues/rules", kind: guardServiceBody, capability: authz.AlertsManage, handler: s.handleCreateIssueRule},
		{method: nethttp.MethodDelete, pattern: "/api/issues/rules/{id}", kind: guardResource, capability: authz.AlertsManage, resource: model.ResourceIssueRule, handler: s.handleDeleteIssueRule},
		{method: nethttp.MethodGet, pattern: "/api/notifications", kind: guardServiceQuery, capability: authz.AlertsRead, listScoped: true, handler: s.handleNotifications},
		{method: nethttp.MethodPost, pattern: "/api/notifications/{id}/read", kind: guardResource, capability: authz.NotificationsAck, resource: model.ResourceNotification, handler: s.handleMarkNotificationRead},
		{method: nethttp.MethodPost, pattern: "/api/notifications/read", kind: guardServiceQuery, capability: authz.NotificationsAck, listScoped: true, handler: s.handleMarkNotificationsRead},

		// SDK keys and ingest. Ingest is public: the key is the credential.
		{method: nethttp.MethodPost, pattern: "/api/ingest", kind: guardPublic, handler: s.handleIngest},
		{method: nethttp.MethodPost, pattern: "/api/ingest/logs", kind: guardPublic, handler: s.handleIngestLogs},
		{method: nethttp.MethodGet, pattern: "/api/keys", kind: guardServiceQuery, capability: authz.KeysRead, handler: s.handleKeys},
		{method: nethttp.MethodPost, pattern: "/api/keys", kind: guardServiceBody, capability: authz.KeysManage, handler: s.handleCreateKey},
		{method: nethttp.MethodPost, pattern: "/api/keys/{id}/revoke", kind: guardResource, capability: authz.KeysManage, resource: model.ResourceAPIKey, handler: s.handleRevokeKey},

		// Audit.
		{method: nethttp.MethodGet, pattern: "/api/audit", kind: guardServiceQuery, capability: authz.AuditRead, listScoped: true, handler: s.handleAudit},

		// Host: system, containers, images, and fleet-wide performance.
		{method: nethttp.MethodGet, pattern: "/api/system", kind: guardFleet, capability: authz.HostRead, handler: s.handleSystem},
		{method: nethttp.MethodGet, pattern: "/api/system/history", kind: guardFleet, capability: authz.HostRead, handler: s.handleSystemHistory},
		{method: nethttp.MethodGet, pattern: "/api/system/containers", kind: guardFleet, capability: authz.HostRead, handler: s.handleContainers},
		{method: nethttp.MethodGet, pattern: "/api/system/containers/{id}", kind: guardFleet, capability: authz.HostRead, handler: s.handleContainer},
		{method: nethttp.MethodPost, pattern: "/api/system/containers/{id}/approve", kind: guardFleet, capability: authz.HostManage, handler: s.handleApproveContainer},
		{method: nethttp.MethodPost, pattern: "/api/system/containers/{id}/ignore", kind: guardFleet, capability: authz.HostManage, handler: s.handleIgnoreContainer},
		{method: nethttp.MethodGet, pattern: "/api/docker/images", kind: guardFleet, capability: authz.HostRead, handler: s.handleDockerImages},
		{method: nethttp.MethodPost, pattern: "/api/docker/images/prune", kind: guardFleet, capability: authz.HostManage, handler: s.handlePruneImages},
		{method: nethttp.MethodGet, pattern: "/api/performance/summary", kind: guardFleet, capability: authz.HostRead, handler: s.handlePerformanceSummary},
		{method: nethttp.MethodGet, pattern: "/api/performance/timeline", kind: guardFleet, capability: authz.HostRead, handler: s.handlePerformanceTimeline},
		{method: nethttp.MethodGet, pattern: "/api/performance/routes", kind: guardFleet, capability: authz.HostRead, handler: s.handlePerformanceRoutes},
		{method: nethttp.MethodGet, pattern: "/api/performance/postgres", kind: guardFleet, capability: authz.HostRead, handler: s.handlePerformancePostgres},
		{method: nethttp.MethodGet, pattern: "/api/performance/redis", kind: guardFleet, capability: authz.HostRead, handler: s.handlePerformanceRedis},
		{method: nethttp.MethodGet, pattern: "/api/collectors/status", kind: guardFleet, capability: authz.HostRead, handler: s.handleCollectorStatus},

		// Security.
		{method: nethttp.MethodGet, pattern: "/api/security/summary", kind: guardFleet, capability: authz.SecurityRead, handler: s.handleSecuritySummary},
		{method: nethttp.MethodGet, pattern: "/api/security/findings", kind: guardFleet, capability: authz.SecurityRead, handler: s.handleSecurityFindings},
		{method: nethttp.MethodGet, pattern: "/api/security/findings/{id}", kind: guardFleet, capability: authz.SecurityRead, handler: s.handleSecurityFinding},
		{method: nethttp.MethodGet, pattern: "/api/security/policies", kind: guardFleet, capability: authz.SecurityRead, handler: s.handleSecurityPolicies},
		{method: nethttp.MethodPost, pattern: "/api/security/findings/{id}/acknowledge", kind: guardFleet, capability: authz.SecurityManage, handler: s.handleSecurityAcknowledge},
		{method: nethttp.MethodPost, pattern: "/api/security/findings/{id}/resolve", kind: guardFleet, capability: authz.SecurityManage, handler: s.handleSecurityResolve},
		{method: nethttp.MethodPost, pattern: "/api/security/findings/{id}/suppress", kind: guardFleet, capability: authz.SecurityManage, handler: s.handleSecuritySuppress},
		{method: nethttp.MethodPost, pattern: "/api/security/policies", kind: guardFleet, capability: authz.SecurityManage, handler: s.handleCreateSecurityPolicy},
		{method: nethttp.MethodPatch, pattern: "/api/security/policies/{id}", kind: guardFleet, capability: authz.SecurityManage, handler: s.handleUpdateSecurityPolicy},
		{method: nethttp.MethodDelete, pattern: "/api/security/policies/{id}", kind: guardFleet, capability: authz.SecurityManage, handler: s.handleDeleteSecurityPolicy},

		// Databases.
		{method: nethttp.MethodGet, pattern: "/api/databases", kind: guardFleet, capability: authz.DatabasesRead, handler: s.handleDatabases},
		{method: nethttp.MethodGet, pattern: "/api/databases/backups", kind: guardFleet, capability: authz.DatabasesRead, handler: s.handleBackups},
		{method: nethttp.MethodPost, pattern: "/api/databases/backups", kind: guardFleet, capability: authz.DatabasesBackup, handler: s.handleStartBackup},
		{method: nethttp.MethodGet, pattern: "/api/databases/jobs", kind: guardFleet, capability: authz.DatabasesRead, handler: s.handleJobs},
		{method: nethttp.MethodGet, pattern: "/api/databases/jobs/{id}", kind: guardFleet, capability: authz.DatabasesRead, handler: s.handleJob},
		{method: nethttp.MethodPost, pattern: "/api/databases/backups/{name}/verify", kind: guardFleet, capability: authz.DatabasesBackup, handler: s.handleVerifyBackup},
		{method: nethttp.MethodPost, pattern: "/api/databases/restore", kind: guardFleet, capability: authz.DatabasesRestore, handler: s.handleRestore},
	}
}

// verdict is what a guard decided about one request.
type verdict int

// Guard verdicts.
const (
	// verdictDeny is the zero value, so an undecided request is refused.
	verdictDeny verdict = iota
	verdictAllow
	verdictNotFound
	verdictMissingService
	verdictBadRequest
	verdictFailed
)

// decision is a verdict with what the denial log needs.
type decision struct {
	verdict   verdict
	serviceID string
	err       error
}

// guard wraps a route's handler with its authorization check. It runs
// inside the auth middleware, so a non-public route always has an
// identity; a route with an unknown kind is refused.
func (s *Server) guard(rt route) nethttp.HandlerFunc {
	if rt.kind == guardPublic {
		return rt.handler
	}

	return func(w nethttp.ResponseWriter, r *nethttp.Request) {
		subject := subjectFrom(r)
		decided := s.authorize(w, r, rt, subject)

		switch decided.verdict {
		case verdictAllow:
			rt.handler(w, r)
		case verdictNotFound:
			s.logDenied(rt, subject, decided.serviceID)
			writeJSON(w, s.logger, nethttp.StatusNotFound, errorResponse{Error: errNotFoundMessage, Code: codeNotFound})
		case verdictMissingService:
			writeError(w, s.logger, errMissingService, "service_id is required", "invalid_request", nethttp.StatusBadRequest)
		case verdictBadRequest:
			writeError(w, s.logger, decided.err, "invalid request", "invalid_request", nethttp.StatusBadRequest)
		case verdictFailed:
			writeError(w, s.logger, decided.err, "failed to check access", "internal", nethttp.StatusInternalServerError)
		case verdictDeny:
			s.logDenied(rt, subject, decided.serviceID)
			writeJSON(w, s.logger, nethttp.StatusForbidden, errorResponse{Error: errForbiddenMessage, Code: codeForbidden})
		default:
			s.logDenied(rt, subject, decided.serviceID)
			writeJSON(w, s.logger, nethttp.StatusForbidden, errorResponse{Error: errForbiddenMessage, Code: codeForbidden})
		}
	}
}

// logDenied records a refused request in the structured log, with who
// asked for what. Denials are not audit rows: the audit trail records
// what happened, and a refused request changed nothing.
func (s *Server) logDenied(rt route, subject authz.Subject, serviceID string) {
	s.logger.Warn(
		"access denied",
		"user", subject.Username,
		"role", string(subject.Role),
		"capability", string(rt.capability),
		"service", serviceID,
		"method", rt.method,
		"route", rt.pattern,
	)
}

// authorize decides one request against its route's guard kind.
func (s *Server) authorize(
	w nethttp.ResponseWriter,
	r *nethttp.Request,
	rt route,
	subject authz.Subject,
) decision {
	switch rt.kind {
	case guardSession, guardFleet:
		return allowIf(subject.Can(rt.capability, ""), "")
	case guardServicePath:
		serviceID := r.PathValue("id")

		return allowIf(subject.Can(rt.capability, serviceID), serviceID)
	case guardServiceQuery:
		return authorizeQuery(r, rt, subject)
	case guardServiceBody:
		return authorizeBody(w, r, rt, subject)
	case guardResource:
		return s.authorizeResource(r, rt, subject)
	case guardPublic, guardUnknown:
		return decision{verdict: verdictDeny}
	default:
		return decision{verdict: verdictDeny}
	}
}

// allowIf turns a policy answer into a decision.
func allowIf(allowed bool, serviceID string) decision {
	if allowed {
		return decision{verdict: verdictAllow, serviceID: serviceID}
	}

	return decision{verdict: verdictDeny, serviceID: serviceID}
}

// authorizeQuery decides a route keyed by ?service_id=. A named service
// must be in scope. Without one, a listScoped route goes ahead and its
// handler bounds the list; any other route is refused as incomplete.
func authorizeQuery(r *nethttp.Request, rt route, subject authz.Subject) decision {
	if !authz.RoleHolds(subject.Role, rt.capability) {
		return decision{verdict: verdictDeny}
	}

	serviceID := r.URL.Query().Get(serviceIDParam)
	if serviceID != "" {
		return allowIf(subject.Can(rt.capability, serviceID), serviceID)
	}

	if rt.listScoped {
		return decision{verdict: verdictAllow}
	}

	return decision{verdict: verdictMissingService}
}

// authorizeBody decides a route whose service is named in its JSON
// body. The role is checked before the body is read, so a caller who
// may never use the route learns nothing about what a valid body is.
// The body is put back for the handler.
func authorizeBody(w nethttp.ResponseWriter, r *nethttp.Request, rt route, subject authz.Subject) decision {
	if !subject.Holds(rt.capability) {
		return decision{verdict: verdictDeny}
	}

	if subject.CanFleet(rt.capability) {
		return decision{verdict: verdictAllow}
	}

	raw, err := io.ReadAll(nethttp.MaxBytesReader(w, r.Body, maxGuardBodyBytes))
	if err != nil {
		return decision{verdict: verdictBadRequest, err: err}
	}

	r.Body = io.NopCloser(bytes.NewReader(raw))

	var body struct {
		ServiceID string `json:"service_id"`
	}

	if err := json.Unmarshal(raw, &body); err != nil {
		return decision{verdict: verdictBadRequest, err: err}
	}

	return allowIf(subject.Can(rt.capability, body.ServiceID), body.ServiceID)
}

// authorizeResource decides a route keyed by a resource id. A caller
// with every service skips the lookup and lets the handler answer. A
// scoped caller gets the same 404 for an id that is missing and for
// one whose service is out of scope.
func (s *Server) authorizeResource(r *nethttp.Request, rt route, subject authz.Subject) decision {
	if !subject.Holds(rt.capability) {
		return decision{verdict: verdictDeny}
	}

	if subject.CanFleet(rt.capability) {
		return decision{verdict: verdictAllow}
	}

	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		return decision{verdict: verdictBadRequest, err: err}
	}

	serviceID, err := s.owners.ServiceOf(r.Context(), rt.resource, id)
	if err != nil {
		if errors.Is(err, service.ErrNoOwner) {
			return decision{verdict: verdictNotFound}
		}

		return decision{verdict: verdictFailed, err: err}
	}

	if !subject.Can(rt.capability, serviceID) {
		return decision{verdict: verdictNotFound, serviceID: serviceID}
	}

	return decision{verdict: verdictAllow, serviceID: serviceID}
}

// listScope reports how a list across services is bounded for the
// caller of a listScoped route: everything, including rows that belong
// to no service, or only the listed services.
func listScope(r *nethttp.Request, capability authz.Capability) (all bool, serviceIDs []string) {
	subject := subjectFrom(r)
	if subject.CanFleet(capability) {
		return true, []string{}
	}

	_, serviceIDs = subject.VisibleServices()

	return false, serviceIDs
}
