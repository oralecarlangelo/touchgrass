package model

import "time"

// Security enums use an empty zero value so missing data cannot silently mean
// a valid severity, confidence, category, or workflow status.
type SecuritySeverity string
type SecurityConfidence string
type SecurityCategory string
type SecurityFindingStatus string

const (
	SecuritySeverityUnknown  SecuritySeverity = ""
	SecuritySeverityLow      SecuritySeverity = "low"
	SecuritySeverityMedium   SecuritySeverity = "medium"
	SecuritySeverityHigh     SecuritySeverity = "high"
	SecuritySeverityCritical SecuritySeverity = "critical"

	SecurityConfidenceUnknown SecurityConfidence = ""
	SecurityConfidenceLow     SecurityConfidence = "low"
	SecurityConfidenceMedium  SecurityConfidence = "medium"
	SecurityConfidenceHigh    SecurityConfidence = "high"

	SecurityCategoryUnknown SecurityCategory = ""
	SecurityCategoryPosture SecurityCategory = "posture"
	SecurityCategoryProcess SecurityCategory = "process"
	SecurityCategoryDrift   SecurityCategory = "drift"

	SecurityFindingStatusUnknown      SecurityFindingStatus = ""
	SecurityFindingStatusOpen         SecurityFindingStatus = "open"
	SecurityFindingStatusAcknowledged SecurityFindingStatus = "acknowledged"
	SecurityFindingStatusResolved     SecurityFindingStatus = "resolved"
	SecurityFindingStatusSuppressed   SecurityFindingStatus = "suppressed"
)

type SecurityActivation struct {
	ActivatedAt *time.Time `json:"activated_at"`
	ActivatedBy string     `json:"activated_by"`
}

type ContainerBaseline struct {
	ID                       int64     `json:"id"`
	ContainerID              string    `json:"container_id"`
	ImageFingerprint         string    `json:"image_fingerprint"`
	ConfigurationFingerprint string    `json:"configuration_fingerprint"`
	ApprovedBy               string    `json:"approved_by"`
	ApprovedAt               time.Time `json:"approved_at"`
	Active                   bool      `json:"active"`
}

type ContainerApproval struct {
	ContainerID              string
	ImageFingerprint         string
	ConfigurationFingerprint string
	Actor                    string
	ApprovedAt               time.Time
}

type ContainerIgnore struct {
	ContainerID string     `json:"container_id"`
	Reason      string     `json:"reason"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
	NonExpiring bool       `json:"non_expiring"`
	Actor       string     `json:"actor"`
	CreatedAt   time.Time  `json:"created_at"`
}

type SecurityPolicy struct {
	ID                     int64     `json:"id,omitempty"`
	Scope                  string    `json:"scope,omitempty"`
	ScopeID                string    `json:"scope_id,omitempty"`
	AllowPrivileged        bool      `json:"allow_privileged"`
	AllowHostPID           bool      `json:"allow_host_pid"`
	AllowHostNetwork       bool      `json:"allow_host_network"`
	AllowDockerSocket      bool      `json:"allow_docker_socket"`
	AllowRoot              bool      `json:"allow_root"`
	AllowWritableRootFS    bool      `json:"allow_writable_root_fs"`
	AllowedCapabilities    []string  `json:"allowed_capabilities"`
	AllowedPublishedPorts  []string  `json:"allowed_published_ports"`
	AllowedExecutablePaths []string  `json:"allowed_executable_paths"`
	AllowedWritablePaths   []string  `json:"allowed_writable_paths"`
	UpdatedBy              string    `json:"updated_by,omitempty"`
	UpdatedAt              time.Time `json:"updated_at,omitempty"`
}

type SecurityObservation struct {
	ContainerID      string
	RuleID           string
	Category         SecurityCategory
	Severity         SecuritySeverity
	Confidence       SecurityConfidence
	Detected         bool
	CoverageComplete bool
	Evidence         map[string]string
	ObservedAt       time.Time
}

type SecurityEvaluation struct {
	Observation         SecurityObservation
	BaselineFingerprint string
	ServiceID           string
}

type SecurityFinding struct {
	ID                 int64                 `json:"id"`
	Fingerprint        string                `json:"fingerprint"`
	Episode            int                   `json:"episode"`
	ContainerID        string                `json:"container_id"`
	RuleID             string                `json:"rule_id"`
	Category           SecurityCategory      `json:"category"`
	Severity           SecuritySeverity      `json:"severity"`
	Confidence         SecurityConfidence    `json:"confidence"`
	Status             SecurityFindingStatus `json:"status"`
	OccurrenceCount    int                   `json:"occurrence_count"`
	OpenedAt           time.Time             `json:"opened_at"`
	LastSeenAt         time.Time             `json:"last_seen_at"`
	Evidence           map[string]string     `json:"evidence"`
	AcknowledgedAt     *time.Time            `json:"acknowledged_at,omitempty"`
	AcknowledgedBy     string                `json:"acknowledged_by,omitempty"`
	ResolvedAt         *time.Time            `json:"resolved_at,omitempty"`
	ResolvedBy         string                `json:"resolved_by,omitempty"`
	SuppressedUntil    *time.Time            `json:"suppressed_until,omitempty"`
	SuppressionReason  string                `json:"suppression_reason,omitempty"`
	SuppressionActor   string                `json:"suppression_actor,omitempty"`
	NotificationStatus string                `json:"notification_status,omitempty"`
}

type SecurityFindingQuery struct {
	Status      SecurityFindingStatus
	Severity    SecuritySeverity
	ContainerID string
	Sort        string
	Limit       int
	Offset      int
}

type SecuritySummary struct {
	Open             int        `json:"open"`
	Critical         int        `json:"critical"`
	High             int        `json:"high"`
	Suppressed       int        `json:"suppressed"`
	CoverageComplete bool       `json:"coverage_complete"`
	LastObservedAt   *time.Time `json:"last_observed_at,omitempty"`
}

type FindingSuppression struct {
	Actor  string
	Reason string
	Until  *time.Time
}
