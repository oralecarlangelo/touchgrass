package docker

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/docker/docker/client"
)

// fakeEngine answers the two engine calls the migrations pillar makes
// with a fixed status, so the request guard can be watched.
func fakeEngine(t *testing.T, status int) *Client {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		isCall := strings.HasSuffix(r.URL.Path, "/archive") || strings.HasSuffix(r.URL.Path, "/exec")
		if !isCall {
			w.WriteHeader(http.StatusOK)

			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)

		if _, err := w.Write([]byte(`{"message":"engine said no"}`)); err != nil {
			t.Errorf("writing the fake engine response: %v", err)
		}
	}))
	t.Cleanup(server.Close)

	cli, err := client.NewClientWithOpts(
		client.WithHost("tcp://"+strings.TrimPrefix(server.URL, "http://")),
		client.WithVersion("1.44"),
	)
	if err != nil {
		t.Fatalf("NewClientWithOpts() error = %v, want nil", err)
	}

	t.Cleanup(func() {
		if err := cli.Close(); err != nil {
			t.Errorf("Close() error = %v, want nil", err)
		}
	})

	return &Client{cli: cli, guard: newRequestGuard(requestGuardConfig{
		MaxConcurrent: 2, FailureLimit: 3, Cooldown: time.Minute,
	})}
}

// A path that is not in the container is an everyday answer: every
// status read asks for optional files. It must never count as a daemon
// failure, or three reads would pause all engine access for a minute.
func TestReadFilesOfAMissingPathDoesNotTripTheGuard(t *testing.T) {
	t.Parallel()

	engine := fakeEngine(t, http.StatusNotFound)

	for attempt := range 10 {
		_, err := engine.ReadFiles(t.Context(), "c0ffee", "/app/prisma.config.ts")
		if !errors.Is(err, ErrPathNotFound) {
			t.Fatalf("ReadFiles() attempt %d error = %v, want ErrPathNotFound", attempt, err)
		}
	}
}

func TestExecInAStoppedContainerDoesNotTripTheGuard(t *testing.T) {
	t.Parallel()

	engine := fakeEngine(t, http.StatusConflict)

	for attempt := range 10 {
		_, err := engine.Exec(t.Context(), "c0ffee", ExecSpec{Cmd: []string{"true"}}, &bytes.Buffer{})
		if err == nil || errors.Is(err, ErrDaemonBackoff) {
			t.Fatalf("Exec() attempt %d error = %v, want the engine's refusal", attempt, err)
		}
	}
}

// A daemon that is really failing still opens the guard.
func TestReadFilesFailuresOfTheDaemonStillTripTheGuard(t *testing.T) {
	t.Parallel()

	engine := fakeEngine(t, http.StatusInternalServerError)

	var last error

	for range 4 {
		_, last = engine.ReadFiles(t.Context(), "c0ffee", "/app/prisma/migrations")
	}

	if !errors.Is(last, ErrDaemonBackoff) {
		t.Errorf("ReadFiles() after repeated daemon failures error = %v, want ErrDaemonBackoff", last)
	}
}
