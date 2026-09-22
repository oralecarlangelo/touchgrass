package http

import (
	"errors"
	"log/slog"
	"strconv"

	nethttp "net/http"

	"github.com/oralecarlangelo/touchgrass/internal/model"
)

// maxHistoryHours bounds the history window query.
const maxHistoryHours = 168

// imagesResponse is the daemon image list payload.
type imagesResponse struct {
	Images []model.ImageView `json:"images"`
}

// handleDockerImages lists local daemon images, newest first.
func (s *Server) handleDockerImages(w nethttp.ResponseWriter, r *nethttp.Request) {
	images, err := s.system.Images(r.Context())
	if err != nil {
		writeServiceError(w, s.logger, err, "failed to list images")

		return
	}

	writeJSON(w, s.logger, nethttp.StatusOK, imagesResponse{Images: images})
}

// handlePruneImages removes dangling images only.
func (s *Server) handlePruneImages(w nethttp.ResponseWriter, r *nethttp.Request) {
	report, err := s.system.PruneImages(r.Context())
	if err != nil {
		writeServiceError(w, s.logger, err, "failed to prune images")

		return
	}

	writeJSON(w, s.logger, nethttp.StatusOK, report)
}

// handleSystem returns the host + daemon + self snapshot.
func (s *Server) handleSystem(w nethttp.ResponseWriter, r *nethttp.Request) {
	snapshot, err := s.system.Snapshot(r.Context())
	if err != nil {
		writeServiceError(w, s.logger, err, "failed to load system snapshot")

		return
	}

	writeJSON(w, s.logger, nethttp.StatusOK, snapshot)
}

// fleetResponse is the latest-per-container fleet payload.
type fleetResponse struct {
	Containers []model.FleetContainer `json:"containers"`
}

// handleFleetContainers lists the latest sample per container, hottest
// CPU first.
func (s *Server) handleFleetContainers(w nethttp.ResponseWriter, r *nethttp.Request) {
	containers, err := s.sampler.FleetContainers(r.Context())
	if err != nil {
		writeServiceError(w, s.logger, err, "failed to load fleet containers")

		return
	}

	writeJSON(w, s.logger, nethttp.StatusOK, fleetResponse{Containers: containers})
}

// historyResponse is the bucket-averaged host history payload.
type historyResponse struct {
	Points []model.HistoryPoint `json:"points"`
}

// handleSystemHistory returns bucket-averaged host points for one
// metric over the trailing hours window.
func (s *Server) handleSystemHistory(w nethttp.ResponseWriter, r *nethttp.Request) {
	hours, ok := historyHours(w, s.logger, r)
	if !ok {
		return
	}

	points, err := s.sampler.HostHistory(r.Context(), r.URL.Query().Get("metric"), hours)
	if err != nil {
		writeServiceError(w, s.logger, err, "failed to load host history")

		return
	}

	writeJSON(w, s.logger, nethttp.StatusOK, historyResponse{Points: points})
}

// historyHours parses the history window query, writing 400 on failure.
func historyHours(w nethttp.ResponseWriter, logger *slog.Logger, r *nethttp.Request) (int, bool) {
	hours, err := parseHistoryHours(r.URL.Query().Get("hours"))
	if err != nil {
		writeError(w, logger, err, "invalid hours (want 1-168)", "invalid_request", nethttp.StatusBadRequest)

		return 0, false
	}

	return hours, true
}

// parseHistoryHours parses the history window; empty selects the
// default and values above the max clamp.
func parseHistoryHours(raw string) (int, error) {
	if raw == "" {
		return 0, nil
	}

	hours, err := strconv.Atoi(raw)
	if err != nil || hours < 1 {
		return 0, errors.New("hours out of range")
	}

	if hours > maxHistoryHours {
		return maxHistoryHours, nil
	}

	return hours, nil
}
