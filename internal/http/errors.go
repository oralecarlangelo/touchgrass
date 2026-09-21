package http

import (
	"context"
	"encoding/json"
	"log/slog"

	nethttp "net/http"
)

// errorResponse is the user-safe API error envelope.
type errorResponse struct {
	Error string `json:"error"`
	Code  string `json:"code"`
}

// writeJSON encodes payload with a JSON content type.
func writeJSON(w nethttp.ResponseWriter, logger *slog.Logger, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	if err := json.NewEncoder(w).Encode(payload); err != nil {
		logger.Error("encoding response", "error", err)
	}
}

// decodeBody decodes a JSON request body, writing 400 on failure.
func decodeBody[T any](
	w nethttp.ResponseWriter,
	logger *slog.Logger,
	r *nethttp.Request,
) (T, bool) {
	var body T

	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, logger, err, "invalid JSON body", "invalid_request", nethttp.StatusBadRequest)

		return body, false
	}

	return body, true
}

// createJSON decodes a body, runs create, and writes the result as 201.
func createJSON[Body, Result any](
	w nethttp.ResponseWriter,
	logger *slog.Logger,
	r *nethttp.Request,
	create func(context.Context, Body) (Result, error),
	failMessage string,
) {
	body, ok := decodeBody[Body](w, logger, r)
	if !ok {
		return
	}

	result, err := create(r.Context(), body)
	if err != nil {
		writeServiceError(w, logger, err, failMessage)

		return
	}

	writeJSON(w, logger, nethttp.StatusCreated, result)
}

// writeError logs the technical error and writes a user-safe envelope.
func writeError(
	w nethttp.ResponseWriter,
	logger *slog.Logger,
	err error,
	message, code string,
	status int,
) {
	logger.Error("request failed", "error", err, "code", code)
	writeJSON(w, logger, status, errorResponse{Error: message, Code: code})
}
