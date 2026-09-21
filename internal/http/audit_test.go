package http

import (
	"context"
	"encoding/json"
	"testing"

	nethttp "net/http"

	"github.com/oralecarlangelo/touchgrass/internal/model"
	"github.com/oralecarlangelo/touchgrass/internal/service"
	"github.com/oralecarlangelo/touchgrass/internal/store"
)

// seedRollbackAudit records one scoped rollback entry for audit tests.
func seedRollbackAudit(t *testing.T, db *store.DB) {
	t.Helper()

	audit := service.NewAudit(store.NewServiceStore(db), store.NewAuditStore(db))

	if _, err := audit.Record(context.Background(), model.AuditRecord{
		ServiceID: testServiceAPI, Actor: "admin", Action: model.AuditRollback,
		Result: model.AuditSuccess, Detail: "rollback to blue: success",
	}); err != nil {
		t.Fatalf("Record() error = %v, want nil", err)
	}
}

func TestHandleAuditScoped(t *testing.T) {
	t.Parallel()

	server, db := fullTestServer(t)
	seedRollbackAudit(t, db)

	status, body := doRequest(t, server, nethttp.MethodGet, "/api/audit?service_id=tn-api&limit=10", "")

	if status != nethttp.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", status, body)
	}

	var got auditResponse

	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decoding body: %v", err)
	}

	if len(got.Audit) != 1 || got.Audit[0].Action != model.AuditRollback {
		t.Fatalf("audit = %+v, want the scoped rollback entry", got.Audit)
	}

	if got.Audit[0].ServiceID == nil || *got.Audit[0].ServiceID != testServiceAPI {
		t.Errorf("service_id = %v, want tn-api", got.Audit[0].ServiceID)
	}
}

func TestHandleAuditUnfiltered(t *testing.T) {
	t.Parallel()

	server, db := fullTestServer(t)
	seedRollbackAudit(t, db)

	status, body := doRequest(t, server, nethttp.MethodGet, "/api/audit?limit=100", "")

	if status != nethttp.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", status, body)
	}

	var got auditResponse

	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decoding body: %v", err)
	}

	found := false

	for _, entry := range got.Audit {
		if entry.ServiceID != nil && *entry.ServiceID == testServiceAPI && entry.Action == model.AuditRollback {
			found = true
		}

		if entry.Actor == "" || entry.Action == "" || entry.Result == "" || entry.CreatedAt.IsZero() {
			t.Errorf("entry = %+v, want actor/action/result/timestamp", entry)
		}
	}

	if !found {
		t.Error("unfiltered audit omits the scoped rollback entry")
	}
}

func TestHandleAuditErrors(t *testing.T) {
	t.Parallel()

	server, _ := fullTestServer(t)

	tests := []struct {
		name   string
		target string
		status int
	}{
		{name: testUnknownService, target: "/api/audit?service_id=nope", status: nethttp.StatusNotFound},
		{name: "bad limit", target: "/api/audit?limit=huge", status: nethttp.StatusBadRequest},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			status, body := doRequest(t, server, nethttp.MethodGet, tt.target, "")

			if status != tt.status {
				t.Errorf("status = %d, want %d (body: %s)", status, tt.status, body)
			}
		})
	}
}
