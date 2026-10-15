package model

import (
	"fmt"
	"time"
)

// HTTPHistogram uses fixed, mergeable upper bounds in milliseconds. The last
// counter is +Inf.
type HTTPHistogram struct {
	Counts [10]int64 `json:"counts"`
}

// Observe adds one duration to the first matching fixed bucket.
func (h *HTTPHistogram) Observe(milliseconds float64) {
	bounds := [...]float64{10, 25, 50, 100, 250, 500, 1000, 2500, 5000}
	for index, bound := range bounds {
		if milliseconds <= bound {
			h.Counts[index]++
			return
		}
	}
	h.Counts[len(h.Counts)-1]++
}

// Merge adds another mergeable histogram.
func (h *HTTPHistogram) Merge(other HTTPHistogram) {
	for index := range h.Counts {
		h.Counts[index] += other.Counts[index]
	}
}

// HTTPObservation is one parsed, privacy-safe timing record. It deliberately
// has no raw URI, query, client, header, cookie, body, or request-ID field.
type HTTPObservation struct {
	Timestamp          time.Time `json:"timestamp"`
	Method             string    `json:"method"`
	RouteFamily        string    `json:"route_family"`
	Status             int       `json:"status"`
	UpstreamStatus     int       `json:"upstream_status,omitempty"`
	DurationMS         float64   `json:"duration_ms"`
	UpstreamConnectMS  float64   `json:"upstream_connect_ms"`
	UpstreamHeaderMS   float64   `json:"upstream_header_ms"`
	UpstreamResponseMS float64   `json:"upstream_response_ms"`
	BytesSent          int64     `json:"bytes_sent"`
	RequestBytes       int64     `json:"request_bytes"`
	ConnectionRequests int64     `json:"connection_requests"`
	Upstream           string    `json:"upstream"`
}

// String intentionally renders only the bounded observation fields.
func (o HTTPObservation) String() string {
	return fmt.Sprintf("%s %s %s %d %.3fms upstream=%s", o.Timestamp.UTC().Format(time.RFC3339), o.Method, o.RouteFamily, o.Status, o.DurationMS, o.Upstream)
}

// HTTPMetricBucket is a mergeable minute or hourly aggregate. IngestKey is an
// opaque cursor/batch identity used only to make additive retries idempotent.
type HTTPMetricBucket struct {
	BucketStart             time.Time     `json:"bucket_start"`
	ServiceID               string        `json:"service_id"`
	Environment             string        `json:"environment"`
	Method                  string        `json:"method"`
	RouteFamily             string        `json:"route_family"`
	Upstream                string        `json:"upstream"`
	StatusClass             int           `json:"status_class"`
	Histogram               HTTPHistogram `json:"histogram"`
	RequestCount            int64         `json:"request_count"`
	ErrorCount              int64         `json:"error_count"`
	DurationTotalMS         float64       `json:"duration_total_ms"`
	UpstreamConnectTotalMS  float64       `json:"upstream_connect_total_ms"`
	UpstreamHeaderTotalMS   float64       `json:"upstream_header_total_ms"`
	UpstreamResponseTotalMS float64       `json:"upstream_response_total_ms"`
	BytesSent               int64         `json:"bytes_sent"`
	RequestBytes            int64         `json:"request_bytes"`
	IngestKey               string        `json:"-"`
}

// CollectorStatus is the safe health projection used by performance queries.
type CollectorStatus struct {
	Source         string    `json:"source"`
	LastSuccessAt  time.Time `json:"last_success_at,omitempty"`
	LastRecordedAt time.Time `json:"last_recorded_at,omitempty"`
	LastError      string    `json:"last_error,omitempty"`
	DroppedRecords int64     `json:"dropped_records"`
	Stale          bool      `json:"stale"`
}

// NumericSample is one bounded database or cache metric. Cumulative marks
// monotonic counters whose deltas are meaningful across valid baselines.
type NumericSample struct {
	Value      float64 `json:"value"`
	Cumulative bool    `json:"cumulative"`
}

// PostgresStatementSnapshot contains only pg_stat_statements numeric fields.
// Raw query text is intentionally absent from this model.
type PostgresStatementSnapshot struct {
	Calls             float64 `json:"calls"`
	TotalExecMS       float64 `json:"total_exec_ms"`
	Rows              float64 `json:"rows"`
	SharedBlocksHit   float64 `json:"shared_blocks_hit"`
	SharedBlocksRead  float64 `json:"shared_blocks_read"`
	TempBlocksRead    float64 `json:"temp_blocks_read"`
	TempBlocksWritten float64 `json:"temp_blocks_written"`
}

// PostgresSnapshot is a privacy-safe point-in-time aggregate.
type PostgresSnapshot struct {
	SampledAt  time.Time                            `json:"sampled_at"`
	Metrics    map[string]NumericSample             `json:"metrics"`
	Statements map[string]PostgresStatementSnapshot `json:"statements"`
	Errors     []string                             `json:"errors,omitempty"`
}

// RedisCommandSnapshot contains bounded command-family totals.
type RedisCommandSnapshot struct {
	Calls        float64 `json:"calls"`
	Microseconds float64 `json:"microseconds"`
}

// RedisSnapshot is a privacy-safe point-in-time aggregate. It has no key,
// value, client-address, or slow-command argument fields.
type RedisSnapshot struct {
	SampledAt time.Time                       `json:"sampled_at"`
	Metrics   map[string]NumericSample        `json:"metrics"`
	Commands  map[string]RedisCommandSnapshot `json:"commands"`
	Errors    []string                        `json:"errors,omitempty"`
}

// PostgresMetricBucket is one fixed-minute native metric aggregate.
type PostgresMetricBucket struct {
	BucketStart time.Time `json:"bucket_start"`
	ServiceID   string    `json:"service_id"`
	Environment string    `json:"environment"`
	Metric      string    `json:"metric"`
	Value       float64   `json:"value"`
	Delta       float64   `json:"delta"`
	Reset       bool      `json:"reset"`
}

// PostgresStatementBucket is one fixed-minute numeric statement delta.
type PostgresStatementBucket struct {
	BucketStart       time.Time `json:"bucket_start"`
	ServiceID         string    `json:"service_id"`
	Environment       string    `json:"environment"`
	QueryID           string    `json:"query_id"`
	Calls             float64   `json:"calls"`
	TotalExecMS       float64   `json:"total_exec_ms"`
	Rows              float64   `json:"rows"`
	SharedBlocksHit   float64   `json:"shared_blocks_hit"`
	SharedBlocksRead  float64   `json:"shared_blocks_read"`
	TempBlocksRead    float64   `json:"temp_blocks_read"`
	TempBlocksWritten float64   `json:"temp_blocks_written"`
	Reset             bool      `json:"reset"`
}

// RedisMetricBucket is one fixed-minute cache or command-family aggregate.
type RedisMetricBucket struct {
	BucketStart   time.Time `json:"bucket_start"`
	ServiceID     string    `json:"service_id"`
	Environment   string    `json:"environment"`
	Metric        string    `json:"metric"`
	CommandFamily string    `json:"command_family,omitempty"`
	Value         float64   `json:"value"`
	Delta         float64   `json:"delta"`
	Reset         bool      `json:"reset"`
}

// LogVolumeBucket is a bounded per-minute count. Raw log content is not part
// of this aggregate model.
type LogVolumeBucket struct {
	BucketStart  time.Time `json:"bucket_start"`
	ServiceID    string    `json:"service_id"`
	Environment  string    `json:"environment"`
	Severity     string    `json:"severity"`
	LineCount    int64     `json:"line_count"`
	DroppedCount int64     `json:"dropped_count"`
	IngestKey    string    `json:"-"`
}

// ContainerEvent is a bounded lifecycle or health transition. Expected marks
// deployment-controlled replacement so UI alerting can suppress routine noise.
type ContainerEvent struct {
	SourceEventID  string    `json:"source_event_id"`
	OccurredAt     time.Time `json:"occurred_at"`
	ServiceID      string    `json:"service_id"`
	Environment    string    `json:"environment"`
	EventType      string    `json:"event_type"`
	ContainerName  string    `json:"container_name"`
	ComposeService string    `json:"compose_service"`
	Expected       bool      `json:"expected"`
}

// PerformanceRange bounds aggregate reads.
type PerformanceRange struct {
	ServiceID string
	Start     time.Time
	End       time.Time
	Limit     int
}

// Performance query resolutions.
const (
	PerformanceResolutionAuto   = "auto"
	PerformanceResolutionMinute = "minute"
	PerformanceResolutionHour   = "hour"
)

// PerformanceQuery is the bounded investigation filter shared by the global
// and service-scoped APIs.
type PerformanceQuery struct {
	ServiceID   string
	Start       time.Time
	End         time.Time
	Resolution  string
	RouteFamily string
	Sort        string
	Limit       int
}
