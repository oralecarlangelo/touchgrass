package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/oralecarlangelo/touchgrass/internal/model"
)

// testLogLine builds one stdout line at an offset from a base time.
func testLogLine(service, container, line string, at time.Time) model.LogLine {
	return model.LogLine{
		ServiceID: service, Container: container, Stream: model.LogStdout,
		Line: line, Ts: at,
	}
}

// testBoomLine is the corpus line every search case expects first.
const testBoomLine = "boom handler failed"

// seedSearchLines stores the shared search corpus: two api lines plus one fe line.
func seedSearchLines(t *testing.T, logs *LogStore, base time.Time) {
	t.Helper()

	if _, err := logs.InsertBatch(t.Context(), []model.LogLine{
		testLogLine(testServiceAPI, "api-blue", "listening on :4101", base),
		testLogLine(testServiceAPI, "api-blue", testBoomLine, base.Add(time.Second)),
		testLogLine("tn-fe", "fe-1", "boom render failed", base.Add(2*time.Second)),
	}); err != nil {
		t.Fatalf("InsertBatch() error = %v, want nil", err)
	}
}

func TestLogStoreInsert(t *testing.T) {
	t.Parallel()

	db := openTestDB(t)
	ctx := context.Background()
	logs := NewLogStore(db)
	base := time.Date(2026, 9, 21, 10, 0, 0, 0, time.UTC)

	inserted, err := logs.InsertBatch(ctx, []model.LogLine{
		testLogLine(testServiceAPI, "api-blue", "listening on :4101", base),
		testLogLine(testServiceAPI, "api-blue", testBoomLine, base.Add(time.Second)),
		testLogLine("tn-fe", "fe-1", "boom render failed", base.Add(2*time.Second)),
	})
	if err != nil {
		t.Fatalf("InsertBatch() error = %v, want nil", err)
	}

	if inserted != 3 {
		t.Fatalf("InsertBatch() = %d, want 3", inserted)
	}

	none, err := logs.InsertBatch(ctx, nil)
	if err != nil || none != 0 {
		t.Fatalf("InsertBatch(nil) = (%d, %v), want (0, nil)", none, err)
	}
}

func TestLogStoreSearch(t *testing.T) {
	t.Parallel()

	db := openTestDB(t)
	ctx := context.Background()
	logs := NewLogStore(db)
	base := time.Date(2026, 9, 21, 10, 0, 0, 0, time.UTC)

	seedSearchLines(t, logs, base)

	pastWindow := base.Add(90 * time.Second)

	cases := []struct {
		name      string
		query     string
		since     *time.Time
		wantCount int
		wantFirst string
	}{
		{name: "scoped term", query: "boom", wantCount: 1, wantFirst: testBoomLine},
		{name: "multiword and", query: "boom handler", wantCount: 1, wantFirst: testBoomLine},
		{name: "quoted terms", query: `"boom" (failed)`, wantCount: 1, wantFirst: testBoomLine},
		{name: "unfiltered newest first", query: "", wantCount: 2, wantFirst: testBoomLine},
		{name: "window excludes all", query: "", since: &pastWindow, wantCount: 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			found, err := logs.Search(ctx, LogFilter{
				ServiceID: testServiceAPI, Query: tc.query, Since: tc.since, Limit: 10,
			})
			if err != nil {
				t.Fatalf("Search() error = %v, want nil", err)
			}

			if len(found) != tc.wantCount {
				t.Fatalf("Search() = %d hits, want %d", len(found), tc.wantCount)
			}

			if tc.wantCount > 0 && found[0].Line != tc.wantFirst {
				t.Errorf("Search() first = %q, want %q", found[0].Line, tc.wantFirst)
			}
		})
	}
}

// testFilterLines is the mixed-stream corpus: ok, stderr failed, ok.
var testFilterLines = []string{"one ok", "two failed", "three ok"}

// seedFilterLines stores the mixed-stream corpus one second apart.
func seedFilterLines(t *testing.T, logs *LogStore, base time.Time) {
	t.Helper()

	lines := make([]model.LogLine, 0, len(testFilterLines))

	for i, line := range testFilterLines {
		stream := model.LogStdout
		if i == 1 {
			stream = model.LogStderr
		}

		lines = append(lines, model.LogLine{
			ServiceID: testServiceAPI, Container: "c", Stream: stream,
			Line: line, Ts: base.Add(time.Duration(i) * time.Second),
		})
	}

	if _, err := logs.InsertBatch(context.Background(), lines); err != nil {
		t.Fatalf("InsertBatch() error = %v, want nil", err)
	}
}

func TestLogStoreSearchStream(t *testing.T) {
	t.Parallel()

	db := openTestDB(t)
	ctx := context.Background()
	logs := NewLogStore(db)
	base := time.Date(2026, 9, 21, 10, 0, 0, 0, time.UTC)

	seedFilterLines(t, logs, base)

	stderr, err := logs.Search(ctx, LogFilter{ServiceID: testServiceAPI, Stream: model.LogStderr, Limit: 10})
	if err != nil {
		t.Fatalf("Search() error = %v, want nil", err)
	}

	if len(stderr) != 1 || stderr[0].Line != testFilterLines[1] {
		t.Fatalf("stream filter = %+v, want the stderr line", stderr)
	}
}

func TestLogStoreSearchBeforeID(t *testing.T) {
	t.Parallel()

	db := openTestDB(t)
	ctx := context.Background()
	logs := NewLogStore(db)
	base := time.Date(2026, 9, 21, 10, 0, 0, 0, time.UTC)

	seedFilterLines(t, logs, base)

	page, err := logs.Search(ctx, LogFilter{ServiceID: testServiceAPI, Limit: 2})
	if err != nil {
		t.Fatalf("Search() error = %v, want nil", err)
	}

	if len(page) != 2 || page[0].Line != testFilterLines[2] || page[1].Line != testFilterLines[1] {
		t.Fatalf("first page = %+v, want newest two", page)
	}

	older, err := logs.Search(ctx, LogFilter{ServiceID: testServiceAPI, BeforeID: page[1].ID, Limit: 2})
	if err != nil {
		t.Fatalf("Search() error = %v, want nil", err)
	}

	if len(older) != 1 || older[0].Line != testFilterLines[0] {
		t.Fatalf("second page = %+v, want the oldest line", older)
	}
}

func TestLogStoreSearchFTSStream(t *testing.T) {
	t.Parallel()

	db := openTestDB(t)
	ctx := context.Background()
	logs := NewLogStore(db)
	base := time.Date(2026, 9, 21, 10, 0, 0, 0, time.UTC)

	seedFilterLines(t, logs, base)

	matched, err := logs.Search(ctx, LogFilter{
		ServiceID: testServiceAPI, Query: "failed", Stream: model.LogStderr, Limit: 10,
	})
	if err != nil {
		t.Fatalf("Search() error = %v, want nil", err)
	}

	if len(matched) != 1 || matched[0].Line != testFilterLines[1] {
		t.Fatalf("fts + stream = %+v, want the stderr match", matched)
	}
}

func TestLogStoreContext(t *testing.T) {
	t.Parallel()

	db := openTestDB(t)
	ctx := context.Background()
	logs := NewLogStore(db)
	base := time.Date(2026, 9, 21, 10, 0, 0, 0, time.UTC)

	lines := []model.LogLine{}
	for i := range 5 {
		lines = append(lines, testLogLine(testServiceAPI, "api-blue", "line", base.Add(time.Duration(i)*time.Second)))
	}

	if _, err := logs.InsertBatch(ctx, lines); err != nil {
		t.Fatalf("InsertBatch() error = %v, want nil", err)
	}

	all, err := logs.Search(ctx, LogFilter{ServiceID: testServiceAPI, Limit: 10})
	if err != nil {
		t.Fatalf("Search() error = %v, want nil", err)
	}

	// all[2] is the middle line (newest first).
	got, err := logs.Context(ctx, all[2].ID, 2, 1)
	if err != nil {
		t.Fatalf("Context() error = %v, want nil", err)
	}

	if got.Anchor.ID != all[2].ID {
		t.Errorf("Context() anchor = %d, want %d", got.Anchor.ID, all[2].ID)
	}

	if len(got.Before) != 2 || got.Before[0].ID > got.Before[1].ID {
		t.Errorf("Context() before = %+v, want 2 oldest-first", got.Before)
	}

	if len(got.After) != 1 || got.After[0].ID != all[1].ID {
		t.Errorf("Context() after = %+v, want the newer line", got.After)
	}

	if _, err := logs.Context(ctx, 9999, 2, 2); !errors.Is(err, ErrLogLineNotFound) {
		t.Errorf("Context() error = %v, want ErrLogLineNotFound", err)
	}
}

func TestLogStoreTrim(t *testing.T) {
	t.Parallel()

	db := openTestDB(t)
	ctx := context.Background()
	logs := NewLogStore(db)
	base := time.Date(2026, 9, 21, 10, 0, 0, 0, time.UTC)

	lines := []model.LogLine{}
	for i := range 5 {
		lines = append(lines, testLogLine(testServiceAPI, "api-blue", "old log", base.Add(time.Duration(i)*time.Second)))
	}

	if _, err := logs.InsertBatch(ctx, lines); err != nil {
		t.Fatalf("InsertBatch() error = %v, want nil", err)
	}

	trimmed, err := logs.TrimBeyondCap(ctx, testServiceAPI, 3)
	if err != nil {
		t.Fatalf("TrimBeyondCap() error = %v, want nil", err)
	}

	if trimmed != 2 {
		t.Errorf("TrimBeyondCap() = %d, want 2", trimmed)
	}

	count, err := logs.CountByService(ctx, testServiceAPI)
	if err != nil {
		t.Fatalf("CountByService() error = %v, want nil", err)
	}

	if count != 3 {
		t.Errorf("CountByService() = %d, want 3", count)
	}

	// The FTS index follows deletes: trimmed rows stop matching.
	found, err := logs.Search(ctx, LogFilter{ServiceID: testServiceAPI, Query: "old log", Limit: 10})
	if err != nil {
		t.Fatalf("Search() error = %v, want nil", err)
	}

	if len(found) != 3 {
		t.Errorf("Search() = %d hits, want 3 surviving rows", len(found))
	}

	trimmed, err = logs.TrimBefore(ctx, base.Add(time.Hour))
	if err != nil {
		t.Fatalf("TrimBefore() error = %v, want nil", err)
	}

	if trimmed != 3 {
		t.Errorf("TrimBefore() = %d, want 3", trimmed)
	}
}
