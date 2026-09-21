package http

import (
	"strconv"
	"time"

	nethttp "net/http"

	"github.com/oralecarlangelo/touchgrass/internal/model"
)

const (
	defaultLogLimit   = 200
	defaultLogContext = 20
	maxLogContext     = 100
)

// logsResponse is the log search payload.
type logsResponse struct {
	Lines []model.LogLine `json:"lines"`
}

// handleLogs searches a service's collected log lines.
func (s *Server) handleLogs(w nethttp.ResponseWriter, r *nethttp.Request) {
	serviceID := r.URL.Query().Get("service_id")
	if serviceID == "" {
		writeError(w, s.logger, errMissingService, "service_id is required", "invalid_request", nethttp.StatusBadRequest)

		return
	}

	after, err := parseLogBound(r.URL.Query().Get("after"))
	if err != nil {
		writeError(w, s.logger, err, "invalid after timestamp (want RFC3339)", "invalid_request", nethttp.StatusBadRequest)

		return
	}

	before, err := parseLogBound(r.URL.Query().Get("before"))
	if err != nil {
		writeError(w, s.logger, err, "invalid before timestamp (want RFC3339)", "invalid_request", nethttp.StatusBadRequest)

		return
	}

	limit, err := parseLimit(r.URL.Query().Get("limit"))
	if err != nil {
		writeError(w, s.logger, err, "invalid limit (want 1-1000)", "invalid_request", nethttp.StatusBadRequest)

		return
	}

	if limit == 0 {
		limit = defaultLogLimit
	}

	lines, err := s.logs.Search(r.Context(), serviceID, r.URL.Query().Get("q"), after, before, limit)
	if err != nil {
		writeServiceError(w, s.logger, err, "failed to load logs")

		return
	}

	writeJSON(w, s.logger, nethttp.StatusOK, logsResponse{Lines: lines})
}

// handleLogStats reports one service's stored lines plus backpressure losses.
func (s *Server) handleLogStats(w nethttp.ResponseWriter, r *nethttp.Request) {
	serviceID := r.URL.Query().Get("service_id")
	if serviceID == "" {
		writeError(w, s.logger, errMissingService, "service_id is required", "invalid_request", nethttp.StatusBadRequest)

		return
	}

	stats, err := s.logs.Stats(r.Context(), serviceID)
	if err != nil {
		writeServiceError(w, s.logger, err, "failed to load log stats")

		return
	}

	writeJSON(w, s.logger, nethttp.StatusOK, stats)
}

// handleLogContext returns one log line with its neighbors.
func (s *Server) handleLogContext(w nethttp.ResponseWriter, r *nethttp.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, s.logger, err, "invalid log line id", "invalid_request", nethttp.StatusBadRequest)

		return
	}

	before, err := parseLogContextCount(r.URL.Query().Get("before"), defaultLogContext)
	if err != nil {
		writeError(w, s.logger, err, "invalid before count (want 0-100)", "invalid_request", nethttp.StatusBadRequest)

		return
	}

	after, err := parseLogContextCount(r.URL.Query().Get("after"), defaultLogContext)
	if err != nil {
		writeError(w, s.logger, err, "invalid after count (want 0-100)", "invalid_request", nethttp.StatusBadRequest)

		return
	}

	got, err := s.logs.Context(r.Context(), id, before, after)
	if err != nil {
		writeServiceError(w, s.logger, err, "failed to load log context")

		return
	}

	writeJSON(w, s.logger, nethttp.StatusOK, got)
}

// parseLogBound parses an optional RFC3339 window bound.
func parseLogBound(raw string) (*time.Time, error) {
	if raw == "" {
		return nil, nil
	}

	bound, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return nil, err
	}

	utc := bound.UTC()

	return &utc, nil
}

// parseLogContextCount parses an optional neighbor count with a default.
func parseLogContextCount(raw string, fallback int) (int, error) {
	if raw == "" {
		return fallback, nil
	}

	count, err := strconv.Atoi(raw)
	if err != nil || count < 0 || count > maxLogContext {
		return 0, errInvalidLogContext
	}

	return count, nil
}
