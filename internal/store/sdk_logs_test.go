package store

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/oralecarlangelo/touchgrass/internal/model"
)

// Shared SDK search corpus values.
const (
	testSDKRelease = "v1.2.3"
	testSDKHandled = "handled request"
)

// testSDKLog builds one info row at an offset from a base time.
func testSDKLog(service, message string, at time.Time) model.SDKLog {
	return model.SDKLog{
		ServiceID: service, Ts: at, Level: model.SDKLogInfo,
		Severity: 9, Message: message,
		Attributes: map[string]model.SDKLogAttribute{},
		Release:    testSDKRelease,
	}
}

// seedSDKLogs stores the shared corpus: two api rows plus one fe row.
func seedSDKLogs(t *testing.T, logs *SDKLogStore, base time.Time) {
	t.Helper()

	traced := testSDKLog(testServiceAPI, testSDKHandled, base.Add(time.Second))
	traced.TraceID = "0123456789abcdef0123456789abcdef"
	traced.SpanID = "0123456789abcdef"
	traced.Attributes["route"] = model.SDKLogAttribute{Value: "/health", Type: model.SDKAttrString}

	if _, err := logs.InsertBatch(t.Context(), []model.SDKLog{
		testSDKLog(testServiceAPI, "listening on :4101", base),
		traced,
		testSDKLog("tn-fe", "rendered page", base.Add(2*time.Second)),
	}); err != nil {
		t.Fatalf("InsertBatch() error = %v, want nil", err)
	}
}

func TestSDKLogStoreInsert(t *testing.T) {
	t.Parallel()

	db := openTestDB(t)
	ctx := context.Background()
	logs := NewSDKLogStore(db)
	base := time.Date(2026, 9, 22, 10, 0, 0, 0, time.UTC)

	inserted, err := logs.InsertBatch(ctx, []model.SDKLog{
		testSDKLog(testServiceAPI, "one", base),
		testSDKLog(testServiceAPI, "two", base.Add(time.Second)),
	})
	if err != nil {
		t.Fatalf("InsertBatch() error = %v, want nil", err)
	}

	if inserted != 2 {
		t.Fatalf("InsertBatch() = %d, want 2", inserted)
	}

	none, err := logs.InsertBatch(ctx, nil)
	if err != nil || none != 0 {
		t.Fatalf("InsertBatch(nil) = (%d, %v), want (0, nil)", none, err)
	}
}

func TestSDKLogStoreSearch(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		filter   func(base time.Time) SDKLogFilter
		expected []string
	}{
		{
			name: "service isolation newest first",
			filter: func(_ time.Time) SDKLogFilter {
				return SDKLogFilter{ServiceID: testServiceAPI}
			},
			expected: []string{testSDKHandled, "listening on :4101"},
		},
		{
			name: "trace filter",
			filter: func(_ time.Time) SDKLogFilter {
				return SDKLogFilter{
					ServiceID: testServiceAPI,
					TraceID:   "0123456789abcdef0123456789abcdef",
				}
			},
			expected: []string{testSDKHandled},
		},
		{
			name: "level filter",
			filter: func(_ time.Time) SDKLogFilter {
				return SDKLogFilter{ServiceID: testServiceAPI, Levels: []string{model.SDKLogInfo}}
			},
			expected: []string{testSDKHandled, "listening on :4101"},
		},
		{
			name: "level filter misses",
			filter: func(_ time.Time) SDKLogFilter {
				return SDKLogFilter{ServiceID: testServiceAPI, Levels: []string{model.SDKLogError}}
			},
			expected: []string{},
		},
		{
			name: "message substring",
			filter: func(_ time.Time) SDKLogFilter {
				return SDKLogFilter{ServiceID: testServiceAPI, Query: "LISTENING"}
			},
			expected: []string{"listening on :4101"},
		},
		{
			name: "like metacharacters match literally",
			filter: func(_ time.Time) SDKLogFilter {
				return SDKLogFilter{ServiceID: testServiceAPI, Query: "handled %"}
			},
			expected: []string{},
		},
		{
			name: "time window",
			filter: func(base time.Time) SDKLogFilter {
				since := base.Add(time.Second)

				return SDKLogFilter{ServiceID: testServiceAPI, Since: &since}
			},
			expected: []string{testSDKHandled},
		},
		{
			name: "limit",
			filter: func(_ time.Time) SDKLogFilter {
				return SDKLogFilter{ServiceID: testServiceAPI, Limit: 1}
			},
			expected: []string{testSDKHandled},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			db := openTestDB(t)
			logs := NewSDKLogStore(db)
			base := time.Date(2026, 9, 22, 10, 0, 0, 0, time.UTC)
			seedSDKLogs(t, logs, base)

			got, err := logs.Search(t.Context(), tt.filter(base))
			if err != nil {
				t.Fatalf("Search() error = %v, want nil", err)
			}

			messages := make([]string, 0, len(got))
			for _, row := range got {
				messages = append(messages, row.Message)
			}

			if !reflect.DeepEqual(messages, tt.expected) {
				t.Errorf("Search() = %v, want %v", messages, tt.expected)
			}
		})
	}
}

func TestSDKLogStoreSearchRow(t *testing.T) {
	t.Parallel()

	db := openTestDB(t)
	logs := NewSDKLogStore(db)
	base := time.Date(2026, 9, 22, 10, 0, 0, 0, time.UTC)
	seedSDKLogs(t, logs, base)

	got, err := logs.Search(t.Context(), SDKLogFilter{
		ServiceID: testServiceAPI,
		TraceID:   "0123456789abcdef0123456789abcdef",
	})
	if err != nil {
		t.Fatalf("Search() error = %v, want nil", err)
	}

	if len(got) != 1 {
		t.Fatalf("Search() = %d rows, want 1", len(got))
	}

	row := got[0]

	if row.ServiceID != testServiceAPI || row.Level != model.SDKLogInfo || row.Severity != 9 {
		t.Errorf("row identity = %+v, want api info severity 9", row)
	}

	if row.SpanID != "0123456789abcdef" || row.Release != testSDKRelease {
		t.Errorf("row correlation = %+v, want span + release", row)
	}

	if !row.Ts.Equal(base.Add(time.Second)) {
		t.Errorf("row ts = %v, want base+1s", row.Ts)
	}

	expected := map[string]model.SDKLogAttribute{
		"route": {Value: "/health", Type: model.SDKAttrString},
	}

	if !reflect.DeepEqual(row.Attributes, expected) {
		t.Errorf("row attributes = %+v, want %+v", row.Attributes, expected)
	}
}

func TestSDKLogStoreTrimBefore(t *testing.T) {
	t.Parallel()

	db := openTestDB(t)
	ctx := context.Background()
	logs := NewSDKLogStore(db)
	base := time.Date(2026, 9, 22, 10, 0, 0, 0, time.UTC)

	if _, err := logs.InsertBatch(ctx, []model.SDKLog{
		testSDKLog(testServiceAPI, "old", base),
		testSDKLog(testServiceAPI, "new", base.Add(time.Hour)),
	}); err != nil {
		t.Fatalf("InsertBatch() error = %v, want nil", err)
	}

	trimmed, err := logs.TrimBefore(ctx, base.Add(30*time.Minute))
	if err != nil {
		t.Fatalf("TrimBefore() error = %v, want nil", err)
	}

	if trimmed != 1 {
		t.Fatalf("TrimBefore() = %d, want 1", trimmed)
	}

	got, err := logs.Search(ctx, SDKLogFilter{ServiceID: testServiceAPI})
	if err != nil {
		t.Fatalf("Search() error = %v, want nil", err)
	}

	if len(got) != 1 || got[0].Message != "new" {
		t.Fatalf("remaining = %+v, want the new row", got)
	}
}
