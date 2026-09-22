package model

import (
	"time"
)

// Database ids served by GET /api/databases.
const (
	DatabasePostgres = "postgres"
	DatabaseRedis    = "redis"
	DatabaseSQLite   = "sqlite"
)

// Database labels shown on the Databases view.
const (
	DatabasePostgresLabel = "PostgreSQL"
	DatabaseRedisLabel    = "Redis"
	DatabaseSQLiteLabel   = "SQLite"
)

// Database is one health entry: postgres, redis, or sqlite. Pointer
// fields stay null where the probe cannot provide them (unconfigured,
// unreachable, or single-query failure): the view renders "n/a",
// never an error.
type Database struct {
	ID              string     `json:"id"`
	Label           string     `json:"label"`
	Configured      bool       `json:"configured"`
	Reachable       bool       `json:"reachable"`
	Version         *string    `json:"version,omitempty"`
	SizeBytes       *int64     `json:"size_bytes,omitempty"`
	ConnectionsUsed *int64     `json:"connections_used,omitempty"`
	ConnectionsMax  *int64     `json:"connections_max,omitempty"`
	UptimeSecs      *int64     `json:"uptime_secs,omitempty"`
	UsedMemoryBytes *int64     `json:"used_memory_bytes,omitempty"`
	Integrity       *string    `json:"integrity,omitempty"`
	LastBackupAt    *time.Time `json:"last_backup_at,omitempty"`
}

// Backup SHA sidecar states.
const (
	BackupSHAPresent = "present"
	BackupSHAMissing = "missing"
)

// BackupInfo is one pg_dump artifact on the host.
type BackupInfo struct {
	Name      string    `json:"name"`
	SizeBytes int64     `json:"size_bytes"`
	CreatedAt time.Time `json:"created_at"`
	SHA256    string    `json:"sha256"`
}

// Database job kinds.
const (
	DBJobBackup  = "backup"
	DBJobRestore = "restore"
)

// Database job statuses.
const (
	DBJobRunning = "running"
	DBJobSuccess = "success"
	DBJobFailed  = "failed"
)

// DBJob is one backup or restore run.
type DBJob struct {
	ID         int64      `json:"id"`
	Kind       string     `json:"kind"`
	Target     string     `json:"target"`
	Status     string     `json:"status"`
	Detail     string     `json:"detail"`
	StartedAt  time.Time  `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
}

// Database audit actions recorded in the append-only log.
const (
	AuditDBBackup  = "db_backup"
	AuditDBRestore = "db_restore"
)

// BackupVerify reports one archive validity check.
type BackupVerify struct {
	Name string `json:"name"`
	OK   bool   `json:"ok"`
}

// SQLiteHealth is the embedded database version plus the first
// PRAGMA integrity_check verdict ("ok" when healthy).
type SQLiteHealth struct {
	Version   string
	Integrity string
}
