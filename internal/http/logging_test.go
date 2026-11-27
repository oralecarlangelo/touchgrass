package http

import (
	"context"
	"errors"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"

	nethttp "net/http"
)

// captureHandler records log records for assertions.
type captureHandler struct {
	records *[]capturedRecord
}

type failingResponseWriter struct {
	header nethttp.Header
	secret string
}

func (w *failingResponseWriter) Header() nethttp.Header {
	return w.header
}

func (w *failingResponseWriter) Write(data []byte) (int, error) {
	return min(3, len(data)), errors.New(w.secret)
}

func (w *failingResponseWriter) WriteHeader(_ int) {}

func TestWithLoggingRedactsUntrustedPaths(t *testing.T) {
	t.Parallel()

	records := []capturedRecord{}
	logger := slog.New(captureHandler{records: &records})
	next := nethttp.HandlerFunc(func(w nethttp.ResponseWriter, _ *nethttp.Request) {
		w.WriteHeader(nethttp.StatusNotFound)
	})
	secret := "privacy-soak-alice@example.invalid"
	req := httptest.NewRequestWithContext(t.Context(), nethttp.MethodGet, "/privacy/"+secret+"?token=secret", nil)

	WithLogging(logger, next).ServeHTTP(httptest.NewRecorder(), req)

	if len(records) != 1 {
		t.Fatalf("records = %d, want 1", len(records))
	}

	path, _ := records[0].attrs["path"].(string)

	if strings.Contains(path, secret) || strings.Contains(path, "token") {
		t.Fatalf("logged path %q contains sensitive request data", path)
	}

	if path != "web" {
		t.Fatalf("logged path = %q, want web", path)
	}
}

// capturedRecord is one log record with its attributes.
type capturedRecord struct {
	message string
	level   slog.Level
	attrs   map[string]any
}

func (h captureHandler) Enabled(_ context.Context, _ slog.Level) bool {
	return true
}

func (h captureHandler) Handle(_ context.Context, record slog.Record) error {
	captured := capturedRecord{message: record.Message, level: record.Level, attrs: map[string]any{}}

	record.Attrs(func(attr slog.Attr) bool {
		captured.attrs[attr.Key] = attr.Value.Any()

		return true
	})

	*h.records = append(*h.records, captured)

	return nil
}

func (h captureHandler) WithAttrs(_ []slog.Attr) slog.Handler {
	return h
}

func (h captureHandler) WithGroup(_ string) slog.Handler {
	return h
}

func TestWithLogging(t *testing.T) {
	t.Parallel()

	records := []capturedRecord{}
	logger := slog.New(captureHandler{records: &records})

	next := nethttp.HandlerFunc(func(w nethttp.ResponseWriter, _ *nethttp.Request) {
		w.WriteHeader(nethttp.StatusTeapot)
		_, _ = w.Write([]byte("short and stout"))
	})

	req := httptest.NewRequestWithContext(t.Context(), nethttp.MethodGet, testHealthPath, nil)
	rec := httptest.NewRecorder()

	WithLogging(logger, next).ServeHTTP(rec, req)

	res := rec.Result()
	if err := res.Body.Close(); err != nil {
		t.Fatalf("closing body: %v", err)
	}

	if res.StatusCode != nethttp.StatusTeapot {
		t.Fatalf("status = %d, want 418", res.StatusCode)
	}

	if len(records) != 1 {
		t.Fatalf("records = %d, want 1", len(records))
	}

	got := records[0]

	if got.message != "request" {
		t.Errorf("message = %q, want request", got.message)
	}

	if got.attrs["method"] != "GET" || got.attrs["path"] != testHealthPath {
		t.Errorf("attrs = %v, want method GET and path /api/health", got.attrs)
	}

	if got.attrs["status"] != int64(418) {
		t.Errorf("status attr = %v, want 418", got.attrs["status"])
	}

	if _, ok := got.attrs["duration_ms"]; !ok {
		t.Errorf("attrs = %v, want duration_ms", got.attrs)
	}

	requestID, ok := got.attrs["request_id"].(string)
	if !ok || requestID == "" {
		t.Errorf("request_id = %v, want a generated value", got.attrs["request_id"])
	}

	if headerID := rec.Header().Get("X-Request-ID"); headerID != requestID {
		t.Errorf("X-Request-ID = %q, want %q", headerID, requestID)
	}

	if got.attrs["response_bytes"] != int64(len("short and stout")) {
		t.Errorf("response_bytes = %v, want %d", got.attrs["response_bytes"], len("short and stout"))
	}

	if got.attrs["write_error"] != false || got.attrs["canceled"] != false {
		t.Errorf("attrs = %v, want successful write and active context", got.attrs)
	}
}

func TestWithLoggingReportsFailedWritesWithoutErrorDetails(t *testing.T) {
	t.Parallel()

	const secret = "connection error containing private upstream details"

	records := []capturedRecord{}
	logger := slog.New(captureHandler{records: &records})
	next := nethttp.HandlerFunc(func(w nethttp.ResponseWriter, _ *nethttp.Request) {
		w.WriteHeader(nethttp.StatusAccepted)

		if _, err := w.Write([]byte("payload")); err == nil {
			t.Error("Write() error = nil, want failure")
		}

		w.WriteHeader(nethttp.StatusInternalServerError)
	})
	w := &failingResponseWriter{header: nethttp.Header{}, secret: secret}
	req := httptest.NewRequestWithContext(t.Context(), nethttp.MethodGet, testHealthPath, nil)
	req.Header.Set("X-Request-ID", "attacker-controlled-id")

	WithLogging(logger, next).ServeHTTP(w, req)

	if len(records) != 1 {
		t.Fatalf("records = %d, want 1", len(records))
	}

	got := records[0]
	if got.level != slog.LevelWarn {
		t.Errorf("level = %v, want warn", got.level)
	}

	if got.attrs["status"] != int64(nethttp.StatusAccepted) {
		t.Errorf("status = %v, want first status %d", got.attrs["status"], nethttp.StatusAccepted)
	}

	if got.attrs["response_bytes"] != int64(3) || got.attrs["write_error"] != true {
		t.Errorf("attrs = %v, want three bytes and a write error signal", got.attrs)
	}

	if strings.Contains(attrsText(got.attrs), secret) {
		t.Fatalf("attrs = %v, want no raw write error", got.attrs)
	}

	requestID, _ := got.attrs["request_id"].(string)
	if requestID == "" || requestID == req.Header.Get("X-Request-ID") || w.header.Get("X-Request-ID") != requestID {
		t.Errorf("request_id = %q, response = %q, want safe generated correlation", requestID, w.header.Get("X-Request-ID"))
	}
}

func TestWithLoggingReportsCancellation(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	records := []capturedRecord{}
	logger := slog.New(captureHandler{records: &records})
	next := nethttp.HandlerFunc(func(_ nethttp.ResponseWriter, _ *nethttp.Request) {
		cancel()
	})
	req := httptest.NewRequestWithContext(ctx, nethttp.MethodGet, testHealthPath, nil)

	WithLogging(logger, next).ServeHTTP(httptest.NewRecorder(), req)

	if len(records) != 1 {
		t.Fatalf("records = %d, want 1", len(records))
	}

	if records[0].level != slog.LevelInfo || records[0].attrs["canceled"] != true {
		t.Errorf("record = %+v, want info with cancellation signal", records[0])
	}
}

func attrsText(attrs map[string]any) string {
	var text strings.Builder

	for key, value := range attrs {
		text.WriteString(key)
		text.WriteString("=")
		text.WriteString(slog.AnyValue(value).String())
	}

	return text.String()
}
