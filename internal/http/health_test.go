package http

import (
	"encoding/json"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	nethttp "net/http"

	"github.com/oralecarlangelo/touchgrass/internal/model"
)

// stubMaintenance reports a fixed sweep status.
type stubMaintenance struct {
	status model.MaintenanceStatus
}

func (s stubMaintenance) Status() model.MaintenanceStatus { return s.status }

// healthServer builds the smallest Server that can answer /api/health. The path
// is public, so the authenticator is not reached and is left out.
func healthServer(reporter MaintenanceReporter) *Server {
	return New(Config{
		Addr:        testDatabaseAddr,
		Version:     testVersion,
		Logger:      slog.New(slog.DiscardHandler),
		Maintenance: reporter,
		Dist:        testDist(),
		Docs:        testDocs(),
	})
}

func getHealth(t *testing.T, server *Server) (int, healthResponse) {
	t.Helper()

	req := httptest.NewRequestWithContext(t.Context(), nethttp.MethodGet, testHealthPath, nil)
	rec := httptest.NewRecorder()
	server.handler.ServeHTTP(rec, req)

	var payload healthResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("Unmarshal() error = %v, want nil, body = %s", err, rec.Body.String())
	}

	return rec.Code, payload
}

func TestHealthReportsReadiness(t *testing.T) {
	t.Parallel()

	recent := time.Now().UTC()

	tests := []struct {
		name        string
		reporter    MaintenanceReporter
		wantCode    int
		wantStatus  string
		wantCheck   string
		wantDetail  string
		wantNoCheck bool
	}{
		{
			// Back-compatible: an instance wired without the reporter still answers ok.
			name:        "no reporter wired",
			reporter:    nil,
			wantCode:    nethttp.StatusOK,
			wantStatus:  healthOK,
			wantNoCheck: true,
		},
		{
			// The first sweep is up to one interval after start-up; a fresh install
			// must not fail the installer's probe.
			name:       "no sweep yet",
			reporter:   stubMaintenance{status: model.MaintenanceStatus{Stale: true}},
			wantCode:   nethttp.StatusOK,
			wantStatus: healthOK,
			wantCheck:  checkPass,
			wantDetail: "no sweep yet",
		},
		{
			// THE regression guard. This is the 10-26 Nov 2026 GRID failure: every
			// write refused, the endpoint answering "ok" for sixteen days.
			name: "writes are failing",
			reporter: stubMaintenance{status: model.MaintenanceStatus{
				LastRunAt: recent,
				LastError: "inserting host sample: database is locked (5) (SQLITE_BUSY)",
			}},
			wantCode:   nethttp.StatusServiceUnavailable,
			wantStatus: healthDegraded,
			wantCheck:  checkFail,
			wantDetail: "SQLITE_BUSY",
		},
		{
			name: "sweeps stopped happening",
			reporter: stubMaintenance{status: model.MaintenanceStatus{
				LastSuccessAt: recent.Add(-2 * time.Hour),
				Stale:         true,
			}},
			wantCode:   nethttp.StatusServiceUnavailable,
			wantStatus: healthDegraded,
			wantCheck:  checkFail,
		},
		{
			// A budget overrun warns: it already logs, and the instance stays usable,
			// so 503 keeps its single meaning.
			name: "over the storage budget",
			reporter: stubMaintenance{status: model.MaintenanceStatus{
				LastSuccessAt: recent,
				LastResult: model.MaintenanceResult{
					DatabaseBytes: 5_000_000_000, MaxBytes: 4_294_967_296, OverBudget: true,
				},
			}},
			wantCode:   nethttp.StatusOK,
			wantStatus: healthOK,
			wantCheck:  checkWarn,
		},
		{
			name:       "healthy sweep",
			reporter:   stubMaintenance{status: model.MaintenanceStatus{LastSuccessAt: recent}},
			wantCode:   nethttp.StatusOK,
			wantStatus: healthOK,
			wantCheck:  checkPass,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			code, payload := getHealth(t, healthServer(tt.reporter))

			if code != tt.wantCode {
				t.Errorf("status code = %d, want %d", code, tt.wantCode)
			}

			if payload.Status != tt.wantStatus {
				t.Errorf("status = %q, want %q", payload.Status, tt.wantStatus)
			}

			if payload.Version != testVersion {
				t.Errorf("version = %q, want %q", payload.Version, testVersion)
			}

			if tt.wantNoCheck {
				if len(payload.Checks) != 0 {
					t.Errorf("checks = %v, want none", payload.Checks)
				}

				return
			}

			if len(payload.Checks) != 1 {
				t.Fatalf("checks = %v, want exactly one", payload.Checks)
			}

			check := payload.Checks[0]
			if check.Name != maintenanceCheckName {
				t.Errorf("check name = %q, want %q", check.Name, maintenanceCheckName)
			}

			if check.Status != tt.wantCheck {
				t.Errorf("check status = %q, want %q", check.Status, tt.wantCheck)
			}

			if tt.wantDetail != "" && !strings.Contains(check.Detail, tt.wantDetail) {
				t.Errorf("check detail = %q, want it to mention %q", check.Detail, tt.wantDetail)
			}
		})
	}
}
