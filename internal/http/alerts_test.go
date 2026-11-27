package http

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	nethttp "net/http"

	"github.com/oralecarlangelo/touchgrass/internal/model"
	"github.com/oralecarlangelo/touchgrass/internal/store"
)

func doRequest(
	t *testing.T,
	server *Server,
	method, target, body string,
) (int, []byte) {
	t.Helper()

	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}

	req := httptest.NewRequestWithContext(t.Context(), method, target, reader)
	req.AddCookie(authCookie(t, server))

	rec := httptest.NewRecorder()

	server.handler.ServeHTTP(rec, req)

	res := rec.Result()

	data, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("reading body: %v", err)
	}

	if err := res.Body.Close(); err != nil {
		t.Fatalf("closing body: %v", err)
	}

	return res.StatusCode, data
}

func TestHandleRules(t *testing.T) {
	t.Parallel()

	server, _ := fullTestServer(t)

	status, body := doRequest(t, server, nethttp.MethodGet, "/api/alerts/rules?service_id=tn-api", "")

	if status != nethttp.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", status, body)
	}

	var got rulesResponse

	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decoding body: %v", err)
	}

	if len(got.Rules) != 1 || got.Rules[0].Metric != model.MetricMem {
		t.Errorf("rules = %+v, want the seeded mem rule", got.Rules)
	}

	status, _ = doRequest(t, server, nethttp.MethodGet, "/api/alerts/rules", "")
	if status != nethttp.StatusBadRequest {
		t.Errorf("missing service_id status = %d, want 400", status)
	}
}

func TestHandleRulePreview(t *testing.T) {
	t.Parallel()

	server, db := fullTestServer(t)

	workload := model.Service{
		ID: "example-worker", Name: "Example worker", Strategy: model.StrategyRecreate, Config: json.RawMessage(`{}`),
	}
	if err := store.NewServiceStore(db).Create(t.Context(), workload); err != nil {
		t.Fatalf("create workload: %v", err)
	}

	now := time.Now().UTC().Truncate(time.Second)

	samples := []model.Metric{
		{ServiceID: workload.ID, ContainerName: "worker-a", SampledAt: now, MemBytes: 30, MemLimit: 100},
		{ServiceID: workload.ID, ContainerName: "worker-b", SampledAt: now, MemBytes: 90, MemLimit: 100},
	}
	if err := store.NewMetricStore(db).InsertBatch(t.Context(), samples); err != nil {
		t.Fatalf("insert metrics: %v", err)
	}

	for _, tc := range []struct {
		name      string
		metric    string
		status    int
		available bool
	}{
		{name: "highest recorded container", metric: model.MetricMem, status: nethttp.StatusOK, available: true},
		{name: "collector has no sample", metric: model.MetricHTTPP95, status: nethttp.StatusOK},
		{name: "unknown metric", metric: "other", status: nethttp.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			target := "/api/alerts/rules/preview?service_id=" + workload.ID + "&metric=" + tc.metric

			status, body := doRequest(t, server, nethttp.MethodGet, target, "")
			if status != tc.status {
				t.Fatalf("status = %d, want %d (body: %s)", status, tc.status, body)
			}

			if status != nethttp.StatusOK {
				return
			}

			var got struct {
				Value     *float64   `json:"value"`
				SampledAt *time.Time `json:"sampled_at"`
			}
			if err := json.Unmarshal(body, &got); err != nil {
				t.Fatalf("decode preview: %v", err)
			}

			if !tc.available {
				if got.Value != nil {
					t.Fatalf("value = %v, want unavailable", *got.Value)
				}

				return
			}

			if got.Value == nil || *got.Value != 90 || got.SampledAt == nil || !got.SampledAt.Equal(now) {
				t.Fatalf("preview = %s, want value 90 sampled at %s", body, now)
			}
		})
	}
}

func TestHandleCreateDeleteRule(t *testing.T) {
	t.Parallel()

	server, _ := fullTestServer(t)

	status, body := doRequest(t, server, nethttp.MethodPost, "/api/alerts/rules",
		`{"service_id":"tn-api","metric":"cpu","threshold":90,"duration_secs":60}`)

	if status != nethttp.StatusCreated {
		t.Fatalf("status = %d, want 201 (body: %s)", status, body)
	}

	var created model.AlertRule

	if err := json.Unmarshal(body, &created); err != nil {
		t.Fatalf("decoding body: %v", err)
	}

	if created.ID == 0 {
		t.Fatal("created id = 0, want non-zero")
	}

	status, body = doRequest(t, server, nethttp.MethodPost, "/api/alerts/rules",
		`{"service_id":"tn-api","metric":"nope","threshold":90,"duration_secs":60}`)

	if status != nethttp.StatusBadRequest {
		t.Errorf("bad metric status = %d, want 400 (body: %s)", status, body)
	}

	status, _ = doRequest(t, server, nethttp.MethodDelete, fmt.Sprintf("/api/alerts/rules/%d", created.ID), "")
	if status != nethttp.StatusNoContent {
		t.Errorf("delete status = %d, want 204", status)
	}

	status, _ = doRequest(t, server, nethttp.MethodDelete, fmt.Sprintf("/api/alerts/rules/%d", created.ID), "")
	if status != nethttp.StatusNotFound {
		t.Errorf("second delete status = %d, want 404", status)
	}
}

func TestHandleNotifications(t *testing.T) {
	t.Parallel()

	server, db := fullTestServer(t)

	if _, err := store.NewNotificationStore(db).Insert(
		context.Background(), "tn-api", model.NotificationAlertBreach, "mem high", "above 85%",
	); err != nil {
		t.Fatalf("Insert() error = %v, want nil", err)
	}

	status, body := doRequest(t, server, nethttp.MethodGet, "/api/notifications", "")

	if status != nethttp.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", status, body)
	}

	var got notificationsResponse

	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decoding body: %v", err)
	}

	if len(got.Notifications) != 1 || got.Notifications[0].Title != "mem high" {
		t.Errorf("notifications = %+v, want the seeded row", got.Notifications)
	}
}
