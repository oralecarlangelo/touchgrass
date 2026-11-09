package http

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	nethttp "net/http"

	"github.com/oralecarlangelo/touchgrass/internal/model"
	"github.com/oralecarlangelo/touchgrass/internal/secret"
	"github.com/oralecarlangelo/touchgrass/internal/service"
	"github.com/oralecarlangelo/touchgrass/internal/store"
)

const (
	testEnvService    = "tn-env"
	testEnvPath       = "/api/services/tn-env/env"
	testEnvTarget     = "/opt/env/tn-env.env"
	testEnvKeyHex     = "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f"
	testEnvSecretKey  = "API_TOKEN"
	testEnvSecretBody = `{"value":"hidden-token","is_secret":true}`
	testEnvPlainBody  = `{"value":"8080","is_secret":false}`
	testEnvApplyBody  = `{"confirm":"tn-env","summary":"first apply"}`
)

// stubEnvHost keeps files in memory and scripts validate/restart.
type stubEnvHost struct {
	mutex        sync.Mutex
	files        map[string][]byte
	failValidate bool
	block        chan struct{}
	commands     []string
}

func newStubEnvHost() *stubEnvHost {
	return &stubEnvHost{files: map[string][]byte{}}
}

func (h *stubEnvHost) ReadFile(_ context.Context, path string, _ bool) ([]byte, error) {
	h.mutex.Lock()
	defer h.mutex.Unlock()

	data, ok := h.files[path]
	if !ok {
		return nil, errors.New("no such file")
	}

	return append([]byte{}, data...), nil
}

func (h *stubEnvHost) WriteFile(_ context.Context, path string, data []byte, _, _ string, _ bool) error {
	h.mutex.Lock()
	defer h.mutex.Unlock()

	h.files[path] = append([]byte{}, data...)

	return nil
}

func (h *stubEnvHost) Replace(_ context.Context, temp, target string, _ bool) (bool, error) {
	h.mutex.Lock()
	defer h.mutex.Unlock()

	previous, had := h.files[target]
	if had {
		h.files[target+".bak"] = previous
	}

	h.files[target] = h.files[temp]
	delete(h.files, temp)

	return had, nil
}

func (h *stubEnvHost) Restore(_ context.Context, target string, hadPrevious, _ bool) error {
	h.mutex.Lock()
	defer h.mutex.Unlock()

	if hadPrevious {
		h.files[target] = h.files[target+".bak"]
	} else {
		delete(h.files, target)
	}

	return nil
}

func (h *stubEnvHost) Remove(_ context.Context, path string, _ bool) error {
	h.mutex.Lock()
	defer h.mutex.Unlock()

	delete(h.files, path)

	return nil
}

func (h *stubEnvHost) Run(ctx context.Context, command string, _ []string, _ bool, emit func(string)) error {
	h.mutex.Lock()
	h.commands = append(h.commands, command)
	failValidate, block := h.failValidate, h.block
	h.mutex.Unlock()

	switch {
	case strings.HasPrefix(command, "validate"):
		if failValidate {
			emit("INVALID tn-env:DB_URL: missing")

			return errors.New("exit status 1")
		}

		emit("OK")

		return nil
	case block != nil:
		select {
		case <-block:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	default:
		emit("restarted")

		return nil
	}
}

// file returns one stored file as a string.
func (h *stubEnvHost) file(path string) string {
	h.mutex.Lock()
	defer h.mutex.Unlock()

	return string(h.files[path])
}

// testEnvService wires the env pillar for the harness.
func newTestEnvService(
	t *testing.T,
	db *store.DB,
	services *store.ServiceStore,
	audit *service.Audit,
	hub *Hub,
	opts testServerOptions,
) *service.Env {
	t.Helper()

	var key *secret.Key

	if !opts.envKeyMissing {
		loaded, err := secret.Load("", testEnvKeyHex)
		if err != nil {
			t.Fatalf("secret.Load() error = %v", err)
		}

		key = loaded
	}

	host := opts.envHost
	if host == nil {
		host = newStubEnvHost()
	}

	return service.NewEnv(service.EnvConfig{
		Services: services, Store: store.NewEnvStore(db), Audit: audit, Key: key, Host: host,
		Prober: stubProber{}, Emit: hub.Broadcast, ProbeInterval: time.Millisecond, RevealLimit: 3,
		Logger: slog.New(slog.DiscardHandler),
	})
}

// seedEnvService inserts an env-configured recreate service.
func seedEnvService(t *testing.T, db *store.DB) {
	t.Helper()

	config := map[string]any{
		"service": "app", "health_url": "http://127.0.0.1:1/health",
		"env": model.EnvConfig{
			Target:      model.EnvTarget{Path: testEnvTarget, Owner: "ops", Mode: "0600"},
			ValidateCmd: "validate --root /opt/env", RestartCmd: "restart tn-env",
			ProbeURL: "http://127.0.0.1:1/__release", Sudo: true,
		},
	}

	raw, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}

	if err := store.NewServiceStore(db).Create(context.Background(), model.Service{
		ID: testEnvService, Name: testEnvService, Strategy: model.StrategyRecreate,
		ComposeProject: "env", ComposeDir: "/opt/env", Config: raw,
	}); err != nil {
		t.Fatalf("Create(env service) error = %v", err)
	}
}

// envTestServer builds a harness server plus the seeded env service.
func envTestServer(t *testing.T, opts testServerOptions) (*Server, *store.DB) {
	t.Helper()

	server, db := fullTestServerWith(t, opts)
	seedEnvService(t, db)

	return server, db
}

// requestAs performs one request with an explicit cookie.
func requestAs(t *testing.T, server *Server, cookie *nethttp.Cookie, method, target, body string) (int, []byte) {
	t.Helper()

	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}

	req := httptest.NewRequestWithContext(t.Context(), method, target, reader)
	req.AddCookie(cookie)

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

// expectStatus fails when status differs.
func expectStatus(t *testing.T, label string, status, want int, body []byte) {
	t.Helper()

	if status != want {
		t.Fatalf("%s status = %d, want %d (body: %s)", label, status, want, body)
	}
}

// expectCode decodes an error envelope and checks its code.
func expectCode(t *testing.T, label string, body []byte, want string) {
	t.Helper()

	var got errorResponse

	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("%s decoding error body %s: %v", label, body, err)
	}

	if got.Code != want {
		t.Errorf("%s code = %q, want %q", label, got.Code, want)
	}
}

// decodeInto unmarshals a body or fails.
func decodeInto(t *testing.T, body []byte, target any) {
	t.Helper()

	if err := json.Unmarshal(body, target); err != nil {
		t.Fatalf("decoding %s: %v", body, err)
	}
}

// stageEntry PUTs one entry as admin and returns the entry view.
func stageEntry(t *testing.T, server *Server, key, body string) model.EnvEntryView {
	t.Helper()

	status, data := doRequest(t, server, nethttp.MethodPut, testEnvPath+"/entries/"+key, body)
	expectStatus(t, "PUT "+key, status, nethttp.StatusOK, data)

	var entry model.EnvEntryView

	decodeInto(t, data, &entry)

	return entry
}

// startApply posts an apply and waits for the run to finish.
func startApply(t *testing.T, server *Server, body string) (string, model.EnvRun) {
	t.Helper()

	status, data := doRequest(t, server, nethttp.MethodPost, testEnvPath+"/apply", body)
	expectStatus(t, "POST apply", status, nethttp.StatusAccepted, data)

	var accepted envRunResponse

	decodeInto(t, data, &accepted)

	return accepted.RunID, awaitRun(t, server, accepted.RunID)
}

// awaitRun waits for a run and returns it via the API.
func awaitRun(t *testing.T, server *Server, runID string) model.EnvRun {
	t.Helper()

	select {
	case <-server.env.Done(runID):
	case <-time.After(10 * time.Second):
		t.Fatal("env run did not finish")
	}

	status, data := doRequest(t, server, nethttp.MethodGet, testEnvPath+"/runs/"+runID, "")
	expectStatus(t, "GET run", status, nethttp.StatusOK, data)

	var run model.EnvRun

	decodeInto(t, data, &run)

	return run
}

// envView fetches the env view with a cookie.
func envView(t *testing.T, server *Server, cookie *nethttp.Cookie) model.EnvView {
	t.Helper()

	status, data := requestAs(t, server, cookie, nethttp.MethodGet, testEnvPath, "")
	expectStatus(t, "GET env", status, nethttp.StatusOK, data)

	var view model.EnvView

	decodeInto(t, data, &view)

	return view
}

// findEntry returns one entry view by key or fails.
func findEntry(t *testing.T, view model.EnvView, key string) model.EnvEntryView {
	t.Helper()

	for _, entry := range view.Entries {
		if entry.Key == key {
			return entry
		}
	}

	t.Fatalf("entry %s missing", key)

	return model.EnvEntryView{}
}

// checkMaskedView asserts the staged view masks the secret for one role.
func checkMaskedView(t *testing.T, view model.EnvView) {
	t.Helper()

	if !view.Configured || view.PendingCount != 2 || view.Target == nil || view.Target.Owner != "ops" {
		t.Fatalf("view = %+v, want configured with 2 pending and the target", view)
	}

	token := findEntry(t, view, testEnvSecretKey)
	if token.Value != nil || !token.Masked || !token.IsSecret || token.State != model.EnvStateAdded {
		t.Errorf("secret entry = %+v, want masked nil value", token)
	}

	port := findEntry(t, view, "PORT")
	if port.Value == nil || *port.Value != "8080" || port.Masked {
		t.Errorf("plain entry = %+v, want visible 8080", port)
	}
}

func TestEnvMaskedForEveryRole(t *testing.T) {
	t.Parallel()

	server, _ := envTestServer(t, testServerOptions{})
	stageEntry(t, server, "PORT", testEnvPlainBody)
	stageEntry(t, server, testEnvSecretKey, testEnvSecretBody)

	for name, cookie := range map[string]*nethttp.Cookie{"admin": authCookie(t, server), "viewer": viewerCookie(t, server)} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			checkMaskedView(t, envView(t, server, cookie))

			status, data := requestAs(t, server, cookie, nethttp.MethodGet, testEnvPath+"/diff", "")
			expectStatus(t, "GET diff", status, nethttp.StatusOK, data)

			if strings.Contains(string(data), "hidden-token") {
				t.Error("diff leaks a secret value")
			}

			status, data = requestAs(t, server, cookie, nethttp.MethodGet, testEnvPath+"/revisions", "")
			expectStatus(t, "GET revisions", status, nethttp.StatusOK, data)

			if strings.TrimSpace(string(data)) != "[]" {
				t.Errorf("revisions = %s, want []", data)
			}
		})
	}
}

func TestEnvAdminApplyFlow(t *testing.T) {
	t.Parallel()

	host := newStubEnvHost()
	server, _ := envTestServer(t, testServerOptions{envHost: host})

	entry := stageEntry(t, server, "PORT", testEnvPlainBody)
	if entry.State != model.EnvStateAdded || entry.UpdatedBy != defaultLoginUsername {
		t.Errorf("staged entry = %+v, want added by admin", entry)
	}

	stageEntry(t, server, testEnvSecretKey, testEnvSecretBody)

	status, data := doRequest(t, server, nethttp.MethodPost, testEnvPath+"/apply", `{"confirm":"nope"}`)
	expectStatus(t, "apply wrong confirm", status, nethttp.StatusBadRequest, data)
	expectCode(t, "apply wrong confirm", data, "confirmation_mismatch")

	runID, run := startApply(t, server, testEnvApplyBody)

	if run.ID != runID || run.State != model.EnvRunSucceeded || run.RevisionID == nil || len(run.Steps) == 0 {
		t.Fatalf("run = %+v, want succeeded with steps", run)
	}

	if got := host.file(testEnvTarget); got != "# managed by touchgrass — do not edit\nAPI_TOKEN=hidden-token\nPORT=8080\n" {
		t.Errorf("written file = %q", got)
	}

	checkAppliedState(t, server)

	status, data = doRequest(t, server, nethttp.MethodPost, testEnvPath+"/apply", testEnvApplyBody)
	expectStatus(t, "apply nothing", status, nethttp.StatusConflict, data)
	expectCode(t, "apply nothing", data, "env_nothing_to_apply")

	status, data = doRequest(t, server, nethttp.MethodGet, "/api/audit?service_id="+testEnvService, "")
	expectStatus(t, "GET audit", status, nethttp.StatusOK, data)

	if !strings.Contains(string(data), `"env.apply"`) || !strings.Contains(string(data), `"env.stage"`) {
		t.Errorf("audit = %s, want env.apply and env.stage rows", data)
	}

	if strings.Contains(string(data), "hidden-token") {
		t.Error("audit leaks a secret value")
	}
}

// checkAppliedState asserts the view and revision list after the first apply.
func checkAppliedState(t *testing.T, server *Server) {
	t.Helper()

	view := envView(t, server, authCookie(t, server))
	if view.PendingCount != 0 || view.AppliedRevision == nil || view.AppliedRevision.Summary != "first apply" {
		t.Errorf("view after apply = %+v", view)
	}

	if view.LastValidation == nil || !view.LastValidation.OK {
		t.Errorf("last_validation = %+v, want ok", view.LastValidation)
	}

	status, data := doRequest(t, server, nethttp.MethodGet, testEnvPath+"/revisions", "")
	expectStatus(t, "GET revisions", status, nethttp.StatusOK, data)

	var revisions []model.EnvRevisionView

	decodeInto(t, data, &revisions)

	if len(revisions) != 1 || !revisions[0].Applied || strings.Join(revisions[0].Added, ",") != "API_TOKEN,PORT" {
		t.Errorf("revisions = %+v, want one applied with both keys added", revisions)
	}
}

func TestEnvValidationFailureAndDiscard(t *testing.T) {
	t.Parallel()

	host := newStubEnvHost()
	host.failValidate = true
	server, _ := envTestServer(t, testServerOptions{envHost: host})
	stageEntry(t, server, "DB_URL", testEnvPlainBody)

	_, run := startApply(t, server, testEnvApplyBody)
	if run.State != model.EnvRunFailed {
		t.Fatalf("run state = %s, want failed", run.State)
	}

	view := envView(t, server, authCookie(t, server))
	if view.LastValidation == nil || view.LastValidation.OK || !findEntry(t, view, "DB_URL").Required {
		t.Errorf("view = %+v, want failed validation marking DB_URL required", view)
	}

	if host.file(testEnvTarget) != "" {
		t.Error("target written despite failed validation")
	}

	status, data := doRequest(t, server, nethttp.MethodPost, testEnvPath+"/discard", "")
	expectStatus(t, "discard", status, nethttp.StatusNoContent, data)

	if envView(t, server, authCookie(t, server)).PendingCount != 0 {
		t.Error("pending remains after discard")
	}
}

func TestEnvRequestValidation(t *testing.T) {
	t.Parallel()

	server, _ := envTestServer(t, testServerOptions{})

	tests := []struct {
		name   string
		method string
		target string
		body   string
		status int
		code   string
	}{
		{name: "lowercase key", method: nethttp.MethodPut, target: testEnvPath + "/entries/port", body: testEnvPlainBody, status: 400, code: "env_key_invalid"},
		{name: "newline value", method: nethttp.MethodPut, target: testEnvPath + "/entries/PORT", body: `{"value":"a\nb"}`, status: 400, code: "env_value_invalid"},
		{name: "bad entry json", method: nethttp.MethodPut, target: testEnvPath + "/entries/PORT", body: testMalformedJSON, status: 400, code: testInvalidRequest},
		{name: "unknown service", method: nethttp.MethodGet, target: "/api/services/nope/env", status: 404, code: testNotFoundCode},
		{name: "unconfigured stage", method: nethttp.MethodPut, target: "/api/services/tn-api/env/entries/PORT", body: testEnvPlainBody, status: 400, code: "env_not_configured"},
		{name: "delete unknown key", method: nethttp.MethodDelete, target: testEnvPath + "/entries/NOPE", status: 404, code: testNotFoundCode},
		{name: "reveal unknown key", method: nethttp.MethodPost, target: testEnvPath + "/entries/NOPE/reveal", status: 404, code: testNotFoundCode},
		{name: "unknown run", method: nethttp.MethodGet, target: testEnvPath + "/runs/nope", status: 404, code: testNotFoundCode},
		{name: "rollback bad rev", method: nethttp.MethodPost, target: testEnvPath + "/revisions/abc/rollback", body: `{"confirm":"tn-env"}`, status: 400, code: testInvalidRequest},
		{name: "rollback unknown rev", method: nethttp.MethodPost, target: testEnvPath + "/revisions/999/rollback", body: `{"confirm":"tn-env"}`, status: 404, code: testNotFoundCode},
		{name: "rollback wrong confirm", method: nethttp.MethodPost, target: testEnvPath + "/revisions/1/rollback", body: `{"confirm":"x"}`, status: 400, code: "confirmation_mismatch"},
		{name: "bad apply json", method: nethttp.MethodPost, target: testEnvPath + "/apply", body: testMalformedJSON, status: 400, code: testInvalidRequest},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			status, data := doRequest(t, server, tt.method, tt.target, tt.body)
			expectStatus(t, tt.name, status, tt.status, data)
			expectCode(t, tt.name, data, tt.code)
		})
	}
}

func TestEnvUnconfiguredView(t *testing.T) {
	t.Parallel()

	server, _ := envTestServer(t, testServerOptions{})

	status, data := doRequest(t, server, nethttp.MethodGet, "/api/services/tn-api/env", "")
	expectStatus(t, "GET unconfigured", status, nethttp.StatusOK, data)

	var view model.EnvView

	decodeInto(t, data, &view)

	if view.Configured || view.Reason != model.EnvReasonNotConfigured || view.Entries == nil {
		t.Errorf("view = %+v, want configured=false not_configured with [] entries", view)
	}
}

func TestEnvKeyMissing(t *testing.T) {
	t.Parallel()

	server, _ := envTestServer(t, testServerOptions{envKeyMissing: true})

	status, data := doRequest(t, server, nethttp.MethodGet, testEnvPath, "")
	expectStatus(t, "GET env", status, nethttp.StatusOK, data)

	var view model.EnvView

	decodeInto(t, data, &view)

	if view.Configured || view.Reason != model.EnvReasonKeyMissing {
		t.Errorf("view = %+v, want key_missing", view)
	}

	for _, route := range []struct {
		method, target, body string
	}{
		{nethttp.MethodPut, testEnvPath + "/entries/PORT", testEnvPlainBody},
		{nethttp.MethodPost, testEnvPath + "/apply", testEnvApplyBody},
		{nethttp.MethodPost, testEnvPath + "/import", ""},
		{nethttp.MethodGet, testEnvPath + "/diff", ""},
	} {
		status, data := doRequest(t, server, route.method, route.target, route.body)
		expectStatus(t, route.method+" "+route.target, status, nethttp.StatusServiceUnavailable, data)
		expectCode(t, route.target, data, "env_key_missing")
	}
}

func TestEnvRevealRateLimitAndAudit(t *testing.T) {
	t.Parallel()

	server, _ := envTestServer(t, testServerOptions{})
	stageEntry(t, server, testEnvSecretKey, testEnvSecretBody)
	target := testEnvPath + "/entries/" + testEnvSecretKey + "/reveal"

	for i := range 3 {
		status, data := doRequest(t, server, nethttp.MethodPost, target, "")
		expectStatus(t, fmt.Sprintf("reveal #%d", i+1), status, nethttp.StatusOK, data)

		var got envRevealResponse

		decodeInto(t, data, &got)

		if got.Value != "hidden-token" {
			t.Errorf("reveal value = %q, want hidden-token", got.Value)
		}
	}

	status, data := doRequest(t, server, nethttp.MethodPost, target, "")
	expectStatus(t, "reveal #4", status, nethttp.StatusTooManyRequests, data)
	expectCode(t, "reveal #4", data, "env_reveal_rate_limited")

	status, data = doRequest(t, server, nethttp.MethodGet, "/api/audit?service_id="+testEnvService, "")
	expectStatus(t, "GET audit", status, nethttp.StatusOK, data)

	var audit auditResponse

	decodeInto(t, data, &audit)

	reveals := 0

	for _, entry := range audit.Audit {
		if entry.Action != model.AuditEnvReveal {
			continue
		}

		reveals++

		if !strings.Contains(entry.Detail, testEnvSecretKey) || strings.Contains(entry.Detail, "hidden") {
			t.Errorf("reveal audit detail = %q, want key name only", entry.Detail)
		}
	}

	if reveals != 4 {
		t.Errorf("reveal audit rows = %d, want 4 (3 ok + 1 limited)", reveals)
	}
}

func TestEnvImport(t *testing.T) {
	t.Parallel()

	host := newStubEnvHost()
	host.files[testEnvTarget] = []byte("PORT=1\nDB_PASSWORD=x\nbad line\n")
	server, _ := envTestServer(t, testServerOptions{envHost: host})

	status, data := doRequest(t, server, nethttp.MethodPost, testEnvPath+"/import", "")
	expectStatus(t, "import", status, nethttp.StatusOK, data)

	var result model.EnvImportResult

	decodeInto(t, data, &result)

	if result.Imported != 2 || result.Skipped != 1 || result.SecretGuessed != 1 {
		t.Errorf("import = %+v, want 2/1/1", result)
	}

	status, data = doRequest(t, server, nethttp.MethodPost, testEnvPath+"/import", "")
	expectStatus(t, "import again", status, nethttp.StatusConflict, data)
	expectCode(t, "import again", data, "env_has_entries")

	status, data = doRequest(t, server, nethttp.MethodPost, testEnvPath+"/import", `{"replace":true}`)
	expectStatus(t, "import replace", status, nethttp.StatusOK, data)

	view := envView(t, server, viewerCookie(t, server))
	if findEntry(t, view, "DB_PASSWORD").Value != nil || findEntry(t, view, "PORT").Value == nil {
		t.Errorf("imported view = %+v, want DB_PASSWORD masked and PORT visible", view.Entries)
	}
}

func TestEnvApplyInProgress(t *testing.T) {
	t.Parallel()

	host := newStubEnvHost()
	host.block = make(chan struct{})
	server, _ := envTestServer(t, testServerOptions{envHost: host})
	stageEntry(t, server, "PORT", testEnvPlainBody)

	status, data := doRequest(t, server, nethttp.MethodPost, testEnvPath+"/apply", testEnvApplyBody)
	expectStatus(t, "apply", status, nethttp.StatusAccepted, data)

	var accepted envRunResponse

	decodeInto(t, data, &accepted)

	status, data = doRequest(t, server, nethttp.MethodPost, testEnvPath+"/apply", testEnvApplyBody)
	expectStatus(t, "second apply", status, nethttp.StatusConflict, data)
	expectCode(t, "second apply", data, "env_apply_in_progress")

	if !envView(t, server, authCookie(t, server)).ApplyInProgress {
		t.Error("apply_in_progress = false during a held run")
	}

	close(host.block)

	if run := awaitRun(t, server, accepted.RunID); run.State != model.EnvRunSucceeded {
		t.Errorf("run state = %s, want succeeded", run.State)
	}
}

func TestEnvRollbackFlow(t *testing.T) {
	t.Parallel()

	host := newStubEnvHost()
	server, _ := envTestServer(t, testServerOptions{envHost: host})

	stageEntry(t, server, "PORT", testEnvPlainBody)
	_, first := startApply(t, server, testEnvApplyBody)
	stageEntry(t, server, "PORT", `{"value":"9090"}`)
	startApply(t, server, `{"confirm":"tn-env","summary":"bump"}`)

	if got := host.file(testEnvTarget); !strings.Contains(got, "PORT=9090") {
		t.Fatalf("file after second apply = %q", got)
	}

	target := fmt.Sprintf("%s/revisions/%d/rollback", testEnvPath, *first.RevisionID)

	status, data := doRequest(t, server, nethttp.MethodPost, target, `{"confirm":"tn-env"}`)
	expectStatus(t, "rollback", status, nethttp.StatusAccepted, data)

	var accepted envRunResponse

	decodeInto(t, data, &accepted)

	if run := awaitRun(t, server, accepted.RunID); run.State != model.EnvRunSucceeded {
		t.Fatalf("rollback run = %+v, want succeeded", run)
	}

	if got := host.file(testEnvTarget); !strings.Contains(got, "PORT=8080") {
		t.Errorf("file after rollback = %q, want PORT=8080", got)
	}

	status, data = doRequest(t, server, nethttp.MethodGet, testEnvPath+"/revisions", "")
	expectStatus(t, "GET revisions", status, nethttp.StatusOK, data)

	var revisions []model.EnvRevisionView

	decodeInto(t, data, &revisions)

	if len(revisions) != 3 || !revisions[0].Applied || strings.Join(revisions[0].Changed, ",") != "PORT" {
		t.Errorf("revisions = %+v, want 3 with the newest applied changing PORT", revisions)
	}
}

func TestEnvSSEEventShape(t *testing.T) {
	t.Parallel()

	server, _ := envTestServer(t, testServerOptions{})
	stageEntry(t, server, "PORT", testEnvPlainBody)

	events, cancel := server.events.Subscribe()
	defer cancel()

	runID, _ := startApply(t, server, testEnvApplyBody)

	steps := map[string]bool{}
	deadline := time.After(5 * time.Second)

	for len(steps) < 6 {
		select {
		case event := <-events:
			raw, err := json.Marshal(event)
			if err != nil {
				t.Fatal(err)
			}

			var got map[string]any

			decodeInto(t, raw, &got)

			if got["type"] != model.EventEnvApply || got["run_id"] != runID || got["service_id"] != testEnvService {
				t.Fatalf("event = %s, want env.apply for run %s", raw, runID)
			}

			for _, field := range []string{"step", "message", "at"} {
				if _, ok := got[field]; !ok {
					t.Errorf("event %s lacks %s", raw, field)
				}
			}

			steps[event.Step] = true
		case <-deadline:
			t.Fatalf("saw steps %v before the deadline", steps)
		}
	}

	for _, step := range []string{"render", "validate", "write", "restart", "probe", "done"} {
		if !steps[step] {
			t.Errorf("missing step %s in %v", step, steps)
		}
	}
}

func TestEnvRoutesDocumented(t *testing.T) {
	t.Parallel()

	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot resolve test path")
	}

	body, err := os.ReadFile(filepath.Join(filepath.Dir(file), "..", "..", "docs-site", "openapi.yaml"))
	if err != nil {
		t.Fatal(err)
	}

	for _, route := range []string{
		"/services/{id}/env:",
		"/services/{id}/env/entries/{key}:",
		"/services/{id}/env/entries/{key}/reveal:",
		"/services/{id}/env/diff:",
		"/services/{id}/env/discard:",
		"/services/{id}/env/import:",
		"/services/{id}/env/apply:",
		"/services/{id}/env/runs/{run_id}:",
		"/services/{id}/env/revisions:",
		"/services/{id}/env/revisions/{rev}/rollback:",
	} {
		if !strings.Contains(string(body), "  "+route) {
			t.Errorf("OpenAPI is missing %s", route)
		}
	}
}
