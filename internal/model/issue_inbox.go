package model

// IssueInboxQuery is one bounded page of the issue inbox.
type IssueInboxQuery struct {
	AllServices bool
	ServiceIDs  []string
	Search      string
	Release     string
	Start       string
	End         string
	Before      IssueCursor
	Limit       int
}

// IssueCursor identifies an exact position in newest-activity order.
type IssueCursor struct {
	LastSeen string `json:"seen"`
	ID       int64  `json:"id"`
}
