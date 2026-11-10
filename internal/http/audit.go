package http

import (
	nethttp "net/http"

	"github.com/oralecarlangelo/touchgrass/internal/authz"
	"github.com/oralecarlangelo/touchgrass/internal/model"
)

// auditResponse is the audit payload.
type auditResponse struct {
	Audit []model.Audit `json:"audit"`
}

// handleAudit lists audit entries newest first. With service_id it is
// one service, which the route guard already checked. Without it, a
// caller holding the whole trail gets every row; a scoped caller gets
// the rows of their services only, and never the rows that belong to no
// service (logins, user management, host actions).
func (s *Server) handleAudit(w nethttp.ResponseWriter, r *nethttp.Request) {
	limit, err := parseLimit(r.URL.Query().Get("limit"))
	if err != nil {
		writeError(w, s.logger, err, "invalid limit (want 1-1000)", "invalid_request", nethttp.StatusBadRequest)

		return
	}

	entries, err := s.auditEntries(r, limit)
	if err != nil {
		writeServiceError(w, s.logger, err, "failed to load audit entries")

		return
	}

	writeJSON(w, s.logger, nethttp.StatusOK, auditResponse{Audit: entries})
}

// auditEntries loads the audit rows the caller asked for and may see.
func (s *Server) auditEntries(r *nethttp.Request, limit int) ([]model.Audit, error) {
	serviceID := r.URL.Query().Get(serviceIDParam)
	if serviceID != "" {
		return s.audit.List(r.Context(), serviceID, limit)
	}

	all, serviceIDs := listScope(r, authz.AuditReadAll)
	if all {
		return s.audit.List(r.Context(), "", limit)
	}

	return s.audit.ListForServices(r.Context(), serviceIDs, limit)
}
