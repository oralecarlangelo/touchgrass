package http

import nethttp "net/http"

func (s *Server) handleStorageUsage(w nethttp.ResponseWriter, r *nethttp.Request) {
	usage, err := s.system.StorageUsage(r.Context())
	if err != nil {
		writeServiceError(w, s.logger, err, "failed to inspect docker storage")
		return
	}

	writeJSON(w, s.logger, nethttp.StatusOK, usage)
}
