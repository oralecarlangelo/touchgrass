package http

import (
	"encoding/json"
	"testing"

	nethttp "net/http"

	"github.com/oralecarlangelo/touchgrass/internal/model"
)

func TestHandleDockerImages(t *testing.T) {
	t.Parallel()

	server, _ := fullTestServer(t)

	status, body := doRequest(t, server, nethttp.MethodGet, "/api/docker/images", "")
	if status != nethttp.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", status, body)
	}

	var got imagesResponse

	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decoding images: %v", err)
	}

	if len(got.Images) != 1 || got.Images[0].ID != "sha256:harness" {
		t.Errorf("images = %+v, want the stubbed image", got.Images)
	}
}

func TestHandlePruneImages(t *testing.T) {
	t.Parallel()

	server, _ := fullTestServer(t)

	status, body := doRequest(t, server, nethttp.MethodPost, "/api/docker/images/prune", "")
	if status != nethttp.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", status, body)
	}

	var got model.ImagePruneView

	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decoding prune report: %v", err)
	}

	if got.Deleted != 1 || got.ReclaimedBytes != 42 {
		t.Errorf("prune = %+v, want 1/42", got)
	}
}

func TestHandleSystem(t *testing.T) {
	t.Parallel()

	server, _ := fullTestServer(t)

	status, body := doRequest(t, server, nethttp.MethodGet, "/api/system", "")
	if status != nethttp.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", status, body)
	}

	var got model.SystemSnapshot

	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decoding snapshot: %v", err)
	}

	if got.Self.Version != "stub-version" {
		t.Errorf("self.version = %q, want stub-version", got.Self.Version)
	}

	if got.Docker == nil || got.Docker.ServerVersion != "stub-daemon" {
		t.Errorf("docker = %+v, want stubbed daemon info", got.Docker)
	}
}
