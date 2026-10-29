package docker

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/api/types/strslice"
)

func TestFromSummaryMapsAllLifecycleStates(t *testing.T) {
	t.Parallel()

	for _, state := range []string{"running", "exited", "dead"} {
		t.Run(state, func(t *testing.T) {
			t.Parallel()

			got := fromSummary(container.Summary{ID: state + "-id", State: state})
			if got.State != state {
				t.Errorf("fromSummary() state = %q, want %q", got.State, state)
			}
		})
	}
}

func TestNormalizeInspectionExcludesSensitiveFields(t *testing.T) {
	t.Parallel()

	got := NormalizeInspection(container.InspectResponse{
		ContainerJSONBase: &container.ContainerJSONBase{
			ID:    "container-id",
			Image: "sha256:image-id",
			HostConfig: &container.HostConfig{
				Privileged:     true,
				ReadonlyRootfs: true,
				CapAdd:         strslice.StrSlice{"NET_ADMIN"},
				PidMode:        "host",
				IpcMode:        "private",
				NetworkMode:    "bridge",
				RestartPolicy:  container.RestartPolicy{Name: container.RestartPolicyAlways},
				Resources: container.Resources{
					Memory:   256 * 1024 * 1024,
					NanoCPUs: 2_000_000_000,
				},
			},
		},
		Config: &container.Config{
			User:       "10001:10001",
			Env:        []string{"DB_PASSWORD=top-secret"},
			Entrypoint: strslice.StrSlice{"/bin/sh", "-c"},
			Cmd:        strslice.StrSlice{"run", "--token=command-secret"},
			Labels: map[string]string{
				LabelComposeProject: "ticketnation",
				LabelComposeService: "api",
				"private.token":     "label-secret",
			},
		},
	})

	if got.ID != "container-id" || got.ImageID != "sha256:image-id" {
		t.Errorf("normalizeInspection() identity = (%q, %q)", got.ID, got.ImageID)
	}
	if got.User != "10001:10001" || !got.Privileged || !got.ReadonlyRootFS {
		t.Errorf("normalizeInspection() security posture = %+v", got)
	}
	if got.Labels[LabelComposeProject] != "ticketnation" || got.Labels[LabelComposeService] != "api" {
		t.Errorf("normalizeInspection() labels = %v, want standard Compose labels", got.Labels)
	}
	if _, ok := got.Labels["private.token"]; ok {
		t.Errorf("normalizeInspection() retained an arbitrary label: %v", got.Labels)
	}
	if got.EntrypointHash == "" || got.CommandHash == "" {
		t.Errorf("normalizeInspection() command hashes = (%q, %q), want non-empty", got.EntrypointHash, got.CommandHash)
	}

	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	for _, sensitive := range []string{"DB_PASSWORD", "top-secret", "command-secret", "label-secret"} {
		if strings.Contains(string(encoded), sensitive) {
			t.Errorf("normalized inspection contains sensitive value %q: %s", sensitive, encoded)
		}
	}
}

func TestNormalizeInspectionRedactsMountSources(t *testing.T) {
	t.Parallel()

	got := NormalizeInspection(container.InspectResponse{
		ContainerJSONBase: &container.ContainerJSONBase{HostConfig: &container.HostConfig{}},
		Mounts: []container.MountPoint{
			{Type: mount.TypeBind, Source: "/var/run/docker.sock", Destination: "/run/docker.sock", RW: true},
			{Type: mount.TypeVolume, Source: "/var/lib/docker/volumes/private/_data", Destination: "/data"},
		},
	})

	if len(got.Mounts) != 2 {
		t.Fatalf("normalizeInspection() mounts = %v, want 2", got.Mounts)
	}
	if got.Mounts[0].Type != string(mount.TypeBind) || got.Mounts[0].Destination != "/run/docker.sock" {
		t.Errorf("normalizeInspection() mount = %+v, want bind destination", got.Mounts[0])
	}
	if !got.HasDockerSocket {
		t.Error("normalizeInspection() HasDockerSocket = false, want true")
	}

	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	for _, source := range []string{"/var/run/docker.sock", "/var/lib/docker/volumes/private/_data"} {
		if strings.Contains(string(encoded), source) {
			t.Errorf("normalized inspection contains host mount source %q: %s", source, encoded)
		}
	}
}
