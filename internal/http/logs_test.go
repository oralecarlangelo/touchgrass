package http

import (
	"encoding/json"
	"fmt"
	"net/url"
	"testing"
	"time"

	nethttp "net/http"

	"github.com/oralecarlangelo/touchgrass/internal/model"
	"github.com/oralecarlangelo/touchgrass/internal/store"
)

// seedLogLines inserts lines for the harness api service and returns them newest first.
func seedLogLines(t *testing.T, db *store.DB, base time.Time, messages ...string) []model.LogLine {
	t.Helper()

	lines := make([]model.LogLine, 0, len(messages))

	for i, message := range messages {
		lines = append(lines, model.LogLine{
			ServiceID: testServiceAPI,
			Container: "ticketnation-api-blue-1",
			Stream:    model.LogStdout,
			Line:      message,
			Ts:        base.Add(time.Duration(i) * time.Second),
		})
	}

	if _, err := store.NewLogStore(db).InsertBatch(t.Context(), lines); err != nil {
		t.Fatalf("InsertBatch() error = %v, want nil", err)
	}

	stored, err := store.NewLogStore(db).Search(t.Context(), store.LogFilter{
		ServiceID: testServiceAPI,
		Limit:     len(messages),
	})
	if err != nil {
		t.Fatalf("Search() error = %v, want nil", err)
	}

	return stored
}

func TestHandleLogs(t *testing.T) {
	t.Parallel()

	server, db := fullTestServer(t)
	base := time.Date(2026, 9, 21, 10, 0, 0, 0, time.UTC)
	seedLogLines(t, db, base, "boot ok", "request failed boom", "shutdown ok")

	status, body := doRequest(t, server, nethttp.MethodGet, "/api/logs?service_id="+testServiceAPI, "")
	if status != nethttp.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", status, body)
	}

	var got logsResponse

	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decoding logs: %v", err)
	}

	if len(got.Lines) != 3 {
		t.Fatalf("lines = %d, want 3", len(got.Lines))
	}

	if got.Lines[0].Line != "shutdown ok" {
		t.Errorf("first line = %q, want newest first", got.Lines[0].Line)
	}

	status, body = doRequest(t, server, nethttp.MethodGet, "/api/logs?service_id="+testServiceAPI+"&q=boom", "")
	if status != nethttp.StatusOK {
		t.Fatalf("search status = %d, want 200 (body: %s)", status, body)
	}

	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decoding search: %v", err)
	}

	if len(got.Lines) != 1 || got.Lines[0].Line != "request failed boom" {
		t.Errorf("search = %+v, want the boom line", got.Lines)
	}

	status, _ = doRequest(t, server, nethttp.MethodGet, "/api/logs", "")
	if status != nethttp.StatusBadRequest {
		t.Errorf("missing service_id status = %d, want 400", status)
	}

	status, _ = doRequest(t, server, nethttp.MethodGet, "/api/logs?service_id="+testServiceAPI+"&after=nope", "")
	if status != nethttp.StatusBadRequest {
		t.Errorf("bad after status = %d, want 400", status)
	}

	status, _ = doRequest(
		t, server, nethttp.MethodGet, "/api/logs?service_id="+url.QueryEscape(testUnknownService), "",
	)
	if status != nethttp.StatusNotFound {
		t.Errorf("unknown service status = %d, want 404", status)
	}
}

func TestHandleLogsFilters(t *testing.T) {
	t.Parallel()

	server, db := fullTestServer(t)
	base := time.Date(2026, 9, 21, 10, 0, 0, 0, time.UTC)
	seedLogLines(t, db, base, "boot ok", "request failed boom", "shutdown ok")

	status, body := doRequest(t, server, nethttp.MethodGet, "/api/logs?service_id="+testServiceAPI+"&level=error", "")
	if status != nethttp.StatusOK {
		t.Fatalf("level status = %d, want 200 (body: %s)", status, body)
	}

	var got logsResponse

	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decoding level filter: %v", err)
	}

	if len(got.Lines) != 1 || got.Lines[0].Level != model.LogLevelError {
		t.Fatalf("level filter = %+v, want the stamped error line", got.Lines)
	}

	status, body = doRequest(t, server, nethttp.MethodGet, "/api/logs?service_id="+testServiceAPI+"&stream=stderr", "")
	if status != nethttp.StatusOK {
		t.Fatalf("stream status = %d, want 200 (body: %s)", status, body)
	}

	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decoding stream filter: %v", err)
	}

	if len(got.Lines) != 0 {
		t.Errorf("stream filter = %d lines, want 0 (all stdout)", len(got.Lines))
	}
}

func TestHandleLogsRejects(t *testing.T) {
	t.Parallel()

	server, _ := fullTestServer(t)

	status, _ := doRequest(t, server, nethttp.MethodGet, "/api/logs?service_id="+testServiceAPI+"&level=bogus", "")
	if status != nethttp.StatusBadRequest {
		t.Errorf("bad level status = %d, want 400", status)
	}

	status, _ = doRequest(t, server, nethttp.MethodGet, "/api/logs?service_id="+testServiceAPI+"&stream=bogus", "")
	if status != nethttp.StatusBadRequest {
		t.Errorf("bad stream status = %d, want 400", status)
	}
}

func TestHandleLogStats(t *testing.T) {
	t.Parallel()

	server, db := fullTestServer(t)
	base := time.Date(2026, 9, 21, 10, 0, 0, 0, time.UTC)
	seedLogLines(t, db, base, "one", "two")

	status, body := doRequest(t, server, nethttp.MethodGet, "/api/logs/stats?service_id="+testServiceAPI, "")
	if status != nethttp.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", status, body)
	}

	var got model.LogStats

	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decoding stats: %v", err)
	}

	if got.Lines != 2 || got.Drops != 0 || got.Truncations != 0 {
		t.Errorf("stats = %+v, want 2 lines and zero losses", got)
	}

	status, _ = doRequest(t, server, nethttp.MethodGet, "/api/logs/stats", "")
	if status != nethttp.StatusBadRequest {
		t.Errorf("missing service_id status = %d, want 400", status)
	}
}

func TestHandleLogContext(t *testing.T) {
	t.Parallel()

	server, db := fullTestServer(t)
	base := time.Date(2026, 9, 21, 10, 0, 0, 0, time.UTC)
	stored := seedLogLines(t, db, base, "one", "two", "three")

	anchor := stored[1].ID

	status, body := doRequest(
		t, server, nethttp.MethodGet, fmt.Sprintf("/api/logs/%d?before=1&after=1", anchor), "",
	)
	if status != nethttp.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", status, body)
	}

	var got model.LogContext

	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decoding context: %v", err)
	}

	if got.Anchor.Line != "two" || len(got.Before) != 1 || len(got.After) != 1 {
		t.Errorf("context = %+v, want anchor two with one neighbor each", got)
	}

	status, _ = doRequest(t, server, nethttp.MethodGet, "/api/logs/9999", "")
	if status != nethttp.StatusNotFound {
		t.Errorf("unknown line status = %d, want 404", status)
	}

	status, _ = doRequest(t, server, nethttp.MethodGet, "/api/logs/nope", "")
	if status != nethttp.StatusBadRequest {
		t.Errorf("bad id status = %d, want 400", status)
	}
}
