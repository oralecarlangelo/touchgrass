package http

import (
	"log/slog"
	"strings"
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
			"path", safeRequestPath(r.URL.Path),
			"status", wrapped.status,
			"duration_ms", time.Since(start).Milliseconds(),
		)
	})
}

var loggedAPIFamilies = map[string]bool{
	"alerts": true, "audit": true, "auth": true, "collectors": true,
	"databases": true, "docker": true, "health": true, "images": true,
	"ingest": true, "issues": true, "keys": true, "logs": true,
	"notifications": true, "onboarding": true, "performance": true,
	"services": true, "system": true, "users": true,
}

// safeRequestPath keeps request logging useful without copying attacker- or
// user-controlled path segments. Query strings are never considered.
func safeRequestPath(path string) string {
	if path == "/api/health" {
		return path
	}

	if rest, ok := strings.CutPrefix(path, "/api/"); ok {
		family, _, _ := strings.Cut(rest, "/")

		if loggedAPIFamilies[family] {
			return "/api/" + family
		}

		return "/api/other"
	}

	if path == "/docs" || strings.HasPrefix(path, "/docs/") {
		return "docs"
	}

	return "web"
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
