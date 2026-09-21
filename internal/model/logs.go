package model

import "time"

// Log streams.
const (
	LogStdout = "stdout"
	LogStderr = "stderr"
)

// LogLine is one collected container log line.
type LogLine struct {
	ID        int64     `json:"id"`
	ServiceID string    `json:"service_id"`
	Container string    `json:"container"`
	Stream    string    `json:"stream"`
	Line      string    `json:"line"`
	Ts        time.Time `json:"ts"`
	CreatedAt time.Time `json:"created_at"`
}

// LogContext pairs a line with its surroundings, oldest first.
type LogContext struct {
	Anchor LogLine   `json:"anchor"`
	Before []LogLine `json:"before"`
	After  []LogLine `json:"after"`
}

// LogStats reports one service's stored lines plus backpressure losses
// since process start. Losses reset on restart; lines persist.
type LogStats struct {
	ServiceID   string `json:"service_id"`
	Lines       int64  `json:"lines"`
	Drops       int64  `json:"drops"`
	Truncations int64  `json:"truncations"`
}
