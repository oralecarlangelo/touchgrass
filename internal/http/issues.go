package http

import (
	"context"
	"strconv"

	nethttp "net/http"

	"github.com/oralecarlangelo/touchgrass/internal/model"
)

// issuesResponse is the issue list payload.
type issuesResponse struct {
	Issues []model.Issue `json:"issues"`
}

// issueDetailResponse pairs an issue with recent occurrences.
type issueDetailResponse struct {
	Issue       model.Issue        `json:"issue"`
	Occurrences []model.Occurrence `json:"occurrences"`
}

// issueRuleRequest is the issue rule creation body.
type issueRuleRequest struct {
	ServiceID  string `json:"service_id"`
	Kind       string `json:"kind"`
	Threshold  int    `json:"threshold"`
	WindowSecs int    `json:"window_secs"`
}

// issueRulesResponse is the issue rule list payload.
type issueRulesResponse struct {
	Rules []model.IssueRule `json:"rules"`
}

// handleIssues lists a service's issues newest-activity first.
func (s *Server) handleIssues(w nethttp.ResponseWriter, r *nethttp.Request) {
	serviceID := r.URL.Query().Get("service_id")
	if serviceID == "" {
		writeError(w, s.logger, errMissingService, "service_id is required", "invalid_request", nethttp.StatusBadRequest)

		return
	}

	issues, err := s.ingestor.Issues(r.Context(), serviceID)
	if err != nil {
		writeServiceError(w, s.logger, err, "failed to load issues")

		return
	}

	writeJSON(w, s.logger, nethttp.StatusOK, issuesResponse{Issues: issues})
}

// handleIssue returns one issue with recent occurrences.
func (s *Server) handleIssue(w nethttp.ResponseWriter, r *nethttp.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, s.logger, err, "invalid issue id", "invalid_request", nethttp.StatusBadRequest)

		return
	}

	limit, err := parseLimit(r.URL.Query().Get("limit"))
	if err != nil {
		writeError(w, s.logger, err, "invalid limit (want 1-1000)", "invalid_request", nethttp.StatusBadRequest)

		return
	}

	issue, err := s.ingestor.Issue(r.Context(), id)
	if err != nil {
		writeServiceError(w, s.logger, err, "failed to load issue")

		return
	}

	occurrences, err := s.ingestor.IssueOccurrences(r.Context(), id, limit)
	if err != nil {
		writeServiceError(w, s.logger, err, "failed to load issue occurrences")

		return
	}

	writeJSON(w, s.logger, nethttp.StatusOK, issueDetailResponse{Issue: issue, Occurrences: occurrences})
}

// issueLogsResponse is the issue-linked log payload.
type issueLogsResponse struct {
	Logs []model.LogLine `json:"logs"`
}

// occurrencesResponse is the recent-occurrences payload.
type occurrencesResponse struct {
	Occurrences []model.Occurrence `json:"occurrences"`
}

// handleOccurrences lists a service's occurrences newest first.
func (s *Server) handleOccurrences(w nethttp.ResponseWriter, r *nethttp.Request) {
	serviceID := r.URL.Query().Get("service_id")
	if serviceID == "" {
		writeError(w, s.logger, errMissingService, "service_id is required", "invalid_request", nethttp.StatusBadRequest)

		return
	}

	limit, err := parseLimit(r.URL.Query().Get("limit"))
	if err != nil {
		writeError(w, s.logger, err, "invalid limit (want 1-1000)", "invalid_request", nethttp.StatusBadRequest)

		return
	}

	occurrences, err := s.ingestor.RecentOccurrences(r.Context(), serviceID, limit)
	if err != nil {
		writeServiceError(w, s.logger, err, "failed to load occurrences")

		return
	}

	writeJSON(w, s.logger, nethttp.StatusOK, occurrencesResponse{Occurrences: occurrences})
}

// handleIssueLogs returns log lines around an issue's newest occurrence.
func (s *Server) handleIssueLogs(w nethttp.ResponseWriter, r *nethttp.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, s.logger, err, "invalid issue id", "invalid_request", nethttp.StatusBadRequest)

		return
	}

	window, err := parseWindowSecs(r.URL.Query().Get("window_secs"))
	if err != nil {
		writeError(w, s.logger, err, "invalid window_secs (want 1-3600)", "invalid_request", nethttp.StatusBadRequest)

		return
	}

	limit, err := parseLimit(r.URL.Query().Get("limit"))
	if err != nil {
		writeError(w, s.logger, err, "invalid limit (want 1-1000)", "invalid_request", nethttp.StatusBadRequest)

		return
	}

	logs, err := s.ingestor.IssueLogs(r.Context(), id, window, limit)
	if err != nil {
		writeServiceError(w, s.logger, err, "failed to load issue logs")

		return
	}

	writeJSON(w, s.logger, nethttp.StatusOK, issueLogsResponse{Logs: logs})
}

// parseWindowSecs parses an optional ±window in seconds; 0 selects the default.
func parseWindowSecs(raw string) (int, error) {
	if raw == "" {
		return 0, nil
	}

	window, err := strconv.Atoi(raw)
	if err != nil || window < 1 || window > 3600 {
		return 0, errInvalidWindow
	}

	return window, nil
}

// handleCreateIssueRule stores an issue alert rule.
func (s *Server) handleCreateIssueRule(w nethttp.ResponseWriter, r *nethttp.Request) {
	createJSON(w, s.logger, r, func(ctx context.Context, body issueRuleRequest) (model.IssueRule, error) {
		return s.ingestor.CreateIssueRule(ctx, model.IssueRuleCreate{
			ServiceID: body.ServiceID, Kind: body.Kind,
			Threshold: body.Threshold, WindowSecs: body.WindowSecs,
		})
	}, "failed to create issue rule")
}

// handleIssueRules lists a service's issue alert rules.
func (s *Server) handleIssueRules(w nethttp.ResponseWriter, r *nethttp.Request) {
	serviceID := r.URL.Query().Get("service_id")
	if serviceID == "" {
		writeError(w, s.logger, errMissingService, "service_id is required", "invalid_request", nethttp.StatusBadRequest)

		return
	}

	rules, err := s.ingestor.IssueRules(r.Context(), serviceID)
	if err != nil {
		writeServiceError(w, s.logger, err, "failed to load issue rules")

		return
	}

	writeJSON(w, s.logger, nethttp.StatusOK, issueRulesResponse{Rules: rules})
}

// handleDeleteIssueRule removes an issue alert rule.
func (s *Server) handleDeleteIssueRule(w nethttp.ResponseWriter, r *nethttp.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, s.logger, err, "invalid rule id", "invalid_request", nethttp.StatusBadRequest)

		return
	}

	if err := s.ingestor.DeleteIssueRule(r.Context(), id); err != nil {
		writeServiceError(w, s.logger, err, "failed to delete issue rule")

		return
	}

	w.WriteHeader(nethttp.StatusNoContent)
}
