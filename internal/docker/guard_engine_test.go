package docker

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/docker/docker/client"
)

// refusingEngine answers every container call with a fixed status, so
// the request guard can be watched from the outside.
func refusingEngine(t *testing.T, status int) *Client {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/containers/") {
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

// engineReads are the per-container reads collectors and pages make.
func engineReads() map[string]func(context.Context, *Client) error {
	return map[string]func(context.Context, *Client) error{
		"inspect": func(ctx context.Context, c *Client) error {
			_, err := c.Inspect(ctx, "c0ffee")

			return err
		},
		"stats": func(ctx context.Context, c *Client) error {
			_, err := c.Stats(ctx, "c0ffee")

			return err
		},
		"logs": func(ctx context.Context, c *Client) error {
			_, err := c.Logs(ctx, "c0ffee", "", 10)

			return err
		},
		"diff": func(ctx context.Context, c *Client) error {
			_, err := c.Diff(ctx, "c0ffee")

			return err
		},
		"processes": func(ctx context.Context, c *Client) error {
			_, err := c.Processes(ctx, "c0ffee")

			return err
		},
	}
}

// A container removed between a listing and a read is an everyday
// answer. It must never count as a daemon failure, or three reads of
// it would pause all engine access for a minute.
func TestReadsOfAMissingContainerDoNotTripTheGuard(t *testing.T) {
	t.Parallel()

	for name, read := range engineReads() {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			engine := refusingEngine(t, http.StatusNotFound)

			for attempt := range 10 {
				err := read(t.Context(), engine)
				if err == nil || errors.Is(err, ErrDaemonBackoff) {
					t.Fatalf("attempt %d error = %v, want the engine's not-found", attempt, err)
				}
			}
		})
	}
}

// A daemon that is really failing still opens the guard.
func TestReadsOfAFailingDaemonStillTripTheGuard(t *testing.T) {
	t.Parallel()

	for name, read := range engineReads() {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			engine := refusingEngine(t, http.StatusInternalServerError)

			var last error

			for range 4 {
				last = read(t.Context(), engine)
			}

			if !errors.Is(last, ErrDaemonBackoff) {
				t.Errorf("error after repeated daemon failures = %v, want %v", last, ErrDaemonBackoff)
			}
		})
	}
}
