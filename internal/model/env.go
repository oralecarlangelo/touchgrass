package model

import (
	"encoding/json"
	"fmt"
	"time"
)

// Env entry states. Applied entries match the file on disk; the other
// three are pending changes that the next apply renders.
const (
	EnvStateApplied = "applied"
	EnvStateAdded   = "added"
	EnvStateChanged = "changed"
	EnvStateRemoved = "removed"
)

// Env run and step statuses.
const (
	EnvRunQueued    = "queued"
	EnvRunRunning   = "running"
	EnvRunSucceeded = "succeeded"
	EnvRunFailed    = "failed"
)

// Env apply steps streamed over SSE and kept in the run log.
const (
	EnvStepRender   = "render"
	EnvStepValidate = "validate"
	EnvStepWrite    = "write"
	EnvStepRestart  = "restart"
	EnvStepProbe    = "probe"
	EnvStepDone     = "done"
	EnvStepFailed   = "failed"
)

// EventEnvApply is the SSE type for env apply progress.
const EventEnvApply = "env.apply"

// Env unconfigured reasons reported by the env view.
const (
	EnvReasonKeyMissing    = "key_missing"
	EnvReasonNotConfigured = "not_configured"
)

// EnvTarget is the managed env file on the host.
type EnvTarget struct {
	Path  string `json:"path"`
	Owner string `json:"owner"`
	Mode  string `json:"mode"`
}

// EnvConfig is the per-service env pillar configuration, stored under the
// "env" key of Service.Config beside the strategy fields.
//
// ReadCmd and WriteCmd are optional adapters: when set, import parses
// ReadCmd's stdout instead of reading the target, and apply pipes the
// rendered file to WriteCmd's stdin instead of installing it (the command
// then owns ownership, mode, and atomic install). Sudo prefixes every
// command and file operation with `sudo -n`; commands may also spell sudo
// themselves and leave Sudo false.
type EnvConfig struct {
	Target           EnvTarget `json:"target"`
	ValidateCmd      string    `json:"validate_cmd"`
	RestartCmd       string    `json:"restart_cmd"`
	ReadCmd          string    `json:"read_cmd,omitempty"`
	WriteCmd         string    `json:"write_cmd,omitempty"`
	ProbeURL         string    `json:"probe_url"`
	ProbeTimeoutSecs int       `json:"probe_timeout_secs,omitempty"`
	Sudo             bool      `json:"sudo"`
}

// envConfigEnvelope is the Service.Config shape the env pillar reads.
type envConfigEnvelope struct {
	Env *EnvConfig `json:"env"`
}

// EnvConfigOf decodes the env pillar config from a service definition. The
// second result is false when the service has no "env" block.
func EnvConfigOf(service Service) (EnvConfig, bool, error) {
	if len(service.Config) == 0 {
		return EnvConfig{}, false, nil
	}

	var envelope envConfigEnvelope

	if err := json.Unmarshal(service.Config, &envelope); err != nil {
		return EnvConfig{}, false, fmt.Errorf("service %q has invalid config: %w", service.ID, err)
	}

	if envelope.Env == nil || envelope.Env.Target.Path == "" {
		return EnvConfig{}, false, nil
	}

	return *envelope.Env, true, nil
}

// EnvEntry is one stored env key. Ciphertext and Nonce never serialize
// and the entry never holds plaintext: decryption happens in request scope.
type EnvEntry struct {
	ServiceID  string    `json:"service_id"`
	Key        string    `json:"key"`
	Ciphertext []byte    `json:"-"`
	Nonce      []byte    `json:"-"`
	KeyID      string    `json:"key_id"`
	IsSecret   bool      `json:"is_secret"`
	State      string    `json:"state"`
	UpdatedBy  string    `json:"updated_by"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// String renders the entry without its ciphertext.
func (e EnvEntry) String() string {
	return fmt.Sprintf("env entry %s/%s (%s, secret=%t)", e.ServiceID, e.Key, e.State, e.IsSecret)
}

// Pending reports whether the entry awaits an apply.
func (e EnvEntry) Pending() bool {
	return e.State != EnvStateApplied
}

// EnvEntryView is the API representation of one entry. Value is nil for
// secrets regardless of role; reveal is a separate audited call.
// AppliedValue carries the currently applied plaintext for changed,
// non-secret entries so a UI can offer undo.
type EnvEntryView struct {
	Key          string    `json:"key"`
	IsSecret     bool      `json:"is_secret"`
	Value        *string   `json:"value"`
	AppliedValue *string   `json:"applied_value,omitempty"`
	Masked       bool      `json:"masked"`
	UpdatedBy    string    `json:"updated_by"`
	UpdatedAt    time.Time `json:"updated_at"`
	State        string    `json:"state"`
	Required     bool      `json:"required"`
}

// EnvRevision is one applied snapshot of every entry.
type EnvRevision struct {
	ID             int64     `json:"id"`
	ServiceID      string    `json:"service_id"`
	CreatedBy      string    `json:"created_by"`
	CreatedAt      time.Time `json:"created_at"`
	Summary        string    `json:"summary"`
	RenderedSHA256 string    `json:"rendered_sha256"`
	Applied        bool      `json:"applied"`
}

// EnvRevisionEntry is one encrypted value inside a revision snapshot.
type EnvRevisionEntry struct {
	RevisionID int64  `json:"revision_id"`
	Key        string `json:"key"`
	Ciphertext []byte `json:"-"`
	Nonce      []byte `json:"-"`
	KeyID      string `json:"key_id"`
	IsSecret   bool   `json:"is_secret"`
}

// String renders the snapshot entry without its ciphertext.
func (e EnvRevisionEntry) String() string {
	return fmt.Sprintf("env revision %d entry %s (secret=%t)", e.RevisionID, e.Key, e.IsSecret)
}

// EnvRevisionSummary is the applied-revision pointer in the env view.
type EnvRevisionSummary struct {
	ID        int64     `json:"id"`
	CreatedAt time.Time `json:"created_at"`
	CreatedBy string    `json:"created_by"`
	Summary   string    `json:"summary"`
}

// EnvRevisionView is one revision in the history list with its key-level
// diff against the previous revision. Values are never included.
type EnvRevisionView struct {
	ID             int64     `json:"id"`
	CreatedAt      time.Time `json:"created_at"`
	CreatedBy      string    `json:"created_by"`
	Summary        string    `json:"summary"`
	Added          []string  `json:"added"`
	Changed        []string  `json:"changed"`
	Removed        []string  `json:"removed"`
	RenderedSHA256 string    `json:"rendered_sha256"`
	Applied        bool      `json:"applied"`
}

// EnvDiff is the pending-versus-applied key diff.
type EnvDiff struct {
	Added          []string `json:"added"`
	Changed        []string `json:"changed"`
	Removed        []string `json:"removed"`
	RenderedSHA256 string   `json:"rendered_sha256"`
}

// EnvRunStep is one logged step of an apply run. Name is the pipeline
// step; Status is running, succeeded, or failed.
type EnvRunStep struct {
	Name    string    `json:"name"`
	Status  string    `json:"status"`
	Message string    `json:"message,omitempty"`
	At      time.Time `json:"at"`
}

// EnvRun is one apply or rollback run with its step log. Error holds the
// failure cause, ValidationLines the validator or write command output
// of a failed run, and Restored whether the previous file was put back.
type EnvRun struct {
	ID              string       `json:"run_id"`
	ServiceID       string       `json:"service_id"`
	RevisionID      *int64       `json:"revision_id"`
	State           string       `json:"status"`
	StartedAt       time.Time    `json:"started_at"`
	FinishedAt      *time.Time   `json:"finished_at,omitempty"`
	Steps           []EnvRunStep `json:"steps"`
	Error           string       `json:"error,omitempty"`
	ValidationLines []string     `json:"validation_lines,omitempty"`
	Restored        bool         `json:"restored,omitempty"`
}

// EnvValidation is the outcome of the last validator run. MissingRequired
// is parsed from "missing required keys: A, B" output lines.
type EnvValidation struct {
	ServiceID       string    `json:"-"`
	OK              bool      `json:"ok"`
	Lines           []string  `json:"lines"`
	MissingRequired []string  `json:"missing_required"`
	At              time.Time `json:"at"`
}

// EnvImportResult reports one import run.
type EnvImportResult struct {
	Imported      int `json:"imported"`
	Skipped       int `json:"skipped"`
	SecretGuessed int `json:"secret_guessed"`
}

// EnvView is the GET /services/{id}/env payload.
type EnvView struct {
	Configured      bool                `json:"configured"`
	Reason          string              `json:"reason,omitempty"`
	Target          *EnvTarget          `json:"target"`
	Entries         []EnvEntryView      `json:"entries"`
	AppliedRevision *EnvRevisionSummary `json:"applied_revision"`
	PendingCount    int                 `json:"pending_count"`
	LastValidation  *EnvValidation      `json:"last_validation"`
	ApplyInProgress bool                `json:"apply_in_progress"`
	ActiveRunID     string              `json:"active_run_id,omitempty"`
}
