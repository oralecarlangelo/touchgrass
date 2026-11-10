package model

// ResourceKind names a kind of row that belongs to one service and is
// addressed by its own id, so its service has to be looked up.
type ResourceKind int

// Supported resource kinds.
const (
	ResourceUnknown ResourceKind = iota
	ResourceIssue
	ResourceLogLine
	ResourceAlertRule
	ResourceIssueRule
	ResourceAPIKey
	ResourceNotification
)
