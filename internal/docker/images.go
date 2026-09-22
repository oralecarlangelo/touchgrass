package docker

import (
	"context"
	"fmt"
	"math"
	"slices"
	"strings"

	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/image"
)

// Image is one local daemon image.
type Image struct {
	ID         string
	RepoTags   []string
	Size       int64
	Created    int64
	Containers int64
	Dangling   bool
}

// DaemonInfo is the subset of docker info the System screen needs.
type DaemonInfo struct {
	ServerVersion   string
	Architecture    string
	OperatingSystem string
	KernelVersion   string
	NCPU            int
	MemTotal        int64
	ContainersRun   int
	ContainersStop  int
	ImageCount      int
}

// PruneReport counts one dangling-prune run.
type PruneReport struct {
	Deleted        int
	ReclaimedBytes int64
}

// Images lists local daemon images, newest first.
func (c *Client) Images(ctx context.Context) ([]Image, error) {
	summaries, err := c.cli.ImageList(ctx, image.ListOptions{All: true})
	if err != nil {
		return nil, fmt.Errorf("listing images: %w", err)
	}

	return summarizeImages(summaries), nil
}

// summarizeImages maps daemon summaries, flagging untagged images as
// dangling and sorting newest first.
func summarizeImages(summaries []image.Summary) []Image {
	images := make([]Image, 0, len(summaries))

	for _, summary := range summaries {
		images = append(images, Image{
			ID:         summary.ID,
			RepoTags:   summary.RepoTags,
			Size:       summary.Size,
			Created:    summary.Created,
			Containers: summary.Containers,
			Dangling:   isDangling(summary),
		})
	}

	slices.SortStableFunc(images, func(a, b Image) int {
		switch {
		case a.Created > b.Created:
			return -1
		case a.Created < b.Created:
			return 1
		default:
			return strings.Compare(a.ID, b.ID)
		}
	})

	return images
}

// isDangling reports untagged images: no tags, or only none placeholders.
func isDangling(summary image.Summary) bool {
	if len(summary.RepoTags) == 0 {
		return true
	}

	for _, tag := range summary.RepoTags {
		if tag != "<none>:<none>" && !strings.HasPrefix(tag, "<none>") {
			return false
		}
	}

	return true
}

// PruneImages removes dangling images only, never tagged ones.
func (c *Client) PruneImages(ctx context.Context) (PruneReport, error) {
	report, err := c.cli.ImagesPrune(ctx, filters.NewArgs(filters.Arg("dangling", "true")))
	if err != nil {
		return PruneReport{}, fmt.Errorf("pruning dangling images: %w", err)
	}

	reclaimed := min(report.SpaceReclaimed, math.MaxInt64)

	return PruneReport{
		Deleted:        len(report.ImagesDeleted),
		ReclaimedBytes: int64(reclaimed),
	}, nil
}

// DaemonInfo queries docker info for the System screen.
func (c *Client) DaemonInfo(ctx context.Context) (DaemonInfo, error) {
	info, err := c.cli.Info(ctx)
	if err != nil {
		return DaemonInfo{}, fmt.Errorf("querying daemon info: %w", err)
	}

	return DaemonInfo{
		ServerVersion:   info.ServerVersion,
		Architecture:    info.Architecture,
		OperatingSystem: info.OperatingSystem,
		KernelVersion:   info.KernelVersion,
		NCPU:            info.NCPU,
		MemTotal:        info.MemTotal,
		ContainersRun:   info.ContainersRunning,
		ContainersStop:  info.ContainersStopped,
		ImageCount:      info.Images,
	}, nil
}
