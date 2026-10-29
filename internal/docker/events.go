package docker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
	"unicode"

	dockerevents "github.com/docker/docker/api/types/events"
	"github.com/docker/docker/api/types/filters"
)

// Event is the bounded container lifecycle projection consumed by Touch Grass.
type Event struct {
	SourceEventID  string
	OccurredAt     time.Time
	Action         string
	ContainerID    string
	ContainerName  string
	ComposeProject string
	ComposeService string
}

// EventStream subscribes to bounded container lifecycle and health events.
// Cancelling ctx closes the Docker stream and the returned projection.
func (c *Client) EventStream(ctx context.Context, since string) (<-chan Event, <-chan error) {
	filter := filters.NewArgs(filters.Arg("type", string(dockerevents.ContainerEventType)))
	messages, daemonErrors := c.cli.Events(ctx, dockerevents.ListOptions{Since: since, Filters: filter})
	events := make(chan Event)
	errorsOut := make(chan error, 1)
	go func() {
		defer close(events)
		defer close(errorsOut)
		for messages != nil || daemonErrors != nil {
			select {
			case <-ctx.Done():
				return
			case message, ok := <-messages:
				if !ok {
					messages = nil
					continue
				}
				if event, keep := fromEventMessage(message); keep {
					select {
					case events <- event:
					case <-ctx.Done():
						return
					}
				}
			case err, ok := <-daemonErrors:
				if !ok {
					daemonErrors = nil
					continue
				}
				if err != nil {
					errorsOut <- fmt.Errorf("streaming docker events: %w", err)
					return
				}
			}
		}
	}()
	return events, errorsOut
}

func fromEventMessage(message dockerevents.Message) (Event, bool) {
	action, ok := boundedEventAction(message.Action)
	if message.Type != dockerevents.ContainerEventType || !ok || message.Actor.ID == "" {
		return Event{}, false
	}
	nanoseconds := message.TimeNano
	if nanoseconds == 0 {
		nanoseconds = message.Time * int64(time.Second)
	}
	occurredAt := time.Unix(0, nanoseconds).UTC()
	identity := fmt.Sprintf("%s\x1f%s\x1f%d", message.Actor.ID, action, nanoseconds)
	hash := sha256.Sum256([]byte(identity))
	return Event{
		SourceEventID: hex.EncodeToString(hash[:]), OccurredAt: occurredAt, Action: action,
		ContainerID:    boundedEventField(message.Actor.ID, 128),
		ContainerName:  boundedEventField(message.Actor.Attributes["name"], 128),
		ComposeProject: boundedEventField(message.Actor.Attributes[LabelComposeProject], 128),
		ComposeService: boundedEventField(message.Actor.Attributes[LabelComposeService], 128),
	}, true
}

func boundedEventAction(action dockerevents.Action) (string, bool) {
	bounded := string(action)
	if prefix, _, ok := strings.Cut(bounded, ":"); ok && strings.HasPrefix(prefix, "exec_") {
		bounded = strings.TrimSpace(prefix)
	}
	switch dockerevents.Action(bounded) { //nolint:exhaustive // Docker adds actions; the default intentionally rejects everything outside the allowlist.
	case dockerevents.ActionCreate:
		return "create", true
	case dockerevents.ActionStart:
		return "start", true
	case dockerevents.ActionStop:
		return "stop", true
	case dockerevents.ActionDie:
		return "die", true
	case dockerevents.ActionRestart:
		return "restart", true
	case dockerevents.ActionPause:
		return "pause", true
	case dockerevents.ActionUnPause:
		return "unpause", true
	case dockerevents.ActionKill:
		return "kill", true
	case dockerevents.ActionRename:
		return "rename", true
	case dockerevents.ActionDestroy:
		return "destroy", true
	case dockerevents.ActionRemove:
		return "remove", true
	case dockerevents.ActionExecCreate:
		return "exec_create", true
	case dockerevents.ActionExecStart:
		return "exec_start", true
	case dockerevents.ActionExecDie:
		return "exec_die", true
	case dockerevents.ActionExecDetach:
		return "exec_detach", true
	case dockerevents.ActionOOM:
		return "oom", true
	case dockerevents.ActionHealthStatusRunning:
		return "health_running", true
	case dockerevents.ActionHealthStatusHealthy:
		return "health_healthy", true
	case dockerevents.ActionHealthStatusUnhealthy:
		return "health_unhealthy", true
	default:
		return "", false
	}
}

func boundedEventField(value string, maximum int) string {
	value = strings.TrimSpace(value)
	if len(value) > maximum || strings.ContainsFunc(value, unicode.IsControl) {
		return ""
	}
	return value
}
