package http

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strconv"

	nethttp "net/http"

	"github.com/oralecarlangelo/touchgrass/internal/service"
	"github.com/oralecarlangelo/touchgrass/internal/store"
)

// envEntryRequest is the PUT entry body.
type envEntryRequest struct {
	Value    string `json:"value"`
	IsSecret bool   `json:"is_secret"`
}

// envRevealResponse carries one decrypted value.
type envRevealResponse struct {
	Value string `json:"value"`
}

// envImportRequest is the optional import body.
type envImportRequest struct {
	Replace bool `json:"replace"`
}

// envApplyRequest is the apply body; confirm must equal the service name.
type envApplyRequest struct {
	Confirm string `json:"confirm"`
	Summary string `json:"summary"`
}

// envRollbackRequest is the rollback body.
type envRollbackRequest struct {
	Confirm string `json:"confirm"`
}

// envRunResponse reports an accepted run.
type envRunResponse struct {
	RunID string `json:"run_id"`
}

// handleEnv returns the env state of a service.
func (s *Server) handleEnv(w nethttp.ResponseWriter, r *nethttp.Request) {
	view, err := s.env.View(r.Context(), r.PathValue("id"))
	if err != nil {
		writeEnvError(w, s.logger, err, "failed to load env")

		return
	}

	writeJSON(w, s.logger, nethttp.StatusOK, view)
}

// handleEnvPut stages one entry.
func (s *Server) handleEnvPut(w nethttp.ResponseWriter, r *nethttp.Request) {
	body, ok := decodeBody[envEntryRequest](w, s.logger, r)
	if !ok {
		return
	}

	entry, err := s.env.Stage(
		r.Context(),
		r.PathValue("id"),
		r.PathValue("key"),
		body.Value,
		body.IsSecret,
		requestActor(r),
	)
	if err != nil {
		writeEnvError(w, s.logger, err, "failed to stage env entry")

		return
	}

	writeJSON(w, s.logger, nethttp.StatusOK, entry)
}

// handleEnvDelete stages the removal of one entry.
func (s *Server) handleEnvDelete(w nethttp.ResponseWriter, r *nethttp.Request) {
	if err := s.env.Remove(r.Context(), r.PathValue("id"), r.PathValue("key"), requestActor(r)); err != nil {
		writeEnvError(w, s.logger, err, "failed to stage env removal")

		return
	}

	w.WriteHeader(nethttp.StatusNoContent)
}

// handleEnvReveal decrypts one value for an admin.
func (s *Server) handleEnvReveal(w nethttp.ResponseWriter, r *nethttp.Request) {
	value, err := s.env.Reveal(r.Context(), r.PathValue("id"), r.PathValue("key"), requestActor(r))
	if err != nil {
		writeEnvError(w, s.logger, err, "failed to reveal env entry")

		return
	}

	writeJSON(w, s.logger, nethttp.StatusOK, envRevealResponse{Value: value})
}

// handleEnvDiff compares pending and applied keys.
func (s *Server) handleEnvDiff(w nethttp.ResponseWriter, r *nethttp.Request) {
	diff, err := s.env.Diff(r.Context(), r.PathValue("id"))
	if err != nil {
		writeEnvError(w, s.logger, err, "failed to diff env")

		return
	}

	writeJSON(w, s.logger, nethttp.StatusOK, diff)
}

// handleEnvDiscard drops pending changes.
func (s *Server) handleEnvDiscard(w nethttp.ResponseWriter, r *nethttp.Request) {
	if err := s.env.Discard(r.Context(), r.PathValue("id"), requestActor(r)); err != nil {
		writeEnvError(w, s.logger, err, "failed to discard env changes")

		return
	}

	w.WriteHeader(nethttp.StatusNoContent)
}

// handleEnvImport reads the target file into the store.
func (s *Server) handleEnvImport(w nethttp.ResponseWriter, r *nethttp.Request) {
	body, ok := decodeOptionalBody[envImportRequest](w, s.logger, r)
	if !ok {
		return
	}

	result, err := s.env.Import(r.Context(), r.PathValue("id"), body.Replace, requestActor(r))
	if err != nil {
		writeEnvError(w, s.logger, err, "failed to import env")

		return
	}

	writeJSON(w, s.logger, nethttp.StatusOK, result)
}

// handleEnvApply starts an async apply run.
func (s *Server) handleEnvApply(w nethttp.ResponseWriter, r *nethttp.Request) {
	body, ok := decodeBody[envApplyRequest](w, s.logger, r)
	if !ok {
		return
	}

	runID, err := s.env.Apply(
		context.WithoutCancel(r.Context()),
		r.PathValue("id"),
		body.Confirm,
		body.Summary,
		requestActor(r),
	)
	if err != nil {
		writeEnvError(w, s.logger, err, "failed to start env apply")

		return
	}

	writeJSON(w, s.logger, nethttp.StatusAccepted, envRunResponse{RunID: runID})
}

// handleEnvRun returns one run with its step log.
func (s *Server) handleEnvRun(w nethttp.ResponseWriter, r *nethttp.Request) {
	run, err := s.env.Run(r.Context(), r.PathValue("id"), r.PathValue("run_id"))
	if err != nil {
		writeEnvError(w, s.logger, err, "failed to load env run")

		return
	}

	writeJSON(w, s.logger, nethttp.StatusOK, run)
}

// handleEnvRevisions lists revisions newest first.
func (s *Server) handleEnvRevisions(w nethttp.ResponseWriter, r *nethttp.Request) {
	revisions, err := s.env.Revisions(r.Context(), r.PathValue("id"))
	if err != nil {
		writeEnvError(w, s.logger, err, "failed to load env revisions")

		return
	}

	writeJSON(w, s.logger, nethttp.StatusOK, revisions)
}

// handleEnvRollback stages a revision and starts an apply run.
func (s *Server) handleEnvRollback(w nethttp.ResponseWriter, r *nethttp.Request) {
	revisionID, err := strconv.ParseInt(r.PathValue("rev"), 10, 64)
	if err != nil || revisionID < 1 {
		writeError(w, s.logger, errInvalidRevision, "invalid revision id", invalidRequestCode, nethttp.StatusBadRequest)

		return
	}

	body, ok := decodeBody[envRollbackRequest](w, s.logger, r)
	if !ok {
		return
	}

	runID, err := s.env.Rollback(
		context.WithoutCancel(r.Context()),
		r.PathValue("id"),
		revisionID,
		body.Confirm,
		requestActor(r),
	)
	if err != nil {
		writeEnvError(w, s.logger, err, "failed to start env rollback")

		return
	}

	writeJSON(w, s.logger, nethttp.StatusAccepted, envRunResponse{RunID: runID})
}

// errInvalidRevision reports a non-numeric revision path value.
var errInvalidRevision = errors.New("revision id must be a positive integer")

// invalidRequestCode is the generic validation code.
const invalidRequestCode = "invalid_request"

// decodeOptionalBody decodes a JSON body, treating an empty body as the
// zero value.
func decodeOptionalBody[T any](
	w nethttp.ResponseWriter,
	logger *slog.Logger,
	r *nethttp.Request,
) (T, bool) {
	var body T

	if err := decodeJSONAllowEmpty(r, &body); err != nil {
		writeError(w, logger, err, "invalid JSON body", invalidRequestCode, nethttp.StatusBadRequest)

		return body, false
	}

	return body, true
}

// decodeJSONAllowEmpty decodes into target, accepting an empty body.
func decodeJSONAllowEmpty(r *nethttp.Request, target any) error {
	data, err := io.ReadAll(io.LimitReader(r.Body, maxOptionalBody))
	if err != nil {
		return err
	}

	if len(data) == 0 {
		return nil
	}

	if err := json.Unmarshal(data, target); err != nil {
		return fmt.Errorf("decoding body: %w", err)
	}

	return nil
}

// maxOptionalBody bounds small optional request bodies.
const maxOptionalBody = 64 * 1024

// revealRetryAfter is the Retry-After hint (seconds) on a rate-limited reveal.
const revealRetryAfter = "60"

// writeEnvError maps env errors to the contract codes before the shared
// service mapping.
func writeEnvError(w nethttp.ResponseWriter, logger *slog.Logger, err error, message string) {
	switch {
	case errors.Is(err, service.ErrEnvKeyMissing):
		writeError(w, logger, err, "env encryption key is not configured", "env_key_missing", nethttp.StatusServiceUnavailable)
	case errors.Is(err, service.ErrEnvNotConfigured):
		writeError(w, logger, err, "env is not configured for this service", "env_not_configured", nethttp.StatusBadRequest)
	case errors.Is(err, service.ErrEnvKeyNameInvalid):
		writeError(w, logger, err, "env key must match ^[A-Z_][A-Z0-9_]*$", "env_key_invalid", nethttp.StatusBadRequest)
	case errors.Is(err, service.ErrEnvValueInvalid):
		writeError(w, logger, err, "env value must be UTF-8 without newlines, at most 64 KiB", "env_value_invalid", nethttp.StatusBadRequest)
	case errors.Is(err, service.ErrEnvHasEntries):
		writeError(w, logger, err, "env entries already exist; pass replace to overwrite", "env_has_entries", nethttp.StatusConflict)
	case errors.Is(err, service.ErrEnvApplyInProgress):
		writeError(w, logger, err, "an env apply is already running", "env_apply_in_progress", nethttp.StatusConflict)
	case errors.Is(err, service.ErrEnvNothingToApply):
		writeError(w, logger, err, "no pending env changes to apply", "env_nothing_to_apply", nethttp.StatusConflict)
	case errors.Is(err, service.ErrEnvConfirmationMismatch):
		writeError(w, logger, err, "confirmation must equal the service name", "confirmation_mismatch", nethttp.StatusBadRequest)
	case errors.Is(err, service.ErrEnvRevealRateLimited):
		w.Header().Set("Retry-After", revealRetryAfter)
		writeError(w, logger, err, "too many reveals, try again later", "env_reveal_rate_limited", nethttp.StatusTooManyRequests)
	case errors.Is(err, store.ErrEnvEntryNotFound):
		writeError(w, logger, err, "unknown env key", "not_found", nethttp.StatusNotFound)
	case errors.Is(err, store.ErrEnvRevisionNotFound):
		writeError(w, logger, err, "unknown env revision", "not_found", nethttp.StatusNotFound)
	case errors.Is(err, store.ErrEnvRunNotFound):
		writeError(w, logger, err, "unknown env run", "not_found", nethttp.StatusNotFound)
	default:
		writeServiceError(w, logger, err, message)
	}
}
