package docker

import (
	"testing"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/build"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/volume"
)

func TestSummarizeStorage(t *testing.T) {
	t.Parallel()

	got := summarizeStorage(types.DiskUsage{
		LayersSize: 123,
		Containers: []*container.Summary{{ID: "worker", Names: []string{"/worker"}, SizeRw: 10, State: "running"}},
		Volumes:    []*volume.Volume{{Name: "customer-data", UsageData: &volume.UsageData{Size: 900, RefCount: 0}}, {Name: "remote", UsageData: &volume.UsageData{Size: -1, RefCount: -1}}, {Name: "missing"}},
		BuildCache: []*build.CacheRecord{{ID: "shared", Size: 30, InUse: true, Shared: true}},
	})
	if got.ImageLayersBytes == nil || *got.ImageLayersBytes != 123 {
		t.Fatalf("physical layers = %v", got.ImageLayersBytes)
	}

	if len(got.Entries) != 5 || got.Entries[0].Name != "customer-data" {
		t.Fatalf("largest-first entries = %+v", got.Entries)
	}

	if got.Entries[0].Status != "unreferenced; may contain persistent data" {
		t.Fatal("unreferenced volume was not protected")
	}

	if got.Entries[1].Status != "in use; shared with images" {
		t.Fatal("shared cache accounting missing")
	}

	for _, entry := range got.Entries[3:] {
		if entry.SizeBytes != nil {
			t.Fatalf("unknown volume size became zero: %+v", entry)
		}
	}
}
