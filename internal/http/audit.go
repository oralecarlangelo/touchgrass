package http

import (
	"errors"

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
	filter, err := auditQuery(r)
	if err != nil {
		writeError(w, s.logger, err, err.Error(), "invalid_request", nethttp.StatusBadRequest)

		return
	}

	entries, err := s.auditEntries(r, filter)
	if err != nil {
		writeServiceError(w, s.logger, err, "failed to load audit entries")

		return
	}

	writeJSON(w, s.logger, nethttp.StatusOK, auditResponse{Audit: entries})
}

func auditQuery(r *nethttp.Request) (model.AuditQuery, error) {
	params := r.URL.Query()

	limit, err := parseLimit(params.Get("limit"))
	if err != nil {
		return model.AuditQuery{}, errors.New("invalid limit (want 1-1000)")
	}

	start, err := parseLogBound(params.Get("start"))
	if err != nil {
		return model.AuditQuery{}, errors.New("invalid start timestamp (want RFC3339)")
	}

	end, err := parseLogBound(params.Get("end"))
	if err != nil {
		return model.AuditQuery{}, errors.New("invalid end timestamp (want RFC3339)")
	}

	if start != nil && end != nil && !start.Before(*end) {
		return model.AuditQuery{}, errors.New("start must be before end")
	}

	return model.AuditQuery{Limit: limit, Start: start, End: end}, nil
}

// auditEntries loads the audit rows the caller asked for and may see.
func (s *Server) auditEntries(r *nethttp.Request, filter model.AuditQuery) ([]model.Audit, error) {
	serviceID := r.URL.Query().Get(serviceIDParam)
	if serviceID != "" {
		return s.audit.Search(r.Context(), serviceID, filter)
	}

	all, serviceIDs := listScope(r, authz.AuditReadAll)
	if !all {
		filter.ServiceIDs = append([]string{}, serviceIDs...)
	}

	return s.audit.Search(r.Context(), "", filter)
}
