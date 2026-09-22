package http

import (
	"encoding/json"
	"errors"
	"strings"

	nethttp "net/http"

	"github.com/oralecarlangelo/touchgrass/internal/model"
	"github.com/oralecarlangelo/touchgrass/internal/service"
)

const (
	// bearerScheme prefixes Authorization credentials on log ingest.
	bearerScheme = "Bearer "
	// maxLogIngestBytes caps one log batch body. A full batch of
	// 1000 items dwarfs the 1MB single-report cap, so batches get
	// headroom while staying bounded.
	maxLogIngestBytes = 10 << 20
)

// logIngestResponse reports the stored batch size.
type logIngestResponse struct {
	Accepted int `json:"accepted"`
}

// logItemErrorResponse reports which batch item failed validation.
// The message is static; payloads are never echoed.
type logItemErrorResponse struct {
	Error string `json:"error"`
	Index int    `json:"index"`
}

// handleIngestLogs stores one SDK log batch. The route is public; the
// credential is a Bearer tg_ key (the same keys as error ingest),
// with the X-Touchgrass-Key header accepted as an alias. Payloads are
// never logged: rejections log the key prefix at most, and validation
// errors carry static messages.
func (s *Server) handleIngestLogs(w nethttp.ResponseWriter, r *nethttp.Request) {
	presented := bearerToken(r.Header.Get("Authorization"))
	if presented == "" {
		presented = r.Header.Get(ingestKeyHeader)
	}

	if presented == "" {
		writeError(w, s.logger, errMissingIngestKey, "api key required", "unauthorized", nethttp.StatusUnauthorized)

		return
	}

	r.Body = nethttp.MaxBytesReader(w, r.Body, maxLogIngestBytes)

	var batch model.SDKLogBatch

	if err := json.NewDecoder(r.Body).Decode(&batch); err != nil {
		if _, ok := errors.AsType[*nethttp.MaxBytesError](err); ok {
			writeError(w, s.logger, err, "log batch exceeds 10MB", "too_large", nethttp.StatusRequestEntityTooLarge)

			return
		}

		writeError(w, s.logger, err, "invalid JSON body", "invalid_request", nethttp.StatusBadRequest)

		return
	}

	accepted, err := s.sdkLogs.IngestLogs(r.Context(), presented, batch)
	if err != nil {
		if errors.Is(err, service.ErrUnauthorized) {
			s.logger.Warn("log ingest rejected", "key_prefix", service.KeyPrefixForLog(presented))
		}

		if itemErr, ok := errors.AsType[*service.SDKLogItemError](err); ok {
			s.logger.Error("request failed", "error", err, "code", "invalid_request")
			writeJSON(w, s.logger, nethttp.StatusBadRequest, logItemErrorResponse{
				Error: itemErr.Message,
				Index: itemErr.Index,
			})

			return
		}

		writeServiceError(w, s.logger, err, "failed to store log batch")

		return
	}

	writeJSON(w, s.logger, nethttp.StatusAccepted, logIngestResponse{Accepted: accepted})
}

// bearerToken extracts a Bearer credential, or "" when absent.
func bearerToken(header string) string {
	token, ok := strings.CutPrefix(header, bearerScheme)
	if !ok {
		return ""
	}

	return token
}
