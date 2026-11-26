package model

import "time"

const (
	DefaultSQLiteMaxBytes              int64 = 1 << 30
	DefaultLogClassMaxBytes            int64 = 512 << 20
	DefaultPerformanceClassMaxBytes    int64 = 256 << 20
	DefaultIssueClassMaxBytes          int64 = 128 << 20
	DefaultAdministrativeClassMaxBytes int64 = 64 << 20
	DefaultSecurityClassMaxBytes       int64 = 64 << 20
	MaximumSecurityEvidenceBytes             = 32 << 10
	MaximumMaintenanceBatch                  = 1000
)

// CollectorCursor is durable progress and health for one native source.
// LastError must contain a safe category, never a rejected payload.
type CollectorCursor struct {
	Source                string    `json:"source"`
	Identity              string    `json:"identity"`
	Offset                int64     `json:"offset"`
	LastRecordedAt        time.Time `json:"last_recorded_at,omitempty"`
	LastSuccessAt         time.Time `json:"last_success_at,omitempty"`
	LastError             string    `json:"last_error,omitempty"`
	DroppedRecords        int64     `json:"dropped_records"`
	CoverageComplete      bool      `json:"coverage_complete"`
	EffectiveIntervalSecs int64     `json:"effective_interval_secs,omitempty"`
	UpdatedAt             time.Time `json:"updated_at"`
}

// MaintenancePolicy bounds one SQLite maintenance pass.
type MaintenancePolicy struct {
	Now                           time.Time
	BatchSize                     int
	MaxBytes                      int64
	LogMaxBytes                   int64
	PerformanceMaxBytes           int64
	IssueMaxBytes                 int64
	AdministrativeMaxBytes        int64
	RetentionMetrics              time.Duration
	RetentionNotifications        time.Duration
	RetentionDeploys              time.Duration
	RetentionErrors               time.Duration
	RetentionLogs                 time.Duration
	RetentionSDKLogs              time.Duration
	RetentionPerformance          time.Duration
	RetentionPerformanceHourly    time.Duration
	RetentionSecurityObservations time.Duration
	RetentionContainerEvents      time.Duration
	RetentionSecurityFindings     time.Duration
}

// MaintenanceResult reports bounded cleanup and storage pressure.
type MaintenanceResult struct {
	RanAt                  time.Time        `json:"ran_at"`
	DeletedRows            int64            `json:"deleted_rows"`
	DeletedByTable         map[string]int64 `json:"deleted_by_table"`
	DatabaseBytes          int64            `json:"database_bytes"`
	MaxBytes               int64            `json:"max_bytes"`
	OverBudget             bool             `json:"over_budget"`
	ClassBytes             map[string]int64 `json:"class_bytes"`
	OverBudgetClasses      []string         `json:"over_budget_classes"`
	WALCheckpointAttempted bool             `json:"wal_checkpoint_attempted"`
	WALBusy                int              `json:"wal_busy"`
	WALLogFrames           int              `json:"wal_log_frames"`
	WALCheckpointedFrames  int              `json:"wal_checkpointed_frames"`
	// AutoVacuum is the database's mode: "incremental" releases freed pages,
	// "none" cannot. A file created before auto_vacuum was set keeps "none"
	// until a full VACUUM rewrites it, which is an operator's decision, so the
	// sweep reports the mode instead of changing it.
	AutoVacuum string `json:"auto_vacuum"`
	// FreelistPages is how many pages were free AFTER the incremental vacuum;
	// VacuumedPages is how many it handed back to the filesystem.
	FreelistPages int64 `json:"freelist_pages"`
	VacuumedPages int64 `json:"vacuumed_pages"`
}

// MaintenanceStatus exposes the last pass without hiding stale state.
type MaintenanceStatus struct {
	LastRunAt     time.Time         `json:"last_run_at,omitempty"`
	LastSuccessAt time.Time         `json:"last_success_at,omitempty"`
	LastError     string            `json:"last_error,omitempty"`
	Stale         bool              `json:"stale"`
	LastResult    MaintenanceResult `json:"last_result"`
}
