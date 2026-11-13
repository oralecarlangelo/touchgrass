package http

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http/httptest"
	"testing"

	nethttp "net/http"

	"github.com/oralecarlangelo/touchgrass/internal/docker"
)

func TestWriteError(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()

	writeError(rec, slog.New(slog.DiscardHandler), errors.New("boom"), "failed cleanly", "test_code", nethttp.StatusBadGateway)

	res := rec.Result()

	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("reading body: %v", err)
	}

	if err := res.Body.Close(); err != nil {
		t.Fatalf("closing body: %v", err)
	}

	if res.StatusCode != nethttp.StatusBadGateway {
		t.Fatalf("status = %d, want 502", res.StatusCode)
	}

	var got errorResponse

	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decoding body: %v", err)
	}

	if got.Error != "failed cleanly" || got.Code != "test_code" {
		t.Errorf("envelope = %+v, want message and code", got)
	}
}

func TestWriteJSON(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()

	writeJSON(rec, slog.New(slog.DiscardHandler), nethttp.StatusCreated, map[string]string{"a": "b"})

	res := rec.Result()

	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("reading body: %v", err)
	}

	if err := res.Body.Close(); err != nil {
		t.Fatalf("closing body: %v", err)
	}

	if res.StatusCode != nethttp.StatusCreated {
		t.Fatalf("status = %d, want 201", res.StatusCode)
	}

	if content := res.Header.Get("Content-Type"); content != "application/json" {
		t.Errorf("content-type = %q, want application/json", content)
	}

	var got map[string]string

	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decoding body: %v", err)
	}

	if got["a"] != "b" {
		t.Errorf("body = %v, want a=b", got)
	}
}

// An engine that is busy or paused is the engine's state, not a server
// fault: the client is told to retry, with one stable code.
func TestWriteServiceErrorReportsAnUnavailableEngine(t *testing.T) {
	t.Parallel()

	tests := map[string]error{
		"engine busy":   fmt.Errorf("listing images: %w", docker.ErrEngineBusy),
		"daemon paused": fmt.Errorf("listing images: %w", docker.ErrDaemonBackoff),
	}

	for name, engineErr := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			rec := httptest.NewRecorder()

			writeServiceError(rec, slog.New(slog.DiscardHandler), engineErr, "failed to list images")

			if rec.Code != nethttp.StatusServiceUnavailable {
				t.Fatalf("status = %d, want 503", rec.Code)
			}

			var got errorResponse

			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatalf("decoding body: %v", err)
			}

			if got.Code != "engine_unavailable" {
				t.Errorf("code = %q, want engine_unavailable", got.Code)
			}
		})
	}
}

// Reads are marked interactive so their engine calls fail fast; writes
// are not, because their context can outlive the request.
func TestOnlyReadRoutesAreInteractive(t *testing.T) {
	t.Parallel()

	tests := map[string]bool{
		nethttp.MethodGet:    true,
		nethttp.MethodPost:   false,
		nethttp.MethodPut:    false,
		nethttp.MethodDelete: false,
	}

	for method, want := range tests {
		t.Run(method, func(t *testing.T) {
			t.Parallel()

			var got bool

			handler := (&Server{}).routeHandler(route{
				method: method,
				kind:   guardPublic,
				handler: func(_ nethttp.ResponseWriter, r *nethttp.Request) {
					got = docker.IsInteractive(r.Context())
				},
			})

			handler(httptest.NewRecorder(), httptest.NewRequestWithContext(t.Context(), method, "/api/anything", nil))

			if got != want {
				t.Errorf("interactive = %t, want %t", got, want)
			}
		})
	}
}
