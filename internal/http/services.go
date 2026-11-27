package http

import (
	"encoding/json"
	"time"

	nethttp "net/http"

	"github.com/oralecarlangelo/touchgrass/internal/model"
	"github.com/oralecarlangelo/touchgrass/internal/service"
)

// servicesResponse is the /api/services payload.
type servicesResponse struct {
	Services  []model.ServiceView `json:"services"`
	Freshness serviceFreshness    `json:"freshness"`
}

type serviceFreshness struct {
	ObservedAt        *time.Time `json:"observed_at"`
	Complete          bool       `json:"complete"`
	Stale             bool       `json:"stale"`
	LastErrorCategory string     `json:"last_error_category,omitempty"`
}

// handleServices lists the managed services in the caller's scope with
// live state. The scope bounds the query itself, so a scoped caller's
// list never held anything else.
func (s *Server) handleServices(w nethttp.ResponseWriter, r *nethttp.Request) {
	state, err := s.visibleServices(r)
	if err != nil {
		writeError(w, s.logger, err, "failed to load services", "services_unavailable", nethttp.StatusInternalServerError)

		return
	}

	var observedAt *time.Time
	if !state.ObservedAt.IsZero() {
		observedAt = &state.ObservedAt
	}

	lastErrorCategory := ""
	if state.LastError != "" {
		lastErrorCategory = "collection_failed"
	}

	writeJSON(w, s.logger, nethttp.StatusOK, servicesResponse{
		Services: state.Services,
		Freshness: serviceFreshness{
			ObservedAt:        observedAt,
			Complete:          state.Complete,
			Stale:             state.Stale,
			LastErrorCategory: lastErrorCategory,
		},
	})
}

// visibleServices loads the service views the caller may see.
func (s *Server) visibleServices(r *nethttp.Request) (service.InventoryState, error) {
	all, serviceIDs := subjectFrom(r).VisibleServices()
	if all {
		return s.inventory.CachedServices(r.Context())
	}

	return s.inventory.CachedServicesIn(r.Context(), serviceIDs)
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
	}, requestActor(r))
	if err != nil {
		writeServiceError(w, s.logger, err, "failed to create service")

		return
	}

	writeJSON(w, s.logger, nethttp.StatusCreated, created)
}

// handleDeleteService removes a service from management. Running
// containers are untouched; only the touchgrass definition and history go.
func (s *Server) handleDeleteService(w nethttp.ResponseWriter, r *nethttp.Request) {
	if err := s.onboarding.Delete(r.Context(), r.PathValue("id"), requestActor(r)); err != nil {
		writeServiceError(w, s.logger, err, "failed to delete service")

		return
	}

	w.WriteHeader(nethttp.StatusNoContent)
}
