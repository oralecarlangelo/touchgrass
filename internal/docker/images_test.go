package docker

import (
	"testing"

	"github.com/docker/docker/api/types/image"
)

func TestSummarizeImages(t *testing.T) {
	t.Parallel()

	summaries := []image.Summary{
		{ID: "sha256:old", RepoTags: []string{"app:v1"}, Size: 100, Created: 100, Containers: 1},
		{ID: "sha256:dangling", Size: 50, Created: 300},
		{ID: "sha256:none", RepoTags: []string{"<none>:<none>"}, Size: 60, Created: 200},
		{ID: "sha256:new", RepoTags: []string{"app:v2", "app:latest"}, Size: 120, Created: 400},
	}

	got := summarizeImages(summaries)

	if len(got) != 4 {
		t.Fatalf("summarizeImages() = %d images, want 4", len(got))
	}

	// Newest first.
	if got[0].ID != "sha256:new" || got[3].ID != "sha256:old" {
		t.Errorf("order = %s..%s, want new..old", got[0].ID, got[3].ID)
	}

	byID := map[string]Image{}
	for _, img := range got {
		byID[img.ID] = img
	}

	if byID["sha256:dangling"].Dangling != true {
		t.Error("untagged image Dangling = false, want true")
	}

	if byID["sha256:none"].Dangling != true {
		t.Error("<none> image Dangling = false, want true")
	}

	if byID["sha256:new"].Dangling != false {
		t.Error("tagged image Dangling = true, want false")
	}

	if len(byID["sha256:new"].RepoTags) != 2 {
		t.Errorf("RepoTags = %v, want both tags", byID["sha256:new"].RepoTags)
	}

	if byID["sha256:old"].Containers != 1 || byID["sha256:old"].Size != 100 {
		t.Errorf("old = %+v, want containers/size carried", byID["sha256:old"])
	}
}
