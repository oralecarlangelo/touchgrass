package http

import (
	"encoding/json"
	"errors"
	"time"

	nethttp "net/http"

	"github.com/oralecarlangelo/touchgrass/internal/model"
)

// errMissingService reports a missing service selector.
var errMissingService = errors.New("service selector is required")

// errInvalidLogContext reports an out-of-range log context count.
var errInvalidLogContext = errors.New("log context count out of range")

// errInvalidWindow reports an out-of-range log window in seconds.
var errInvalidWindow = errors.New("log window out of range")

// deploysResponse is the deploy history payload.
type deploysResponse struct {
	Deploys []model.Deploy `json:"deploys"`
}

// deployIDResponse is the record-creation payload.
type deployIDResponse struct {
	ID int64 `json:"id"`
}

// deployRequest is the manual-record body.
type deployRequest struct {
	SHA        string `json:"sha"`
	Actor      string `json:"actor"`
	Type       string `json:"type"`
	Outcome    string `json:"outcome"`
	Notes      string `json:"notes"`
	StartedAt  string `json:"started_at"`
	FinishedAt string `json:"finished_at"`
}

// handleDeploys lists history for a service.
func (s *Server) handleDeploys(w nethttp.ResponseWriter, r *nethttp.Request) {
	limit, err := parseLimit(r.URL.Query().Get("limit"))
	if err != nil {
		writeError(w, s.logger, err, "invalid limit (want 1-1000)", "invalid_request", nethttp.StatusBadRequest)

		return
	}

	deploys, err := s.deploys.History(r.Context(), r.PathValue("id"), limit)
	if err != nil {
		writeServiceError(w, s.logger, err, "failed to load deploy history")

		return
	}

	writeJSON(w, s.logger, nethttp.StatusOK, deploysResponse{Deploys: deploys})
}

// handleRecordDeploy stores a manually-run deploy.
func (s *Server) handleRecordDeploy(w nethttp.ResponseWriter, r *nethttp.Request) {
	var body deployRequest

	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, s.logger, err, "invalid JSON body", "invalid_request", nethttp.StatusBadRequest)

		return
	}

	record := model.DeployRecord{
		ServiceID: r.PathValue("id"),
		SHA:       body.SHA,
		Actor:     body.Actor,
		Type:      body.Type,
		Outcome:   body.Outcome,
		Notes:     body.Notes,
	}

	if record.Type == "" {
		record.Type = model.DeployManual
	}

	var err error

	record.StartedAt, err = parseOptionalTime(body.StartedAt)
	if err != nil {
		writeError(w, s.logger, err, "invalid started_at (want RFC3339)", "invalid_request", nethttp.StatusBadRequest)

		return
	}

	record.FinishedAt, err = parseOptionalTime(body.FinishedAt)
	if err != nil {
		writeError(w, s.logger, err, "invalid finished_at (want RFC3339)", "invalid_request", nethttp.StatusBadRequest)

		return
	}

	id, err := s.deploys.Record(r.Context(), record)
	if err != nil {
		writeServiceError(w, s.logger, err, "failed to record deploy")

		return
	}

	writeJSON(w, s.logger, nethttp.StatusCreated, deployIDResponse{ID: id})
}

// parseOptionalTime parses an optional RFC3339 timestamp.
func parseOptionalTime(raw string) (*time.Time, error) {
	if raw == "" {
		return nil, nil
	}

	parsed, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return nil, err
	}

	return &parsed, nil
}
