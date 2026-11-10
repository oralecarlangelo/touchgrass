package http

import (
	"bufio"
	"context"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	nethttp "net/http"

	"github.com/oralecarlangelo/touchgrass/internal/model"
)

func TestHubBroadcast(t *testing.T) {
	t.Parallel()

	hub := NewHub(slog.New(slog.DiscardHandler))

	first, cancelFirst := hub.Subscribe()
	second, cancelSecond := hub.Subscribe()

	defer cancelFirst()
	defer cancelSecond()

	event := model.Event{Type: model.EventNginxChanged, ServiceID: testServiceAPI, Message: "flip", At: time.Now()}
	hub.Broadcast(event)

	if got := <-first; got != event {
		t.Errorf("first = %+v, want %+v", got, event)
	}

	if got := <-second; got != event {
		t.Errorf("second = %+v, want %+v", got, event)
	}

	cancelFirst()
	hub.Broadcast(event)

	if got := <-second; got != event {
		t.Errorf("second = %+v, want %+v", got, event)
	}

	if _, ok := <-first; ok {
		t.Error("cancelled subscriber received an event")
	}
}

func TestHandleEvents(t *testing.T) {
	t.Parallel()

	server, _ := fullTestServer(t)

	ctx, cancel := context.WithCancel(t.Context())

	req := httptest.NewRequestWithContext(ctx, nethttp.MethodGet, "/api/events", nil)
	req.AddCookie(authCookie(t, server))

	rec := httptest.NewRecorder()

	done := make(chan struct{})

	go func() {
		defer close(done)

		server.handler.ServeHTTP(rec, req)
	}()

	time.Sleep(50 * time.Millisecond)
	server.events.Broadcast(model.Event{
		Type:      model.EventDeployStarted,
		ServiceID: testServiceAPI,
		Message:   "cutover started",
		At:        time.Now(),
	})
	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("events handler did not stop after cancel")
	}

	body := rec.Body.String()

	if !strings.Contains(body, "data:") || !strings.Contains(body, "deploy_started") {
		t.Errorf("body = %q, want SSE event", body)
	}

	if content := rec.Header().Get("Content-Type"); content != "text/event-stream" {
		t.Errorf("content-type = %q, want text/event-stream", content)
	}
}

// streamEvents serves the full handler chain on a real listener, whose
// connections carry the write deadline a recorder lacks, and returns
// the lines an authenticated client reads from the event stream. The
// channel closes when the stream ends.
func streamEvents(t *testing.T, server *Server, serverWriteTimeout time.Duration) <-chan string {
	t.Helper()

	listener := httptest.NewUnstartedServer(server.handler)
	listener.Config.WriteTimeout = serverWriteTimeout
	listener.Start()
	t.Cleanup(listener.Close)

	req, err := nethttp.NewRequestWithContext(t.Context(), nethttp.MethodGet, listener.URL+"/api/events", nil)
	if err != nil {
		t.Fatalf("NewRequestWithContext() error = %v, want nil", err)
	}

	req.AddCookie(authCookie(t, server))

	resp, err := listener.Client().Do(req)
	if err != nil {
		t.Fatalf("Do() error = %v, want nil", err)
	}

	t.Cleanup(func() {
		if err := resp.Body.Close(); err != nil {
			t.Errorf("Body.Close() error = %v, want nil", err)
		}
	})

	if resp.StatusCode != nethttp.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, nethttp.StatusOK)
	}

	lines := make(chan string)

	go func() {
		defer close(lines)

		scanner := bufio.NewScanner(resp.Body)

		for scanner.Scan() {
			select {
			case lines <- scanner.Text():
			case <-t.Context().Done():
				return
			}
		}
	}()

	return lines
}

// awaitStreamLine reads the stream until a line contains want.
func awaitStreamLine(t *testing.T, lines <-chan string, want string) {
	t.Helper()

	timeout := time.After(5 * time.Second)

	for {
		select {
		case line, ok := <-lines:
			if !ok {
				t.Fatalf("stream ended before a line containing %q", want)
			}

			if strings.Contains(line, want) {
				return
			}
		case <-timeout:
			t.Fatalf("no line containing %q within 5s", want)
		}
	}
}

func TestHandleEventsOutlivesWriteTimeout(t *testing.T) {
	t.Parallel()

	const serverWriteTimeout = 100 * time.Millisecond

	server, _ := fullTestServer(t)
	lines := streamEvents(t, server, serverWriteTimeout)

	awaitStreamLine(t, lines, ": connected")

	// Two rounds: the stream must survive the timeout, not just one write.
	for _, message := range []string{"first late event", "second late event"} {
		time.Sleep(3 * serverWriteTimeout)

		server.events.Broadcast(model.Event{
			Type:      model.EventDeployStarted,
			ServiceID: testServiceAPI,
			Message:   message,
			At:        time.Now(),
		})

		awaitStreamLine(t, lines, message)
	}
}

func TestHandleEventsHeartbeat(t *testing.T) {
	t.Parallel()

	server, _ := fullTestServer(t)
	server.eventHeartbeat = 20 * time.Millisecond

	// The timeout is shorter than two heartbeats, so the second ping
	// also proves an idle stream keeps moving its deadline.
	lines := streamEvents(t, server, 30*time.Millisecond)

	awaitStreamLine(t, lines, ": connected")
	awaitStreamLine(t, lines, ": ping")
	awaitStreamLine(t, lines, ": ping")
}
