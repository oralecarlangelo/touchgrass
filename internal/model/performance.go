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

// PerformanceRange bounds aggregate reads.
type PerformanceRange struct {
	ServiceID string
	Start     time.Time
	End       time.Time
	Limit     int
}
