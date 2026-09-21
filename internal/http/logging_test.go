package http

import (
	"context"
	"log/slog"
	"net/http/httptest"
	"testing"

	nethttp "net/http"
)

// captureHandler records log records for assertions.
type captureHandler struct {
	records *[]capturedRecord
}

// capturedRecord is one log record with its attributes.
type capturedRecord struct {
	message string
	attrs   map[string]any
}

func (h captureHandler) Enabled(_ context.Context, _ slog.Level) bool {
	return true
}

func (h captureHandler) Handle(_ context.Context, record slog.Record) error {
	captured := capturedRecord{message: record.Message, attrs: map[string]any{}}

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
}
