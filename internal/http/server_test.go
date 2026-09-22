package http

import (
	"encoding/json"
	"io"
	"io/fs"
	"log/slog"
	"net/http/httptest"
	"testing"
	"testing/fstest"

	nethttp "net/http"
)

func testDist() fs.FS {
	return fstest.MapFS{
		"index.html": {Data: []byte("<p>test spa</p>")},
	}
}

// testContentHTML is the expected docs/SPA page content type.
const testContentHTML = "text/html"

func testDocs() fs.FS {
	return fstest.MapFS{
		"index.html":     {Data: []byte("<p>test docs</p>")},
		"guide.html":     {Data: []byte("<p>test guide</p>")},
		"api/index.html": {Data: []byte("<p>test api ref</p>")},
		"404.html":       {Data: []byte("<p>test docs 404</p>")},
	}
}

func testServer(t *testing.T) *Server {
	t.Helper()

	logger := slog.New(slog.DiscardHandler)

	return New(Config{
		Addr:    "127.0.0.1:0",
		Version: testVersion,
		Logger:  logger,
		Auth:    testAuthenticator(t),
		Events:  NewHub(logger),
		Dist:    testDist(),
	})
}

func TestHandleHealth(t *testing.T) {
	t.Parallel()

	server := testServer(t)

	tests := []struct {
		name           string
		method         string
		target         string
		expectedStatus int
		expectedBody   healthResponse
		checkBody      bool
	}{
		{
			name:           "get health returns ok",
			method:         nethttp.MethodGet,
			target:         testHealthPath,
			expectedStatus: nethttp.StatusOK,
			expectedBody: healthResponse{
				Status:  "ok",
				Version: testVersion,
			},
			checkBody: true,
		},
		{
			name:           "post health is not allowed",
			method:         nethttp.MethodPost,
			target:         testHealthPath,
			expectedStatus: nethttp.StatusMethodNotAllowed,
		},
		{
			name:           "unknown path falls back to spa",
			method:         nethttp.MethodGet,
			target:         "/nope",
			expectedStatus: nethttp.StatusOK,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			req := httptest.NewRequestWithContext(t.Context(), tt.method, tt.target, nil)
			rec := httptest.NewRecorder()

			server.handler.ServeHTTP(rec, req)

			res := rec.Result()

			if res.StatusCode != tt.expectedStatus {
				t.Fatalf("%s %s status = %d, want %d", tt.method, tt.target, res.StatusCode, tt.expectedStatus)
			}

			if !tt.checkBody {
				if err := res.Body.Close(); err != nil {
					t.Fatalf("closing body: %v", err)
				}

				return
			}

			body, err := io.ReadAll(res.Body)
			if err != nil {
				t.Fatalf("reading body: %v", err)
			}

			if err := res.Body.Close(); err != nil {
				t.Fatalf("closing body: %v", err)
			}

			var got healthResponse

			if err := json.Unmarshal(body, &got); err != nil {
				t.Fatalf("decoding body: %v", err)
			}

			if got != tt.expectedBody {
				t.Errorf("body = %+v, want %+v", got, tt.expectedBody)
			}
		})
	}
}
