package http

import (
	"context"
	"encoding/json"
	nethttp "net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/oralecarlangelo/touchgrass/internal/model"
	"github.com/oralecarlangelo/touchgrass/internal/objectstore"
	"github.com/oralecarlangelo/touchgrass/internal/store"
)

func (s stubStatsLister) StorageUsage(_ context.Context) (model.StorageUsage, error) {
	return model.StorageUsage{Entries: []model.StorageEntry{{ID: "sample-volume", Name: "sample-volume", Kind: "volume"}}}, nil
}

func TestStorageUsageEndpoint(t *testing.T) {
	t.Parallel()
	server, _ := fullTestServer(t)

	status, body := doRequest(t, server, nethttp.MethodGet, "/api/storage/usage", "")
	if status != nethttp.StatusOK {
		t.Fatalf("status %d: %s", status, body)
	}

	var result model.StorageUsage
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatal(err)
	}

	if len(result.Entries) != 1 || result.Entries[0].SizeBytes != nil {
		t.Fatalf("unknown sizes must stay null: %+v", result)
	}
}

func TestBucketsDisabledByDefault(t *testing.T) {
	t.Parallel()
	server, _ := fullTestServer(t)

	status, body := doRequest(t, server, nethttp.MethodGet, "/api/storage/buckets", "")
	if status != nethttp.StatusOK {
		t.Fatalf("status %d: %s", status, body)
	}

	var result struct {
		Configured bool
		Writable   bool
		Buckets    []string
	}
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatal(err)
	}

	if result.Configured || result.Writable || len(result.Buckets) != 0 {
		t.Fatalf("unsafe default: %+v", result)
	}
}

func TestStorageActionsRejectUnsafeRequests(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		writable bool
		body     string
		want     int
	}{
		{name: "read only upload", body: `{"action":"upload","bucket":"example-assets","key":"","filename":"x.txt"}`, want: nethttp.StatusForbidden},
		{name: "outside bucket", writable: true, body: `{"action":"delete","bucket":"other-assets","key":"x.txt","confirmation":"x.txt"}`, want: nethttp.StatusForbidden},
		{name: "missing confirmation", writable: true, body: `{"action":"delete","bucket":"example-assets","key":"x.txt"}`, want: nethttp.StatusBadRequest},
		{name: "mismatched confirmation", writable: true, body: `{"action":"delete","bucket":"example-assets","key":"x.txt","confirmation":"y.txt"}`, want: nethttp.StatusBadRequest},
		{name: "empty deletion", writable: true, body: `{"action":"delete","bucket":"example-assets","key":"","confirmation":""}`, want: nethttp.StatusBadRequest},
		{name: "unknown action", writable: true, body: `{"action":"delete_bucket","bucket":"example-assets","key":""}`, want: nethttp.StatusBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			server, _ := fullTestServer(t)
			server.objects = storageTestConnection(t, tt.writable)

			status, body := doRequest(t, server, nethttp.MethodPost, "/api/storage/action", tt.body)
			if status != tt.want {
				t.Fatalf("status = %d, want %d: %s", status, tt.want, body)
			}
		})
	}
}

func storageTestConnection(t *testing.T, writable bool) *objectstore.Client {
	t.Helper()
	//nolint:gosec // Synthetic test credentials are deliberately serialized to a private fixture file, never an API response.
	data, err := json.Marshal(objectstore.Config{Endpoint: "https://objects.example.test", Region: "us-east-1", AccessKey: "test-access", SecretKey: "test-secret", Buckets: []string{"example-assets"}, Writable: writable})
	if err != nil {
		t.Fatal(err)
	}

	file := filepath.Join(t.TempDir(), "storage.json")
	if err := os.WriteFile(file, data, 0o600); err != nil {
		t.Fatal(err)
	}

	connection, err := objectstore.Open(file)
	if err != nil {
		t.Fatal(err)
	}

	return connection
}

func TestStorageDownloadAudited(t *testing.T) {
	t.Parallel()
	server, db := fullTestServer(t)
	server.objects = storageTestConnection(t, false)

	status, body := doRequest(t, server, nethttp.MethodPost, "/api/storage/action", `{"action":"download","bucket":"example-assets","key":"photos/a.jpg"}`)
	if status != nethttp.StatusOK {
		t.Fatalf("status = %d: %s", status, body)
	}

	rows, err := store.NewAuditStore(db).List(t.Context(), "", 10)
	if err != nil {
		t.Fatal(err)
	}

	count := 0

	for _, row := range rows {
		if row.Action != model.AuditStorageAction {
			continue
		}

		count++

		if strings.Contains(row.Detail, "X-Amz") || strings.Contains(row.Detail, "test-secret") {
			t.Fatal("audit contains signed credentials")
		}

		if !strings.Contains(row.Detail, "photos/a.jpg") {
			t.Fatal("audit is missing selected key")
		}
	}

	if count != 2 {
		t.Fatalf("storage audit records = %d, want intent and grant issuance", count)
	}
}
