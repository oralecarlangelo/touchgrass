package docker

import (
	"strings"
	"testing"
	"time"

	dockerevents "github.com/docker/docker/api/types/events"
)

func TestFromEventMessageKeepsOnlyBoundedContainerFields(t *testing.T) {
	t.Parallel()

	message := dockerevents.Message{
		Type: dockerevents.ContainerEventType, Action: dockerevents.ActionOOM,
		Actor: dockerevents.Actor{ID: "abcdef1234567890", Attributes: map[string]string{
			"name": "ticketnation-api-blue-1", LabelComposeProject: "ticketnation",
			LabelComposeService: "api-blue", "secret": "password=do-not-store",
		}},
		TimeNano: time.Date(2026, 10, 16, 4, 5, 6, 7, time.UTC).UnixNano(),
	}

	event, ok := fromEventMessage(message)
	if !ok {
		t.Fatal("event was rejected")
	}
	if event.Action != "oom" || event.ContainerName != "ticketnation-api-blue-1" || event.ComposeService != "api-blue" {
		t.Fatalf("event = %+v", event)
	}
	if strings.Contains(event.SourceEventID, "password") || strings.Contains(event.ContainerName, "password") {
		t.Fatalf("event leaked unbounded attributes: %+v", event)
	}
}

func TestFromEventMessageAllowsLifecycleAndHealthOnly(t *testing.T) {
	t.Parallel()

	allowed := []dockerevents.Action{
		dockerevents.ActionCreate, dockerevents.ActionStart, dockerevents.ActionStop, dockerevents.ActionDie,
		dockerevents.ActionRestart, dockerevents.ActionOOM, dockerevents.ActionHealthStatusHealthy,
		dockerevents.ActionHealthStatusUnhealthy, dockerevents.ActionPause, dockerevents.ActionUnPause,
		dockerevents.ActionKill, dockerevents.ActionRename, dockerevents.ActionDestroy, dockerevents.ActionRemove,
		dockerevents.ActionExecCreate, dockerevents.ActionExecStart, dockerevents.ActionExecDie,
	}
	for _, action := range allowed {
		message := dockerevents.Message{Type: dockerevents.ContainerEventType, Action: action, Actor: dockerevents.Actor{ID: "id"}, Time: 10}
		if _, ok := fromEventMessage(message); !ok {
			t.Errorf("action %q rejected", action)
		}
	}
	message := dockerevents.Message{Type: dockerevents.ContainerEventType,
		Action: dockerevents.Action("exec_create: /bin/sh -c 'password=secret'"), Actor: dockerevents.Actor{ID: "id"}}
	event, ok := fromEventMessage(message)
	if !ok || event.Action != "exec_create" || strings.Contains(event.Action, "password") {
		t.Fatalf("bounded exec event = (%+v, %v), want action without command", event, ok)
	}
}
