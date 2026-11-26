package docker

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/oralecarlangelo/touchgrass/internal/model"
)

// StorageUsage queries the daemon on demand with a bounded deadline.
func (c *Client) StorageUsage(ctx context.Context) (model.StorageUsage, error) {
	usage, err := guardedCall(ctx, c.guard, 5*time.Second,
		func(callCtx context.Context) (types.DiskUsage, error) {
			return c.cli.DiskUsage(callCtx, types.DiskUsageOptions{})
		})
	if err != nil {
		return model.StorageUsage{}, fmt.Errorf("querying docker storage: %w", err)
	}

	return summarizeStorage(usage), nil
}

func knownSize(size int64) *int64 {
	if size < 0 {
		return nil
	}

	return &size
}

func summarizeStorage(usage types.DiskUsage) model.StorageUsage {
	result := model.StorageUsage{SampledAt: time.Now().UTC(), ImageLayersBytes: knownSize(usage.LayersSize), Entries: []model.StorageEntry{}}
	for _, item := range usage.Containers {
		if item == nil {
			continue
		}

		result.Entries = append(result.Entries, model.StorageEntry{ID: item.ID, Name: strings.Join(item.Names, ", "), Kind: "container", SizeBytes: knownSize(item.SizeRw), Status: item.State})
	}

	for _, item := range usage.Volumes {
		if item == nil {
			continue
		}

		entry := model.StorageEntry{ID: item.Name, Name: item.Name, Kind: "volume", Status: "references unknown"}
		if item.UsageData != nil {
			entry.SizeBytes = knownSize(item.UsageData.Size)
			switch {
			case item.UsageData.RefCount > 0:
				entry.Status = "referenced"
			case item.UsageData.RefCount == 0:
				entry.Status = "unreferenced; may contain persistent data"
			}
		}

		result.Entries = append(result.Entries, entry)
	}

	for _, item := range usage.BuildCache {
		if item == nil {
			continue
		}

		status := "unused"
		if item.InUse {
			status = "in use"
		}

		if item.Shared {
			status += "; shared with images"
		}

		result.Entries = append(result.Entries, model.StorageEntry{ID: item.ID, Name: item.ID, Kind: "build cache", SizeBytes: knownSize(item.Size), Status: status})
	}

	slices.SortStableFunc(result.Entries, compareStorageEntries)

	return result
}

func compareStorageEntries(a, b model.StorageEntry) int {
	if a.SizeBytes == nil {
		if b.SizeBytes == nil {
			return strings.Compare(a.ID, b.ID)
		}

		return 1
	}

	if b.SizeBytes == nil {
		return -1
	}

	switch {
	case *a.SizeBytes > *b.SizeBytes:
		return -1
	case *a.SizeBytes < *b.SizeBytes:
		return 1
	default:
		return strings.Compare(a.ID, b.ID)
	}
}
