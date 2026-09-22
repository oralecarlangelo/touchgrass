package http

import (
	"encoding/json"
	"strings"
	"testing"

	nethttp "net/http"
)

func TestHandleCutoverValidation(t *testing.T) {
	t.Parallel()

	server, _ := fullTestServer(t)

	tests := []struct {
		name         string
		target       string
		body         string
		expectedCode string
		status       int
	}{
		{
			name:         "unconfigured script",
			target:       "/api/services/tn-api/cutover",
			body:         `{"target":"green"}`,
			expectedCode: testInvalidRequest,
			status:       nethttp.StatusBadRequest,
		},
		{
			name:         "bad target",
			target:       "/api/services/tn-api/cutover",
			body:         `{"target":"purple"}`,
			expectedCode: testInvalidRequest,
			status:       nethttp.StatusBadRequest,
		},
		{
			name:         "recreate service",
			target:       "/api/services/tn-fe/cutover",
			body:         `{"target":"green"}`,
			expectedCode: testInvalidRequest,
			status:       nethttp.StatusBadRequest,
		},
		{
			name:         testUnknownService,
			target:       "/api/services/nope/cutover",
			body:         `{}`,
			expectedCode: "not_found",
			status:       nethttp.StatusNotFound,
		},
		{
			name:         "bad json",
			target:       "/api/services/tn-api/cutover",
			body:         testMalformedJSON,
			expectedCode: testInvalidRequest,
			status:       nethttp.StatusBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			status, body := doRequest(t, server, nethttp.MethodPost, tt.target, tt.body)

			if status != tt.status {
				t.Fatalf("status = %d, want %d (body: %s)", status, tt.status, body)
			}

			var got errorResponse

			if err := json.Unmarshal(body, &got); err != nil {
				t.Fatalf("decoding body: %v", err)
			}

			if got.Code != tt.expectedCode {
				t.Errorf("code = %q, want %q", got.Code, tt.expectedCode)
			}
		})
	}

	status, body := doRequest(t, server, nethttp.MethodPost, "/api/services/tn-api/cutover", `{"target":"green"}`)

	if status != nethttp.StatusBadRequest {
		t.Fatalf("status = %d, want 400", status)
	}

	var got errorResponse

	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decoding body: %v", err)
	}

	if !strings.Contains(got.Error, "not configured") {
		t.Errorf("error = %q, want configuration guidance", got.Error)
	}
}

func TestHandleRollbackValidation(t *testing.T) {
	t.Parallel()

	server, _ := fullTestServer(t)

	tests := []struct {
		name         string
		target       string
		expectedCode string
		status       int
	}{
		{
			name:         "unconfigured script",
			target:       "/api/services/tn-api/rollback",
			expectedCode: testInvalidRequest,
			status:       nethttp.StatusBadRequest,
		},
		{
			name:         "recreate service",
			target:       "/api/services/tn-fe/rollback",
			expectedCode: testInvalidRequest,
			status:       nethttp.StatusBadRequest,
		},
		{
			name:         testUnknownService,
			target:       "/api/services/nope/rollback",
			expectedCode: "not_found",
			status:       nethttp.StatusNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			status, body := doRequest(t, server, nethttp.MethodPost, tt.target, "")

			if status != tt.status {
				t.Fatalf("status = %d, want %d (body: %s)", status, tt.status, body)
			}

			var got errorResponse

			if err := json.Unmarshal(body, &got); err != nil {
				t.Fatalf("decoding body: %v", err)
			}

			if got.Code != tt.expectedCode {
				t.Errorf("code = %q, want %q", got.Code, tt.expectedCode)
			}
		})
	}
}
