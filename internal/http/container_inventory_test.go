package http

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	nethttp "net/http"

	"github.com/oralecarlangelo/touchgrass/internal/model"
	"github.com/oralecarlangelo/touchgrass/internal/store"
)

func TestHandleContainersIncludesStoppedAndNullMetrics(t *testing.T) {
	t.Parallel()

	server, db := fullTestServer(t)
	seedContainerInventory(t, db, model.InventoryBatch{ObservedAt: time.Now().UTC(), Complete: true,
		Instances: []model.ContainerInstance{
			{ID: "running-id", Name: "api-1", State: "running", Classification: model.ContainerClassificationManaged},
			{ID: "stopped-id", Name: "worker-1", State: "exited", Classification: model.ContainerClassificationUnknown},
		}})

	status, body := doRequest(t, server, nethttp.MethodGet, "/api/system/containers", "")
	if status != nethttp.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", status, body)
	}
	var got containerListResponse
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if len(got.Containers) != 2 {
		t.Fatalf("containers = %d, want 2", len(got.Containers))
	}
	byID := map[string]containerListItem{}
	for _, item := range got.Containers {
		byID[item.ID] = item
	}
	if byID["stopped-id"].State != "exited" || byID["stopped-id"].CPUPercent != nil ||
		byID["stopped-id"].MemBytes != nil {
		t.Errorf("stopped container = %+v, want exited with null live metrics", byID["stopped-id"])
	}
}

func TestHandleContainerDetailExcludesSensitiveInspection(t *testing.T) {
	t.Parallel()

	server, db := fullTestServer(t)
	seedContainerInventory(t, db, model.InventoryBatch{ObservedAt: time.Now().UTC(), Complete: true,
		Instances: []model.ContainerInstance{{
			ID: "detail-id", Name: "api-1", State: "running",
			Inspection: model.SafeInspection{
				User: "10001", CommandHash: "safe-command-hash",
				Labels: map[string]string{"private.token": "secret-label"},
				Mounts: []model.SafeMount{{Type: "bind", Destination: "/data", Writable: true}},
			},
		}}})

	status, body := doRequest(t, server, nethttp.MethodGet, "/api/system/containers/detail-id", "")
	if status != nethttp.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", status, body)
	}
	for _, forbidden := range []string{"secret-label", "private.token", "environment", "source"} {
		if strings.Contains(string(body), forbidden) {
			t.Errorf("detail response contains forbidden inspection value %q: %s", forbidden, body)
		}
	}
	if !strings.Contains(string(body), "safe-command-hash") || !strings.Contains(string(body), "\"destination\":\"/data\"") {
		t.Errorf("detail response omitted safe posture fields: %s", body)
	}
}

func TestHandleContainersReturnsEmptyArray(t *testing.T) {
	t.Parallel()

	server, _ := fullTestServer(t)
	status, body := doRequest(t, server, nethttp.MethodGet, "/api/system/containers", "")
	if status != nethttp.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatal(err)
	}
	if string(raw["containers"]) != "[]" {
		t.Errorf("containers = %s, want []", raw["containers"])
	}
}

func TestHandleContainersRejectsUnknownSort(t *testing.T) {
	t.Parallel()

	server, _ := fullTestServer(t)
	status, _ := doRequest(t, server, nethttp.MethodGet, "/api/system/containers?sort=secrets", "")
	if status != nethttp.StatusBadRequest {
		t.Errorf("status = %d, want 400", status)
	}
}

func TestHandleContainersShowsStaleCollector(t *testing.T) {
	t.Parallel()

	server, db := fullTestServer(t)
	seedContainerInventory(t, db, model.InventoryBatch{ObservedAt: time.Now().UTC().Add(-10 * time.Minute), Complete: true})
	status, body := doRequest(t, server, nethttp.MethodGet, "/api/system/containers", "")
	if status != nethttp.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
	var got containerListResponse
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	if !got.Freshness.Stale || !got.Freshness.Complete {
		t.Errorf("freshness = %+v, want complete and stale", got.Freshness)
	}
}

func seedContainerInventory(t *testing.T, db *store.DB, batch model.InventoryBatch) {
	t.Helper()
	if err := store.NewContainerStore(db).Reconcile(context.Background(), batch); err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
}
