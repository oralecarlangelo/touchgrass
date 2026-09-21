package http

import (
	"context"
	"encoding/json"
	"io"
	"net/http/httptest"
	"testing"
	"time"

	nethttp "net/http"

	"github.com/oralecarlangelo/touchgrass/internal/model"
	"github.com/oralecarlangelo/touchgrass/internal/store"
)

func TestHandleMetrics(t *testing.T) {
	t.Parallel()

	server, db := fullTestServer(t)
	ctx := context.Background()

	if err := store.NewMetricStore(db).Insert(ctx, model.Metric{
		ServiceID: testServiceAPI, ContainerName: "api-blue-1",
		CPUPercent: 33, MemBytes: 200, MemLimit: 1000,
		SampledAt: time.Now(),
	}); err != nil {
		t.Fatalf("Insert() error = %v, want nil", err)
	}

	req := httptest.NewRequestWithContext(t.Context(), nethttp.MethodGet, "/api/services/tn-api/metrics", nil)
	req.AddCookie(authCookie(t, server))

	rec := httptest.NewRecorder()

	server.handler.ServeHTTP(rec, req)

	res := rec.Result()

	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("reading body: %v", err)
	}

	if err := res.Body.Close(); err != nil {
		t.Fatalf("closing body: %v", err)
	}

	if res.StatusCode != nethttp.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", res.StatusCode, body)
	}

	var got metricsResponse

	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decoding body: %v", err)
	}

	if len(got.Metrics) != 1 || got.Metrics[0].CPUPercent != 33 {
		t.Errorf("metrics = %+v, want the seeded sample", got.Metrics)
	}
}

func TestHandleMetricsErrors(t *testing.T) {
	t.Parallel()

	server, _ := fullTestServer(t)

	tests := []struct {
		name           string
		target         string
		expectedStatus int
		expectedCode   string
	}{
		{
			name:           testUnknownService,
			target:         "/api/services/nope/metrics",
			expectedStatus: nethttp.StatusNotFound,
			expectedCode:   "not_found",
		},
		{
			name:           "bad since",
			target:         "/api/services/tn-api/metrics?since=soon",
			expectedStatus: nethttp.StatusBadRequest,
			expectedCode:   testInvalidRequest,
		},
		{
			name:           "bad limit",
			target:         "/api/services/tn-api/metrics?limit=huge",
			expectedStatus: nethttp.StatusBadRequest,
			expectedCode:   testInvalidRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			req := httptest.NewRequestWithContext(t.Context(), nethttp.MethodGet, tt.target, nil)
			req.AddCookie(authCookie(t, server))

			rec := httptest.NewRecorder()

			server.handler.ServeHTTP(rec, req)

			res := rec.Result()

			body, err := io.ReadAll(res.Body)
			if err != nil {
				t.Fatalf("reading body: %v", err)
			}

			if err := res.Body.Close(); err != nil {
				t.Fatalf("closing body: %v", err)
			}

			if res.StatusCode != tt.expectedStatus {
				t.Fatalf("status = %d, want %d (body: %s)", res.StatusCode, tt.expectedStatus, body)
			}

			var got errorResponse

			if err := json.Unmarshal(body, &got); err != nil {
				t.Fatalf("decoding body: %v", err)
			}

			if got.Code != tt.expectedCode {
				t.Errorf("code = %q, want %q", got.Code, tt.expectedCode)
			}
		})
	}
}
