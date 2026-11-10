package http

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	nethttp "net/http"

	"github.com/oralecarlangelo/touchgrass/internal/authz"
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
	events, cancel := s.events.Subscribe(subjectFrom(r))
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
