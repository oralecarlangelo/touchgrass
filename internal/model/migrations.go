package model

import (
	"time"
)

// Schema migration states of one migration, as the Migrations view
// reports them.
const (
	// MigrationApplied is recorded as applied and, when the shipped
	// files are readable, matches the file in the running build.
	MigrationApplied = "applied"
	// MigrationPending ships in the running build and is not applied.
	MigrationPending = "pending"
	// MigrationFailed started and never finished.
	MigrationFailed = "failed"
	// MigrationModified is applied, and the shipped file differs from
	// the one that was applied.
	MigrationModified = "modified"
	// MigrationMissing is applied, and the running build does not ship it.
	MigrationMissing = "missing"
	// MigrationRolledBack was marked rolled back and is not shipped.
	MigrationRolledBack = "rolled_back"
)

// Schema migration verdicts for a whole service, worst first.
const (
	MigrationVerdictFailed  = "failed"
	MigrationVerdictDrift   = "drift"
	MigrationVerdictPending = "pending"
	MigrationVerdictInSync  = "in_sync"
	MigrationVerdictUnknown = "unknown"
)

// Schema migration run kinds.
const (
	MigrationRunDeploy  = "deploy"
	MigrationRunResolve = "resolve"
	MigrationRunSeed    = "seed"
)

// Schema migration run states.
const (
	MigrationRunRunning   = "running"
	MigrationRunSucceeded = "succeeded"
	MigrationRunFailed    = "failed"
)

// Resolutions a failed migration can be given.
const (
	MigrationResolveApplied    = "applied"
	MigrationResolveRolledBack = "rolled_back"
)

// Schema migration audit actions recorded in the append-only log.
const (
	AuditMigrationConfig  = "migration_config"
	AuditMigrationDeploy  = "migration_deploy"
	AuditMigrationResolve = "migration_resolve"
	AuditSeedRun          = "seed_run"
)

// Migration is one schema migration of a service.
type Migration struct {
	Name            string     `json:"name"`
	State           string     `json:"state"`
	ChecksumApplied string     `json:"checksum_applied,omitempty"`
	ChecksumShipped string     `json:"checksum_shipped,omitempty"`
	StartedAt       *time.Time `json:"started_at,omitempty"`
	FinishedAt      *time.Time `json:"finished_at,omitempty"`
	RolledBackAt    *time.Time `json:"rolled_back_at,omitempty"`
	DurationMS      *int64     `json:"duration_ms,omitempty"`
	Steps           int        `json:"steps"`
	// Attempts counts the history rows for this name, so a migration
	// that failed, was resolved, and then applied reads as 2.
	Attempts int `json:"attempts"`
	// Logs is the failure text the migration tool recorded.
	Logs string `json:"logs,omitempty"`
}

// MigrationCounts is the number of migrations in each state.
type MigrationCounts struct {
	Applied    int `json:"applied"`
	Pending    int `json:"pending"`
	Failed     int `json:"failed"`
	Modified   int `json:"modified"`
	Missing    int `json:"missing"`
	RolledBack int `json:"rolled_back"`
}

// MigrationProbe reports whether one side of the comparison could be
// read. Error is a short operator-facing reason, never a credential.
type MigrationProbe struct {
	OK     bool   `json:"ok"`
	Target string `json:"target"`
	Error  string `json:"error,omitempty"`
}

// MigrationSeed says whether the running build declares a seed.
type MigrationSeed struct {
	Configured bool   `json:"configured"`
	Command    string `json:"command,omitempty"`
}

// MigrationStatus is the Migrations view of one service.
type MigrationStatus struct {
	Configured bool   `json:"configured"`
	Tool       string `json:"tool,omitempty"`
	Verdict    string `json:"verdict"`
	// History is the applied record, read from the database.
	History MigrationProbe `json:"history"`
	// Shipped is the set of migrations in the running container.
	Shipped    MigrationProbe  `json:"shipped"`
	Counts     MigrationCounts `json:"counts"`
	Migrations []Migration     `json:"migrations"`
	Seed       MigrationSeed   `json:"seed"`
	CheckedAt  time.Time       `json:"checked_at"`
}

// MigrationConfig is the stored migration setup of one service. The
// database URL is write-only: the API reports where it points, never
// the URL.
type MigrationConfig struct {
	ServiceID string `json:"service_id"`
	Tool      string `json:"tool"`
	// Database is the redacted target (host, port, database, schema).
	Database string `json:"database"`
	// Container is the compose service whose container ships the
	// migrations and runs the commands.
	Container string `json:"container"`
	// Workdir overrides the container's working directory.
	Workdir string `json:"workdir"`
	// MigrationsDir overrides the tool's default, relative to Workdir.
	MigrationsDir string `json:"migrations_dir"`
	// Command overrides the tool's default command prefix.
	Command   []string  `json:"command"`
	UpdatedBy string    `json:"updated_by"`
	UpdatedAt time.Time `json:"updated_at"`
}

// MigrationTool describes a migration tool this build offers and what
// it assumes when a setup leaves a field empty. The setup form shows
// these, so a new tool needs no change to the console.
type MigrationTool struct {
	Name          string   `json:"name"`
	MigrationsDir string   `json:"default_migrations_dir"`
	Command       []string `json:"default_command"`
}

// MigrationConfigInput is a config write. An empty DatabaseURL keeps
// the stored one.
type MigrationConfigInput struct {
	Tool          string   `json:"tool"`
	DatabaseURL   string   `json:"database_url"`
	Container     string   `json:"container"`
	Workdir       string   `json:"workdir"`
	MigrationsDir string   `json:"migrations_dir"`
	Command       []string `json:"command"`
}

// MigrationRun is one command touchgrass ran for a service: a migrate
// deploy, a resolve, or a seed. Output is only served by the run
// detail route.
type MigrationRun struct {
	ID         int64      `json:"id"`
	ServiceID  string     `json:"service_id"`
	Kind       string     `json:"kind"`
	Tool       string     `json:"tool"`
	Detail     string     `json:"detail"`
	Actor      string     `json:"actor"`
	State      string     `json:"state"`
	ExitCode   *int       `json:"exit_code,omitempty"`
	Container  string     `json:"container"`
	Image      string     `json:"image"`
	StartedAt  time.Time  `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
	Output     string     `json:"output,omitempty"`
	Truncated  bool       `json:"output_truncated,omitempty"`
}
