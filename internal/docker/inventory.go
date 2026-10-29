package docker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"strings"

	"github.com/docker/docker/api/types/container"
)

const dockerSocketPath = "/var/run/docker.sock"

// InventoryLister reads the complete container inventory and normalized
// security posture. Client is the production implementation.
type InventoryLister interface {
	ListAll(ctx context.Context) ([]Container, error)
	Inspect(ctx context.Context, id string) (Inspection, error)
}

// Inspection is a secret-safe subset of Docker's container inspection data.
// It intentionally excludes environment variables, raw command arguments,
// arbitrary labels, and host mount sources.
type Inspection struct {
	ID              string            `json:"id"`
	ImageID         string            `json:"imageId"`
	User            string            `json:"user"`
	EntrypointHash  string            `json:"entrypointHash,omitempty"`
	CommandHash     string            `json:"commandHash,omitempty"`
	Privileged      bool              `json:"privileged"`
	ReadonlyRootFS  bool              `json:"readonlyRootFs"`
	CapAdd          []string          `json:"capAdd,omitempty"`
	PIDMode         string            `json:"pidMode,omitempty"`
	IPCMode         string            `json:"ipcMode,omitempty"`
	NetworkMode     string            `json:"networkMode,omitempty"`
	RestartPolicy   string            `json:"restartPolicy,omitempty"`
	MemoryBytes     int64             `json:"memoryBytes,omitempty"`
	NanoCPUs        int64             `json:"nanoCpus,omitempty"`
	PIDsLimit       *int64            `json:"pidsLimit,omitempty"`
	Mounts          []InspectionMount `json:"mounts,omitempty"`
	HasDockerSocket bool              `json:"hasDockerSocket"`
	Labels          map[string]string `json:"labels,omitempty"`
}

// InspectionMount contains only the non-sensitive mount attributes needed for
// posture evaluation. Host source paths are never retained.
type InspectionMount struct {
	Type        string `json:"type"`
	Destination string `json:"destination"`
	Writable    bool   `json:"writable"`
}

// ListAll returns containers in every lifecycle state.
func (c *Client) ListAll(ctx context.Context) ([]Container, error) {
	summaries, err := c.cli.ContainerList(ctx, container.ListOptions{All: true})
	if err != nil {
		return nil, fmt.Errorf("listing all containers: %w", err)
	}

	containers := make([]Container, 0, len(summaries))
	for _, summary := range summaries {
		containers = append(containers, fromSummary(summary))
	}

	return containers, nil
}

// Inspect returns a normalized, secret-safe security view of one container.
func (c *Client) Inspect(ctx context.Context, id string) (Inspection, error) {
	response, err := c.cli.ContainerInspect(ctx, id)
	if err != nil {
		return Inspection{}, fmt.Errorf("inspecting container %q: %w", id, err)
	}

	return NormalizeInspection(response), nil
}

// NormalizeInspection converts Docker's raw response to the same secret-safe
// representation persisted by the production collector.
func NormalizeInspection(response container.InspectResponse) Inspection {
	inspection := Inspection{
		Mounts: make([]InspectionMount, 0, len(response.Mounts)),
		Labels: make(map[string]string, 2),
	}

	if response.ContainerJSONBase != nil {
		inspection.ID = response.ID
		inspection.ImageID = response.Image
	}

	if response.Config != nil {
		inspection.User = response.Config.User
		inspection.EntrypointHash = hashArguments(response.Config.Entrypoint)
		inspection.CommandHash = hashArguments(response.Config.Cmd)
		copyComposeLabel(inspection.Labels, response.Config.Labels, LabelComposeProject)
		copyComposeLabel(inspection.Labels, response.Config.Labels, LabelComposeService)
	}

	if response.ContainerJSONBase != nil && response.HostConfig != nil {
		host := response.HostConfig
		inspection.Privileged = host.Privileged
		inspection.ReadonlyRootFS = host.ReadonlyRootfs
		inspection.CapAdd = slices.Clone([]string(host.CapAdd))
		inspection.PIDMode = string(host.PidMode)
		inspection.IPCMode = string(host.IpcMode)
		inspection.NetworkMode = string(host.NetworkMode)
		inspection.RestartPolicy = string(host.RestartPolicy.Name)
		inspection.MemoryBytes = host.Memory
		inspection.NanoCPUs = host.NanoCPUs
		if host.PidsLimit != nil {
			limit := *host.PidsLimit
			inspection.PIDsLimit = &limit
		}
	}

	for _, item := range response.Mounts {
		inspection.Mounts = append(inspection.Mounts, InspectionMount{
			Type:        string(item.Type),
			Destination: item.Destination,
			Writable:    item.RW,
		})
		if isDockerSocket(item.Source) || isDockerSocket(item.Destination) {
			inspection.HasDockerSocket = true
		}
	}

	return inspection
}

func copyComposeLabel(destination, source map[string]string, key string) {
	if value, ok := source[key]; ok {
		destination[key] = value
	}
}

func hashArguments(arguments []string) string {
	if len(arguments) == 0 {
		return ""
	}

	sum := sha256.Sum256([]byte(strings.Join(arguments, "\x00")))
	return hex.EncodeToString(sum[:])
}

func isDockerSocket(path string) bool {
	return path == dockerSocketPath || path == "/run/docker.sock"
}
