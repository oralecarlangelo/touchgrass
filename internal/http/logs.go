package http

import (
	"errors"
	"strconv"
	"time"

	nethttp "net/http"

	"github.com/oralecarlangelo/touchgrass/internal/model"
	"github.com/oralecarlangelo/touchgrass/internal/service"
)

const (
	defaultLogLimit   = 200
	defaultLogContext = 20
	maxLogContext     = 100
)

const (
	// logSourceContainers selects docker-collected lines (the default).
	logSourceContainers = "containers"
	// logSourceSDK selects structured SDK rows.
	logSourceSDK = "sdk"
)

// logsResponse is the log search payload.
type logsResponse struct {
	Lines []model.LogLine `json:"lines"`
}

// sdkLogsResponse is the SDK log search payload: the same "lines"
// envelope carrying structured rows.
type sdkLogsResponse struct {
	Lines []model.SDKLog `json:"lines"`
}

// handleLogs searches a service's collected log lines, or its SDK rows
// with source=sdk.
func (s *Server) handleLogs(w nethttp.ResponseWriter, r *nethttp.Request) {
	serviceID := r.URL.Query().Get("service_id")
	if serviceID == "" {
		writeError(w, s.logger, errMissingService, "service_id is required", "invalid_request", nethttp.StatusBadRequest)

		return
	}

	query := r.URL.Query()

	source, err := parseLogSource(query.Get("source"), query.Get("trace_id"), query.Get("stream"))
	if err != nil {
		writeError(w, s.logger, err, err.Error(), "invalid_request", nethttp.StatusBadRequest)

		return
	}

	after, err := parseLogBound(query.Get("after"))
	if err != nil {
		writeError(w, s.logger, err, "invalid after timestamp (want RFC3339)", "invalid_request", nethttp.StatusBadRequest)

		return
	}

	before, err := parseLogBound(query.Get("before"))
	if err != nil {
		writeError(w, s.logger, err, "invalid before timestamp (want RFC3339)", "invalid_request", nethttp.StatusBadRequest)

		return
	}

	limit, err := parseLimit(query.Get("limit"))
	if err != nil {
		writeError(w, s.logger, err, "invalid limit (want 1-1000)", "invalid_request", nethttp.StatusBadRequest)

		return
	}

	if limit == 0 {
		limit = defaultLogLimit
	}

	if source == logSourceSDK {
		rows, err := s.sdkLogs.SearchLogs(r.Context(), service.SDKLogSearch{
			ServiceID: serviceID, Query: query.Get("q"),
			Level: query.Get("level"), TraceID: query.Get("trace_id"),
			Since: after, Until: before, Limit: limit,
		})
		if err != nil {
			writeServiceError(w, s.logger, err, "failed to load logs")

			return
		}

		writeJSON(w, s.logger, nethttp.StatusOK, sdkLogsResponse{Lines: rows})

		return
	}

	lines, err := s.logs.Search(r.Context(), service.LogSearch{
		ServiceID: serviceID, Query: query.Get("q"),
		Stream: query.Get("stream"), Level: query.Get("level"),
		Since: after, Until: before, Limit: limit,
	})
	if err != nil {
		writeServiceError(w, s.logger, err, "failed to load logs")

		return
	}

	writeJSON(w, s.logger, nethttp.StatusOK, logsResponse{Lines: lines})
}

// Log source selector errors, reported verbatim to callers.
var (
	errInvalidSource         = errors.New("invalid source (want sdk or containers)")
	errTraceNeedsSDK         = errors.New("trace_id requires source=sdk")
	errStreamNeedsContainers = errors.New("stream requires source=containers")
)

// parseLogSource resolves the source selector, defaulting to
// containers, and rejects filters bound to the other source.
func parseLogSource(source, traceID, stream string) (string, error) {
	if source == "" {
		source = logSourceContainers
	}

	switch source {
	case logSourceContainers:
		if traceID != "" {
			return "", errTraceNeedsSDK
		}
	case logSourceSDK:
		if stream != "" {
			return "", errStreamNeedsContainers
		}
	default:
		return "", errInvalidSource
	}

	return source, nil
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
