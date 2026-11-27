package http

import (
	"context"
	"errors"
	nethttp "net/http"

	"github.com/oralecarlangelo/touchgrass/internal/docker"
)

// handleOnboardingSuggest drafts a service row for one fleet container.
func (s *Server) handleOnboardingSuggest(w nethttp.ResponseWriter, r *nethttp.Request) {
	draft, err := s.onboarding.Suggest(r.Context(), r.URL.Query().Get("container"))
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, docker.ErrDaemonBackoff) {
			w.Header().Set("Retry-After", "5")
			writeError(
				w,
				s.logger,
				err,
				"Container discovery is busy. Retry in a few seconds.",
				"discovery_unavailable",
				nethttp.StatusServiceUnavailable,
			)

			return
		}
		writeServiceError(w, s.logger, err, "failed to suggest service")

		return
	}

	writeJSON(w, s.logger, nethttp.StatusOK, draft)
}
