package model

import (
	"time"
)

// Host history metrics served by the fleet history endpoint.
const (
	// MetricLoad selects the 1-minute load average series. CPU and memory
	// reuse MetricCPU/MetricMem (history mem means used bytes, not the
	// alert rule's percent-of-limit).
	MetricLoad = "load"
)

// HostSample is one host row per sample interval. Value fields are
// pointers because non-Linux hosts and unreadable /proc fields degrade
// to null instead of failing the sample.
type HostSample struct {
	ID         int64     `json:"id"`
	SampledAt  time.Time `json:"sampled_at"`
	CPUPercent *float64  `json:"cpu_percent"`
	MemUsed    *int64    `json:"mem_used"`
	MemTotal   *int64    `json:"mem_total"`
	DiskUsed   *int64    `json:"disk_used"`
	DiskTotal  *int64    `json:"disk_total"`
	Load1      *float64  `json:"load_1"`
}

// ContainerSample is one per-container fleet row. Managed rows carry
// their service id; unmanaged rows leave it empty.
type ContainerSample struct {
	ID            int64     `json:"id"`
	SampledAt     time.Time `json:"sampled_at"`
	ContainerName string    `json:"container_name"`
	Project       string    `json:"project"`
	Managed       bool      `json:"managed"`
	ServiceID     string    `json:"service_id,omitempty"`
	State         string    `json:"state"`
	CPUPercent    float64   `json:"cpu_percent"`
	MemBytes      uint64    `json:"mem_bytes"`
	MemLimit      uint64    `json:"mem_limit"`
	Restarts      int       `json:"restarts"`
}

// FleetContainer is the latest sample per container for the fleet table.
type FleetContainer struct {
	Name       string    `json:"name"`
	Project    string    `json:"project"`
	Managed    bool      `json:"managed"`
	ServiceID  string    `json:"service_id,omitempty"`
	State      string    `json:"state"`
	CPUPercent float64   `json:"cpu_percent"`
	MemBytes   uint64    `json:"mem_bytes"`
	MemLimit   uint64    `json:"mem_limit"`
	Restarts   int       `json:"restarts"`
	SampledAt  time.Time `json:"sampled_at"`
}

// HistoryPoint is one bucket-averaged host history value.
type HistoryPoint struct {
	TS    time.Time `json:"ts"`
	Value float64   `json:"value"`
}
