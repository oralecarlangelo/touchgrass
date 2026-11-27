package http

import (
	"context"
	"crypto/rand"
	"errors"
	"log/slog"
	"strings"
	"time"

	nethttp "net/http"
)

// WithLogging logs every request as one JSON record.
func WithLogging(logger *slog.Logger, next nethttp.Handler) nethttp.Handler {
	return nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		start := time.Now()
		requestID := rand.Text()
		w.Header().Set("X-Request-ID", requestID)

		wrapped := &statusWriter{ResponseWriter: w}
		next.ServeHTTP(wrapped, r)

		duration := time.Since(start)

		responseStartDuration := duration
		if !wrapped.firstWriteAt.IsZero() {
			responseStartDuration = wrapped.firstWriteAt.Sub(start)
		}

		isCanceled := errors.Is(r.Context().Err(), context.Canceled)
		isDeadlineExceeded := errors.Is(r.Context().Err(), context.DeadlineExceeded)

		logger.Log(
			r.Context(),
			requestLogLevel(responseStartDuration, wrapped.hasWriteError),
			"request",
			"request_id", requestID,
			"method", r.Method,
			"path", safeRequestPath(r.URL.Path),
			"route", wrapped.routeTemplate,
			"status", wrapped.Status(),
			"duration_ms", duration.Milliseconds(),
			"response_bytes", wrapped.bytes,
			"write_error", wrapped.hasWriteError,
			"canceled", isCanceled,
			"deadline_exceeded", isDeadlineExceeded,
		)
	})
}

// withLogRoute records the static template supplied by the route registry.
func withLogRoute(template string, next nethttp.HandlerFunc) nethttp.HandlerFunc {
	return func(w nethttp.ResponseWriter, r *nethttp.Request) {
		if response, ok := w.(*statusWriter); ok {
			response.routeTemplate = template
		}

		next(w, r)
	}
}

func requestLogLevel(responseStartDuration time.Duration, hasWriteError bool) slog.Level {
	if responseStartDuration >= writeTimeout || hasWriteError {
		return slog.LevelWarn
	}

	return slog.LevelInfo
}

var loggedAPIFamilies = map[string]bool{
	"alerts": true, "audit": true, "auth": true, "collectors": true,
	"databases": true, "docker": true, "health": true, "images": true,
	"ingest": true, "issues": true, "keys": true, "logs": true,
	"notifications": true, "onboarding": true, "performance": true,
	"services": true, "storage": true, "system": true, "users": true,
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
	routeTemplate string
	status        int
	bytes         int64
	hasWriteError bool
	wroteHeader   bool
	firstWriteAt  time.Time
}

// WriteHeader records the status before delegating.
func (w *statusWriter) WriteHeader(status int) {
	if w.wroteHeader {
		return
	}

	if status >= 100 && status < 200 && status != nethttp.StatusSwitchingProtocols {
		w.ResponseWriter.WriteHeader(status)

		return
	}

	w.status = status
	w.wroteHeader = true
	w.firstWriteAt = time.Now()
	w.ResponseWriter.WriteHeader(status)
}

// Write records bytes accepted by the underlying writer and whether it failed.
func (w *statusWriter) Write(data []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(nethttp.StatusOK)
	}

	n, err := w.ResponseWriter.Write(data)
	w.bytes += int64(n)
	w.hasWriteError = w.hasWriteError || err != nil

	return n, err
}

// Status returns the response status, including the implicit 200 when the
// handler did not write a header or body.
func (w *statusWriter) Status() int {
	if !w.wroteHeader {
		return nethttp.StatusOK
	}

	return w.status
}

// Unwrap exposes the underlying writer so http.ResponseController can
// reach the connection, which the event stream needs to move its write
// deadline.
func (w *statusWriter) Unwrap() nethttp.ResponseWriter {
	return w.ResponseWriter
}

// Flush passes streaming through to the underlying writer.
func (w *statusWriter) Flush() {
	if err := w.flush(); err != nil && !errors.Is(err, nethttp.ErrNotSupported) {
		w.hasWriteError = true
	}
}

// FlushError lets ResponseController preserve flush failures when the
// underlying writer exposes them.
func (w *statusWriter) FlushError() error {
	err := w.flush()
	w.hasWriteError = w.hasWriteError || (err != nil && !errors.Is(err, nethttp.ErrNotSupported))

	return err
}

func (w *statusWriter) flush() error {
	if !w.wroteHeader {
		w.WriteHeader(nethttp.StatusOK)
	}

	if flusher, ok := w.ResponseWriter.(interface{ FlushError() error }); ok {
		return flusher.FlushError()
	}

	if flusher, ok := w.ResponseWriter.(nethttp.Flusher); ok {
		flusher.Flush()

		return nil
	}

	return nethttp.ErrNotSupported
}
