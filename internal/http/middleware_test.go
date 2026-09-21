package http

import (
	"net/http/httptest"
	"testing"

	nethttp "net/http"
)

// noFlushWriter implements ResponseWriter without Flush.
type noFlushWriter struct {
	header nethttp.Header
}

func (w noFlushWriter) Header() nethttp.Header {
	return w.header
}

func (w noFlushWriter) Write(data []byte) (int, error) {
	return len(data), nil
}

func (w noFlushWriter) WriteHeader(_ int) {}

func TestWithSecurityHeaders(t *testing.T) {
	t.Parallel()

	next := nethttp.HandlerFunc(func(w nethttp.ResponseWriter, _ *nethttp.Request) {
		w.WriteHeader(nethttp.StatusNoContent)
	})

	req := httptest.NewRequestWithContext(t.Context(), nethttp.MethodGet, "/", nil)
	rec := httptest.NewRecorder()

	WithSecurityHeaders(next).ServeHTTP(rec, req)

	res := rec.Result()

	if err := res.Body.Close(); err != nil {
		t.Fatalf("closing body: %v", err)
	}

	if got := res.Header.Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q, want nosniff", got)
	}
}

func TestStatusWriterFlush(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	writer := &statusWriter{ResponseWriter: rec, status: nethttp.StatusOK}
	writer.Flush()

	if !rec.Flushed {
		t.Error("Flush() did not pass through to the recorder")
	}

	plain := &statusWriter{ResponseWriter: noFlushWriter{header: nethttp.Header{}}, status: nethttp.StatusOK}
	plain.Flush()
}
