package http

import (
	"encoding/json"
	"strconv"
	"testing"

	nethttp "net/http"

	"github.com/oralecarlangelo/touchgrass/internal/model"
	"github.com/oralecarlangelo/touchgrass/internal/store"
)

func TestHandleRecordListDeploys(t *testing.T) {
	t.Parallel()

	server, _ := fullTestServer(t)

	status, body := doRequest(t, server, nethttp.MethodPost, testDeploysPath,
		`{"sha":"abc123","actor":"carl","outcome":"success","notes":"scripted"}`)

	if status != nethttp.StatusCreated {
		t.Fatalf("status = %d, want 201 (body: %s)", status, body)
	}

	var created deployIDResponse

	if err := json.Unmarshal(body, &created); err != nil {
		t.Fatalf("decoding body: %v", err)
	}

	if created.ID == 0 {
		t.Fatal("created id = 0, want non-zero")
	}

	status, body = doRequest(t, server, nethttp.MethodGet, testDeploysPath, "")

	if status != nethttp.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", status, body)
	}

	var got deploysResponse

	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decoding body: %v", err)
	}

	if len(got.Deploys) != 1 || got.Deploys[0].SHA != "abc123" || got.Deploys[0].Type != "manual" {
		t.Errorf("deploys = %+v, want the manual record", got.Deploys)
	}
}

func TestHandleDeployHistoryBeforeID(t *testing.T) {
	t.Parallel()

	server, db := fullTestServer(t)
	deploys := store.NewDeployStore(db)
	ids := []int64{}

	for _, sha := range []string{"first", "second", "third"} {
		id, err := deploys.Record(t.Context(), model.Deploy{
			ServiceID: testServiceAPI, SHA: sha, Actor: "example-operator",
			Type: model.DeployManual, Outcome: model.DeploySuccess,
		})
		if err != nil {
			t.Fatal(err)
		}

		ids = append(ids, id)
	}

	target := testDeploysPath + "?limit=1&before_id=" + strconv.FormatInt(ids[1], 10)

	status, body := doRequest(t, server, nethttp.MethodGet, target, "")
	if status != nethttp.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", status, body)
	}

	var got deploysResponse
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}

	if len(got.Deploys) != 1 || got.Deploys[0].ID != ids[0] {
		t.Fatalf("history = %s, want the entry before the cursor", body)
	}
}

func TestHandleRecordDeployErrors(t *testing.T) {
	t.Parallel()

	server, _ := fullTestServer(t)

	tests := []struct {
		name   string
		method string
		target string
		body   string
		status int
	}{
		{
			name:   testUnknownService,
			method: nethttp.MethodPost,
			target: "/api/services/nope/deploys",
			body:   `{"sha":"a","actor":"b","outcome":"success"}`,
			status: nethttp.StatusNotFound,
		},
		{
			name:   "empty sha",
			method: nethttp.MethodPost,
			target: testDeploysPath,
			body:   `{"sha":"","actor":"b","outcome":"success"}`,
			status: nethttp.StatusBadRequest,
		},
		{
			name:   "bad time",
			method: nethttp.MethodPost,
			target: testDeploysPath,
			body:   `{"sha":"a","actor":"b","outcome":"success","started_at":"soon"}`,
			status: nethttp.StatusBadRequest,
		},
		{
			name:   "history unknown service",
			method: nethttp.MethodGet,
			target: "/api/services/nope/deploys",
			body:   "",
			status: nethttp.StatusNotFound,
		},
		{
			name:   "invalid history cursor",
			method: nethttp.MethodGet,
			target: testDeploysPath + "?before_id=-3",
			status: nethttp.StatusBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			status, body := doRequest(t, server, tt.method, tt.target, tt.body)

			if status != tt.status {
				t.Errorf("status = %d, want %d (body: %s)", status, tt.status, body)
			}
		})
	}
}
