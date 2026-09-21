package http

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http/httptest"
	"testing"

	nethttp "net/http"
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
