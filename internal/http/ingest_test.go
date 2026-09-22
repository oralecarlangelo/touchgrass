package http

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	nethttp "net/http"

	"github.com/oralecarlangelo/touchgrass/internal/store"
)

// doIngestRequest posts one report without a session cookie.
func doIngestRequest(t *testing.T, server *Server, key, body string) (int, []byte) {
	t.Helper()

	req := httptest.NewRequestWithContext(
		t.Context(),
		nethttp.MethodPost,
		"/api/ingest",
		strings.NewReader(body),
	)

	if key != "" {
		req.Header.Set(ingestKeyHeader, key)
	}

	rec := httptest.NewRecorder()

	server.handler.ServeHTTP(rec, req)

	res := rec.Result()

	data, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("reading body: %v", err)
	}

	if err := res.Body.Close(); err != nil {
		t.Fatalf("closing body: %v", err)
	}

	return res.StatusCode, data
}

// mintTestKey creates a key over HTTP and returns its plaintext and id.
func mintTestKey(t *testing.T, server *Server, serviceID, rate string) (string, int64) {
	t.Helper()

	status, body := doRequest(t, server, nethttp.MethodPost, testKeysPath,
		`{"service_id":"`+serviceID+`","sample_rate":`+rate+`}`)

	if status != nethttp.StatusCreated {
		t.Fatalf("create key status = %d, want 201 (body: %s)", status, body)
	}

	var created createdKeyResponse

	if err := json.Unmarshal(body, &created); err != nil {
		t.Fatalf("decoding key: %v", err)
	}

	return created.Key, created.ID
}

const testReportBody = `{"type":"exception","message":"boom","release":"v1.2.3",` +
	`"stack":[{"function":"handler","file":"app.js","line":10,"column":3}],` +
	`"breadcrumbs":[{"at":"2026-09-21T00:00:00Z","category":"nav","message":"route"}]}`

func TestHandleIngest(t *testing.T) {
	t.Parallel()

	server, db := fullTestServer(t)
	plaintext, _ := mintTestKey(t, server, testServiceAPI, "1")

	status, body := doIngestRequest(t, server, plaintext, testReportBody)

	if status != nethttp.StatusAccepted {
		t.Fatalf("status = %d, want 202 (body: %s)", status, body)
	}

	var got ingestResponse

	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decoding body: %v", err)
	}

	if !got.Sampled {
		t.Error("sampled = false, want true at rate 1")
	}

	listed, err := store.NewOccurrenceStore(db).ListByService(t.Context(), testServiceAPI, 10)
	if err != nil {
		t.Fatalf("ListByService() error = %v, want nil", err)
	}

	if len(listed) != 1 || listed[0].Message != "boom" || listed[0].Release != "v1.2.3" {
		t.Fatalf("occurrences = %+v, want the stored report", listed)
	}
}

func TestHandleIngestSampledOut(t *testing.T) {
	t.Parallel()

	server, db := fullTestServer(t)
	plaintext, _ := mintTestKey(t, server, testServiceAPI, "0")

	status, body := doIngestRequest(t, server, plaintext, testReportBody)

	if status != nethttp.StatusAccepted {
		t.Fatalf("status = %d, want 202 (body: %s)", status, body)
	}

	var got ingestResponse

	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decoding body: %v", err)
	}

	if got.Sampled {
		t.Error("sampled = true, want false at rate 0")
	}

	count, err := store.NewOccurrenceStore(db).CountByService(t.Context(), testServiceAPI)
	if err != nil {
		t.Fatalf("CountByService() error = %v, want nil", err)
	}

	if count != 0 {
		t.Errorf("CountByService() = %d, want 0 after sample-out", count)
	}
}

func TestHandleIngestRejects(t *testing.T) {
	t.Parallel()

	server, _ := fullTestServer(t)
	plaintext, _ := mintTestKey(t, server, testServiceAPI, "1")

	tests := []struct {
		name   string
		key    string
		body   string
		status int
	}{
		{name: "missing key", key: "", body: testReportBody, status: nethttp.StatusUnauthorized},
		{name: "bad key", key: "bogus", body: testReportBody, status: nethttp.StatusUnauthorized},
		{name: "bad json", key: plaintext, body: testMalformedJSON, status: nethttp.StatusBadRequest},
		{
			name: "bad type", key: plaintext,
			body:   `{"type":"trace","message":"x"}`,
			status: nethttp.StatusBadRequest,
		},
		{
			name: "empty message", key: plaintext,
			body:   `{"type":"message","message":""}`,
			status: nethttp.StatusBadRequest,
		},
		{
			name: "too large", key: plaintext,
			body:   `{"type":"message","message":"` + strings.Repeat("x", maxIngestBytes) + `"}`,
			status: nethttp.StatusRequestEntityTooLarge,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if status, body := doIngestRequest(t, server, tt.key, tt.body); status != tt.status {
				t.Errorf("status = %d, want %d (body: %s)", status, tt.status, body)
			}
		})
	}
}

func TestHandleKeys(t *testing.T) {
	t.Parallel()

	server, _ := fullTestServer(t)

	status, body := doRequest(t, server, nethttp.MethodPost, testKeysPath,
		`{"service_id":"tn-api","sample_rate":0.5}`)

	if status != nethttp.StatusCreated {
		t.Fatalf("status = %d, want 201 (body: %s)", status, body)
	}

	var created createdKeyResponse

	if err := json.Unmarshal(body, &created); err != nil {
		t.Fatalf("decoding key: %v", err)
	}

	if !strings.HasPrefix(created.Key, "tg_") || created.KeyPrefix == "" {
		t.Errorf("key = %+v, want plaintext plus prefix", created)
	}

	status, body = doRequest(t, server, nethttp.MethodGet, "/api/keys?service_id=tn-api", "")

	if status != nethttp.StatusOK {
		t.Fatalf("list status = %d, want 200 (body: %s)", status, body)
	}

	var listed keysResponse

	if err := json.Unmarshal(body, &listed); err != nil {
		t.Fatalf("decoding keys: %v", err)
	}

	if len(listed.Keys) != 1 || listed.Keys[0].SampleRate != 0.5 {
		t.Fatalf("keys = %+v, want the created key", listed.Keys)
	}

	status, _ = doRequest(t, server, nethttp.MethodGet, testKeysPath, "")
	if status != nethttp.StatusBadRequest {
		t.Errorf("missing service_id status = %d, want 400", status)
	}
}

func TestHandleKeysRejects(t *testing.T) {
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
			name: "missing rate", method: nethttp.MethodPost, target: testKeysPath,
			body: `{"service_id":"tn-api"}`, status: nethttp.StatusBadRequest,
		},
		{
			name: "bad rate", method: nethttp.MethodPost, target: testKeysPath,
			body: `{"service_id":"tn-api","sample_rate":2}`, status: nethttp.StatusBadRequest,
		},
		{
			name: "unknown service", method: nethttp.MethodPost, target: testKeysPath,
			body: `{"service_id":"nope","sample_rate":1}`, status: nethttp.StatusNotFound,
		},
		{
			name: "bad json", method: nethttp.MethodPost, target: testKeysPath,
			body: testMalformedJSON, status: nethttp.StatusBadRequest,
		},
		{
			name: "bad revoke id", method: nethttp.MethodPost, target: "/api/keys/abc/revoke",
			body: "", status: nethttp.StatusBadRequest,
		},
		{
			name: "unknown revoke id", method: nethttp.MethodPost, target: "/api/keys/9999/revoke",
			body: "", status: nethttp.StatusNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if status, body := doRequest(t, server, tt.method, tt.target, tt.body); status != tt.status {
				t.Errorf("status = %d, want %d (body: %s)", status, tt.status, body)
			}
		})
	}
}

func TestHandleRevokeKey(t *testing.T) {
	t.Parallel()

	server, _ := fullTestServer(t)
	plaintext, id := mintTestKey(t, server, testServiceAPI, "1")

	status, body := doIngestRequest(t, server, plaintext, testReportBody)
	if status != nethttp.StatusAccepted {
		t.Fatalf("ingest status = %d, want 202 (body: %s)", status, body)
	}

	revokeTarget := fmt.Sprintf("/api/keys/%d/revoke", id)

	if status, body := doRequest(t, server, nethttp.MethodPost, revokeTarget, ""); status != nethttp.StatusNoContent {
		t.Fatalf("revoke status = %d, want 204 (body: %s)", status, body)
	}

	if status, _ := doIngestRequest(t, server, plaintext, testReportBody); status != nethttp.StatusUnauthorized {
		t.Errorf("ingest after revoke status = %d, want 401", status)
	}
}
