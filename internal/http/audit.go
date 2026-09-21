package http

import (
	nethttp "net/http"

	"github.com/oralecarlangelo/touchgrass/internal/model"
)

// auditResponse is the audit payload.
type auditResponse struct {
	Audit []model.Audit `json:"audit"`
}

// handleAudit lists audit entries newest first, optionally for one service.
func (s *Server) handleAudit(w nethttp.ResponseWriter, r *nethttp.Request) {
	limit, err := parseLimit(r.URL.Query().Get("limit"))
	if err != nil {
		writeError(w, s.logger, err, "invalid limit (want 1-1000)", "invalid_request", nethttp.StatusBadRequest)

		return
	}

	entries, err := s.audit.List(r.Context(), r.URL.Query().Get("service_id"), limit)
	if err != nil {
		writeServiceError(w, s.logger, err, "failed to load audit entries")

		return
	}

	writeJSON(w, s.logger, nethttp.StatusOK, auditResponse{Audit: entries})
}
