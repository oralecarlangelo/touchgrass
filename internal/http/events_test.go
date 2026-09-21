package http

import (
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
