package docker

import (
	"context"
	"fmt"
	"path"
	"strings"

	containertypes "github.com/docker/docker/api/types/container"
)

const (
	maxLayerChanges = 512
	maxLayerPath    = 256
)

// LayerChangeKind is a normalized Docker writable-layer operation.
type LayerChangeKind string

const (
	LayerChangeAdded    LayerChangeKind = "added"
	LayerChangeModified LayerChangeKind = "modified"
	LayerChangeDeleted  LayerChangeKind = "deleted"
)

// LayerChange contains path metadata only; file contents are never read.
type LayerChange struct {
	Kind LayerChangeKind `json:"kind"`
	Path string          `json:"path"`
}

// DiffLister reads bounded writable-layer metadata.
type DiffLister interface {
	Diff(ctx context.Context, id string) ([]LayerChange, error)
}

// Diff reads and normalizes Docker's metadata-only filesystem diff.
func (c *Client) Diff(ctx context.Context, id string) ([]LayerChange, error) {
	changes, err := c.cli.ContainerDiff(ctx, id)
	if err != nil {
		return []LayerChange{}, fmt.Errorf("listing container layer changes: %w", err)
	}
	return normalizeLayerChanges(changes, maxLayerChanges, maxLayerPath)
}

func normalizeLayerChanges(
	values []containertypes.FilesystemChange,
	rowLimit, pathLimit int,
) ([]LayerChange, error) {
	if rowLimit <= 0 || pathLimit <= 0 {
		return nil, fmt.Errorf("normalizing layer changes: positive bounds are required")
	}
	changes := make([]LayerChange, 0, min(len(values), rowLimit))
	for index, value := range values {
		if len(changes) == rowLimit {
			break
		}
		if containsUnsafeControl(value.Path) {
			return nil, fmt.Errorf("normalizing layer change %d: control character rejected", index)
		}
		cleaned := path.Clean(strings.TrimSpace(value.Path))
		if !strings.HasPrefix(cleaned, "/") {
			return nil, fmt.Errorf("normalizing layer change %d: absolute path required", index)
		}
		if len(cleaned) > pathLimit {
			cleaned = cleaned[:pathLimit]
		}
		var kind LayerChangeKind
		switch value.Kind {
		case containertypes.ChangeAdd:
			kind = LayerChangeAdded
		case containertypes.ChangeModify:
			kind = LayerChangeModified
		case containertypes.ChangeDelete:
			kind = LayerChangeDeleted
		default:
			return nil, fmt.Errorf("normalizing layer change %d: unknown kind", index)
		}
		changes = append(changes, LayerChange{Kind: kind, Path: cleaned})
	}
	return changes, nil
}
