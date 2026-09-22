package http

import (
	nethttp "net/http"

	"github.com/oralecarlangelo/touchgrass/internal/model"
)

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
