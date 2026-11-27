package http

import (
	"errors"
	"log/slog"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	nethttp "net/http"
)

// noFlushWriter implements ResponseWriter without Flush.
type noFlushWriter struct {
	header nethttp.Header
}

type errorFlushWriter struct {
	noFlushWriter
}

type headerWriter struct {
	noFlushWriter
	statuses []int
}

func (w errorFlushWriter) FlushError() error {
	return errors.New("flush failed")
}

func (w *headerWriter) WriteHeader(status int) {
	w.statuses = append(w.statuses, status)
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

func TestStatusWriterUnwrap(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	writer := &statusWriter{ResponseWriter: rec, status: nethttp.StatusOK}

	if got := writer.Unwrap(); got != nethttp.ResponseWriter(rec) {
		t.Errorf("Unwrap() = %v, want the wrapped recorder", got)
	}
}

func TestStatusWriterKeepsFirstFinalStatus(t *testing.T) {
	t.Parallel()

	underlying := &headerWriter{noFlushWriter: noFlushWriter{header: nethttp.Header{}}, statuses: []int{}}
	writer := &statusWriter{ResponseWriter: underlying}

	writer.WriteHeader(nethttp.StatusContinue)
	writer.WriteHeader(nethttp.StatusEarlyHints)
	writer.WriteHeader(nethttp.StatusAccepted)
	writer.WriteHeader(nethttp.StatusInternalServerError)

	if writer.Status() != nethttp.StatusAccepted {
		t.Errorf("Status() = %d, want %d", writer.Status(), nethttp.StatusAccepted)
	}

	expected := []int{nethttp.StatusContinue, nethttp.StatusEarlyHints, nethttp.StatusAccepted}
	if !slices.Equal(underlying.statuses, expected) {
		t.Errorf("statuses = %v, want %v", underlying.statuses, expected)
	}

	switching := &statusWriter{ResponseWriter: &headerWriter{
		noFlushWriter: noFlushWriter{header: nethttp.Header{}},
		statuses:      []int{},
	}}
	switching.WriteHeader(nethttp.StatusSwitchingProtocols)
	switching.WriteHeader(nethttp.StatusOK)

	if switching.Status() != nethttp.StatusSwitchingProtocols {
		t.Errorf("switching Status() = %d, want %d", switching.Status(), nethttp.StatusSwitchingProtocols)
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

	if plain.hasWriteError {
		t.Error("Flush() marked unsupported flushing as a write failure")
	}

	if err := nethttp.NewResponseController(plain).Flush(); !errors.Is(err, nethttp.ErrNotSupported) {
		t.Errorf("ResponseController.Flush() error = %v, want ErrNotSupported", err)
	}

	failing := &statusWriter{
		ResponseWriter: errorFlushWriter{
			noFlushWriter: noFlushWriter{header: nethttp.Header{}},
		},
	}
	if err := failing.FlushError(); err == nil {
		t.Error("FlushError() error = nil, want failure")
	}

	if !failing.hasWriteError || failing.Status() != nethttp.StatusOK {
		t.Errorf("writer = %+v, want failed write and implicit status 200", failing)
	}
}

func TestRequestLogLevel(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name                  string
		responseStartDuration time.Duration
		hasWriteError         bool
		expected              slog.Level
	}{
		{name: "ordinary request", responseStartDuration: time.Second, expected: slog.LevelInfo},
		{name: "slow response start", responseStartDuration: writeTimeout, expected: slog.LevelWarn},
		{name: "long stream with prompt response", responseStartDuration: time.Millisecond, expected: slog.LevelInfo},
		{name: "failed write", hasWriteError: true, expected: slog.LevelWarn},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := requestLogLevel(tt.responseStartDuration, tt.hasWriteError)
			if got != tt.expected {
				t.Errorf("requestLogLevel() = %v, want %v", got, tt.expected)
			}
		})
	}
}
