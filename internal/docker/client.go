// Package docker wraps the Docker Engine API.
package docker

import (
	"context"
	"fmt"
	"maps"
	"strings"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
)

// Compose label keys used to match containers to managed services.
const (
	LabelComposeProject    = "com.docker.compose.project"
	LabelComposeService    = "com.docker.compose.service"
	LabelComposeWorkingDir = "com.docker.compose.project.working_dir"
)

// PublishedPort is one host-published port mapping.
type PublishedPort struct {
	HostIP        string
	HostPort      uint16
	ContainerPort uint16
	Proto         string
}

// Container is a running container.
type Container struct {
	ID        string
	Name      string
	Image     string
	ImageID   string
	State     string
	Status    string
	Ports     []string
	Published []PublishedPort
	Labels    map[string]string
}

// Lister lists containers. Client is the production implementation.
type Lister interface {
	List(ctx context.Context) ([]Container, error)
}

// Client queries the local Docker daemon.
type Client struct {
	cli *client.Client
}

// New builds a Client from the standard Docker environment.
func New() (*Client, error) {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, fmt.Errorf("creating docker client: %w", err)
	}

	return &Client{cli: cli}, nil
}

// Close releases daemon resources.
func (c *Client) Close() error {
	if err := c.cli.Close(); err != nil {
		return fmt.Errorf("closing docker client: %w", err)
	}

	return nil
}

// List returns all running containers.
func (c *Client) List(ctx context.Context) ([]Container, error) {
	summaries, err := c.cli.ContainerList(ctx, container.ListOptions{All: false})
	if err != nil {
		return nil, fmt.Errorf("listing containers: %w", err)
	}

	containers := make([]Container, 0, len(summaries))

	for _, summary := range summaries {
		containers = append(containers, fromSummary(summary))
	}

	return containers, nil
}

// fromSummary maps an engine summary to a Container.
func fromSummary(summary container.Summary) Container {
	ports := make([]string, 0, len(summary.Ports))
	published := []PublishedPort{}

	for _, port := range summary.Ports {
		ports = append(ports, formatPort(port))

		if port.PublicPort > 0 {
			published = append(published, PublishedPort{
				HostIP:        port.IP,
				HostPort:      port.PublicPort,
				ContainerPort: port.PrivatePort,
				Proto:         port.Type,
			})
		}
	}

	return Container{
		ID:        summary.ID,
		Name:      strings.TrimPrefix(first(summary.Names), "/"),
		Image:     summary.Image,
		ImageID:   summary.ImageID,
		State:     summary.State,
		Status:    summary.Status,
		Ports:     ports,
		Published: published,
		Labels:    maps.Clone(summary.Labels),
	}
}

// formatPort renders one published mapping like docker ps.
func formatPort(port container.Port) string {
	if port.PublicPort > 0 && port.IP != "" {
		return fmt.Sprintf("%s:%d->%d/%s", port.IP, port.PublicPort, port.PrivatePort, port.Type)
	}

	return fmt.Sprintf("%d/%s", port.PrivatePort, port.Type)
}

// first returns the first name or "".
func first(names []string) string {
	if len(names) == 0 {
		return ""
	}

	return names[0]
}
