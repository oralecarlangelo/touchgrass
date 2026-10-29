package http

import (
	"database/sql"
	"errors"
	"strconv"
	"strings"
	"time"

	nethttp "net/http"

	"github.com/oralecarlangelo/touchgrass/internal/model"
	"github.com/oralecarlangelo/touchgrass/internal/service"
)

type securityFindingsResponse struct {
	Findings []model.SecurityFinding `json:"findings"`
	Limit    int                     `json:"limit"`
	Offset   int                     `json:"offset"`
}

type securityPoliciesResponse struct {
	Policies []model.SecurityPolicy `json:"policies"`
}

type suppressFindingRequest struct {
	Reason string     `json:"reason"`
	Until  *time.Time `json:"until"`
}

type createSecurityPolicyRequest struct {
	Scope   string               `json:"scope"`
	ScopeID string               `json:"scope_id"`
	Policy  model.SecurityPolicy `json:"policy"`
}

type updateSecurityPolicyRequest struct {
	Policy model.SecurityPolicy `json:"policy"`
}

func (s *Server) handleSecuritySummary(w nethttp.ResponseWriter, r *nethttp.Request) {
	summary, err := s.security.Summary(r.Context())
	if err != nil {
		writeServiceError(w, s.logger, err, "failed to load security summary")
		return
	}
	writeJSON(w, s.logger, nethttp.StatusOK, summary)
}

func (s *Server) handleSecurityFindings(w nethttp.ResponseWriter, r *nethttp.Request) {
	query, ok := parseSecurityFindingQuery(w, s, r)
	if !ok {
		return
	}
	findings, err := s.security.Findings(r.Context(), query)
	if err != nil {
		writeServiceError(w, s.logger, err, "failed to load security findings")
		return
	}
	if findings == nil {
		findings = []model.SecurityFinding{}
	}
	writeJSON(w, s.logger, nethttp.StatusOK, securityFindingsResponse{
		Findings: findings, Limit: securityResponseLimit(query.Limit), Offset: query.Offset,
	})
}

func (s *Server) handleSecurityFinding(w nethttp.ResponseWriter, r *nethttp.Request) {
	id, ok := securityID(w, s, r)
	if !ok {
		return
	}
	finding, err := s.security.Finding(r.Context(), id)
	if err != nil {
		writeSecurityError(w, s, err, "failed to load security finding")
		return
	}
	writeJSON(w, s.logger, nethttp.StatusOK, finding)
}

func (s *Server) handleSecurityPolicies(w nethttp.ResponseWriter, r *nethttp.Request) {
	policies, err := s.security.Policies(r.Context())
	if err != nil {
		writeServiceError(w, s.logger, err, "failed to load security policies")
		return
	}
	if policies == nil {
		policies = []model.SecurityPolicy{}
	}
	for index := range policies {
		policies[index] = safePolicy(policies[index])
	}
	writeJSON(w, s.logger, nethttp.StatusOK, securityPoliciesResponse{Policies: policies})
}

func (s *Server) handleSecurityAcknowledge(w nethttp.ResponseWriter, r *nethttp.Request) {
	s.handleSecurityTransition(w, r, func(id int64, actor string) (model.SecurityFinding, error) {
		return s.security.Acknowledge(r.Context(), id, actor)
	})
}

func (s *Server) handleSecurityResolve(w nethttp.ResponseWriter, r *nethttp.Request) {
	s.handleSecurityTransition(w, r, func(id int64, actor string) (model.SecurityFinding, error) {
		return s.security.Resolve(r.Context(), id, actor)
	})
}

func (s *Server) handleSecurityTransition(
	w nethttp.ResponseWriter,
	r *nethttp.Request,
	transition func(int64, string) (model.SecurityFinding, error),
) {
	id, ok := securityID(w, s, r)
	if !ok {
		return
	}
	finding, err := transition(id, requestActor(r))
	if err != nil {
		writeSecurityError(w, s, err, "failed to update security finding")
		return
	}
	writeJSON(w, s.logger, nethttp.StatusOK, finding)
}

func (s *Server) handleSecuritySuppress(w nethttp.ResponseWriter, r *nethttp.Request) {
	id, ok := securityID(w, s, r)
	if !ok {
		return
	}
	body, ok := decodeBody[suppressFindingRequest](w, s.logger, r)
	if !ok {
		return
	}
	finding, err := s.security.Suppress(r.Context(), id, model.FindingSuppression{
		Actor: requestActor(r), Reason: body.Reason, Until: body.Until,
	})
	if err != nil {
		writeSecurityError(w, s, err, "invalid finding suppression")
		return
	}
	writeJSON(w, s.logger, nethttp.StatusOK, finding)
}

func (s *Server) handleCreateSecurityPolicy(w nethttp.ResponseWriter, r *nethttp.Request) {
	body, ok := decodeBody[createSecurityPolicyRequest](w, s.logger, r)
	if !ok {
		return
	}
	body.Policy.Scope, body.Policy.ScopeID = body.Scope, body.ScopeID
	policy, err := s.security.CreatePolicy(r.Context(), body.Policy, requestActor(r))
	if err != nil {
		writeSecurityError(w, s, err, "invalid security policy")
		return
	}
	writeJSON(w, s.logger, nethttp.StatusCreated, safePolicy(policy))
}

func (s *Server) handleUpdateSecurityPolicy(w nethttp.ResponseWriter, r *nethttp.Request) {
	id, ok := securityID(w, s, r)
	if !ok {
		return
	}
	body, ok := decodeBody[updateSecurityPolicyRequest](w, s.logger, r)
	if !ok {
		return
	}
	policy, err := s.security.UpdatePolicy(r.Context(), id, body.Policy, requestActor(r))
	if err != nil {
		writeSecurityError(w, s, err, "invalid security policy")
		return
	}
	writeJSON(w, s.logger, nethttp.StatusOK, safePolicy(policy))
}

func (s *Server) handleDeleteSecurityPolicy(w nethttp.ResponseWriter, r *nethttp.Request) {
	id, ok := securityID(w, s, r)
	if !ok {
		return
	}
	if err := s.security.DeletePolicy(r.Context(), id, requestActor(r)); err != nil {
		writeSecurityError(w, s, err, "failed to delete security policy")
		return
	}
	w.WriteHeader(nethttp.StatusNoContent)
}

func parseSecurityFindingQuery(w nethttp.ResponseWriter, s *Server, r *nethttp.Request) (model.SecurityFindingQuery, bool) {
	query := model.SecurityFindingQuery{
		ContainerID: r.URL.Query().Get("container_id"), Sort: r.URL.Query().Get("sort"),
	}
	if raw := r.URL.Query().Get("status"); raw != "" {
		query.Status = model.SecurityFindingStatus(raw)
		switch query.Status {
		case model.SecurityFindingStatusOpen, model.SecurityFindingStatusAcknowledged,
			model.SecurityFindingStatusResolved, model.SecurityFindingStatusSuppressed:
		case model.SecurityFindingStatusUnknown:
			writeError(w, s.logger, errors.New("invalid status"), "invalid status", "invalid_request", nethttp.StatusBadRequest)
			return model.SecurityFindingQuery{}, false
		}
	}
	if raw := r.URL.Query().Get("severity"); raw != "" {
		query.Severity = model.SecuritySeverity(raw)
		switch query.Severity {
		case model.SecuritySeverityLow, model.SecuritySeverityMedium, model.SecuritySeverityHigh, model.SecuritySeverityCritical:
		case model.SecuritySeverityUnknown:
			writeError(w, s.logger, errors.New("invalid severity"), "invalid severity", "invalid_request", nethttp.StatusBadRequest)
			return model.SecurityFindingQuery{}, false
		}
	}
	if query.Sort != "" && query.Sort != "last_seen" && query.Sort != "opened" && query.Sort != "severity" {
		writeError(w, s.logger, errors.New("invalid sort"), "invalid sort", "invalid_request", nethttp.StatusBadRequest)
		return model.SecurityFindingQuery{}, false
	}
	var err error
	query.Limit, err = optionalNonNegativeInt(r.URL.Query().Get("limit"))
	if err != nil || query.Limit > 1000 {
		writeError(w, s.logger, errors.New("invalid limit"), "invalid limit (want 1-1000)", "invalid_request", nethttp.StatusBadRequest)
		return model.SecurityFindingQuery{}, false
	}
	query.Offset, err = optionalNonNegativeInt(r.URL.Query().Get("offset"))
	if err != nil {
		writeError(w, s.logger, errors.New("invalid offset"), "invalid offset", "invalid_request", nethttp.StatusBadRequest)
		return model.SecurityFindingQuery{}, false
	}
	return query, true
}

func securityID(w nethttp.ResponseWriter, s *Server, r *nethttp.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, s.logger, errors.New("invalid security id"), "invalid security id", "invalid_request", nethttp.StatusBadRequest)
		return 0, false
	}
	return id, true
}

func writeSecurityError(w nethttp.ResponseWriter, s *Server, err error, message string) {
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, s.logger, err, "security resource not found", "not_found", nethttp.StatusNotFound)
		return
	}
	if errors.Is(err, service.ErrInvalidInput) || stringsContainValidation(err.Error()) {
		writeError(w, s.logger, err, message, "invalid_request", nethttp.StatusBadRequest)
		return
	}
	writeServiceError(w, s.logger, err, message)
}

func stringsContainValidation(message string) bool {
	for _, fragment := range []string{"required", "must", "invalid", "allowlist", "scope"} {
		if strings.Contains(message, fragment) {
			return true
		}
	}
	return false
}

func safePolicy(policy model.SecurityPolicy) model.SecurityPolicy {
	policy.AllowedCapabilities = nonNilStrings(policy.AllowedCapabilities)
	policy.AllowedPublishedPorts = nonNilStrings(policy.AllowedPublishedPorts)
	policy.AllowedExecutablePaths = nonNilStrings(policy.AllowedExecutablePaths)
	policy.AllowedWritablePaths = nonNilStrings(policy.AllowedWritablePaths)
	return policy
}

func securityResponseLimit(limit int) int {
	if limit <= 0 {
		return 100
	}
	return limit
}
