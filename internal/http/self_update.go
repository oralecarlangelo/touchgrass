package http

import (
	"errors"
	nethttp "net/http"
	"net/url"

	"github.com/oralecarlangelo/touchgrass/internal/model"
	"github.com/oralecarlangelo/touchgrass/internal/service"
)

type startSelfUpdateRequest struct {
	Tag     string `json:"tag"`
	Confirm bool   `json:"confirm"`
}

func (s *Server) handleSelfUpdate(w nethttp.ResponseWriter, r *nethttp.Request) {
	if s.selfUpdate == nil {
		writeJSON(w, s.logger, nethttp.StatusServiceUnavailable, errorResponse{
			Error: "Updates are not configured.", Code: "updates_unavailable",
		})

		return
	}

	status := s.selfUpdate.Check
	if r.URL.Query().Get("refresh") == "true" {
		status = s.selfUpdate.CheckNow
	}

	writeJSON(w, s.logger, nethttp.StatusOK, status(r.Context()))
}

func (s *Server) handleStartSelfUpdate(w nethttp.ResponseWriter, r *nethttp.Request) {
	if s.selfUpdate == nil {
		writeJSON(w, s.logger, nethttp.StatusServiceUnavailable, errorResponse{
			Error: "Updates are not configured.", Code: "updates_unavailable",
		})

		return
	}

	if !validSelfUpdateOrigin(r, s.selfUpdate.Origin()) {
		writeJSON(w, s.logger, nethttp.StatusForbidden, errorResponse{
			Error: "This request must come from the configured console origin.", Code: "forbidden",
		})

		return
	}

	r.Body = nethttp.MaxBytesReader(w, r.Body, 1024)

	body, ok := decodeBody[startSelfUpdateRequest](w, s.logger, r)
	if !ok {
		return
	}

	if !body.Confirm {
		writeJSON(w, s.logger, nethttp.StatusBadRequest, errorResponse{
			Error: "Confirm the exact release before installing.", Code: "confirmation_required",
		})

		return
	}

	if _, err := s.audit.Record(r.Context(), model.AuditRecord{
		Actor: requestActor(r), Action: model.AuditSelfUpdate,
		Result: model.AuditSuccess, Detail: "requested console update " + body.Tag,
	}); err != nil {
		writeError(w, s.logger, err, "Could not record the update request.", "audit_unavailable", nethttp.StatusInternalServerError)
		return
	}

	if err := s.selfUpdate.Start(r.Context(), body.Tag); err != nil {
		status := nethttp.StatusServiceUnavailable
		code, message := "updates_unavailable", "Could not start the update. Retry or use the recovery workflow."

		switch {
		case errors.Is(err, service.ErrUpdateBusy):
			status, code, message = nethttp.StatusConflict, "update_busy", "An update is already running."
		case errors.Is(err, service.ErrUpdateInvalid):
			status, code, message = nethttp.StatusBadRequest, "invalid_update", "This release is not available for installation."
		}

		if _, auditErr := s.audit.Record(r.Context(), model.AuditRecord{
			Actor: requestActor(r), Action: model.AuditSelfUpdate,
			Result: model.AuditFailure, Detail: "console update dispatch failed for " + body.Tag,
		}); auditErr != nil {
			s.logger.Warn("update failure audit failed", "error", auditErr)
		}

		writeError(w, s.logger, err, message, code, status)

		return
	}

	writeJSON(w, s.logger, nethttp.StatusAccepted, s.selfUpdate.Check(r.Context()))
}

// Explicit origins remain authoritative. Standard installs use the browser's
// same-origin headers and the request Host, never forwarded host headers.
func validSelfUpdateOrigin(r *nethttp.Request, configured string) bool {
	origin := r.Header.Get("Origin")
	if configured != "" {
		return origin == configured
	}

	parsed, err := url.Parse(origin)
	if err != nil || parsed.User != nil || parsed.Host == "" || parsed.Host != r.Host {
		return false
	}

	if parsed.Scheme != "https" && parsed.Scheme != "http" {
		return false
	}

	if parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return false
	}

	return nethttp.NewCrossOriginProtection().Check(r) == nil
}
