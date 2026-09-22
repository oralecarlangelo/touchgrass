package http

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	nethttp "net/http"

	"github.com/oralecarlangelo/touchgrass/internal/model"
	"github.com/oralecarlangelo/touchgrass/internal/store"
)

const testIssueReportBody = `{"type":"exception","message":"user 101 not found","release":"v7",` +
	`"stack":[{"function":"handler","file":"app.js","line":10,"column":3}],"breadcrumbs":[]}`

// seedIssue ingests one report and returns the resulting issue id.
func seedIssue(t *testing.T, server *Server, body string) int64 {
	t.Helper()

	plaintext, _ := mintTestKey(t, server, testServiceAPI, "1")

	status, data := doIngestRequest(t, server, plaintext, body)
	if status != nethttp.StatusAccepted {
		t.Fatalf("ingest status = %d, want 202 (body: %s)", status, data)
	}

	status, data = doRequest(t, server, nethttp.MethodGet, "/api/issues?service_id="+testServiceAPI, "")
	if status != nethttp.StatusOK {
		t.Fatalf("list status = %d, want 200 (body: %s)", status, data)
	}

	var listed issuesResponse

	if err := json.Unmarshal(data, &listed); err != nil {
		t.Fatalf("decoding issues: %v", err)
	}

	if len(listed.Issues) == 0 {
		t.Fatal("issues empty, want the seeded group")
	}

	return listed.Issues[0].ID
}

func TestHandleIssues(t *testing.T) {
	t.Parallel()

	server, _ := fullTestServer(t)
	seedIssue(t, server, testIssueReportBody)

	status, body := doRequest(t, server, nethttp.MethodGet, "/api/issues?service_id="+testServiceAPI, "")
	if status != nethttp.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", status, body)
	}

	var got issuesResponse

	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decoding issues: %v", err)
	}

	if len(got.Issues) != 1 || got.Issues[0].Count != 1 {
		t.Fatalf("issues = %+v, want one count-1 group", got.Issues)
	}

	if len(got.Issues[0].Releases) != 1 || got.Issues[0].Releases[0] != "v7" {
		t.Errorf("releases = %v, want [v7]", got.Issues[0].Releases)
	}

	status, _ = doRequest(t, server, nethttp.MethodGet, "/api/issues", "")
	if status != nethttp.StatusBadRequest {
		t.Errorf("missing service_id status = %d, want 400", status)
	}
}

func TestHandleIssue(t *testing.T) {
	t.Parallel()

	server, _ := fullTestServer(t)
	id := seedIssue(t, server, testIssueReportBody)

	status, body := doRequest(t, server, nethttp.MethodGet, fmt.Sprintf("/api/issues/%d", id), "")
	if status != nethttp.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", status, body)
	}

	var got issueDetailResponse

	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decoding detail: %v", err)
	}

	if got.Issue.ID != id || got.Issue.Title == "" {
		t.Errorf("issue = %+v, want the seeded group", got.Issue)
	}

	if len(got.Occurrences) != 1 || len(got.Occurrences[0].Stack) != 1 {
		t.Fatalf("occurrences = %+v, want one traced report", got.Occurrences)
	}

	status, _ = doRequest(t, server, nethttp.MethodGet, "/api/issues/9999", "")
	if status != nethttp.StatusNotFound {
		t.Errorf("unknown issue status = %d, want 404", status)
	}

	status, _ = doRequest(t, server, nethttp.MethodGet, "/api/issues/abc", "")
	if status != nethttp.StatusBadRequest {
		t.Errorf("bad id status = %d, want 400", status)
	}
}

func TestHandleOccurrences(t *testing.T) {
	t.Parallel()

	server, _ := fullTestServer(t)
	seedIssue(t, server, testIssueReportBody)

	status, body := doRequest(t, server, nethttp.MethodGet, "/api/issues/occurrences?service_id="+testServiceAPI, "")
	if status != nethttp.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", status, body)
	}

	var got occurrencesResponse

	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decoding occurrences: %v", err)
	}

	if len(got.Occurrences) != 1 {
		t.Fatalf("occurrences = %+v, want the seeded report", got.Occurrences)
	}

	status, _ = doRequest(t, server, nethttp.MethodGet, "/api/issues/occurrences", "")
	if status != nethttp.StatusBadRequest {
		t.Errorf("missing service_id status = %d, want 400", status)
	}
}

func TestHandleIssueLogs(t *testing.T) {
	t.Parallel()

	server, db := fullTestServer(t)
	id := seedIssue(t, server, testIssueReportBody)

	now := time.Now()
	lines := []model.LogLine{
		{ServiceID: testServiceAPI, Container: "api-blue", Stream: model.LogStderr, Line: "linked boom", Ts: now},
		{ServiceID: testServiceAPI, Container: "api-blue", Stream: model.LogStdout, Line: "ancient", Ts: now.Add(-2 * time.Hour)},
	}

	if _, err := store.NewLogStore(db).InsertBatch(t.Context(), lines); err != nil {
		t.Fatalf("InsertBatch() error = %v, want nil", err)
	}

	target := fmt.Sprintf("/api/issues/%d/logs", id)

	status, body := doRequest(t, server, nethttp.MethodGet, target, "")
	if status != nethttp.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", status, body)
	}

	var got issueLogsResponse

	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decoding issue logs: %v", err)
	}

	if len(got.Logs) != 1 || got.Logs[0].Line != "linked boom" {
		t.Errorf("logs = %+v, want the windowed line", got.Logs)
	}

	status, _ = doRequest(t, server, nethttp.MethodGet, "/api/issues/9999/logs", "")
	if status != nethttp.StatusNotFound {
		t.Errorf("unknown issue status = %d, want 404", status)
	}

	status, _ = doRequest(t, server, nethttp.MethodGet, target+"?window_secs=nope", "")
	if status != nethttp.StatusBadRequest {
		t.Errorf("bad window status = %d, want 400", status)
	}
}

func TestHandleIssueRules(t *testing.T) {
	t.Parallel()

	server, _ := fullTestServer(t)

	status, body := doRequest(t, server, nethttp.MethodPost, testIssueRulesPath,
		`{"service_id":"tn-api","kind":"spike","threshold":10,"window_secs":300}`)

	if status != nethttp.StatusCreated {
		t.Fatalf("status = %d, want 201 (body: %s)", status, body)
	}

	status, body = doRequest(t, server, nethttp.MethodGet, "/api/issues/rules?service_id=tn-api", "")
	if status != nethttp.StatusOK {
		t.Fatalf("list status = %d, want 200 (body: %s)", status, body)
	}

	var listed issueRulesResponse

	if err := json.Unmarshal(body, &listed); err != nil {
		t.Fatalf("decoding rules: %v", err)
	}

	if len(listed.Rules) != 1 || listed.Rules[0].Kind != "spike" {
		t.Fatalf("rules = %+v, want the created spike rule", listed.Rules)
	}

	target := fmt.Sprintf("/api/issues/rules/%d", listed.Rules[0].ID)

	if status, body := doRequest(t, server, nethttp.MethodDelete, target, ""); status != nethttp.StatusNoContent {
		t.Fatalf("delete status = %d, want 204 (body: %s)", status, body)
	}

	if status, _ := doRequest(t, server, nethttp.MethodDelete, target, ""); status != nethttp.StatusNotFound {
		t.Errorf("second delete status = %d, want 404", status)
	}
}

func TestHandleIssueRulesReject(t *testing.T) {
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
			name: "bad kind", method: nethttp.MethodPost, target: testIssueRulesPath,
			body:   `{"service_id":"tn-api","kind":"flaky","threshold":5,"window_secs":60}`,
			status: nethttp.StatusBadRequest,
		},
		{
			name: "spike needs threshold", method: nethttp.MethodPost, target: testIssueRulesPath,
			body:   `{"service_id":"tn-api","kind":"spike","window_secs":60}`,
			status: nethttp.StatusBadRequest,
		},
		{
			name: "unknown service", method: nethttp.MethodPost, target: testIssueRulesPath,
			body:   `{"service_id":"nope","kind":"new_issue","window_secs":60}`,
			status: nethttp.StatusNotFound,
		},
		{
			name: "missing service_id", method: nethttp.MethodGet, target: testIssueRulesPath,
			body: "", status: nethttp.StatusBadRequest,
		},
		{
			name: "bad delete id", method: nethttp.MethodDelete, target: "/api/issues/rules/abc",
			body: "", status: nethttp.StatusBadRequest,
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
