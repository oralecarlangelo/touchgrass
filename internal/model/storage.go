package model

import "time"

// StorageEntry is one Docker object. Unknown sizes remain null.
type StorageEntry struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	SizeBytes *int64 `json:"size_bytes"`
	Status    string `json:"status"`
}

// StorageUsage reports Docker accounting without summing overlapping layers.
type StorageUsage struct {
	SampledAt        time.Time           `json:"sampled_at"`
	ImageLayersBytes *int64              `json:"image_layers_bytes"`
	Entries          []StorageEntry      `json:"entries"`
	Freshness        CollectionFreshness `json:"freshness"`
}
