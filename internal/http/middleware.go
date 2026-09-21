package http

import (
	"log/slog"
	"time"

	nethttp "net/http"
)

// WithLogging logs every request as one JSON record.
func WithLogging(logger *slog.Logger, next nethttp.Handler) nethttp.Handler {
	return nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		start := time.Now()

		wrapped := &statusWriter{ResponseWriter: w, status: nethttp.StatusOK}
		next.ServeHTTP(wrapped, r)

		logger.Info("request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", wrapped.status,
			"duration_ms", time.Since(start).Milliseconds(),
		)
	})
}

// WithSecurityHeaders sets baseline response headers.
func WithSecurityHeaders(next nethttp.Handler) nethttp.Handler {
	return nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		next.ServeHTTP(w, r)
	})
}

// statusWriter captures the response status code.
type statusWriter struct {
	nethttp.ResponseWriter
	status int
}

// WriteHeader records the status before delegating.
func (w *statusWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

// Flush passes streaming through to the underlying writer.
func (w *statusWriter) Flush() {
	if flusher, ok := w.ResponseWriter.(nethttp.Flusher); ok {
		flusher.Flush()
	}
}
