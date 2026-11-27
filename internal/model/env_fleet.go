package model

import "time"

// EnvFleetSummary describes the state of a target without listing its entries.
type EnvFleetSummary struct {
	ServiceID       string     `json:"service_id"`
	Configured      bool       `json:"configured"`
	Reason          string     `json:"reason,omitempty"`
	TargetPath      string     `json:"target_path,omitempty"`
	KeyCount        int        `json:"key_count"`
	SecretCount     int        `json:"secret_count"`
	PendingCount    int        `json:"pending_count"`
	LastAppliedAt   *time.Time `json:"last_applied_at,omitempty"`
	ApplyInProgress bool       `json:"apply_in_progress"`
}
