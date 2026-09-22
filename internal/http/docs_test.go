package http

import (
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	nethttp "net/http"
)

func TestDocs(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.DiscardHandler)

	tests := []struct {
		name            string
		target          string
		expectedStatus  int
		expectedBody    string
		expectedContent string
	}{
		{
			name:            "root serves index",
			target:          "/docs/",
			expectedStatus:  nethttp.StatusOK,
			expectedBody:    "<p>test docs</p>",
			expectedContent: testContentHTML,
		},
		{
			name:            "page served",
			target:          "/docs/guide.html",
			expectedStatus:  nethttp.StatusOK,
			expectedBody:    "<p>test guide</p>",
			expectedContent: testContentHTML,
		},
		{
			name:            "directory serves index",
			target:          "/docs/api/",
			expectedStatus:  nethttp.StatusOK,
			expectedBody:    "<p>test api ref</p>",
			expectedContent: testContentHTML,
		},
		{
			name:            "bare directory serves index",
			target:          "/docs/api",
			expectedStatus:  nethttp.StatusOK,
			expectedBody:    "<p>test api ref</p>",
			expectedContent: testContentHTML,
		},
		{
			name:            "missing page serves 404",
			target:          "/docs/nope.html",
			expectedStatus:  nethttp.StatusNotFound,
			expectedBody:    "<p>test docs 404</p>",
			expectedContent: testContentHTML,
		},
		{
			name:            "traversal serves 404",
			target:          "/docs/../secret",
			expectedStatus:  nethttp.StatusNotFound,
			expectedBody:    "<p>test docs 404</p>",
			expectedContent: testContentHTML,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			req := httptest.NewRequestWithContext(t.Context(), nethttp.MethodGet, tt.target, nil)
			rec := httptest.NewRecorder()

			Docs(logger, testDocs()).ServeHTTP(rec, req)

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

func TestDocsUnbuilt(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.DiscardHandler)

	req := httptest.NewRequestWithContext(t.Context(), nethttp.MethodGet, "/docs/", nil)
	rec := httptest.NewRecorder()

	Docs(logger, fstest.MapFS{}).ServeHTTP(rec, req)

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

	if !strings.Contains(string(body), "docs_not_built") {
		t.Errorf("body = %q, want the docs_not_built code", body)
	}
}

func TestDocsRoute(t *testing.T) {
	t.Parallel()

	server, _ := fullTestServer(t)

	status, body := doRequest(t, server, nethttp.MethodGet, "/docs/guide.html", "")
	if status != nethttp.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", status, body)
	}

	if !strings.Contains(string(body), "test guide") {
		t.Errorf("body = %q, want the guide page", body)
	}

	status, _ = doRequest(t, server, nethttp.MethodGet, "/docs", "")
	if status != nethttp.StatusFound {
		t.Errorf("bare /docs status = %d, want 302", status)
	}
}
