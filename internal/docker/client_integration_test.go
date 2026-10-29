//go:build integration

package docker

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
)

func TestClientListAllIncludesStoppedContainer(t *testing.T) {
	if os.Getenv("DOCKER_HOST") == "" {
		if _, err := os.Stat("/var/run/docker.sock"); err != nil {
			t.Skip("no docker socket available")
		}
	}

	cli, err := New()
	if err != nil {
		t.Fatalf("New() error = %v, want nil", err)
	}
	defer func() {
		if err := cli.Close(); err != nil {
			t.Errorf("Close() error = %v, want nil", err)
		}
	}()

	ctx := context.Background()
	images, err := cli.cli.ImageList(ctx, image.ListOptions{})
	if err != nil {
		t.Fatalf("ImageList() error = %v, want nil", err)
	}
	if len(images) == 0 {
		t.Skip("no local image available for stopped-container fixture")
	}

	name := fmt.Sprintf("touchgrass-inventory-test-%d", time.Now().UnixNano())
	created, err := cli.cli.ContainerCreate(ctx, &container.Config{Image: images[0].ID}, nil, nil, nil, name)
	if err != nil {
		t.Fatalf("ContainerCreate() error = %v, want nil", err)
	}
	defer func() {
		if err := cli.cli.ContainerRemove(ctx, created.ID, container.RemoveOptions{Force: true}); err != nil {
			t.Errorf("ContainerRemove(%q) error = %v", created.ID, err)
		}
	}()

	containers, err := cli.ListAll(ctx)
	if err != nil {
		t.Fatalf("ListAll() error = %v, want nil", err)
	}

	for _, item := range containers {
		if item.ID == created.ID {
			if item.State == "running" {
				t.Errorf("ListAll() fixture state = %q, want a non-running state", item.State)
			}
			return
		}
	}

	t.Fatalf("ListAll() did not include stopped fixture %q", created.ID)
}

func TestClientListIntegration(t *testing.T) {
	if os.Getenv("DOCKER_HOST") == "" {
		if _, err := os.Stat("/var/run/docker.sock"); err != nil {
			t.Skip("no docker socket available")
		}
	}

	cli, err := New()
	if err != nil {
		t.Fatalf("New() error = %v, want nil", err)
	}

	defer func() {
		if err := cli.Close(); err != nil {
			t.Errorf("Close() error = %v, want nil", err)
		}
	}()

	containers, err := cli.List(context.Background())
	if err != nil {
		t.Fatalf("List() error = %v, want nil", err)
	}

	t.Logf("listed %d containers", len(containers))

	if len(containers) == 0 {
		t.Skip("no running containers to stat")
	}

	stats, err := cli.Stats(context.Background(), containers[0].ID)
	if err != nil {
		t.Fatalf("Stats() error = %v, want nil", err)
	}

	t.Logf("stats for %s: cpu=%.1f%% mem=%d/%d restarts=%d",
		containers[0].Name, stats.CPUPercent, stats.MemBytes, stats.MemLimit, stats.Restarts)
}

func TestClientLogsIntegration(t *testing.T) {
	if os.Getenv("DOCKER_HOST") == "" {
		if _, err := os.Stat("/var/run/docker.sock"); err != nil {
			t.Skip("no docker socket available")
		}
	}

	cli, err := New()
	if err != nil {
		t.Fatalf("New() error = %v, want nil", err)
	}

	defer func() {
		if err := cli.Close(); err != nil {
			t.Errorf("Close() error = %v, want nil", err)
		}
	}()

	containers, err := cli.List(context.Background())
	if err != nil {
		t.Fatalf("List() error = %v, want nil", err)
	}

	if len(containers) == 0 {
		t.Skip("no running containers to tail")
	}

	lines, err := cli.Logs(context.Background(), containers[0].ID, "", 10)
	if err != nil {
		t.Fatalf("Logs() error = %v, want nil", err)
	}

	t.Logf("tailed %d lines from %s", len(lines), containers[0].Name)

	for _, line := range lines {
		if line.Stream != "stdout" && line.Stream != "stderr" {
			t.Errorf("line stream = %q, want stdout or stderr", line.Stream)
		}
	}
}
