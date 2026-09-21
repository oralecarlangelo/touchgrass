package http

import (
	"encoding/json"
	"testing"

	nethttp "net/http"
)

func TestHandleRecordListDeploys(t *testing.T) {
	t.Parallel()

	server, _ := fullTestServer(t)

	status, body := doRequest(t, server, nethttp.MethodPost, "/api/services/tn-api/deploys",
		`{"sha":"abc123","actor":"carl","outcome":"success","notes":"scripted"}`)

	if status != nethttp.StatusCreated {
		t.Fatalf("status = %d, want 201 (body: %s)", status, body)
	}

	var created deployIDResponse

	if err := json.Unmarshal(body, &created); err != nil {
		t.Fatalf("decoding body: %v", err)
	}

	if created.ID == 0 {
		t.Fatal("created id = 0, want non-zero")
	}

	status, body = doRequest(t, server, nethttp.MethodGet, "/api/services/tn-api/deploys", "")

	if status != nethttp.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", status, body)
	}

	var got deploysResponse

	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decoding body: %v", err)
	}

	if len(got.Deploys) != 1 || got.Deploys[0].SHA != "abc123" || got.Deploys[0].Type != "manual" {
		t.Errorf("deploys = %+v, want the manual record", got.Deploys)
	}
}

func TestHandleRecordDeployErrors(t *testing.T) {
	t.Parallel()

	server, _ := fullTestServer(t)

	tests := []struct {
		name   string
		method string
		target string
		body   string
		status int
	}{
		{
			name:   testUnknownService,
			method: nethttp.MethodPost,
			target: "/api/services/nope/deploys",
			body:   `{"sha":"a","actor":"b","outcome":"success"}`,
			status: nethttp.StatusNotFound,
		},
		{
			name:   "empty sha",
			method: nethttp.MethodPost,
			target: "/api/services/tn-api/deploys",
			body:   `{"sha":"","actor":"b","outcome":"success"}`,
			status: nethttp.StatusBadRequest,
		},
		{
			name:   "bad time",
			method: nethttp.MethodPost,
			target: "/api/services/tn-api/deploys",
			body:   `{"sha":"a","actor":"b","outcome":"success","started_at":"soon"}`,
			status: nethttp.StatusBadRequest,
		},
		{
			name:   "history unknown service",
			method: nethttp.MethodGet,
			target: "/api/services/nope/deploys",
			body:   "",
			status: nethttp.StatusNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			status, body := doRequest(t, server, tt.method, tt.target, tt.body)

			if status != tt.status {
				t.Errorf("status = %d, want %d (body: %s)", status, tt.status, body)
			}
		})
	}
}
