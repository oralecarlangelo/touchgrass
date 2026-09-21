package http

import (
	"encoding/json"
	"errors"
	"log/slog"
	"sync"

	nethttp "net/http"

	"github.com/oralecarlangelo/touchgrass/internal/model"
)

// eventBuffer holds events for slow subscribers before drops.
const eventBuffer = 16

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
	flusher, ok := w.(nethttp.Flusher)
	if !ok {
		writeError(w, s.logger, errNoFlush, "streaming unsupported", "internal", nethttp.StatusInternalServerError)

		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	if _, err := w.Write([]byte(": connected\n\n")); err != nil {
		return
	}

	flusher.Flush()

	events, cancel := s.events.Subscribe()
	defer cancel()

	ctx := r.Context()

	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-events:
			if !ok {
				return
			}

			data, err := json.Marshal(event)
			if err != nil {
				s.logger.Error("encoding event", "error", err)

				return
			}

			if _, err := w.Write([]byte("data: " + string(data) + "\n\n")); err != nil {
				return
			}

			flusher.Flush()
		}
	}
}
