package http

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	nethttp "net/http"

	"github.com/oralecarlangelo/touchgrass/internal/model"
)

// eventBuffer holds events for slow subscribers before drops.
const eventBuffer = 16

// eventHeartbeat is how often an idle stream gets a comment frame. It
// stays under the 30-60s idle timeouts common in reverse proxies, and
// the write is also what notices a client that vanished without closing.
const eventHeartbeat = 15 * time.Second

// Stream frames that carry no event; clients ignore comment lines.
const (
	frameConnected = ": connected\n\n"
	framePing      = ": ping\n\n"
)

// errNoFlush reports a writer without streaming support.
var errNoFlush = errors.New("response writer lacks Flush")

// Hub broadcasts events to SSE subscribers.
type Hub struct {
	logger *slog.Logger
	mutex  sync.Mutex
	subs   map[int]chan model.Event
	next   int
}

// NewHub builds a Hub.
func NewHub(logger *slog.Logger) *Hub {
	return &Hub{logger: logger, subs: map[int]chan model.Event{}}
}

// Broadcast sends an event to every subscriber without blocking.
func (h *Hub) Broadcast(event model.Event) {
	h.mutex.Lock()
	defer h.mutex.Unlock()

	for id, sub := range h.subs {
		select {
		case sub <- event:
		default:
			h.logger.Warn("dropping event for slow subscriber", "subscriber", id, "type", event.Type)
		}
	}
}

// Subscribe registers a subscriber; cancel unregisters.
func (h *Hub) Subscribe() (<-chan model.Event, func()) {
	h.mutex.Lock()
	defer h.mutex.Unlock()

	id := h.next
	h.next++

	sub := make(chan model.Event, eventBuffer)
	h.subs[id] = sub

	var once sync.Once

	cancel := func() {
		h.mutex.Lock()
		defer h.mutex.Unlock()

		sub, ok := h.subs[id]
		if !ok {
			return
		}

		delete(h.subs, id)
		close(sub)
	}

	return sub, func() { once.Do(cancel) }
}

// handleEvents streams server-sent events until the client disconnects.
func (s *Server) handleEvents(w nethttp.ResponseWriter, r *nethttp.Request) {
	if _, ok := w.(nethttp.Flusher); !ok {
		writeError(w, s.logger, errNoFlush, "streaming unsupported", "internal", nethttp.StatusInternalServerError)

		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	// Subscribe before the first byte so a client that has seen the
	// stream open cannot miss an event it triggers next.
	events, cancel := s.events.Subscribe()
	defer cancel()

	control := nethttp.NewResponseController(w)

	if err := writeFrame(w, control, frameConnected); err != nil {
		return
	}

	heartbeat := time.NewTicker(s.eventHeartbeat)
	defer heartbeat.Stop()

	ctx := r.Context()

	for {
		select {
		case <-ctx.Done():
			return
		case <-heartbeat.C:
			if err := writeFrame(w, control, framePing); err != nil {
				return
			}
		case event, ok := <-events:
			if !ok {
				return
			}

			data, err := json.Marshal(event)
			if err != nil {
				s.logger.Error("encoding event", "error", err)

				return
			}

			if err := writeFrame(w, control, sseFrame(event.Type, data)); err != nil {
				return
			}
		}
	}
}

// writeFrame writes and flushes one frame under its own write deadline.
// The server's WriteTimeout is a deadline for the whole response, set
// once when the request arrives, so a stream has to move it forward
// before every write or the first frame after it passes fails. Each
// frame still gets only writeTimeout, so a client that stops reading is
// dropped instead of pinning the handler.
func writeFrame(w nethttp.ResponseWriter, control *nethttp.ResponseController, frame string) error {
	err := control.SetWriteDeadline(time.Now().Add(writeTimeout))
	// A writer without deadlines has no server deadline to outlive.
	if err != nil && !errors.Is(err, nethttp.ErrNotSupported) {
		return fmt.Errorf("extending write deadline: %w", err)
	}

	if _, err := w.Write([]byte(frame)); err != nil {
		return fmt.Errorf("writing frame: %w", err)
	}

	if err := control.Flush(); err != nil {
		return fmt.Errorf("flushing frame: %w", err)
	}

	return nil
}

// sseFrame renders one event. Env apply progress is a named event so
// EventSource listeners can subscribe to it directly; every other type
// stays an unnamed message for existing clients.
func sseFrame(eventType string, data []byte) string {
	frame := "data: " + string(data) + "\n\n"

	if eventType == model.EventEnvApply {
		return "event: " + eventType + "\n" + frame
	}

	return frame
}
