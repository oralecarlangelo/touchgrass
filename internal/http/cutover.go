package http

import (
	"context"
	"encoding/json"
	"errors"
	"io"

	nethttp "net/http"
)

// actorAdmin is the single v1 identity.
const actorAdmin = "admin"

// cutoverRequest is the cutover body; empty target means auto.
type cutoverRequest struct {
	Target string `json:"target"`
}

// cutoverResponse reports the resolved target.
type cutoverResponse struct {
	Target string `json:"target"`
}

// handleRollback starts an async rollback to the opposite live color.
func (s *Server) handleRollback(w nethttp.ResponseWriter, r *nethttp.Request) {
	target, err := s.cutover.StartRollback(
		context.WithoutCancel(r.Context()),
		r.PathValue("id"),
		actorAdmin,
	)
	if err != nil {
		writeServiceError(w, s.logger, err, "failed to start rollback")

		return
	}

	writeJSON(w, s.logger, nethttp.StatusAccepted, cutoverResponse{Target: target})
}

// handleDeploy starts an async recreate deploy.
func (s *Server) handleDeploy(w nethttp.ResponseWriter, r *nethttp.Request) {
	target, err := s.cutover.StartDeploy(
		context.WithoutCancel(r.Context()),
		r.PathValue("id"),
		actorAdmin,
	)
	if err != nil {
		writeServiceError(w, s.logger, err, "failed to start deploy")

		return
	}

	writeJSON(w, s.logger, nethttp.StatusAccepted, cutoverResponse{Target: target})
}

// handleCutover starts an async cutover.
func (s *Server) handleCutover(w nethttp.ResponseWriter, r *nethttp.Request) {
	var body cutoverRequest

	if err := json.NewDecoder(r.Body).Decode(&body); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, s.logger, err, "invalid JSON body", "invalid_request", nethttp.StatusBadRequest)

		return
	}

	target, err := s.cutover.Start(
		context.WithoutCancel(r.Context()),
		r.PathValue("id"),
		body.Target,
		actorAdmin,
	)
	if err != nil {
		writeServiceError(w, s.logger, err, "failed to start cutover")

		return
	}

	writeJSON(w, s.logger, nethttp.StatusAccepted, cutoverResponse{Target: target})
}
