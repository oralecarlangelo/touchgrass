package http

import (
	nethttp "net/http"

	"github.com/oralecarlangelo/touchgrass/internal/model"
)

// servicesResponse is the /api/services payload.
type servicesResponse struct {
	Services []model.ServiceView `json:"services"`
}

// handleServices lists managed services with live state.
func (s *Server) handleServices(w nethttp.ResponseWriter, r *nethttp.Request) {
	views, err := s.inventory.Services(r.Context())
	if err != nil {
		writeError(w, s.logger, err, "failed to load services", "services_unavailable", nethttp.StatusInternalServerError)

		return
	}

	writeJSON(w, s.logger, nethttp.StatusOK, servicesResponse{Services: views})
}
