package http

import (
	"encoding/json"
	"errors"
	"log/slog"
	"sync"

	nethttp "net/http"

	"github.com/oralecarlangelo/touchgrass/internal/authz"
	"github.com/oralecarlangelo/touchgrass/internal/model"
)

// eventBuffer holds events for slow subscribers before drops.
const eventBuffer = 16

// errNoFlush reports a writer without streaming support.
var errNoFlush = errors.New("response writer lacks Flush")

// eventCapabilities maps every event type to the capability a
// subscriber needs to receive it. A type that is not listed is
// delivered to nobody, so a new event stays private until someone
// decides who may see it.
var eventCapabilities = map[string]authz.Capability{
	model.EventNginxChanged:     authz.DeployRead,
	model.EventDeployStarted:    authz.DeployRead,
	model.EventDeployProgress:   authz.DeployRead,
	model.EventDeployFinished:   authz.DeployRead,
	model.EventRollbackStarted:  authz.DeployRead,
	model.EventRollbackProgress: authz.DeployRead,
	model.EventRollbackFinished: authz.DeployRead,
	model.EventEnvApply:         authz.EnvReadKeys,
	model.EventSecurityFinding:  authz.SecurityRead,
	model.EventSecurityRecovery: authz.SecurityRead,
}

// eventVisible reports whether subject may receive event: they must
// hold the event's capability for the event's service. An event with no
// service is fleet-level and needs the all-services scope.
func eventVisible(subject authz.Subject, event model.Event) bool {
	capability, ok := eventCapabilities[event.Type]
	if !ok {
		return false
	}

	if event.ServiceID == "" {
		return subject.CanFleet(capability)
	}

	return subject.Can(capability, event.ServiceID)
}

// subscriber is one open stream and who it belongs to.
type subscriber struct {
	subject authz.Subject
	events  chan model.Event
}

// Hub broadcasts events to SSE subscribers, each of whom receives only
// what their role and scope allow.
type Hub struct {
	logger *slog.Logger
	mutex  sync.Mutex
	subs   map[int]subscriber
	next   int
}

// NewHub builds a Hub.
func NewHub(logger *slog.Logger) *Hub {
	return &Hub{logger: logger, subs: map[int]subscriber{}}
}

// Broadcast sends an event to every subscriber allowed to see it,
// without blocking.
func (h *Hub) Broadcast(event model.Event) {
	h.mutex.Lock()
	defer h.mutex.Unlock()

	if _, ok := eventCapabilities[event.Type]; !ok {
		h.logger.Warn("dropping event with no capability mapping", "type", event.Type)

		return
	}

	for id, sub := range h.subs {
		if !eventVisible(sub.subject, event) {
			continue
		}

		select {
		case sub.events <- event:
		default:
			h.logger.Warn("dropping event for slow subscriber", "subscriber", id, "type", event.Type)
		}
	}
}

// Subscribe registers a subscriber for subject; cancel unregisters.
// The subject is fixed for the life of the stream, so whoever changes a
// user's access must Disconnect them.
func (h *Hub) Subscribe(subject authz.Subject) (<-chan model.Event, func()) {
	h.mutex.Lock()
	defer h.mutex.Unlock()

	id := h.next
	h.next++

	events := make(chan model.Event, eventBuffer)
	h.subs[id] = subscriber{subject: subject, events: events}

	var once sync.Once

	cancel := func() {
		h.mutex.Lock()
		defer h.mutex.Unlock()

		h.drop(id)
	}

	return events, func() { once.Do(cancel) }
}

// Disconnect closes every stream username holds and reports how many.
// It is called when their role, scope, password, or disabled state
// changes, or the account is deleted: the browser reconnects, and the
// new stream is authorized against the account as it is now.
func (h *Hub) Disconnect(username string) int {
	h.mutex.Lock()
	defer h.mutex.Unlock()

	closed := 0

	for id, sub := range h.subs {
		if sub.subject.Username != username {
			continue
		}

		h.drop(id)

		closed++
	}

	return closed
}

// drop unregisters one subscriber and closes its channel. The caller
// holds the mutex; dropping twice is a no-op.
func (h *Hub) drop(id int) {
	sub, ok := h.subs[id]
	if !ok {
		return
	}

	delete(h.subs, id)
	close(sub.events)
}

// handleEvents streams server-sent events until the client disconnects
// or the caller's access changes.
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

	events, cancel := s.events.Subscribe(subjectFrom(r))
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

			if _, err := w.Write([]byte(sseFrame(event.Type, data))); err != nil {
				return
			}

			flusher.Flush()
		}
	}
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
