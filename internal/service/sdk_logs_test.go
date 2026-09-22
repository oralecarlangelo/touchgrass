package service

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/oralecarlangelo/touchgrass/internal/model"
	"github.com/oralecarlangelo/touchgrass/internal/store"
)

// testLogIngestor builds a LogIngestor on the seeded store.
func testLogIngestor(t *testing.T) (*LogIngestor, *store.DB) {
	t.Helper()

	db := openInventoryDB(t)

	ingestor := NewLogIngestor(LogIngestorConfig{
		Services: store.NewServiceStore(db),
		Keys:     store.NewKeyStore(db),
		SDKLogs:  store.NewSDKLogStore(db),
	})

	return ingestor, db
}

// mintLogKey creates an error-ingest key for log tests: log ingest
// accepts the same keys as error ingest.
func mintLogKey(t *testing.T, db *store.DB, serviceID string) string {
	t.Helper()

	created, err := NewIngestor(IngestorConfig{
		Services: store.NewServiceStore(db),
		Keys:     store.NewKeyStore(db),
	}).CreateKey(context.Background(), serviceID, 1)
	if err != nil {
		t.Fatalf("CreateKey() error = %v, want nil", err)
	}

	return created.Plaintext
}

// testSDKItem builds one valid warn item.
func testSDKItem() model.SDKLogItem {
	return model.SDKLogItem{
		Timestamp: 1788220800.5,
		Level:     model.SDKLogWarn,
		Body:      "disk almost full",
		TraceID:   "0123456789abcdef0123456789abcdef",
		SpanID:    "0123456789abcdef",
		Attributes: map[string]model.SDKLogAttribute{
			"disk.free": {Value: 1.5, Type: model.SDKAttrDouble},
			"retries":   {Value: 3.0, Type: model.SDKAttrInteger},
			"urgent":    {Value: true, Type: model.SDKAttrBoolean},
		},
	}
}

// testShortHex is a too-short correlation id for rejection cases.
const testShortHex = "abc"

func TestIngestLogs(t *testing.T) {
	t.Parallel()

	ingestor, db := testLogIngestor(t)
	ctx := context.Background()
	key := mintLogKey(t, db, testServiceAPI)

	accepted, err := ingestor.IngestLogs(ctx, key, model.SDKLogBatch{
		Release: "v1.2.3",
		Items:   []model.SDKLogItem{testSDKItem(), testSDKItem()},
	})
	if err != nil {
		t.Fatalf("IngestLogs() error = %v, want nil", err)
	}

	if accepted != 2 {
		t.Fatalf("IngestLogs() = %d, want 2", accepted)
	}

	got, err := store.NewSDKLogStore(db).Search(ctx, store.SDKLogFilter{ServiceID: testServiceAPI})
	if err != nil {
		t.Fatalf("Search() error = %v, want nil", err)
	}

	if len(got) != 2 {
		t.Fatalf("Search() = %d rows, want 2", len(got))
	}

	row := got[0]

	if row.Level != model.SDKLogWarn || row.Severity != 13 {
		t.Errorf("row = %+v, want warn with inferred severity 13", row)
	}

	if row.Message != "disk almost full" || row.Release != "v1.2.3" {
		t.Errorf("row = %+v, want body + batch release", row)
	}

	if row.TraceID != "0123456789abcdef0123456789abcdef" || row.SpanID != "0123456789abcdef" {
		t.Errorf("row = %+v, want trace correlation", row)
	}

	wantTs := time.Unix(1788220800, 500*1000*1000).UTC()

	if !row.Ts.Equal(wantTs) {
		t.Errorf("row ts = %v, want %v", row.Ts, wantTs)
	}

	if len(row.Attributes) != 3 {
		t.Errorf("row attributes = %+v, want 3 typed values", row.Attributes)
	}
}

func TestIngestLogsAuth(t *testing.T) {
	t.Parallel()

	ingestor, db := testLogIngestor(t)
	ctx := context.Background()
	batch := model.SDKLogBatch{Items: []model.SDKLogItem{testSDKItem()}}

	if _, err := ingestor.IngestLogs(ctx, testBogusValue, batch); !errors.Is(err, ErrUnauthorized) {
		t.Errorf("bogus key error = %v, want unauthorized", err)
	}

	revoked, err := NewIngestor(IngestorConfig{
		Services: store.NewServiceStore(db),
		Keys:     store.NewKeyStore(db),
	}).CreateKey(ctx, testServiceAPI, 1)
	if err != nil {
		t.Fatalf("CreateKey() error = %v, want nil", err)
	}

	if err := store.NewKeyStore(db).Revoke(ctx, revoked.Key.ID); err != nil {
		t.Fatalf("Revoke() error = %v, want nil", err)
	}

	if _, err := ingestor.IngestLogs(ctx, revoked.Plaintext, batch); !errors.Is(err, ErrUnauthorized) {
		t.Errorf("revoked key error = %v, want unauthorized", err)
	}
}

// sdkValidationCase is one all-or-nothing batch rejection case.
type sdkValidationCase struct {
	name  string
	batch model.SDKLogBatch
	index int
}

// invalidBatch wraps one mutated item in a batch.
func invalidBatch(apply func(*model.SDKLogItem)) model.SDKLogBatch {
	item := testSDKItem()
	apply(&item)

	return model.SDKLogBatch{Items: []model.SDKLogItem{item}}
}

// invalidSecondBatch pairs a valid item with a mutated second item.
func invalidSecondBatch(apply func(*model.SDKLogItem)) model.SDKLogBatch {
	second := testSDKItem()
	apply(&second)

	return model.SDKLogBatch{Items: []model.SDKLogItem{testSDKItem(), second}}
}

// invalidAttributeBatch builds a batch whose item carries one attribute.
func invalidAttributeBatch(key string, value any, attrType string) model.SDKLogBatch {
	return invalidBatch(func(item *model.SDKLogItem) {
		item.Attributes = map[string]model.SDKLogAttribute{
			key: {Value: value, Type: attrType},
		}
	})
}

// oversizeAttributesBatch builds a batch whose item carries 65 attributes.
func oversizeAttributesBatch() model.SDKLogBatch {
	item := testSDKItem()
	item.Attributes = map[string]model.SDKLogAttribute{}

	for n := range maxSDKLogAttributes + 1 {
		key := "attr-" + strconv.Itoa(n)
		item.Attributes[key] = model.SDKLogAttribute{Value: true, Type: model.SDKAttrBoolean}
	}

	return model.SDKLogBatch{Items: []model.SDKLogItem{item}}
}

func TestIngestLogsValidation(t *testing.T) {
	t.Parallel()

	tests := []sdkValidationCase{
		{
			name:  "missing timestamp",
			batch: invalidBatch(func(item *model.SDKLogItem) { item.Timestamp = 0 }),
			index: 0,
		},
		{
			name:  "negative timestamp",
			batch: invalidBatch(func(item *model.SDKLogItem) { item.Timestamp = -1 }),
			index: 0,
		},
		{
			name:  "millisecond timestamp rejected",
			batch: invalidBatch(func(item *model.SDKLogItem) { item.Timestamp = 1788220800500 }),
			index: 0,
		},
		{
			name:  "unknown level",
			batch: invalidBatch(func(item *model.SDKLogItem) { item.Level = testBogusValue }),
			index: 0,
		},
		{
			name:  "uppercase level rejected",
			batch: invalidBatch(func(item *model.SDKLogItem) { item.Level = "WARN" }),
			index: 0,
		},
		{
			name:  "empty body",
			batch: invalidBatch(func(item *model.SDKLogItem) { item.Body = "" }),
			index: 0,
		},
		{
			name: "oversize body",
			batch: invalidBatch(func(item *model.SDKLogItem) {
				item.Body = strings.Repeat("x", maxSDKLogBody+1)
			}),
			index: 0,
		},
		{
			name:  "severity zero",
			batch: invalidBatch(func(item *model.SDKLogItem) { item.SeverityNumber = new(0) }),
			index: 0,
		},
		{
			name:  "severity above range",
			batch: invalidBatch(func(item *model.SDKLogItem) { item.SeverityNumber = new(25) }),
			index: 0,
		},
		{
			name:  "short trace id",
			batch: invalidBatch(func(item *model.SDKLogItem) { item.TraceID = testShortHex }),
			index: 0,
		},
		{
			name: "uppercase trace id rejected",
			batch: invalidBatch(func(item *model.SDKLogItem) {
				item.TraceID = "0123456789ABCDEF0123456789ABCDEF"
			}),
			index: 0,
		},
		{
			name:  "short span id",
			batch: invalidBatch(func(item *model.SDKLogItem) { item.SpanID = testShortHex }),
			index: 0,
		},
		{
			name:  "too many attributes",
			batch: oversizeAttributesBatch(),
			index: 0,
		},
		{
			name: "oversize attribute key",
			batch: invalidAttributeBatch(
				strings.Repeat("k", maxSDKLogAttrKey+1), true, model.SDKAttrBoolean,
			),
			index: 0,
		},
		{
			name:  "unknown attribute type",
			batch: invalidAttributeBatch("when", "now", "datetime"),
			index: 0,
		},
		{
			name:  "string type with number value",
			batch: invalidAttributeBatch("route", 1.0, model.SDKAttrString),
			index: 0,
		},
		{
			name:  "integer type with fractional value",
			batch: invalidAttributeBatch("retries", 1.5, model.SDKAttrInteger),
			index: 0,
		},
		{
			name: "oversize attribute string",
			batch: invalidAttributeBatch(
				"blob", strings.Repeat("x", maxSDKLogAttrString+1), model.SDKAttrString,
			),
			index: 0,
		},
		{
			name:  "second item reports index 1",
			batch: invalidSecondBatch(func(item *model.SDKLogItem) { item.Level = testBogusValue }),
			index: 1,
		},
	}

	checkIngestLogsRejects(t, tests)
}

// checkIngestLogsRejects runs rejection cases: every batch fails with
// the indexed item error and stores nothing.
func checkIngestLogsRejects(t *testing.T, tests []sdkValidationCase) {
	t.Helper()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ingestor, db := testLogIngestor(t)
			ctx := context.Background()
			key := mintLogKey(t, db, testServiceAPI)

			accepted, err := ingestor.IngestLogs(ctx, key, tt.batch)
			if err == nil {
				t.Fatalf("IngestLogs() error = nil, want invalid input")
			}

			if !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("IngestLogs() error = %v, want invalid input", err)
			}

			var itemErr *SDKLogItemError

			if !errors.As(err, &itemErr) {
				t.Fatalf("IngestLogs() error = %T, want item error", err)
			}

			if itemErr.Index != tt.index {
				t.Errorf("item index = %d, want %d", itemErr.Index, tt.index)
			}

			if accepted != 0 {
				t.Errorf("accepted = %d, want 0", accepted)
			}

			stored, searchErr := store.NewSDKLogStore(db).Search(ctx, store.SDKLogFilter{ServiceID: testServiceAPI})
			if searchErr != nil {
				t.Fatalf("Search() error = %v, want nil", searchErr)
			}

			if len(stored) != 0 {
				t.Errorf("stored = %d rows, want 0 (all-or-nothing)", len(stored))
			}
		})
	}
}

func TestIngestLogsBatchBounds(t *testing.T) {
	t.Parallel()

	ingestor, db := testLogIngestor(t)
	ctx := context.Background()
	key := mintLogKey(t, db, testServiceAPI)

	if _, err := ingestor.IngestLogs(ctx, key, model.SDKLogBatch{}); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("empty batch error = %v, want invalid input", err)
	}

	oversize := make([]model.SDKLogItem, 0, maxSDKLogItems+1)
	for range maxSDKLogItems + 1 {
		oversize = append(oversize, testSDKItem())
	}

	if _, err := ingestor.IngestLogs(ctx, key, model.SDKLogBatch{Items: oversize}); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("oversize batch error = %v, want invalid input", err)
	}
}

func TestIngestLogsSeverityInference(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		level    string
		expected int
	}{
		{name: "trace", level: model.SDKLogTrace, expected: 1},
		{name: "debug", level: model.SDKLogDebug, expected: 5},
		{name: "info", level: model.SDKLogInfo, expected: 9},
		{name: "warn", level: model.SDKLogWarn, expected: 13},
		{name: "error", level: model.SDKLogError, expected: 17},
		{name: "fatal", level: model.SDKLogFatal, expected: 21},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ingestor, db := testLogIngestor(t)
			ctx := context.Background()
			key := mintLogKey(t, db, testServiceAPI)

			item := testSDKItem()
			item.Level = tt.level
			item.SeverityNumber = nil
			item.TraceID = ""
			item.SpanID = ""
			item.Attributes = nil

			if _, err := ingestor.IngestLogs(ctx, key, model.SDKLogBatch{Items: []model.SDKLogItem{item}}); err != nil {
				t.Fatalf("IngestLogs() error = %v, want nil", err)
			}

			got, err := store.NewSDKLogStore(db).Search(ctx, store.SDKLogFilter{ServiceID: testServiceAPI})
			if err != nil {
				t.Fatalf("Search() error = %v, want nil", err)
			}

			if len(got) != 1 || got[0].Severity != tt.expected {
				t.Fatalf("severity = %+v, want %d", got, tt.expected)
			}
		})
	}
}

func TestIngestLogsExplicitSeverity(t *testing.T) {
	t.Parallel()

	ingestor, db := testLogIngestor(t)
	ctx := context.Background()
	key := mintLogKey(t, db, testServiceAPI)

	item := testSDKItem()
	item.Level = model.SDKLogInfo
	item.SeverityNumber = new(12)

	if _, err := ingestor.IngestLogs(ctx, key, model.SDKLogBatch{Items: []model.SDKLogItem{item}}); err != nil {
		t.Fatalf("IngestLogs() error = %v, want nil", err)
	}

	got, err := store.NewSDKLogStore(db).Search(ctx, store.SDKLogFilter{ServiceID: testServiceAPI})
	if err != nil {
		t.Fatalf("Search() error = %v, want nil", err)
	}

	if len(got) != 1 || got[0].Severity != 12 {
		t.Fatalf("severity = %+v, want explicit 12", got)
	}
}

func TestIngestLogsTruncatesRelease(t *testing.T) {
	t.Parallel()

	ingestor, db := testLogIngestor(t)
	ctx := context.Background()
	key := mintLogKey(t, db, testServiceAPI)

	batch := model.SDKLogBatch{
		Release: strings.Repeat("r", maxSDKLogRelease+10),
		Items:   []model.SDKLogItem{testSDKItem()},
	}

	if _, err := ingestor.IngestLogs(ctx, key, batch); err != nil {
		t.Fatalf("IngestLogs() error = %v, want nil", err)
	}

	got, err := store.NewSDKLogStore(db).Search(ctx, store.SDKLogFilter{ServiceID: testServiceAPI})
	if err != nil {
		t.Fatalf("Search() error = %v, want nil", err)
	}

	if len(got) != 1 || len(got[0].Release) != maxSDKLogRelease {
		t.Fatalf("release = %+v, want truncated to %d", got, maxSDKLogRelease)
	}
}

// TestTrimSDKLogs proves the sampler trim sweep enforces sdk-log
// retention: a stale row disappears after one trim cycle.
func TestTrimSDKLogs(t *testing.T) {
	t.Parallel()

	sampler, db := testSampler(t, stubStatsLister{})
	sampler.retention.SDKLogs = time.Nanosecond
	ctx := context.Background()

	stale := time.Now().Add(-time.Hour)

	if _, err := store.NewSDKLogStore(db).InsertBatch(ctx, []model.SDKLog{{
		ServiceID: testServiceAPI, Ts: stale, Level: model.SDKLogInfo,
		Severity: 9, Message: "stale", Attributes: map[string]model.SDKLogAttribute{},
	}}); err != nil {
		t.Fatalf("InsertBatch() error = %v, want nil", err)
	}

	sampler.trim(ctx)

	got, err := store.NewSDKLogStore(db).Search(ctx, store.SDKLogFilter{ServiceID: testServiceAPI})
	if err != nil {
		t.Fatalf("Search() error = %v, want nil", err)
	}

	if len(got) != 0 {
		t.Errorf("Search() = %d rows, want 0 after retention trim", len(got))
	}
}

// seedSearchLogs stores one traced warn row plus one fatal row.
func seedSearchLogs(t *testing.T) *LogIngestor {
	t.Helper()

	ingestor, db := testLogIngestor(t)
	key := mintLogKey(t, db, testServiceAPI)

	traced := testSDKItem()
	fatal := testSDKItem()
	fatal.Level = model.SDKLogFatal
	fatal.Body = "process crashed"
	fatal.TraceID = ""
	fatal.SpanID = ""

	if _, err := ingestor.IngestLogs(context.Background(), key, model.SDKLogBatch{
		Items: []model.SDKLogItem{traced, fatal},
	}); err != nil {
		t.Fatalf("IngestLogs() error = %v, want nil", err)
	}

	return ingestor
}

func TestSearchLogs(t *testing.T) {
	t.Parallel()

	ingestor := seedSearchLogs(t)
	ctx := context.Background()

	got, err := ingestor.SearchLogs(ctx, SDKLogSearch{ServiceID: testServiceAPI})
	if err != nil {
		t.Fatalf("SearchLogs() error = %v, want nil", err)
	}

	if len(got) != 2 || got[0].Message != "process crashed" {
		t.Fatalf("SearchLogs() = %+v, want newest first", got)
	}

	got, err = ingestor.SearchLogs(ctx, SDKLogSearch{ServiceID: testServiceAPI, Level: "fatal"})
	if err != nil {
		t.Fatalf("SearchLogs(fatal) error = %v, want nil", err)
	}

	if len(got) != 1 || got[0].Level != model.SDKLogFatal {
		t.Fatalf("SearchLogs(fatal) = %+v, want the fatal row", got)
	}

	got, err = ingestor.SearchLogs(ctx, SDKLogSearch{
		ServiceID: testServiceAPI,
		TraceID:   "0123456789abcdef0123456789abcdef",
	})
	if err != nil {
		t.Fatalf("SearchLogs(trace) error = %v, want nil", err)
	}

	if len(got) != 1 || got[0].Message != "disk almost full" {
		t.Fatalf("SearchLogs(trace) = %+v, want the traced row", got)
	}
}

func TestSearchLogsRejects(t *testing.T) {
	t.Parallel()

	ingestor := seedSearchLogs(t)
	ctx := context.Background()

	if _, err := ingestor.SearchLogs(ctx, SDKLogSearch{
		ServiceID: testServiceAPI,
		TraceID:   "short",
	}); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("short trace error = %v, want invalid input", err)
	}

	search := SDKLogSearch{ServiceID: testServiceAPI, Level: testBogusValue}

	if _, err := ingestor.SearchLogs(ctx, search); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("bogus level error = %v, want invalid input", err)
	}

	if _, err := ingestor.SearchLogs(ctx, SDKLogSearch{ServiceID: testUnknownServiceID}); err == nil {
		t.Error("unknown service error = nil, want not found")
	}
}
