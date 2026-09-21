package http

import (
	"context"
	"strconv"

	nethttp "net/http"

	"github.com/oralecarlangelo/touchgrass/internal/model"
)

// rulesResponse is the alert rules payload.
type rulesResponse struct {
	Rules []model.AlertRule `json:"rules"`
}

// ruleRequest is the rule creation body.
type ruleRequest struct {
	ServiceID    string  `json:"service_id"`
	Metric       string  `json:"metric"`
	Threshold    float64 `json:"threshold"`
	DurationSecs int     `json:"duration_secs"`
}

// notificationsResponse is the notifications payload.
type notificationsResponse struct {
	Notifications []model.Notification `json:"notifications"`
	Unread        int64                `json:"unread"`
}

// markedResponse reports a mark-read count.
type markedResponse struct {
	Marked int64 `json:"marked"`
}

// handleRules lists alert rules for one service.
func (s *Server) handleRules(w nethttp.ResponseWriter, r *nethttp.Request) {
	serviceID := r.URL.Query().Get("service_id")
	if serviceID == "" {
		writeError(w, s.logger, errMissingService, "service_id is required", "invalid_request", nethttp.StatusBadRequest)

		return
	}

	rules, err := s.sampler.Rules(r.Context(), serviceID)
	if err != nil {
		writeServiceError(w, s.logger, err, "failed to load alert rules")

		return
	}

	writeJSON(w, s.logger, nethttp.StatusOK, rulesResponse{Rules: rules})
}

// handleCreateRule stores an alert rule.
func (s *Server) handleCreateRule(w nethttp.ResponseWriter, r *nethttp.Request) {
	createJSON(w, s.logger, r, func(ctx context.Context, body ruleRequest) (model.AlertRule, error) {
		return s.sampler.CreateRule(ctx, model.RuleCreate{
			ServiceID:    body.ServiceID,
			Metric:       body.Metric,
			Threshold:    body.Threshold,
			DurationSecs: body.DurationSecs,
		})
	}, "failed to create alert rule")
}

// handleDeleteRule removes an alert rule.
func (s *Server) handleDeleteRule(w nethttp.ResponseWriter, r *nethttp.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, s.logger, err, "invalid rule id", "invalid_request", nethttp.StatusBadRequest)

		return
	}

	if err := s.sampler.DeleteRule(r.Context(), id); err != nil {
		writeServiceError(w, s.logger, err, "failed to delete alert rule")

		return
	}

	w.WriteHeader(nethttp.StatusNoContent)
}

// handleNotifications lists notifications newest first with an unread count,
// optionally filtered to one service.
func (s *Server) handleNotifications(w nethttp.ResponseWriter, r *nethttp.Request) {
	limit, err := parseLimit(r.URL.Query().Get("limit"))
	if err != nil {
		writeError(w, s.logger, err, "invalid limit (want 1-1000)", "invalid_request", nethttp.StatusBadRequest)

		return
	}

	notifications, unread, err := s.sampler.Notifications(r.Context(), r.URL.Query().Get("service_id"), limit)
	if err != nil {
		writeServiceError(w, s.logger, err, "failed to load notifications")

		return
	}

	writeJSON(w, s.logger, nethttp.StatusOK, notificationsResponse{Notifications: notifications, Unread: unread})
}

// handleMarkNotificationRead stamps one notification read.
func (s *Server) handleMarkNotificationRead(w nethttp.ResponseWriter, r *nethttp.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, s.logger, err, "invalid notification id", "invalid_request", nethttp.StatusBadRequest)

		return
	}

	if err := s.sampler.MarkNotificationRead(r.Context(), id); err != nil {
		writeServiceError(w, s.logger, err, "failed to mark notification read")

		return
	}

	w.WriteHeader(nethttp.StatusNoContent)
}

// handleMarkNotificationsRead stamps every unread notification read in scope.
func (s *Server) handleMarkNotificationsRead(w nethttp.ResponseWriter, r *nethttp.Request) {
	marked, err := s.sampler.MarkNotificationsRead(r.Context(), r.URL.Query().Get("service_id"))
	if err != nil {
		writeServiceError(w, s.logger, err, "failed to mark notifications read")

		return
	}

	writeJSON(w, s.logger, nethttp.StatusOK, markedResponse{Marked: marked})
}
