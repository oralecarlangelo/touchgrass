package model

import "time"

// ImageView is one local daemon image for the Images screen.
type ImageView struct {
	ID         string    `json:"id"`
	RepoTags   []string  `json:"repo_tags"`
	SizeBytes  int64     `json:"size_bytes"`
	CreatedAt  time.Time `json:"created_at"`
	Containers int64     `json:"containers"`
	Dangling   bool      `json:"dangling"`
}

// ImagePruneView reports one dangling-prune run.
type ImagePruneView struct {
	Deleted        int   `json:"deleted"`
	ReclaimedBytes int64 `json:"reclaimed_bytes"`
}

// DaemonSnapshot is the docker info subset the System screen shows.
type DaemonSnapshot struct {
	ServerVersion   string `json:"server_version"`
	Architecture    string `json:"architecture"`
	OperatingSystem string `json:"operating_system"`
	KernelVersion   string `json:"kernel_version"`
	NCPU            int    `json:"ncpu"`
	MemTotalBytes   int64  `json:"mem_total_bytes"`
	ContainersRun   int    `json:"containers_running"`
	ContainersStop  int    `json:"containers_stopped"`
	ImageCount      int    `json:"images"`
}

// SelfSnapshot describes the touchgrass process itself.
type SelfSnapshot struct {
	Version       string `json:"version"`
	UptimeSecs    int64  `json:"uptime_secs"`
	DatabaseBytes int64  `json:"database_bytes"`
}

// SystemSnapshot is the host + daemon + self picture. Pointer fields
// stay null where the platform cannot provide them (non-Linux hosts,
// unreadable /proc, unreachable daemon): the screen renders "n/a",
// never an error. CPUPercent is null on the first snapshot — usage
// needs two /proc/stat samples to form a delta.
type SystemSnapshot struct {
	Hostname       string          `json:"hostname"`
	OS             string          `json:"os"`
	Arch           string          `json:"arch"`
	UptimeSecs     *int64          `json:"uptime_secs"`
	Load1          *float64        `json:"load_1"`
	CPUPercent     *float64        `json:"cpu_percent"`
	MemUsedBytes   *int64          `json:"mem_used_bytes"`
	MemTotalBytes  *int64          `json:"mem_total_bytes"`
	DiskUsedBytes  *int64          `json:"disk_used_bytes"`
	DiskTotalBytes *int64          `json:"disk_total_bytes"`
	Docker         *DaemonSnapshot `json:"docker"`
	Self           SelfSnapshot    `json:"self"`
}
