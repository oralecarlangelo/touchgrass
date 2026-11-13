package http

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	nethttp "net/http"

	"github.com/oralecarlangelo/touchgrass/internal/dbmigrate"
	"github.com/oralecarlangelo/touchgrass/internal/docker"
	"github.com/oralecarlangelo/touchgrass/internal/model"
	"github.com/oralecarlangelo/touchgrass/internal/testfixture"
)

const (
	// testDBPassword must never appear in any response body.
	testDBPassword = "correct-horse-Battery9"
	testDBURL      = "postgresql://app:" + testDBPassword + "@db.internal:5432/billing?schema=ledger"
	testLeafStatus = "migrations"
	testLeafConfig = "migrations/config"
	testLeafRuns   = "migrations/runs"
	testLeafDeploy = "migrations/deploy"
	testConfirm    = `{"confirm":"Billing worker"}`
)

// migrationHTTPFixture is a server whose granted service has a running
// container that ships two migrations and declares a seed.
type migrationHTTPFixture struct {
	scopedFixture

	engine *stubMigrationDocker
	tool   *stubMigrationTool
}

func newMigrationHTTPFixture(t *testing.T) migrationHTTPFixture {
	t.Helper()

	applied := time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)
	done := applied.Add(time.Second)

	engine := &stubMigrationDocker{
		workdir: "/srv/app",
		containers: []docker.Container{{
			ID: "c0ffee", Name: "billing-worker-1", Image: "billing/worker:42",
			Labels: map[string]string{docker.LabelComposeProject: "billing", docker.LabelComposeService: "worker"},
		}},
		files: map[string]map[string][]byte{
			"/srv/app/changes":     {"001_init": []byte("a"), "002_new": []byte("b")},
			"/srv/app/ledger.json": {"ledger.json": []byte("ledger seed")},
		},
		output: "ok\n",
	}
	tool := &stubMigrationTool{records: []dbmigrate.Record{
		{Name: "001_init", Checksum: "a", StartedAt: applied, FinishedAt: &done, Steps: 1},
	}}

	return migrationHTTPFixture{
		scopedFixture: newScopedFixtureWith(t, testServerOptions{migrationDocker: engine, migrationTool: tool}),
		engine:        engine,
		tool:          tool,
	}
}

// do calls the real handler chain and returns status and body.
func (f migrationHTTPFixture) do(t *testing.T, cookie *nethttp.Cookie, method, leaf, body string) (int, string) {
	t.Helper()

	status, _, response := call(t, f.server.handler, cookie, method, servicePath(testfixture.GenericGranted, leaf), body)

	return status, response
}

// configure saves a setup for the granted service as the admin.
func (f migrationHTTPFixture) configure(t *testing.T) {
	t.Helper()

	status, body := f.do(t, authCookie(t, f.server), nethttp.MethodPut, testLeafConfig,
		`{"tool":"ledger","database_url":"`+testDBURL+`"}`)
	if status != nethttp.StatusOK {
		t.Fatalf("PUT config = %d %s, want 200", status, body)
	}
}

// awaitRun polls the run detail until the command is over.
func (f migrationHTTPFixture) awaitRun(t *testing.T, id int64) model.MigrationRun {
	t.Helper()

	admin := authCookie(t, f.server)
	deadline := time.Now().Add(5 * time.Second)

	for time.Now().Before(deadline) {
		status, body := f.do(t, admin, nethttp.MethodGet, testLeafRuns+"/"+jsonNumber(id), "")
		if status != nethttp.StatusOK {
			t.Fatalf("GET run = %d %s, want 200", status, body)
		}

		var run model.MigrationRun
		if err := json.Unmarshal([]byte(body), &run); err != nil {
			t.Fatalf("decoding run: %v", err)
		}

		if run.State != model.MigrationRunRunning {
			waitForAudit(t, f.db, testfixture.GenericGranted, migrationAuditOf(run.Kind))

			return run
		}

		time.Sleep(5 * time.Millisecond)
	}

	t.Fatalf("run %d did not finish", id)

	return model.MigrationRun{}
}

// migrationAuditOf maps a run kind to the audit action it writes.
func migrationAuditOf(kind string) string {
	switch kind {
	case model.MigrationRunSeed:
		return model.AuditSeedRun
	case model.MigrationRunResolve:
		return model.AuditMigrationResolve
	default:
		return model.AuditMigrationDeploy
	}
}

// jsonNumber formats an id for a path.
func jsonNumber(id int64) string {
	raw, _ := json.Marshal(id)

	return string(raw)
}

// The setup holds a database password. No response may carry it back.
func TestMigrationConfigNeverReturnsTheDatabaseURL(t *testing.T) {
	t.Parallel()

	fixture := newScopedFixture(t)
	admin := authCookie(t, fixture.server)
	target := servicePath(testfixture.GenericGranted, testLeafConfig)

	status, _, body := call(t, fixture.server.handler, admin, nethttp.MethodGet, target, "")
	if status != nethttp.StatusOK || !strings.Contains(body, `"configured":false`) || !strings.Contains(body, `"name":"prisma","default_migrations_dir":"prisma/migrations","default_command":["npx","--no-install","prisma"]`) {
		t.Fatalf("GET config before setup = %d %s, want configured false and the prisma tool offered", status, body)
	}

	status, _, body = call(t, fixture.server.handler, admin, nethttp.MethodPut, target,
		`{"tool":"prisma","database_url":"`+testDBURL+`","container":"worker"}`)
	if status != nethttp.StatusOK {
		t.Fatalf("PUT config = %d %s, want 200", status, body)
	}

	_, _, read := call(t, fixture.server.handler, admin, nethttp.MethodGet, target, "")

	for name, response := range map[string]string{"PUT": body, "GET": read} {
		if strings.Contains(response, testDBPassword) || strings.Contains(response, "postgresql://") {
			t.Errorf("%s config response holds the database url: %s", name, response)
		}

		if !strings.Contains(response, `"database":"db.internal:5432/billing (schema ledger)"`) {
			t.Errorf("%s config response = %s, want the redacted target", name, response)
		}
	}

	audit := lastAudit(t, fixture.db, testfixture.GenericGranted)
	if audit.Action != model.AuditMigrationConfig || strings.Contains(audit.Detail, testDBPassword) {
		t.Errorf("audit = %+v, want a migration_config row without the password", audit)
	}
}

func TestMigrationConfigRejectsAndRemoves(t *testing.T) {
	t.Parallel()

	fixture := newScopedFixture(t)
	admin := authCookie(t, fixture.server)
	target := servicePath(testfixture.GenericGranted, testLeafConfig)

	status, _, body := call(t, fixture.server.handler, admin, nethttp.MethodPut, target,
		`{"tool":"prisma","database_url":"mysql://a:`+testDBPassword+`@db/x"}`)
	if status != nethttp.StatusBadRequest || strings.Contains(body, testDBPassword) {
		t.Errorf("PUT config with an unsupported url = %d %s, want 400 without the password", status, body)
	}

	status, _, _ = call(t, fixture.server.handler, admin, nethttp.MethodDelete, target, "")
	if status != nethttp.StatusNoContent {
		t.Errorf("DELETE config = %d, want 204", status)
	}
}

func TestMigrationStatusOverHTTP(t *testing.T) {
	t.Parallel()

	fixture := newMigrationHTTPFixture(t)
	fixture.configure(t)

	status, body := fixture.do(t, fixture.cookieFor(t, testDevUsername), nethttp.MethodGet, testLeafStatus, "")
	if status != nethttp.StatusOK {
		t.Fatalf("GET status as a developer = %d %s, want 200", status, body)
	}

	var got model.MigrationStatus
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("decoding status: %v", err)
	}

	wantCounts := model.MigrationCounts{Applied: 1, Pending: 1}
	if got.Verdict != model.MigrationVerdictPending || got.Counts != wantCounts || !got.Seed.Configured {
		t.Errorf("status = %+v, want pending with 1 applied, 1 pending and a seed", got)
	}

	if strings.Contains(body, testDBPassword) {
		t.Errorf("status response holds the database password: %s", body)
	}
}

// Who may do what, through the real handlers: a viewer nothing, a
// developer the two reads of a granted service, an admin everything.
func TestMigrationRoutesByRole(t *testing.T) {
	t.Parallel()

	fixture := newMigrationHTTPFixture(t)
	fixture.configure(t)

	developer := fixture.cookieFor(t, testDevUsername)
	viewer := fixture.cookieFor(t, testScopedUsername)

	tests := []struct {
		name   string
		cookie *nethttp.Cookie
		method string
		leaf   string
		body   string
		want   int
	}{
		{name: "viewer status", cookie: viewer, method: nethttp.MethodGet, leaf: testLeafStatus, want: nethttp.StatusForbidden},
		{name: "viewer runs", cookie: viewer, method: nethttp.MethodGet, leaf: testLeafRuns, want: nethttp.StatusForbidden},
		{name: "developer runs", cookie: developer, method: nethttp.MethodGet, leaf: testLeafRuns, want: nethttp.StatusOK},
		{name: "developer config", cookie: developer, method: nethttp.MethodGet, leaf: testLeafConfig, want: nethttp.StatusForbidden},
		{
			name: "developer config write", cookie: developer, method: nethttp.MethodPut, leaf: testLeafConfig,
			body: `{"tool":"ledger","database_url":"ledger://x"}`, want: nethttp.StatusForbidden,
		},
		{name: "developer config delete", cookie: developer, method: nethttp.MethodDelete, leaf: testLeafConfig, want: nethttp.StatusForbidden},
		{name: "developer deploy", cookie: developer, method: nethttp.MethodPost, leaf: testLeafDeploy, body: testConfirm, want: nethttp.StatusForbidden},
		{name: "developer seed", cookie: developer, method: nethttp.MethodPost, leaf: "migrations/seed", body: testConfirm, want: nethttp.StatusForbidden},
		{
			name: "developer resolve", cookie: developer, method: nethttp.MethodPost, leaf: "migrations/resolve",
			body: `{"confirm":"Billing worker","migration":"001_init","resolution":"applied"}`, want: nethttp.StatusForbidden,
		},
		{name: "developer run output", cookie: developer, method: nethttp.MethodGet, leaf: testLeafRuns + "/1", want: nethttp.StatusForbidden},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			if status, body := fixture.do(t, test.cookie, test.method, test.leaf, test.body); status != test.want {
				t.Errorf("%s %s = %d %s, want %d", test.method, test.leaf, status, body, test.want)
			}
		})
	}

	// A developer granted one service does not see another's migrations.
	status, _, _ := call(t, fixture.server.handler, developer, nethttp.MethodGet,
		servicePath(testfixture.GenericOther, testLeafStatus), "")
	if status != nethttp.StatusForbidden {
		t.Errorf("developer GET status of a service out of scope = %d, want 403", status)
	}

	if execs := fixture.engine.executed(); len(execs) != 0 {
		t.Errorf("refused requests executed %+v", execs)
	}
}

func TestMigrationCommandsOverHTTP(t *testing.T) {
	t.Parallel()

	fixture := newMigrationHTTPFixture(t)
	fixture.configure(t)

	admin := authCookie(t, fixture.server)

	status, body := fixture.do(t, admin, nethttp.MethodPost, testLeafDeploy, testConfirm)
	if status != nethttp.StatusAccepted {
		t.Fatalf("POST deploy = %d %s, want 202", status, body)
	}

	var started model.MigrationRun
	if err := json.Unmarshal([]byte(body), &started); err != nil {
		t.Fatalf("decoding run: %v", err)
	}

	run := fixture.awaitRun(t, started.ID)
	if run.State != model.MigrationRunSucceeded || run.Output != "ok\n" || run.Detail != "ledger up" {
		t.Errorf("deploy run = %+v, want succeeded with its output", run)
	}

	status, body = fixture.do(t, admin, nethttp.MethodPost, "migrations/seed", testConfirm)
	if status != nethttp.StatusAccepted {
		t.Fatalf("POST seed = %d %s, want 202", status, body)
	}

	if err := json.Unmarshal([]byte(body), &started); err != nil {
		t.Fatalf("decoding run: %v", err)
	}

	fixture.awaitRun(t, started.ID)

	// The run list is the seed history; it carries no output.
	status, body = fixture.do(t, fixture.cookieFor(t, testDevUsername), nethttp.MethodGet, testLeafRuns, "")
	if status != nethttp.StatusOK || strings.Contains(body, `"output"`) || !strings.Contains(body, `"kind":"seed"`) {
		t.Errorf("GET runs as a developer = %d %s, want the seed run listed without output", status, body)
	}

	audit := lastAudit(t, fixture.db, testfixture.GenericGranted)
	if audit.Action != model.AuditSeedRun || audit.Actor != defaultLoginUsername {
		t.Errorf("audit = %+v, want a seed_run row by admin", audit)
	}
}

func TestMigrationCommandErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		configured bool
		method     string
		leaf       string
		body       string
		wantStatus int
		wantCode   string
	}{
		{
			name: "wrong confirmation", configured: true, method: nethttp.MethodPost, leaf: testLeafDeploy,
			body: `{"confirm":"billing-worker"}`, wantStatus: nethttp.StatusBadRequest, wantCode: "confirmation_mismatch",
		},
		{
			name: "not configured", method: nethttp.MethodPost, leaf: testLeafDeploy,
			body: testConfirm, wantStatus: nethttp.StatusBadRequest, wantCode: "migrations_not_configured",
		},
		{
			name: "resolve of a healthy migration", configured: true, method: nethttp.MethodPost, leaf: "migrations/resolve",
			body:       `{"confirm":"Billing worker","migration":"001_init","resolution":"rolled_back"}`,
			wantStatus: nethttp.StatusBadRequest, wantCode: "invalid_request",
		},
		{
			name: "unknown run", configured: true, method: nethttp.MethodGet, leaf: testLeafRuns + "/999",
			wantStatus: nethttp.StatusNotFound, wantCode: codeNotFound,
		},
		{
			name: "run id that is not a number", configured: true, method: nethttp.MethodGet, leaf: testLeafRuns + "/latest",
			wantStatus: nethttp.StatusBadRequest, wantCode: "invalid_request",
		},
		{
			name: "body that is not json", configured: true, method: nethttp.MethodPost, leaf: testLeafDeploy,
			body: `confirm`, wantStatus: nethttp.StatusBadRequest, wantCode: "invalid_request",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			fixture := newMigrationHTTPFixture(t)
			if test.configured {
				fixture.configure(t)
			}

			status, body := fixture.do(t, authCookie(t, fixture.server), test.method, test.leaf, test.body)
			if status != test.wantStatus || !strings.Contains(body, `"code":"`+test.wantCode+`"`) {
				t.Errorf("%s %s = %d %s, want %d %s", test.method, test.leaf, status, body, test.wantStatus, test.wantCode)
			}

			if execs := fixture.engine.executed(); len(execs) != 0 {
				t.Errorf("a refused request executed %+v", execs)
			}
		})
	}
}

func TestMigrationCommandWhileOneIsRunning(t *testing.T) {
	t.Parallel()

	fixture := newMigrationHTTPFixture(t)
	fixture.engine.release = make(chan struct{})
	fixture.configure(t)

	admin := authCookie(t, fixture.server)

	status, body := fixture.do(t, admin, nethttp.MethodPost, testLeafDeploy, testConfirm)
	if status != nethttp.StatusAccepted {
		t.Fatalf("POST deploy = %d %s, want 202", status, body)
	}

	var started model.MigrationRun
	if err := json.Unmarshal([]byte(body), &started); err != nil {
		t.Fatalf("decoding run: %v", err)
	}

	status, body = fixture.do(t, admin, nethttp.MethodPost, "migrations/seed", testConfirm)
	if status != nethttp.StatusConflict || !strings.Contains(body, `"code":"migration_in_progress"`) {
		t.Errorf("POST seed during a deploy = %d %s, want 409 migration_in_progress", status, body)
	}

	close(fixture.engine.release)
	fixture.awaitRun(t, started.ID)
}

func TestMigrationConfigWithoutTheEnvKey(t *testing.T) {
	t.Parallel()

	fixture := newScopedFixtureWith(t, testServerOptions{envKeyMissing: true})

	status, _, body := call(t, fixture.server.handler, authCookie(t, fixture.server), nethttp.MethodPut,
		servicePath(testfixture.GenericGranted, testLeafConfig), `{"tool":"prisma","database_url":"`+testDBURL+`"}`)
	if status != nethttp.StatusServiceUnavailable || !strings.Contains(body, `"code":"env_key_missing"`) {
		t.Errorf("PUT config without the env key = %d %s, want 503 env_key_missing", status, body)
	}
}
