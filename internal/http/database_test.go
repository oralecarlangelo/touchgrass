package http

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	nethttp "net/http"

	"github.com/oralecarlangelo/touchgrass/internal/model"
	"github.com/oralecarlangelo/touchgrass/internal/service"
	"github.com/oralecarlangelo/touchgrass/internal/store"
)

// testDatabaseAddr is the fixture listen address. It lives in a const
// so the shared "127.0.0.1:0" literal stays under the goconst threshold.
const testDatabaseAddr = "127.0.0.1:0"

// testNotFoundCode pins the unknown-id error code without tripping goconst.
const testNotFoundCode = "not_found"

// stubExecRunner routes docker exec calls to a canned handler.
type stubExecRunner struct {
	mutex   sync.Mutex
	handler func(args []string, stdin io.Reader, stdout io.Writer) error
}

func (s *stubExecRunner) run(
	_ context.Context,
	_ string,
	args []string,
	stdin io.Reader,
	stdout io.Writer,
) error {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	return s.handler(args, stdin, stdout)
}

// cannedDatabaseExec emulates healthy postgres + redis for handler tests.
func cannedDatabaseExec(dump []byte) func([]string, io.Reader, io.Writer) error {
	return func(args []string, stdin io.Reader, stdout io.Writer) error {
		tool := ""

		for _, arg := range args {
			switch arg {
			case "pg_isready", "psql", "pg_dump", "pg_restore", "redis-cli":
				tool = arg
			}
		}

		switch tool {
		case "pg_isready":
			return nil
		case "psql":
			_, err := io.WriteString(stdout, cannedQuery(args[len(args)-1]))

			return err
		case "pg_dump":
			_, err := stdout.Write(dump)

			return err
		case "pg_restore":
			_, err := io.Copy(io.Discard, stdin)

			return err
		case "redis-cli":
			out := "redis_version:7.2.4\nuptime_in_seconds:3600\n"
			if args[len(args)-1] == "memory" {
				out = "used_memory:1048576\n"
			}

			_, err := io.WriteString(stdout, out)

			return err
		default:
			return errors.New("unexpected tool")
		}
	}
}

// cannedQuery answers known psql read queries for handler tests.
func cannedQuery(query string) string {
	switch {
	case strings.Contains(query, "pg_terminate_backend"):
		return "t\n"
	case strings.Contains(query, "version()"):
		return "PostgreSQL 16.3 on x86_64-pc-linux-gnu, 64-bit\n"
	case strings.Contains(query, "pg_database_size"):
		return "12345678\n"
	case strings.Contains(query, "pg_stat_activity"):
		return "7\n"
	case strings.Contains(query, "max_connections"):
		return "100\n"
	case strings.Contains(query, "EXTRACT(EPOCH"):
		return "86400\n"
	case strings.Contains(query, "pg_tables"):
		return "42\n"
	case query == "SELECT 1":
		return "1\n"
	default:
		return ""
	}
}

// databaseFixture wires a Server with a Database service on temp storage.
type databaseFixture struct {
	server *Server
	dir    string
}

// newDatabaseFixture builds a Server with a canned database runner.
func newDatabaseFixture(
	t *testing.T,
	handler func([]string, io.Reader, io.Writer) error,
	mutate func(*service.DatabaseConfig),
) *databaseFixture {
	t.Helper()

	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("Open() error = %v, want nil", err)
	}

	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("Close() error = %v, want nil", err)
		}
	})

	if _, err := db.MigrateUp(context.Background()); err != nil {
		t.Fatalf("MigrateUp() error = %v, want nil", err)
	}

	services := store.NewServiceStore(db)
	audit := service.NewAudit(services, store.NewAuditStore(db))
	stub := &stubExecRunner{handler: handler}
	dir := t.TempDir()

	cfg := service.DatabaseConfig{
		Container: "pg-test", User: "postgres", DBName: "ticketnation",
		BackupDir: dir, BackupKeep: 14,
		RedisContainer: "redis-test", DBPath: filepath.Join(dir, "touchgrass.db"),
		Jobs: store.NewDBJobStore(db), Audit: store.NewAuditStore(db), SQLite: db,
		Run: stub.run, Logger: slog.New(slog.DiscardHandler),
	}

	if mutate != nil {
		mutate(&cfg)
	}

	logger := slog.New(slog.DiscardHandler)

	server := New(Config{
		Addr: testDatabaseAddr, Version: testVersion, Logger: logger,
		Audit: audit, Auth: testAuthenticator(t), Events: NewHub(logger),
		Database: service.NewDatabase(cfg),
		Dist:     testDist(), Docs: testDocs(),
	})

	return &databaseFixture{server: server, dir: dir}
}

// waitHTTPJob polls GET /api/databases/jobs/{id} to a terminal status.
func waitHTTPJob(t *testing.T, server *Server, id int64) model.DBJob {
	t.Helper()

	target := "/api/databases/jobs/" + strconv.FormatInt(id, 10)
	deadline := time.Now().Add(5 * time.Second)

	for time.Now().Before(deadline) {
		status, body := doRequest(t, server, nethttp.MethodGet, target, "")

		if status != nethttp.StatusOK {
			t.Fatalf("job status = %d, want 200 (body: %s)", status, body)
		}

		var job model.DBJob

		if err := json.Unmarshal(body, &job); err != nil {
			t.Fatalf("decoding job: %v", err)
		}

		if job.Status != model.DBJobRunning {
			return job
		}

		time.Sleep(5 * time.Millisecond)
	}

	t.Fatalf("job %d still running after 5s", id)

	return model.DBJob{}
}

func TestHandleDatabases(t *testing.T) {
	t.Parallel()

	fixture := newDatabaseFixture(t, cannedDatabaseExec(nil), nil)

	status, body := doRequest(t, fixture.server, nethttp.MethodGet, "/api/databases", "")
	if status != nethttp.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", status, body)
	}

	var got databasesResponse

	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decoding databases: %v", err)
	}

	if len(got.Databases) != 3 {
		t.Fatalf("databases = %d entries, want 3", len(got.Databases))
	}

	ids := []string{got.Databases[0].ID, got.Databases[1].ID, got.Databases[2].ID}

	if ids[0] != "postgres" || ids[1] != "redis" || ids[2] != "sqlite" {
		t.Errorf("ids = %v, want postgres/redis/sqlite", ids)
	}

	postgres := got.Databases[0]

	if !postgres.Configured || !postgres.Reachable {
		t.Errorf("postgres = %+v, want configured reachable", postgres)
	}

	if postgres.Version == nil || *postgres.Version != "16.3" {
		t.Errorf("postgres version = %v, want 16.3", postgres.Version)
	}
}

func TestHandleDatabasesUnconfigured(t *testing.T) {
	t.Parallel()

	fixture := newDatabaseFixture(t, cannedDatabaseExec(nil), func(cfg *service.DatabaseConfig) {
		cfg.Container = ""
		cfg.RedisContainer = ""
	})

	status, body := doRequest(t, fixture.server, nethttp.MethodGet, "/api/databases", "")
	if status != nethttp.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", status, body)
	}

	var got databasesResponse

	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decoding databases: %v", err)
	}

	if got.Databases[0].Configured || got.Databases[1].Configured {
		t.Errorf("databases = %+v, want postgres/redis unconfigured", got.Databases)
	}

	if !got.Databases[2].Configured || !got.Databases[2].Reachable {
		t.Errorf("sqlite = %+v, want configured reachable", got.Databases[2])
	}
}

func TestHandleBackups(t *testing.T) {
	t.Parallel()

	fixture := newDatabaseFixture(t, cannedDatabaseExec(nil), nil)

	status, body := doRequest(t, fixture.server, nethttp.MethodGet, "/api/databases/backups", "")
	if status != nethttp.StatusOK {
		t.Fatalf("empty status = %d, want 200 (body: %s)", status, body)
	}

	var empty backupsResponse

	if err := json.Unmarshal(body, &empty); err != nil {
		t.Fatalf("decoding backups: %v", err)
	}

	if len(empty.Backups) != 0 {
		t.Fatalf("backups = %d entries, want 0", len(empty.Backups))
	}

	if err := os.WriteFile(filepath.Join(fixture.dir, "a-20260922T120000Z.dump"), []byte("x"), 0o600); err != nil {
		t.Fatalf("seeding backup: %v", err)
	}

	status, body = doRequest(t, fixture.server, nethttp.MethodGet, "/api/databases/backups", "")
	if status != nethttp.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", status, body)
	}

	var got backupsResponse

	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decoding backups: %v", err)
	}

	if len(got.Backups) != 1 || got.Backups[0].Name != "a-20260922T120000Z.dump" {
		t.Errorf("backups = %+v, want the seeded dump", got.Backups)
	}

	if got.Backups[0].SHA256 != model.BackupSHAMissing {
		t.Errorf("sha256 = %q, want missing", got.Backups[0].SHA256)
	}
}

func TestHandleStartBackup(t *testing.T) {
	t.Parallel()

	fixture := newDatabaseFixture(t, cannedDatabaseExec([]byte("dump-bytes")), nil)

	status, body := doRequest(t, fixture.server, nethttp.MethodPost, "/api/databases/backups", "")
	if status != nethttp.StatusAccepted {
		t.Fatalf("status = %d, want 202 (body: %s)", status, body)
	}

	var accepted jobAcceptedResponse

	if err := json.Unmarshal(body, &accepted); err != nil {
		t.Fatalf("decoding accepted: %v", err)
	}

	if accepted.ID == 0 || accepted.Status != model.DBJobRunning {
		t.Errorf("accepted = %+v, want id plus running", accepted)
	}

	job := waitHTTPJob(t, fixture.server, accepted.ID)

	if job.Kind != model.DBJobBackup || job.Status != model.DBJobSuccess {
		t.Errorf("job = %+v, want successful backup", job)
	}
}

func TestHandleStartUnconfigured(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		target string
		body   string
	}{
		{name: "backup", target: "/api/databases/backups", body: ""},
		{name: "restore", target: "/api/databases/restore", body: `{"name":"a.dump"}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			fixture := newDatabaseFixture(t, cannedDatabaseExec(nil), func(cfg *service.DatabaseConfig) {
				cfg.Container = ""
			})

			status, body := doRequest(t, fixture.server, nethttp.MethodPost, tt.target, tt.body)
			if status != nethttp.StatusServiceUnavailable {
				t.Fatalf("status = %d, want 503 (body: %s)", status, body)
			}

			var got errorResponse

			if err := json.Unmarshal(body, &got); err != nil {
				t.Fatalf("decoding body: %v", err)
			}

			if got.Code != "unconfigured" {
				t.Errorf("code = %q, want unconfigured", got.Code)
			}
		})
	}
}

func TestHandleStartBackupBusy(t *testing.T) {
	t.Parallel()

	release := make(chan struct{})

	handler := func(args []string, stdin io.Reader, stdout io.Writer) error {
		if slices.Contains(args, "pg_dump") {
			<-release
		}

		return cannedDatabaseExec([]byte("dump"))(args, stdin, stdout)
	}
	fixture := newDatabaseFixture(t, handler, nil)

	status, body := doRequest(t, fixture.server, nethttp.MethodPost, "/api/databases/backups", "")
	if status != nethttp.StatusAccepted {
		t.Fatalf("first status = %d, want 202 (body: %s)", status, body)
	}

	var accepted jobAcceptedResponse

	if err := json.Unmarshal(body, &accepted); err != nil {
		t.Fatalf("decoding accepted: %v", err)
	}

	status, body = doRequest(t, fixture.server, nethttp.MethodPost, "/api/databases/backups", "")
	if status != nethttp.StatusConflict {
		t.Fatalf("second status = %d, want 409 (body: %s)", status, body)
	}

	var got errorResponse

	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decoding body: %v", err)
	}

	if got.Code != "busy" {
		t.Errorf("code = %q, want busy", got.Code)
	}

	close(release)

	if job := waitHTTPJob(t, fixture.server, accepted.ID); job.Status != model.DBJobSuccess {
		t.Errorf("job = %+v, want success after release", job)
	}
}

func TestHandleJob(t *testing.T) {
	t.Parallel()

	fixture := newDatabaseFixture(t, cannedDatabaseExec([]byte("dump")), nil)

	status, body := doRequest(t, fixture.server, nethttp.MethodPost, "/api/databases/backups", "")
	if status != nethttp.StatusAccepted {
		t.Fatalf("start status = %d, want 202 (body: %s)", status, body)
	}

	var accepted jobAcceptedResponse

	if err := json.Unmarshal(body, &accepted); err != nil {
		t.Fatalf("decoding accepted: %v", err)
	}

	target := "/api/databases/jobs/" + strconv.FormatInt(accepted.ID, 10)
	status, body = doRequest(t, fixture.server, nethttp.MethodGet, target, "")

	if status != nethttp.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", status, body)
	}

	var job model.DBJob

	if err := json.Unmarshal(body, &job); err != nil {
		t.Fatalf("decoding job: %v", err)
	}

	if job.ID != accepted.ID || job.Kind != model.DBJobBackup {
		t.Errorf("job = %+v, want the started backup", job)
	}

	if finished := waitHTTPJob(t, fixture.server, accepted.ID); finished.Status != model.DBJobSuccess {
		t.Errorf("job = %+v, want success", finished)
	}
}

func TestHandleJobsList(t *testing.T) {
	t.Parallel()

	fixture := newDatabaseFixture(t, cannedDatabaseExec([]byte("dump")), nil)

	status, body := doRequest(t, fixture.server, nethttp.MethodPost, "/api/databases/backups", "")
	if status != nethttp.StatusAccepted {
		t.Fatalf("start status = %d, want 202 (body: %s)", status, body)
	}

	var accepted jobAcceptedResponse

	if err := json.Unmarshal(body, &accepted); err != nil {
		t.Fatalf("decoding accepted: %v", err)
	}

	status, body = doRequest(t, fixture.server, nethttp.MethodGet, "/api/databases/jobs", "")
	if status != nethttp.StatusOK {
		t.Fatalf("list status = %d, want 200 (body: %s)", status, body)
	}

	var listed jobsResponse

	if err := json.Unmarshal(body, &listed); err != nil {
		t.Fatalf("decoding jobs: %v", err)
	}

	if len(listed.Jobs) != 1 || listed.Jobs[0].ID != accepted.ID {
		t.Errorf("jobs = %+v, want the one job", listed.Jobs)
	}

	if finished := waitHTTPJob(t, fixture.server, accepted.ID); finished.Status != model.DBJobSuccess {
		t.Errorf("job = %+v, want success", finished)
	}
}

func TestHandleJobErrors(t *testing.T) {
	t.Parallel()

	fixture := newDatabaseFixture(t, cannedDatabaseExec(nil), nil)

	tests := []struct {
		name         string
		target       string
		status       int
		expectedCode string
	}{
		{name: "unknown job", target: "/api/databases/jobs/999", status: nethttp.StatusNotFound, expectedCode: testNotFoundCode},
		{name: "bad id", target: "/api/databases/jobs/abc", status: nethttp.StatusBadRequest, expectedCode: testInvalidRequest},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			status, body := doRequest(t, fixture.server, nethttp.MethodGet, tt.target, "")

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

func TestHandleVerifyBackup(t *testing.T) {
	t.Parallel()

	fixture := newDatabaseFixture(t, cannedDatabaseExec(nil), nil)

	name := "v-20260922T120000Z.dump"

	if err := os.WriteFile(filepath.Join(fixture.dir, name), []byte("archive"), 0o600); err != nil {
		t.Fatalf("seeding backup: %v", err)
	}

	status, body := doRequest(
		t, fixture.server, nethttp.MethodPost, "/api/databases/backups/"+name+"/verify", "",
	)
	if status != nethttp.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", status, body)
	}

	var got model.BackupVerify

	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decoding verify: %v", err)
	}

	if !got.OK || got.Name != name {
		t.Errorf("verify = %+v, want ok", got)
	}

	tests := []struct {
		name         string
		target       string
		expectedCode string
	}{
		{name: "bad name", target: "/api/databases/backups/nope.sql/verify", expectedCode: testInvalidRequest},
		{name: "missing file", target: "/api/databases/backups/gone-20260922T120000Z.dump/verify", expectedCode: testInvalidRequest},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			status, body := doRequest(t, fixture.server, nethttp.MethodPost, tt.target, "")

			if status != nethttp.StatusBadRequest {
				t.Fatalf("status = %d, want 400 (body: %s)", status, body)
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

func TestHandleRestore(t *testing.T) {
	t.Parallel()

	fixture := newDatabaseFixture(t, cannedDatabaseExec([]byte("safety")), nil)

	name := "r-20260922T120000Z.dump"

	if err := os.WriteFile(filepath.Join(fixture.dir, name), []byte("archive"), 0o600); err != nil {
		t.Fatalf("seeding backup: %v", err)
	}

	status, body := doRequest(
		t, fixture.server, nethttp.MethodPost, "/api/databases/restore", `{"name":"`+name+`"}`,
	)
	if status != nethttp.StatusAccepted {
		t.Fatalf("status = %d, want 202 (body: %s)", status, body)
	}

	var accepted jobAcceptedResponse

	if err := json.Unmarshal(body, &accepted); err != nil {
		t.Fatalf("decoding accepted: %v", err)
	}

	if job := waitHTTPJob(t, fixture.server, accepted.ID); job.Status != model.DBJobSuccess {
		t.Errorf("job = %+v, want successful restore", job)
	}

	tests := []struct {
		name         string
		body         string
		status       int
		expectedCode string
	}{
		{name: "malformed json", body: `{bad`, status: nethttp.StatusBadRequest, expectedCode: testInvalidRequest},
		{name: "bad name", body: `{"name":"nope.sql"}`, status: nethttp.StatusBadRequest, expectedCode: testInvalidRequest},
		{name: "missing file", body: `{"name":"gone-20260922T120000Z.dump"}`, status: nethttp.StatusBadRequest, expectedCode: testInvalidRequest},
		{name: "empty name", body: `{"name":""}`, status: nethttp.StatusBadRequest, expectedCode: testInvalidRequest},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			status, body := doRequest(t, fixture.server, nethttp.MethodPost, "/api/databases/restore", tt.body)

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
