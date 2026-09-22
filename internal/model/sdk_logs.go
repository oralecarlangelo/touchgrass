package model

import "time"

// SDK log levels, stored per row (unlike container levels, which are
// parsed at read time). Fatal outranks error; trace is below debug.
const (
	SDKLogTrace = "trace"
	SDKLogDebug = "debug"
	SDKLogInfo  = "info"
	SDKLogWarn  = "warn"
	SDKLogError = "error"
	SDKLogFatal = "fatal"
)

// SDK log attribute value types, mirroring the Sentry Log protocol.
const (
	SDKAttrString  = "string"
	SDKAttrInteger = "integer"
	SDKAttrDouble  = "double"
	SDKAttrBoolean = "boolean"
)

// SDKLogAttribute is one typed structured-log attribute.
type SDKLogAttribute struct {
	Value any    `json:"value"`
	Type  string `json:"type"`
}

// SDKLogItem is one wire log record in an ingest batch.
type SDKLogItem struct {
	Timestamp      float64                    `json:"timestamp"`
	Level          string                     `json:"level"`
	Body           string                     `json:"body"`
	SeverityNumber *int                       `json:"severity_number,omitempty"`
	TraceID        string                     `json:"trace_id,omitempty"`
	SpanID         string                     `json:"span_id,omitempty"`
	Attributes     map[string]SDKLogAttribute `json:"attributes,omitempty"`
}

// SDKLogBatch is the POST /api/ingest/logs body: one release tag plus
// 1..1000 items, validated all-or-nothing.
type SDKLogBatch struct {
	Release string       `json:"release,omitempty"`
	Items   []SDKLogItem `json:"items"`
}

// SDKLog is one stored structured log row.
type SDKLog struct {
	ID         int64                      `json:"id"`
	ServiceID  string                     `json:"service_id"`
	Ts         time.Time                  `json:"ts"`
	Level      string                     `json:"level"`
	Severity   int                        `json:"severity_number"`
	Message    string                     `json:"message"`
	Attributes map[string]SDKLogAttribute `json:"attributes"`
	TraceID    string                     `json:"trace_id"`
	SpanID     string                     `json:"span_id"`
	Release    string                     `json:"release"`
	CreatedAt  time.Time                  `json:"created_at"`
}
