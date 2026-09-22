package http

import (
	nethttp "net/http"
)

// handleOnboardingSuggest drafts a service row for one fleet container.
func (s *Server) handleOnboardingSuggest(w nethttp.ResponseWriter, r *nethttp.Request) {
	draft, err := s.onboarding.Suggest(r.Context(), r.URL.Query().Get("container"))
	if err != nil {
		writeServiceError(w, s.logger, err, "failed to suggest service")

		return
	}

	writeJSON(w, s.logger, nethttp.StatusOK, draft)
}
