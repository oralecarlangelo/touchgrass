package http

import (
	"context"
	"encoding/json"
	"io"
	"net/http/httptest"
	"slices"
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
	seedContainerInventory(t, db, model.InventoryBatch{
		ObservedAt: time.Now().UTC(), Complete: true,
		Instances: []model.ContainerInstance{
			{ID: "running-id", Name: "api-1", State: "running", Classification: model.ContainerClassificationManaged},
			{ID: "stopped-id", Name: "worker-1", State: "exited", Classification: model.ContainerClassificationUnknown},
		},
	})

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

func TestHandleContainersFiltersAndOrdersBeforePaging(t *testing.T) {
	t.Parallel()

	const dbID = "b-db"

	server, db := fullTestServer(t)
	lowCPU, highCPU := 2.0, 65.0
	seedContainerInventory(t, db, model.InventoryBatch{
		ObservedAt: time.Now().UTC(), Complete: true,
		Instances: []model.ContainerInstance{
			{
				ID: "z-worker", Name: "working", ComposeProject: "alpha-jobs", State: "running",
				Classification: model.ContainerClassificationManaged, CPUPercent: &highCPU,
			},
			{
				ID: "a-cache", Name: "cache", ComposeProject: "beta-api", State: "running",
				Classification: model.ContainerClassificationManaged, CPUPercent: &lowCPU,
			},
			{
				ID: dbID, Name: "db", ComposeProject: "beta-api", State: "exited",
				Classification: model.ContainerClassificationUnknown,
			},
			{
				ID: "a-worker", Name: "worker", ComposeProject: "alpha-jobs", State: "exited",
				Classification: model.ContainerClassificationUnknown,
			},
		},
	})

	_, err := server.security.Evaluate(t.Context(), model.SecurityEvaluation{Observation: model.SecurityObservation{
		ContainerID: dbID, RuleID: "test-example-rule", Category: model.SecurityCategoryPosture,
		Severity: model.SecuritySeverityHigh, Confidence: model.SecurityConfidenceHigh,
		Detected: true, CoverageComplete: true, Evidence: map[string]string{"fixture": "active risk"},
	}})
	if err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name  string
		query string
		want  []string
	}{
		{name: "hottest before truncation", query: "sort=cpu&limit=1", want: []string{"z-worker"}},
		{name: "compose keeps peers together", query: "sort=project&limit=2", want: []string{"a-worker", "z-worker"}},
		{name: "literal case insensitive search before pagination", query: "q=BETA&sort=project&limit=1&offset=1", want: []string{dbID}},
		{name: "stopped includes exited", query: "filter=stopped&q=alpha&limit=1", want: []string{"a-worker"}},
		{name: "active risk filters before pagination", query: "filter=risky&limit=1", want: []string{dbID}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			status, body := doRequest(t, server, nethttp.MethodGet, "/api/system/containers?"+tc.query, "")
			if status != nethttp.StatusOK {
				t.Fatalf("status = %d, want 200 (body: %s)", status, body)
			}

			var got containerListResponse
			if err := json.Unmarshal(body, &got); err != nil {
				t.Fatal(err)
			}

			ids := make([]string, 0, len(got.Containers))
			for _, item := range got.Containers {
				ids = append(ids, item.ID)
			}

			if !slices.Equal(ids, tc.want) {
				t.Fatalf("returned IDs %v, want %v", ids, tc.want)
			}
		})
	}
}

func TestHandleContainerSummaryCountsCurrentPosture(t *testing.T) {
	t.Parallel()

	const workerID = "demo-stopped-worker"

	server, db := fullTestServer(t)
	seedContainerInventory(t, db, model.InventoryBatch{
		ObservedAt: time.Now().UTC(), Complete: true,
		Instances: []model.ContainerInstance{
			{ID: "demo-db", Name: "demo-db", State: "running", Classification: model.ContainerClassificationManaged},
			{ID: workerID, Name: "demo-worker", State: "exited", Classification: model.ContainerClassificationUnknown},
		},
	})

	for _, rule := range []string{"demo-rule-one", "demo-rule-two"} {
		_, err := server.security.Evaluate(t.Context(), model.SecurityEvaluation{Observation: model.SecurityObservation{
			ContainerID: workerID, RuleID: rule, Category: model.SecurityCategoryPosture,
			Severity: model.SecuritySeverityCritical, Confidence: model.SecurityConfidenceHigh,
			Detected: true, CoverageComplete: true, Evidence: map[string]string{"detected": "true"},
		}})
		if err != nil {
			t.Fatal(err)
		}
	}

	status, body := doRequest(t, server, nethttp.MethodGet, "/api/system/containers?view=summary", "")
	if status != nethttp.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", status, body)
	}

	var got struct {
		Total      int                `json:"total"`
		Critical   int                `json:"critical"`
		Unknown    int                `json:"unknown"`
		Stopped    int                `json:"stopped"`
		Freshness  collectorFreshness `json:"freshness"`
		Containers json.RawMessage    `json:"containers"`
	}

	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}

	if got.Total != 2 || got.Critical != 1 || got.Unknown != 1 || got.Stopped != 1 ||
		got.Freshness.Stale || !got.Freshness.Complete || len(got.Containers) != 0 {
		t.Errorf("summary = %s, want counts per container, current freshness, and no inventory rows", body)
	}
}

func TestHandleContainerDetailExcludesSensitiveInspection(t *testing.T) {
	t.Parallel()

	server, db := fullTestServer(t)
	seedContainerInventory(t, db, model.InventoryBatch{
		ObservedAt: time.Now().UTC(), Complete: true,
		Instances: []model.ContainerInstance{{
			ID: "detail-id", Name: "api-1", State: "running",
			Inspection: model.SafeInspection{
				User: "10001", CommandHash: "safe-command-hash",
				Mounts: []model.SafeMount{{Type: "bind", Destination: "/data", Writable: true}},
			},
		}},
	})

	status, body := doRequest(t, server, nethttp.MethodGet, "/api/system/containers/detail-id", "")
	if status != nethttp.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", status, body)
	}
	for _, forbidden := range []string{"private.token", "environment", "source"} {
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

func TestApproveContainerViewerForbiddenAndAdminAudited(t *testing.T) {
	t.Parallel()

	server, db := fullTestServer(t)
	seedContainerInventory(t, db, model.InventoryBatch{
		ObservedAt: time.Now().UTC(), Complete: true,
		Instances: []model.ContainerInstance{{
			ID: "approve-http", Name: "helper-1", ImageID: "sha256:image", State: "running",
			Inspection: model.SafeInspection{User: "10001", CapAdd: []string{}, Mounts: []model.SafeMount{}},
		}},
	})

	status, _ := requestWithCookie(t, server, viewerCookie(t, server), nethttp.MethodPost,
		"/api/system/containers/approve-http/approve", "")
	if status != nethttp.StatusForbidden {
		t.Fatalf("viewer status = %d, want 403", status)
	}
	status, body := doRequest(t, server, nethttp.MethodPost, "/api/system/containers/approve-http/approve", "")
	if status != nethttp.StatusOK {
		t.Fatalf("admin status = %d, want 200 (body: %s)", status, body)
	}
	entries, err := store.NewAuditStore(db).List(context.Background(), "", 20)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, entry := range entries {
		if entry.Action == model.AuditContainerApprove && entry.Actor == defaultLoginUsername &&
			strings.Contains(entry.Detail, "approve-http") {
			found = true
		}
	}
	if !found {
		t.Errorf("audit entries = %+v, want admin container approval", entries)
	}
}

func TestIgnoreContainerRequiresReasonHandler(t *testing.T) {
	t.Parallel()

	server, db := fullTestServer(t)
	seedContainerInventory(t, db, model.InventoryBatch{
		ObservedAt: time.Now().UTC(), Complete: true,
		Instances: []model.ContainerInstance{{ID: "ignore-http", Name: "helper-1"}},
	})
	status, _ := doRequest(t, server, nethttp.MethodPost, "/api/system/containers/ignore-http/ignore",
		`{"non_expiring":true}`)
	if status != nethttp.StatusBadRequest {
		t.Errorf("status = %d, want 400", status)
	}
}

func requestWithCookie(t *testing.T, server *Server, cookie *nethttp.Cookie, method, target, body string) (int, []byte) {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), method, target, strings.NewReader(body))
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	server.handler.ServeHTTP(rec, req)
	res := rec.Result()
	defer func() { _ = res.Body.Close() }()
	data, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	return res.StatusCode, data
}

func seedContainerInventory(t *testing.T, db *store.DB, batch model.InventoryBatch) {
	t.Helper()
	if err := store.NewContainerStore(db).Reconcile(context.Background(), batch); err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
}
