package model

import "time"

// Occurrence types accepted at ingestion.
const (
	OccurrenceException = "exception"
	OccurrenceMessage   = "message"
)

// APIKey is one revocable ingestion credential. The hash never leaves the
// store; List shows the prefix only.
type APIKey struct {
	ID         int64      `json:"id"`
	ServiceID  string     `json:"service_id"`
	KeyHash    string     `json:"-"`
	KeyPrefix  string     `json:"key_prefix"`
	SampleRate float64    `json:"sample_rate"`
	RevokedAt  *time.Time `json:"revoked_at"`
	CreatedAt  time.Time  `json:"created_at"`
}

// KeyCreate carries API key minting input.
type KeyCreate struct {
	ServiceID  string  `json:"service_id"`
	SampleRate float64 `json:"sample_rate"`
}

// StackFrame is one parsed V8 stack frame.
type StackFrame struct {
	Function string `json:"function"`
	File     string `json:"file"`
	Line     int    `json:"line"`
	Column   int    `json:"column"`
}

// Breadcrumb is one SDK trail entry.
type Breadcrumb struct {
	At       string `json:"at"`
	Category string `json:"category"`
	Message  string `json:"message"`
}

// IngestReport is one validated SDK error report.
type IngestReport struct {
	Type        string       `json:"type"`
	Message     string       `json:"message"`
	Stack       []StackFrame `json:"stack"`
	Breadcrumbs []Breadcrumb `json:"breadcrumbs"`
	Release     string       `json:"release"`
}

// Occurrence is one stored error report. IssueID is null for pre-S8 rows.
type Occurrence struct {
	ID          int64        `json:"id"`
	ServiceID   string       `json:"service_id"`
	Type        string       `json:"type"`
	Message     string       `json:"message"`
	Stack       []StackFrame `json:"stack"`
	Breadcrumbs []Breadcrumb `json:"breadcrumbs"`
	Release     string       `json:"release"`
	IssueID     *int64       `json:"issue_id"`
	CreatedAt   time.Time    `json:"created_at"`
}
