package http

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	nethttp "net/http"

	"github.com/oralecarlangelo/touchgrass/internal/model"
	"github.com/oralecarlangelo/touchgrass/internal/store"
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

func TestHandleFleetContainers(t *testing.T) {
	t.Parallel()

	server, db := fullTestServer(t)
	ctx := context.Background()
	fleet := store.NewFleetStore(db)

	now := time.Now().Truncate(time.Second)

	seed := []model.ContainerSample{
		{
			ContainerName: "api-blue-1", Project: "fleet-app", Managed: true, ServiceID: testServiceAPI,
			State: testRunningState, CPUPercent: 12.5, MemBytes: 200, MemLimit: 1000, Restarts: 2, SampledAt: now,
		},
		{
			ContainerName: "cache-1", Project: "infra", Managed: false,
			State: testRunningState, CPUPercent: 40, MemBytes: 900, MemLimit: 1000, Restarts: 0, SampledAt: now,
		},
	}

	for _, sample := range seed {
		if err := fleet.InsertContainer(ctx, sample); err != nil {
			t.Fatalf("InsertContainer() error = %v, want nil", err)
		}
	}

	status, body := doRequest(t, server, nethttp.MethodGet, "/api/system/containers", "")
	if status != nethttp.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", status, body)
	}

	var got fleetResponse

	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decoding fleet: %v", err)
	}

	if len(got.Containers) != 2 {
		t.Fatalf("containers = %d, want 2", len(got.Containers))
	}

	if got.Containers[0].Name != "cache-1" || got.Containers[1].Name != "api-blue-1" {
		t.Errorf("order = %q, %q, want cache-1 then api-blue-1 (cpu desc)",
			got.Containers[0].Name, got.Containers[1].Name)
	}

	managed := got.Containers[1]

	if !managed.Managed || managed.ServiceID != testServiceAPI || managed.Project != "fleet-app" {
		t.Errorf("managed = %+v, want managed tn-api row", managed)
	}

	var raw map[string][]map[string]any

	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatalf("decoding raw fleet: %v", err)
	}

	if _, ok := raw["containers"][0]["service_id"]; ok {
		t.Error("unmanaged row carries service_id, want it omitted")
	}
}

func TestHandleSystemHistory(t *testing.T) {
	t.Parallel()

	server, db := fullTestServer(t)
	ctx := context.Background()

	cpu, load := 25.0, 1.5
	used := int64(2048)

	if err := store.NewFleetStore(db).InsertHost(ctx, model.HostSample{
		SampledAt:  time.Now(),
		CPUPercent: &cpu,
		MemUsed:    &used,
		Load1:      &load,
	}); err != nil {
		t.Fatalf("InsertHost() error = %v, want nil", err)
	}

	tests := []struct {
		name          string
		target        string
		wantStatus    int
		wantPoints    int
		wantValue     float64
		wantErrorCode string
	}{
		{name: "cpu", target: "/api/system/history?metric=cpu&hours=1", wantStatus: nethttp.StatusOK, wantPoints: 1, wantValue: 25},
		{name: "mem", target: "/api/system/history?metric=mem&hours=1", wantStatus: nethttp.StatusOK, wantPoints: 1, wantValue: 2048},
		{name: "load", target: "/api/system/history?metric=load&hours=1", wantStatus: nethttp.StatusOK, wantPoints: 1, wantValue: 1.5},
		{name: "default hours", target: "/api/system/history?metric=cpu", wantStatus: nethttp.StatusOK, wantPoints: 1, wantValue: 25},
		{name: "clamped hours", target: "/api/system/history?metric=cpu&hours=999", wantStatus: nethttp.StatusOK, wantPoints: 1, wantValue: 25},
		{name: "missing metric", target: "/api/system/history", wantStatus: nethttp.StatusBadRequest, wantErrorCode: testInvalidRequest},
		{name: "bad metric", target: "/api/system/history?metric=disk", wantStatus: nethttp.StatusBadRequest, wantErrorCode: testInvalidRequest},
		{name: "bad hours", target: "/api/system/history?metric=cpu&hours=abc", wantStatus: nethttp.StatusBadRequest, wantErrorCode: testInvalidRequest},
		{name: "zero hours", target: "/api/system/history?metric=cpu&hours=0", wantStatus: nethttp.StatusBadRequest, wantErrorCode: testInvalidRequest},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			status, body := doRequest(t, server, nethttp.MethodGet, tt.target, "")
			if status != tt.wantStatus {
				t.Fatalf("status = %d, want %d (body: %s)", status, tt.wantStatus, body)
			}

			if tt.wantStatus != nethttp.StatusOK {
				var got errorResponse

				if err := json.Unmarshal(body, &got); err != nil {
					t.Fatalf("decoding error: %v", err)
				}

				if got.Code != tt.wantErrorCode {
					t.Errorf("code = %q, want %q", got.Code, tt.wantErrorCode)
				}

				return
			}

			var got historyResponse

			if err := json.Unmarshal(body, &got); err != nil {
				t.Fatalf("decoding history: %v", err)
			}

			if len(got.Points) != tt.wantPoints {
				t.Fatalf("points = %d, want %d", len(got.Points), tt.wantPoints)
			}

			if got.Points[0].Value != tt.wantValue {
				t.Errorf("value = %v, want %v", got.Points[0].Value, tt.wantValue)
			}
		})
	}
}

func TestParseHistoryHours(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		raw       string
		expected  int
		wantError bool
	}{
		{name: "empty selects default", raw: "", expected: 0},
		{name: "valid", raw: "6", expected: 6},
		{name: "max", raw: "168", expected: 168},
		{name: "above max clamps", raw: "999", expected: 168},
		{name: "zero errors", raw: "0", wantError: true},
		{name: "negative errors", raw: "-1", wantError: true},
		{name: "text errors", raw: "abc", wantError: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := parseHistoryHours(tt.raw)
			if tt.wantError {
				if err == nil {
					t.Errorf("parseHistoryHours(%q) error = nil, want error", tt.raw)
				}

				return
			}

			if err != nil {
				t.Fatalf("parseHistoryHours(%q) error = %v, want nil", tt.raw, err)
			}

			if got != tt.expected {
				t.Errorf("parseHistoryHours(%q) = %d, want %d", tt.raw, got, tt.expected)
			}
		})
	}
}
