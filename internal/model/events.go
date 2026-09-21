package model

import (
	"time"
)

// SSE event types.
const (
	EventNginxChanged     = "nginx_changed"
	EventDeployStarted    = "deploy_started"
	EventDeployProgress   = "deploy_progress"
	EventDeployFinished   = "deploy_finished"
	EventRollbackStarted  = "rollback_started"
	EventRollbackProgress = "rollback_progress"
	EventRollbackFinished = "rollback_finished"
)

// Event is one server-sent event.
type Event struct {
	Type      string    `json:"type"`
	ServiceID string    `json:"service_id"`
	Message   string    `json:"message"`
	At        time.Time `json:"at"`
}
