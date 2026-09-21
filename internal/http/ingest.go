package http

import (
	"encoding/json"
	"errors"
	"strconv"

	nethttp "net/http"

	"github.com/oralecarlangelo/touchgrass/internal/model"
	"github.com/oralecarlangelo/touchgrass/internal/service"
)

const (
	// ingestKeyHeader carries the per-service ingestion key.
	ingestKeyHeader = "X-Touchgrass-Key"
	// maxIngestBytes caps one report body at write time.
	maxIngestBytes = 1 << 20
)

// errMissingIngestKey reports a request without credentials.
var errMissingIngestKey = errors.New("missing api key")

// ingestResponse reports whether a report was sampled in.
type ingestResponse struct {
	Sampled bool `json:"sampled"`
}

// keyRequest is the key creation body.
type keyRequest struct {
	ServiceID  string   `json:"service_id"`
	SampleRate *float64 `json:"sample_rate"`
}

// createdKeyResponse carries the one-time key plaintext.
type createdKeyResponse struct {
	ID         int64   `json:"id"`
	ServiceID  string  `json:"service_id"`
	Key        string  `json:"key"`
	KeyPrefix  string  `json:"key_prefix"`
	SampleRate float64 `json:"sample_rate"`
}

// keysResponse lists keys without hashes.
type keysResponse struct {
	Keys []model.APIKey `json:"keys"`
}

// handleIngest stores one SDK report. The route is public; the key header
// is the credential. Payloads are never logged: rejections log the key
// prefix at most, and validation errors carry static messages.
func (s *Server) handleIngest(w nethttp.ResponseWriter, r *nethttp.Request) {
	presented := r.Header.Get(ingestKeyHeader)
	if presented == "" {
		writeError(w, s.logger, errMissingIngestKey, "api key required", "unauthorized", nethttp.StatusUnauthorized)

		return
	}

	r.Body = nethttp.MaxBytesReader(w, r.Body, maxIngestBytes)

	var report model.IngestReport

	if err := json.NewDecoder(r.Body).Decode(&report); err != nil {
		if _, ok := errors.AsType[*nethttp.MaxBytesError](err); ok {
			writeError(w, s.logger, err, "report exceeds 1MB", "too_large", nethttp.StatusRequestEntityTooLarge)

			return
		}

		writeError(w, s.logger, err, "invalid JSON body", "invalid_request", nethttp.StatusBadRequest)

		return
	}

	sampled, err := s.ingestor.Ingest(r.Context(), presented, report)
	if err != nil {
		if errors.Is(err, service.ErrUnauthorized) {
			s.logger.Warn("ingest rejected", "key_prefix", service.KeyPrefixForLog(presented))
		}

		writeServiceError(w, s.logger, err, "failed to store report")

		return
	}

	writeJSON(w, s.logger, nethttp.StatusAccepted, ingestResponse{Sampled: sampled})
}

// handleCreateKey mints one ingestion key, returning its plaintext once.
func (s *Server) handleCreateKey(w nethttp.ResponseWriter, r *nethttp.Request) {
	body, ok := decodeBody[keyRequest](w, s.logger, r)
	if !ok {
		return
	}

	if body.SampleRate == nil {
		writeError(w, s.logger, errMissingService, "sample_rate is required", "invalid_request", nethttp.StatusBadRequest)

		return
	}

	created, err := s.ingestor.CreateKey(r.Context(), body.ServiceID, *body.SampleRate)
	if err != nil {
		writeServiceError(w, s.logger, err, "failed to create api key")

		return
	}

	writeJSON(w, s.logger, nethttp.StatusCreated, createdKeyResponse{
		ID:         created.Key.ID,
		ServiceID:  created.Key.ServiceID,
		Key:        created.Plaintext,
		KeyPrefix:  created.Key.KeyPrefix,
		SampleRate: created.Key.SampleRate,
	})
}

// handleKeys lists a service's keys without hashes.
func (s *Server) handleKeys(w nethttp.ResponseWriter, r *nethttp.Request) {
	serviceID := r.URL.Query().Get("service_id")
	if serviceID == "" {
		writeError(w, s.logger, errMissingService, "service_id is required", "invalid_request", nethttp.StatusBadRequest)

		return
	}

	keys, err := s.ingestor.Keys(r.Context(), serviceID)
	if err != nil {
		writeServiceError(w, s.logger, err, "failed to load api keys")

		return
	}

	writeJSON(w, s.logger, nethttp.StatusOK, keysResponse{Keys: keys})
}

// handleRevokeKey stamps one key revoked.
func (s *Server) handleRevokeKey(w nethttp.ResponseWriter, r *nethttp.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, s.logger, err, "invalid key id", "invalid_request", nethttp.StatusBadRequest)

		return
	}

	if err := s.ingestor.RevokeKey(r.Context(), id); err != nil {
		writeServiceError(w, s.logger, err, "failed to revoke api key")

		return
	}

	w.WriteHeader(nethttp.StatusNoContent)
}
