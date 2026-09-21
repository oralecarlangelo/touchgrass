package http

import (
	"errors"
	"log/slog"
	"strconv"
	"strings"
	"time"

	nethttp "net/http"

	"github.com/oralecarlangelo/touchgrass/internal/model"
	"github.com/oralecarlangelo/touchgrass/internal/service"
	"github.com/oralecarlangelo/touchgrass/internal/store"
)

// metricsResponse is the metrics payload.
type metricsResponse struct {
	Metrics []model.Metric `json:"metrics"`
}

// handleMetrics lists samples for a service.
func (s *Server) handleMetrics(w nethttp.ResponseWriter, r *nethttp.Request) {
	serviceID := r.PathValue("id")

	since, err := parseSince(r.URL.Query().Get("since"))
	if err != nil {
		writeError(w, s.logger, err, "invalid since timestamp (want RFC3339)", "invalid_request", nethttp.StatusBadRequest)

		return
	}

	limit, err := parseLimit(r.URL.Query().Get("limit"))
	if err != nil {
		writeError(w, s.logger, err, "invalid limit (want 1-1000)", "invalid_request", nethttp.StatusBadRequest)

		return
	}

	metrics, err := s.sampler.Metrics(r.Context(), serviceID, since, limit)
	if err != nil {
		writeServiceError(w, s.logger, err, "failed to load metrics")

		return
	}

	writeJSON(w, s.logger, nethttp.StatusOK, metricsResponse{Metrics: metrics})
}

// parseSince parses an optional RFC3339 lower bound.
func parseSince(raw string) (time.Time, error) {
	if raw == "" {
		return time.Time{}, nil
	}

	return time.Parse(time.RFC3339, raw)
}

// parseLimit parses an optional list bound; 0 selects the default.
func parseLimit(raw string) (int, error) {
	if raw == "" {
		return 0, nil
	}

	limit, err := strconv.Atoi(raw)
	if err != nil || limit < 1 || limit > 1000 {
		return 0, errors.New("limit out of range")
	}

	return limit, nil
}

// writeServiceError maps service errors to status codes.
func writeServiceError(
	w nethttp.ResponseWriter,
	logger *slog.Logger,
	err error,
	message string,
) {
	switch {
	case errors.Is(err, service.ErrInvalidInput):
		message := strings.TrimPrefix(err.Error(), service.ErrInvalidInput.Error()+": ")
		writeError(w, logger, err, message, "invalid_request", nethttp.StatusBadRequest)
	case errors.Is(err, service.ErrConflict):
		message := strings.TrimPrefix(err.Error(), service.ErrConflict.Error()+": ")
		writeError(w, logger, err, message, "conflict", nethttp.StatusConflict)
	case errors.Is(err, store.ErrServiceNotFound):
		writeError(w, logger, err, "unknown service", "not_found", nethttp.StatusNotFound)
	case errors.Is(err, store.ErrKeyNotFound):
		writeError(w, logger, err, "unknown api key", "not_found", nethttp.StatusNotFound)
	case errors.Is(err, store.ErrRuleNotFound):
		writeError(w, logger, err, "unknown alert rule", "not_found", nethttp.StatusNotFound)
	case errors.Is(err, store.ErrIssueNotFound):
		writeError(w, logger, err, "unknown issue", "not_found", nethttp.StatusNotFound)
	case errors.Is(err, store.ErrIssueRuleNotFound):
		writeError(w, logger, err, "unknown issue rule", "not_found", nethttp.StatusNotFound)
	case errors.Is(err, store.ErrLogLineNotFound):
		writeError(w, logger, err, "unknown log line", "not_found", nethttp.StatusNotFound)
	case errors.Is(err, service.ErrUnauthorized):
		writeError(w, logger, err, "bad api key", "unauthorized", nethttp.StatusUnauthorized)
	default:
		writeError(w, logger, err, message, "internal", nethttp.StatusInternalServerError)
	}
}
