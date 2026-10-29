package model

import "time"

// ContainerClassification is the operator relationship to an observed
// container. The empty value is deliberately invalid/unspecified.
type ContainerClassification string

const (
	ContainerClassificationUnspecified      ContainerClassification = ""
	ContainerClassificationManaged          ContainerClassification = "managed"
	ContainerClassificationApprovedObserved ContainerClassification = "approved_observed"
	ContainerClassificationUnknown          ContainerClassification = "unknown"
	ContainerClassificationIgnored          ContainerClassification = "ignored"
	ContainerClassificationMissingExpected  ContainerClassification = "missing_expected"
)

// SafeMount is the non-sensitive portion of a container mount.
type SafeMount struct {
	Type        string `json:"type"`
	Destination string `json:"destination"`
	Writable    bool   `json:"writable"`
}

// SafeInspection contains security-relevant normalized configuration. It has
// no fields capable of accepting environment values or host mount sources.
type SafeInspection struct {
	CoverageComplete bool        `json:"coverage_complete"`
	User             string      `json:"user"`
	EntrypointHash   string      `json:"entrypoint_hash,omitempty"`
	CommandHash      string      `json:"command_hash,omitempty"`
	Privileged       bool        `json:"privileged"`
	ReadonlyRootFS   bool        `json:"readonly_root_fs"`
	CapAdd           []string    `json:"cap_add"`
	PIDMode          string      `json:"pid_mode,omitempty"`
	IPCMode          string      `json:"ipc_mode,omitempty"`
	NetworkMode      string      `json:"network_mode,omitempty"`
	RestartPolicy    string      `json:"restart_policy,omitempty"`
	MemoryBytes      int64       `json:"memory_bytes,omitempty"`
	NanoCPUs         int64       `json:"nano_cpus,omitempty"`
	PIDsLimit        *int64      `json:"pids_limit,omitempty"`
	Mounts           []SafeMount `json:"mounts"`
	PublishedPorts   []string    `json:"published_ports,omitempty"`
	HasDockerSocket  bool        `json:"has_docker_socket"`
}

// ContainerInstance is one durable Docker identity. Names may be reused;
// container IDs are the lifecycle key.
type ContainerInstance struct {
	ID                       string                  `json:"id"`
	Name                     string                  `json:"name"`
	Image                    string                  `json:"image"`
	ImageID                  string                  `json:"image_id"`
	State                    string                  `json:"state"`
	Status                   string                  `json:"status"`
	ComposeProject           string                  `json:"compose_project"`
	ComposeService           string                  `json:"compose_service"`
	ServiceID                string                  `json:"service_id,omitempty"`
	Classification           ContainerClassification `json:"classification"`
	Current                  bool                    `json:"current"`
	Present                  bool                    `json:"present"`
	FirstSeenAt              time.Time               `json:"first_seen_at"`
	LastSeenAt               time.Time               `json:"last_seen_at"`
	AbsentAt                 *time.Time              `json:"absent_at,omitempty"`
	ImageFingerprint         string                  `json:"image_fingerprint,omitempty"`
	ConfigurationFingerprint string                  `json:"configuration_fingerprint,omitempty"`
	Inspection               SafeInspection          `json:"inspection"`
	CPUPercent               *float64                `json:"cpu_percent"`
	MemBytes                 *uint64                 `json:"mem_bytes"`
	MemLimit                 *uint64                 `json:"mem_limit"`
	Restarts                 *int                    `json:"restarts"`
}

// InventoryBatch is one reconciliation result. Only a complete batch is
// authoritative for absence detection.
type InventoryBatch struct {
	ObservedAt        time.Time
	Instances         []ContainerInstance
	Complete          bool
	MissingGrace      time.Duration
	LastErrorCategory string
}

// ContainerQuery bounds and filters lifecycle inventory reads.
type ContainerQuery struct {
	Classification    ContainerClassification
	State             string
	ServiceID         string
	IncludeHistorical bool
	Sort              string
	Limit             int
	Offset            int
}

// InventorySummary describes the last authoritative inventory and counts.
type InventorySummary struct {
	ReconciledAt      *time.Time `json:"reconciled_at"`
	Complete          bool       `json:"complete"`
	LastErrorCategory string     `json:"last_error_category,omitempty"`
	ObservedCount     int        `json:"observed_count"`
	CurrentCount      int        `json:"current_count"`
	PresentCount      int        `json:"present_count"`
	Stale             bool       `json:"stale"`
}
