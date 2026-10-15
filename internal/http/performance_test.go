package http

import (
	"context"
	"encoding/json"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	nethttp "net/http"

	"github.com/oralecarlangelo/touchgrass/internal/model"
	"github.com/oralecarlangelo/touchgrass/internal/service"
	"github.com/oralecarlangelo/touchgrass/internal/store"
)

func performanceRequest(t *testing.T, server *Server, cookie *nethttp.Cookie, target string) (int, []byte) {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), nethttp.MethodGet, target, nil)
	if cookie != nil {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	server.handler.ServeHTTP(rec, req)
	res := rec.Result()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	if err := res.Body.Close(); err != nil {
		t.Fatal(err)
	}
	return res.StatusCode, body
}

func TestPerformanceRoutesRequireAuthAndAllowViewer(t *testing.T) {
	t.Parallel()

	server, _ := fullTestServer(t)
	status, _ := performanceRequest(t, server, nil, "/api/performance/summary")
	if status != nethttp.StatusUnauthorized {
		t.Fatalf("anonymous status = %d, want 401", status)
	}
	status, body := performanceRequest(t, server, viewerCookie(t, server), "/api/performance/summary")
	if status != nethttp.StatusOK {
		t.Fatalf("viewer status = %d, want 200 (body: %s)", status, body)
	}
}

func TestPerformanceGlobalAndServiceResponses(t *testing.T) {
	t.Parallel()

	server, db := fullTestServer(t)
	now := time.Now().UTC().Truncate(time.Minute)
	histogram := model.HTTPHistogram{}
	histogram.Observe(250)
	if err := store.NewPerformanceStore(db).UpsertHTTP(context.Background(), model.HTTPMetricBucket{
		BucketStart: now.Add(-time.Minute), ServiceID: testServiceAPI, Environment: "prod",
		Method: "GET", RouteFamily: "account_orders", Upstream: "api", StatusClass: 2,
		Histogram: histogram, RequestCount: 1, DurationTotalMS: 250, IngestKey: "handler",
	}); err != nil {
		t.Fatal(err)
	}
	cookie := authCookie(t, server)
	for _, target := range []string{
		"/api/performance/summary", "/api/performance/timeline", "/api/performance/routes",
		"/api/performance/postgres", "/api/performance/redis", "/api/collectors/status",
		"/api/services/tn-api/performance/summary", "/api/services/tn-api/performance/timeline",
		"/api/services/tn-api/performance/routes", "/api/services/tn-api/performance/postgres",
		"/api/services/tn-api/performance/redis",
	} {
		status, body := performanceRequest(t, server, cookie, target)
		if status != nethttp.StatusOK {
			t.Errorf("GET %s status = %d, want 200 (body: %s)", target, status, body)
		}
		for _, collection := range []string{"anomalies", "points", "events", "deployments", "routes", "metrics", "collectors"} {
			if strings.Contains(string(body), `"`+collection+`":null`) {
				t.Errorf("GET %s returned null %s collection: %s", target, collection, body)
			}
		}
	}
}

func TestPerformanceHandlerValidationAndUnknownService(t *testing.T) {
	t.Parallel()

	server, _ := fullTestServer(t)
	cookie := authCookie(t, server)
	tests := []struct {
		target string
		status int
	}{
		{"/api/performance/summary?start=soon", nethttp.StatusBadRequest},
		{"/api/performance/summary?start=2026-10-16T12:00:00Z&end=2026-10-16T11:00:00Z", nethttp.StatusBadRequest},
		{"/api/performance/routes?limit=101", nethttp.StatusBadRequest},
		{"/api/performance/routes?sort=raw_sql", nethttp.StatusBadRequest},
		{"/api/performance/routes?route_family=%2Faccount%2Forders%3Femail%3Dsecret", nethttp.StatusBadRequest},
		{"/api/performance/summary?resolution=second", nethttp.StatusBadRequest},
		{"/api/services/nope/performance/summary", nethttp.StatusNotFound},
	}
	for _, test := range tests {
		status, body := performanceRequest(t, server, cookie, test.target)
		if status != test.status {
			t.Errorf("GET %s status = %d, want %d (body: %s)", test.target, status, test.status, body)
		}
	}
}

func TestCollectorStatusReturnsStaleSource(t *testing.T) {
	t.Parallel()

	server, db := fullTestServer(t)
	if err := store.NewCursorStore(db).Upsert(context.Background(), model.CollectorCursor{
		Source: "nginx", LastSuccessAt: time.Now().Add(-time.Hour), LastError: "nginx_partial",
	}); err != nil {
		t.Fatal(err)
	}
	status, body := performanceRequest(t, server, authCookie(t, server), "/api/collectors/status")
	if status != nethttp.StatusOK {
		t.Fatalf("status = %d, body = %s", status, body)
	}
	var response collectorsResponse
	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Collectors) == 0 || !response.Collectors[0].Stale {
		t.Fatalf("collectors = %+v", response.Collectors)
	}
}

func TestPerformanceInternalErrorDoesNotLeakStoreDetails(t *testing.T) {
	t.Parallel()

	server, db := fullTestServer(t)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequestWithContext(t.Context(), nethttp.MethodGet, "/api/performance/summary", nil)
	rec := httptest.NewRecorder()
	server.handlePerformanceSummary(rec, req)
	res := rec.Result()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != nethttp.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 (body: %s)", res.StatusCode, body)
	}
	if strings.Contains(strings.ToLower(string(body)), "sql") || strings.Contains(string(body), "collector_cursors") {
		t.Fatalf("internal response leaked store details: %s", body)
	}
}

var _ = service.PerformanceSummary{}
