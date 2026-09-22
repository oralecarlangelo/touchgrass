package http

import (
	"encoding/json"

	nethttp "net/http"

	"github.com/oralecarlangelo/touchgrass/internal/model"
	"github.com/oralecarlangelo/touchgrass/internal/service"
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

// createServiceRequest is the service creation body. Config holds raw
// strategy JSON validated per strategy.
type createServiceRequest struct {
	ID             string          `json:"id"`
	Name           string          `json:"name"`
	Strategy       model.Strategy  `json:"strategy"`
	ComposeProject string          `json:"compose_project"`
	ComposeDir     string          `json:"compose_dir"`
	Config         json.RawMessage `json:"config"`
}

// handleCreateService validates and stores a new managed service.
func (s *Server) handleCreateService(w nethttp.ResponseWriter, r *nethttp.Request) {
	body, ok := decodeBody[createServiceRequest](w, s.logger, r)
	if !ok {
		return
	}

	created, err := s.onboarding.Create(r.Context(), service.CreateInput{
		ID:             body.ID,
		Name:           body.Name,
		Strategy:       body.Strategy,
		ComposeProject: body.ComposeProject,
		ComposeDir:     body.ComposeDir,
		Config:         body.Config,
	}, actorAdmin)
	if err != nil {
		writeServiceError(w, s.logger, err, "failed to create service")

		return
	}

	writeJSON(w, s.logger, nethttp.StatusCreated, created)
}

// handleDeleteService removes a service from management. Running
// containers are untouched; only the touchgrass definition and history go.
func (s *Server) handleDeleteService(w nethttp.ResponseWriter, r *nethttp.Request) {
	if err := s.onboarding.Delete(r.Context(), r.PathValue("id"), actorAdmin); err != nil {
		writeServiceError(w, s.logger, err, "failed to delete service")

		return
	}

	w.WriteHeader(nethttp.StatusNoContent)
}
