package docker

import (
	"encoding/json"
	"strings"
	"testing"

	containertypes "github.com/docker/docker/api/types/container"
)

func TestDiffNormalizationKindsAndBounds(t *testing.T) {
	t.Parallel()
	changes, err := normalizeLayerChanges([]containertypes.FilesystemChange{
		{Kind: containertypes.ChangeAdd, Path: "/tmp/new"},
		{Kind: containertypes.ChangeModify, Path: "/etc/app.conf"},
		{Kind: containertypes.ChangeDelete, Path: "/var/cache/old"},
	}, 2, 12)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 2 || changes[0].Kind != LayerChangeAdded ||
		changes[1].Kind != LayerChangeModified || len(changes[1].Path) > 12 {
		t.Fatalf("normalizeLayerChanges() = %+v, want bounded normalized kinds", changes)
	}
}

func TestDiffNormalizationRejectsControlCharacters(t *testing.T) {
	t.Parallel()
	_, err := normalizeLayerChanges([]containertypes.FilesystemChange{{
		Kind: containertypes.ChangeAdd, Path: "/tmp/bad\npath",
	}}, 10, 256)
	if err == nil {
		t.Fatal("normalizeLayerChanges() error = nil, want control-character rejection")
	}
}

func TestDiffNormalizationReturnsNonNilEmpty(t *testing.T) {
	t.Parallel()
	got, err := normalizeLayerChanges(nil, 10, 256)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || len(got) != 0 {
		t.Fatalf("normalizeLayerChanges() = %#v, want non-nil empty", got)
	}
}

func TestDiffNormalizationContainsNoFileContent(t *testing.T) {
	t.Parallel()
	got, err := normalizeLayerChanges([]containertypes.FilesystemChange{{
		Kind: containertypes.ChangeAdd, Path: "/tmp/new",
	}}, 10, 256)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(got)
	if strings.Contains(strings.ToLower(string(encoded)), "content") {
		t.Fatalf("normalized diff exposes file content field: %s", encoded)
	}
}
