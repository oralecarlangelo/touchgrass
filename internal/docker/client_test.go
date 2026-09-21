package docker

import (
	"testing"

	"github.com/docker/docker/api/types/container"
)

func TestNew(t *testing.T) {
	t.Parallel()

	cli, err := New()
	if err != nil {
		t.Fatalf("New() error = %v, want nil", err)
	}

	if err := cli.Close(); err != nil {
		t.Errorf("Close() error = %v, want nil", err)
	}
}

const testStateRunning = "running"

func TestFromSummary(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		summary  container.Summary
		expected Container
	}{
		{
			name: "blue api container",
			summary: container.Summary{
				ID:      "abc123def456789",
				Names:   []string{"/ticketnation-api-blue-1"},
				Image:   "ticketnation-api:latest",
				ImageID: "sha256:1111222233334444",
				State:   testStateRunning,
				Status:  "Up 2 hours",
				Ports:   []container.Port{{IP: "127.0.0.1", PrivatePort: 4000, PublicPort: 4101, Type: "tcp"}},
				Labels:  map[string]string{LabelComposeProject: "ticketnation", LabelComposeService: "api-blue"},
			},
			expected: Container{
				ID:      "abc123def456789",
				Name:    "ticketnation-api-blue-1",
				Image:   "ticketnation-api:latest",
				ImageID: "sha256:1111222233334444",
				State:   testStateRunning,
				Status:  "Up 2 hours",
				Ports:   []string{"127.0.0.1:4101->4000/tcp"},
				Labels:  map[string]string{LabelComposeProject: "ticketnation", LabelComposeService: "api-blue"},
			},
		},
		{
			name: "unpublished port and missing name",
			summary: container.Summary{
				ID:     "fff000",
				Names:  []string{},
				Image:  "redis:7-alpine",
				State:  testStateRunning,
				Ports:  []container.Port{{PrivatePort: 6379, Type: "tcp"}},
				Labels: map[string]string{},
			},
			expected: Container{
				ID:     "fff000",
				Name:   "",
				Image:  "redis:7-alpine",
				State:  testStateRunning,
				Ports:  []string{"6379/tcp"},
				Labels: map[string]string{},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := fromSummary(tt.summary)

			if got.ID != tt.expected.ID || got.Name != tt.expected.Name {
				t.Errorf("fromSummary() identity = (%q, %q), want (%q, %q)",
					got.ID, got.Name, tt.expected.ID, tt.expected.Name)
			}

			if got.Image != tt.expected.Image || got.ImageID != tt.expected.ImageID {
				t.Errorf("fromSummary() image = (%q, %q), want (%q, %q)",
					got.Image, got.ImageID, tt.expected.Image, tt.expected.ImageID)
			}

			if got.State != tt.expected.State || got.Status != tt.expected.Status {
				t.Errorf("fromSummary() state = (%q, %q), want (%q, %q)",
					got.State, got.Status, tt.expected.State, tt.expected.Status)
			}

			if len(got.Ports) != len(tt.expected.Ports) {
				t.Fatalf("fromSummary() ports = %v, want %v", got.Ports, tt.expected.Ports)
			}

			for i := range got.Ports {
				if got.Ports[i] != tt.expected.Ports[i] {
					t.Errorf("fromSummary() ports[%d] = %q, want %q", i, got.Ports[i], tt.expected.Ports[i])
				}
			}

			if len(got.Labels) != len(tt.expected.Labels) {
				t.Errorf("fromSummary() labels = %v, want %v", got.Labels, tt.expected.Labels)
			}
		})
	}
}
