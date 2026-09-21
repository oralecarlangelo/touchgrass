package model

import (
	"time"
)

// Metric is one container sample.
type Metric struct {
	ID            int64     `json:"id"`
	ServiceID     string    `json:"service_id"`
	ContainerName string    `json:"container_name"`
	SampledAt     time.Time `json:"sampled_at"`
	CPUPercent    float64   `json:"cpu_percent"`
	MemBytes      uint64    `json:"mem_bytes"`
	MemLimit      uint64    `json:"mem_limit"`
	DiskBytes     int64     `json:"disk_bytes"`
	Restarts      int       `json:"restarts"`
	UptimeSecs    int64     `json:"uptime_secs"`
}

// Alert metrics evaluated by threshold rules.
const (
	MetricCPU  = "cpu"
	MetricMem  = "mem"
	MetricDisk = "disk"
)

// AlertRule fires when a metric stays above threshold for the duration.
type AlertRule struct {
	ID           int64     `json:"id"`
	ServiceID    string    `json:"service_id"`
	Metric       string    `json:"metric"`
	Threshold    float64   `json:"threshold"`
	DurationSecs int       `json:"duration_secs"`
	Enabled      bool      `json:"enabled"`
	CreatedAt    time.Time `json:"created_at"`
}

// RuleCreate carries new-rule input.
type RuleCreate struct {
	ServiceID    string
	Metric       string
	Threshold    float64
	DurationSecs int
}

// Notification kinds.
const (
	NotificationAlertBreach = "alert_breach"
	NotificationDeploy      = "deploy"
	NotificationIssue       = "issue"
)

// Notification is one in-app notification row.
type Notification struct {
	ID        int64      `json:"id"`
	ServiceID string     `json:"service_id"`
	Kind      string     `json:"kind"`
	Title     string     `json:"title"`
	Body      string     `json:"body"`
	CreatedAt time.Time  `json:"created_at"`
	ReadAt    *time.Time `json:"read_at"`
}

// Deploy types and outcomes.
const (
	DeployCutover  = "cutover"
	DeployRollback = "rollback"
	DeployManual   = "manual"
	DeployDeploy   = "deploy"

	DeploySuccess = "success"
	DeployFailure = "failure"
)

// DeployProbe is one public-URL health sample taken during an operation.
type DeployProbe struct {
	ID         int64     `json:"id"`
	ServiceID  string    `json:"service_id"`
	OK         bool      `json:"ok"`
	StatusCode int       `json:"status_code"`
	LatencyMs  int64     `json:"latency_ms"`
	SampledAt  time.Time `json:"sampled_at"`
}

// Deploy is one history entry.
type Deploy struct {
	ID           int64      `json:"id"`
	ServiceID    string     `json:"service_id"`
	SHA          string     `json:"sha"`
	Actor        string     `json:"actor"`
	Type         string     `json:"type"`
	Outcome      string     `json:"outcome"`
	StartedAt    *time.Time `json:"started_at"`
	FinishedAt   *time.Time `json:"finished_at"`
	DurationSecs *int64     `json:"duration_secs"`
	DowntimeSecs *float64   `json:"downtime_secs"`
	Notes        string     `json:"notes"`
	CreatedAt    time.Time  `json:"created_at"`
}

// DeployRecord carries new-entry input.
type DeployRecord struct {
	ServiceID    string
	SHA          string
	Actor        string
	Type         string
	Outcome      string
	Notes        string
	StartedAt    *time.Time
	FinishedAt   *time.Time
	DowntimeSecs *float64
}
