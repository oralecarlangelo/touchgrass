package http

import (
	"encoding/json"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	nethttp "net/http"

	"github.com/oralecarlangelo/touchgrass/internal/store"
)

// doLogIngestRequest posts one batch without a session cookie,
// credentialed by a Bearer token, a raw key header, or neither.
func doLogIngestRequest(
	t *testing.T,
	server *Server,
	bearer, keyHeader, body string,
) (int, []byte) {
	t.Helper()

	req := httptest.NewRequestWithContext(
		t.Context(),
		nethttp.MethodPost,
		"/api/ingest/logs",
		strings.NewReader(body),
	)

	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}

	if keyHeader != "" {
		req.Header.Set(ingestKeyHeader, keyHeader)
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

// Shared log-batch body values.
const (
	testLogErrorBody = "boom"
	testLogRelease   = "v1.2.3"
	testTraceID      = "0123456789abcdef0123456789abcdef"
)

const testLogBatchBody = `{"release":"` + testLogRelease + `","items":[` +
	`{"timestamp":1788220800.5,"level":"info","body":"boot ok"},` +
	`{"timestamp":1788220801,"level":"error","body":"` + testLogErrorBody + `","severity_number":17,` +
	`"trace_id":"` + testTraceID + `","span_id":"0123456789abcdef",` +
	`"attributes":{"route":{"value":"/health","type":"string"}}}]}`

func TestHandleIngestLogs(t *testing.T) {
	t.Parallel()

	server, db := fullTestServer(t)
	plaintext, _ := mintTestKey(t, server, testServiceAPI, "1")

	status, body := doLogIngestRequest(t, server, plaintext, "", testLogBatchBody)

	if status != nethttp.StatusAccepted {
		t.Fatalf("status = %d, want 202 (body: %s)", status, body)
	}

	var got logIngestResponse

	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decoding body: %v", err)
	}

	if got.Accepted != 2 {
		t.Errorf("accepted = %d, want 2", got.Accepted)
	}

	stored, err := store.NewSDKLogStore(db).Search(t.Context(), store.SDKLogFilter{ServiceID: testServiceAPI})
	if err != nil {
		t.Fatalf("Search() error = %v, want nil", err)
	}

	if len(stored) != 2 || stored[0].Message != testLogErrorBody || stored[0].Release != testLogRelease {
		t.Fatalf("rows = %+v, want the batch newest first", stored)
	}
}

func TestHandleIngestLogsKeyHeader(t *testing.T) {
	t.Parallel()

	server, _ := fullTestServer(t)
	plaintext, _ := mintTestKey(t, server, testServiceAPI, "1")

	status, body := doLogIngestRequest(t, server, "", plaintext, testLogBatchBody)

	if status != nethttp.StatusAccepted {
		t.Fatalf("key-header status = %d, want 202 (body: %s)", status, body)
	}

	var got logIngestResponse

	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decoding body: %v", err)
	}

	if got.Accepted != 2 {
		t.Errorf("accepted = %d, want 2", got.Accepted)
	}
}

func TestHandleIngestLogsRejects(t *testing.T) {
	t.Parallel()

	server, _ := fullTestServer(t)
	plaintext, _ := mintTestKey(t, server, testServiceAPI, "1")

	status, _ := doLogIngestRequest(t, server, "", "", testLogBatchBody)
	if status != nethttp.StatusUnauthorized {
		t.Errorf("missing key status = %d, want 401", status)
	}

	status, _ = doLogIngestRequest(t, server, "bogus", "", testLogBatchBody)
	if status != nethttp.StatusUnauthorized {
		t.Errorf("bad key status = %d, want 401", status)
	}

	status, _ = doLogIngestRequest(t, server, plaintext, "", "not json")
	if status != nethttp.StatusBadRequest {
		t.Errorf("bad json status = %d, want 400", status)
	}

	status, _ = doLogIngestRequest(t, server, plaintext, "", `{"release":"v1","items":[]}`)
	if status != nethttp.StatusBadRequest {
		t.Errorf("empty batch status = %d, want 400", status)
	}
}

func TestHandleIngestLogsItemError(t *testing.T) {
	t.Parallel()

	server, db := fullTestServer(t)
	plaintext, _ := mintTestKey(t, server, testServiceAPI, "1")

	bad := `{"items":[` +
		`{"timestamp":1788220800,"level":"info","body":"ok"},` +
		`{"timestamp":1788220801,"level":"bogus","body":"bad"}]}`

	status, body := doLogIngestRequest(t, server, plaintext, "", bad)

	if status != nethttp.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body: %s)", status, body)
	}

	var got logItemErrorResponse

	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decoding body: %v", err)
	}

	if got.Index != 1 || got.Error == "" {
		t.Errorf("item error = %+v, want index 1 with a message", got)
	}

	stored, err := store.NewSDKLogStore(db).Search(t.Context(), store.SDKLogFilter{ServiceID: testServiceAPI})
	if err != nil {
		t.Fatalf("Search() error = %v, want nil", err)
	}

	if len(stored) != 0 {
		t.Errorf("stored = %d rows, want 0 (all-or-nothing)", len(stored))
	}
}
