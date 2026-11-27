package http

import (
	nethttp "net/http"

	"github.com/oralecarlangelo/touchgrass/internal/authz"
	"github.com/oralecarlangelo/touchgrass/internal/model"
)

type envFleetResponse struct {
	Services []model.EnvFleetSummary `json:"services"`
}

func (s *Server) handleEnvFleet(w nethttp.ResponseWriter, r *nethttp.Request) {
	all, ids := listScope(r, authz.EnvReadKeys)
	if serviceID := r.URL.Query().Get(serviceIDParam); serviceID != "" {
		all = false
		ids = []string{serviceID}
	}

	views, err := s.env.FleetSummary(r.Context(), all, ids)
	if err != nil {
		writeEnvError(w, s.logger, err, "failed to load environment summaries")

		return
	}

	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, s.logger, nethttp.StatusOK, envFleetResponse{Services: views})
}
