package http

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	nethttp "net/http"
)

// errTestDown is a stubbed backend failure.
var errTestDown = errors.New("backend down")

func TestSPA(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.DiscardHandler)
	dist := fstest.MapFS{
		"index.html":    {Data: []byte("<p>app</p>")},
		"assets/app.js": {Data: []byte("console.log(1)")},
	}

	tests := []struct {
		name            string
		target          string
		expectedStatus  int
		expectedBody    string
		expectedContent string
	}{
		{
			name:            "root serves index",
			target:          "/",
			expectedStatus:  nethttp.StatusOK,
			expectedBody:    "<p>app</p>",
			expectedContent: "text/html",
		},
		{
			name:            "deep route falls back to index",
			target:          "/services/tn-api",
			expectedStatus:  nethttp.StatusOK,
			expectedBody:    "<p>app</p>",
			expectedContent: "text/html",
		},
		{
			name:            "asset served with js content type",
			target:          "/assets/app.js",
			expectedStatus:  nethttp.StatusOK,
			expectedBody:    "console.log(1)",
			expectedContent: "javascript",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			req := httptest.NewRequestWithContext(t.Context(), nethttp.MethodGet, tt.target, nil)
			rec := httptest.NewRecorder()

			SPA(logger, dist).ServeHTTP(rec, req)

			res := rec.Result()

			body, err := io.ReadAll(res.Body)
			if err != nil {
				t.Fatalf("reading body: %v", err)
			}

			if err := res.Body.Close(); err != nil {
				t.Fatalf("closing body: %v", err)
			}

			if res.StatusCode != tt.expectedStatus {
				t.Fatalf("status = %d, want %d", res.StatusCode, tt.expectedStatus)
			}

			if string(body) != tt.expectedBody {
				t.Errorf("body = %q, want %q", body, tt.expectedBody)
			}

			if content := res.Header.Get("Content-Type"); !strings.Contains(content, tt.expectedContent) {
				t.Errorf("content-type = %q, want containing %q", content, tt.expectedContent)
			}
		})
	}
}

func TestSPAUnbuilt(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.DiscardHandler)

	req := httptest.NewRequestWithContext(t.Context(), nethttp.MethodGet, "/", nil)
	rec := httptest.NewRecorder()

	SPA(logger, fstest.MapFS{}).ServeHTTP(rec, req)

	res := rec.Result()

	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("reading body: %v", err)
	}

	if err := res.Body.Close(); err != nil {
		t.Fatalf("closing body: %v", err)
	}

	if res.StatusCode != nethttp.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", res.StatusCode)
	}

	var got errorResponse

	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decoding body: %v", err)
	}

	if got.Code != "spa_not_built" {
		t.Errorf("code = %q, want spa_not_built", got.Code)
	}
}

// roundTripFunc stubs backend transport without sockets.
type roundTripFunc func(*nethttp.Request) (*nethttp.Response, error)

func (f roundTripFunc) RoundTrip(req *nethttp.Request) (*nethttp.Response, error) {
	return f(req)
}

func TestDevProxy(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.DiscardHandler)
	transport := roundTripFunc(func(_ *nethttp.Request) (*nethttp.Response, error) {
		return &nethttp.Response{
			StatusCode:    nethttp.StatusOK,
			Body:          io.NopCloser(strings.NewReader("vite")),
			Header:        nethttp.Header{},
			ContentLength: 4,
		}, nil
	})

	req := httptest.NewRequestWithContext(t.Context(), nethttp.MethodGet, "/", nil)
	rec := httptest.NewRecorder()

	devProxy(logger, "http://vite", transport).ServeHTTP(rec, req)

	res := rec.Result()

	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("reading body: %v", err)
	}

	if err := res.Body.Close(); err != nil {
		t.Fatalf("closing body: %v", err)
	}

	if res.StatusCode != nethttp.StatusOK || string(body) != "vite" {
		t.Errorf("proxied = (%d, %q), want (200, vite)", res.StatusCode, body)
	}
}

func TestDevProxyUnreachable(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.DiscardHandler)
	transport := roundTripFunc(func(_ *nethttp.Request) (*nethttp.Response, error) {
		return nil, errTestDown
	})

	req := httptest.NewRequestWithContext(t.Context(), nethttp.MethodGet, "/", nil)
	rec := httptest.NewRecorder()

	devProxy(logger, "http://127.0.0.1:1", transport).ServeHTTP(rec, req)

	res := rec.Result()

	if err := res.Body.Close(); err != nil {
		t.Fatalf("closing body: %v", err)
	}

	if res.StatusCode != nethttp.StatusBadGateway {
		t.Errorf("status = %d, want 502", res.StatusCode)
	}
}

func TestDevProxyBadTarget(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.DiscardHandler)

	req := httptest.NewRequestWithContext(t.Context(), nethttp.MethodGet, "/", nil)
	rec := httptest.NewRecorder()

	DevProxy(logger, "://bad-target").ServeHTTP(rec, req)

	res := rec.Result()

	if err := res.Body.Close(); err != nil {
		t.Fatalf("closing body: %v", err)
	}

	if res.StatusCode != nethttp.StatusBadGateway {
		t.Errorf("status = %d, want 502", res.StatusCode)
	}
}
