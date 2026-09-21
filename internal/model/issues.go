package model

import "time"

// Issue rule kinds.
const (
	IssueRuleNewIssue = "new_issue"
	IssueRuleSpike    = "spike"
)

// Issue is one fingerprinted error group with live stats.
type Issue struct {
	ID          int64      `json:"id"`
	ServiceID   string     `json:"service_id"`
	Fingerprint string     `json:"fingerprint"`
	Title       string     `json:"title"`
	FirstSeen   time.Time  `json:"first_seen"`
	LastSeen    time.Time  `json:"last_seen"`
	Count       int64      `json:"count"`
	Releases    []string   `json:"releases"`
	NotifiedAt  *time.Time `json:"notified_at"`
	CreatedAt   time.Time  `json:"created_at"`
}

// IssueRule fires issue notifications.
type IssueRule struct {
	ID         int64     `json:"id"`
	ServiceID  string    `json:"service_id"`
	Kind       string    `json:"kind"`
	Threshold  int       `json:"threshold"`
	WindowSecs int       `json:"window_secs"`
	Enabled    bool      `json:"enabled"`
	CreatedAt  time.Time `json:"created_at"`
}

// IssueRuleCreate carries issue rule input.
type IssueRuleCreate struct {
	ServiceID  string `json:"service_id"`
	Kind       string `json:"kind"`
	Threshold  int    `json:"threshold"`
	WindowSecs int    `json:"window_secs"`
}
