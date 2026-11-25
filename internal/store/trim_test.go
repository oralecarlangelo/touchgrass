package store

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/oralecarlangelo/touchgrass/internal/model"
	"github.com/oralecarlangelo/touchgrass/internal/testfixture"
)

// openGenericTestDB returns a migrated in-memory database holding the generic
// service fixture.
func openGenericTestDB(t *testing.T) *DB {
	t.Helper()

	db, err := Open(":memory:")
	if err != nil {
		t.Fatalf("Open() error = %v, want nil", err)
	}

	t.Cleanup(func() { _ = db.Close() })

	if _, err := db.MigrateUp(context.Background()); err != nil {
		t.Fatalf("MigrateUp() error = %v, want nil", err)
	}

	services := NewServiceStore(db)
	for _, service := range testfixture.GenericServices() {
		if err := services.Create(context.Background(), service); err != nil {
			t.Fatalf("Create(%s) error = %v, want nil", service.ID, err)
		}
	}

	return db
}

// seedLogLines stores count lines for a service, one second apart from base.
func seedLogLines(t *testing.T, db *DB, serviceID string, count int, base time.Time) {
	t.Helper()

	lines := make([]model.LogLine, 0, count)
	for i := range count {
		lines = append(lines, model.LogLine{
			ServiceID: serviceID, Container: "worker", Stream: model.LogStdout,
			Line: fmt.Sprintf("line %d", i), Ts: base.Add(time.Duration(i) * time.Second),
		})
	}

	if _, err := NewLogStore(db).InsertBatch(t.Context(), lines); err != nil {
		t.Fatalf("InsertBatch() error = %v, want nil", err)
	}
}

// seedMetrics stores count samples for the granted generic service, one
// second apart from base.
func seedMetrics(t *testing.T, db *DB, count int, base time.Time) {
	t.Helper()

	metrics := make([]model.Metric, 0, count)
	for i := range count {
		metrics = append(metrics, model.Metric{
			ServiceID: testfixture.GenericGranted, ContainerName: "worker",
			SampledAt: base.Add(time.Duration(i) * time.Second),
		})
	}

	if err := NewMetricStore(db).InsertBatch(t.Context(), metrics); err != nil {
		t.Fatalf("InsertBatch() error = %v, want nil", err)
	}
}

// countRows counts a table's rows matching an optional condition.
func countRows(t *testing.T, db *DB, table, condition string, args ...any) int64 {
	t.Helper()

	query := "SELECT COUNT(*) FROM " + table
	if condition != "" {
		query += " WHERE " + condition
	}

	var count int64
	if err := db.read.QueryRowContext(t.Context(), query, args...).Scan(&count); err != nil {
		t.Fatalf("counting %s: %v", table, err)
	}

	return count
}

func TestLogStoreTrimBeyondCapInChunks(t *testing.T) {
	t.Parallel()

	const (
		seeded     = 2*trimChunkRows + 250
		otherLines = 30
	)

	tests := []struct {
		name        string
		maximum     int
		wantTrimmed int64
		wantLeft    int64
	}{
		{name: "over cap across several chunks", maximum: 100, wantTrimmed: seeded - 100, wantLeft: 100},
		{name: "exactly one chunk over", maximum: seeded - trimChunkRows, wantTrimmed: trimChunkRows, wantLeft: seeded - trimChunkRows},
		{name: "at cap", maximum: seeded, wantTrimmed: 0, wantLeft: seeded},
		{name: "under cap", maximum: seeded + 1, wantTrimmed: 0, wantLeft: seeded},
		{name: "zero cap removes everything", maximum: 0, wantTrimmed: seeded, wantLeft: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			db := openGenericTestDB(t)
			base := time.Now().Add(-time.Hour)
			seedLogLines(t, db, testfixture.GenericGranted, seeded, base)
			seedLogLines(t, db, testfixture.GenericOther, otherLines, base)

			var newestKept int64
			if tt.wantLeft > 0 {
				if err := db.read.QueryRowContext(t.Context(),
					"SELECT id FROM log_lines WHERE service_id = ? ORDER BY id DESC LIMIT 1 OFFSET ?",
					testfixture.GenericGranted, tt.wantLeft-1,
				).Scan(&newestKept); err != nil {
					t.Fatalf("finding expected boundary: %v", err)
				}
			}

			trimmed, err := NewLogStore(db).TrimBeyondCap(t.Context(), testfixture.GenericGranted, tt.maximum)
			if err != nil {
				t.Fatalf("TrimBeyondCap() error = %v, want nil", err)
			}

			if trimmed != tt.wantTrimmed {
				t.Errorf("TrimBeyondCap() = %d, want %d", trimmed, tt.wantTrimmed)
			}

			if left := countRows(t, db, "log_lines", "service_id = ?", testfixture.GenericGranted); left != tt.wantLeft {
				t.Errorf("rows left = %d, want %d", left, tt.wantLeft)
			}

			if older := countRows(t, db, "log_lines", "service_id = ? AND id < ?",
				testfixture.GenericGranted, newestKept); older != 0 {
				t.Errorf("rows older than the kept window = %d, want 0 (the newest rows are kept)", older)
			}

			if other := countRows(t, db, "log_lines", "service_id = ?", testfixture.GenericOther); other != otherLines {
				t.Errorf("other service rows = %d, want %d untouched", other, otherLines)
			}

			if indexed := countRows(t, db, "log_lines_fts", "log_lines_fts MATCH 'line'"); indexed != tt.wantLeft+otherLines {
				t.Errorf("search index rows = %d, want %d", indexed, tt.wantLeft+otherLines)
			}
		})
	}
}

func TestOccurrenceStoreTrimBeyondCapKeepsOtherServices(t *testing.T) {
	t.Parallel()

	db := openGenericTestDB(t)
	occurrences := NewOccurrenceStore(db)

	for _, serviceID := range []string{testfixture.GenericGranted, testfixture.GenericOther} {
		for i := range 12 {
			if _, err := occurrences.Insert(t.Context(), model.Occurrence{
				ServiceID: serviceID, Type: "Error", Message: fmt.Sprintf("failure %d", i),
			}); err != nil {
				t.Fatalf("Insert() error = %v, want nil", err)
			}
		}
	}

	trimmed, err := occurrences.TrimBeyondCap(t.Context(), testfixture.GenericGranted, 5)
	if err != nil {
		t.Fatalf("TrimBeyondCap() error = %v, want nil", err)
	}

	if trimmed != 7 {
		t.Errorf("TrimBeyondCap() = %d, want 7", trimmed)
	}

	if left := countRows(t, db, "occurrences", "service_id = ?", testfixture.GenericOther); left != 12 {
		t.Errorf("other service occurrences = %d, want 12 untouched", left)
	}
}

func TestMetricStoreTrimBeforeInChunks(t *testing.T) {
	t.Parallel()

	const (
		expired = 2*trimChunkRows + 10
		fresh   = 40
	)

	db := openGenericTestDB(t)
	cutoff := time.Now().UTC().Truncate(time.Second)
	seedMetrics(t, db, expired, cutoff.Add(-2*time.Hour))
	seedMetrics(t, db, fresh, cutoff)

	trimmed, err := NewMetricStore(db).TrimBefore(t.Context(), cutoff)
	if err != nil {
		t.Fatalf("TrimBefore() error = %v, want nil", err)
	}

	if trimmed != expired {
		t.Errorf("TrimBefore() = %d, want %d", trimmed, expired)
	}

	if left := countRows(t, db, "metrics", ""); left != fresh {
		t.Errorf("rows left = %d, want %d fresh rows", left, fresh)
	}

	if stale := countRows(t, db, "metrics", "sampled_at < ?", formatTime(cutoff)); stale != 0 {
		t.Errorf("rows older than cutoff = %d, want 0", stale)
	}
}

func TestTrimKeepsProgressAndResumes(t *testing.T) {
	t.Parallel()

	const seeded = 3 * trimChunkRows

	db := openGenericTestDB(t)
	cutoff := time.Now().UTC().Truncate(time.Second)
	seedMetrics(t, db, seeded, cutoff.Add(-2*time.Hour))

	// A row in the second chunk refuses to be deleted, which stops the trim
	// after the first chunk committed.
	var blocker int64
	if err := db.read.QueryRowContext(t.Context(),
		"SELECT id FROM metrics ORDER BY sampled_at LIMIT 1 OFFSET ?", trimChunkRows+5,
	).Scan(&blocker); err != nil {
		t.Fatalf("finding blocker row: %v", err)
	}

	if _, err := db.sql.ExecContext(t.Context(), fmt.Sprintf(
		`CREATE TRIGGER block_trim BEFORE DELETE ON metrics WHEN old.id = %d
		 BEGIN SELECT RAISE(ABORT, 'blocked'); END`, blocker)); err != nil {
		t.Fatalf("creating blocker trigger: %v", err)
	}

	metrics := NewMetricStore(db)

	trimmed, err := metrics.TrimBefore(t.Context(), cutoff)
	if err == nil {
		t.Fatal("TrimBefore() error = nil, want the blocked chunk reported")
	}

	if trimmed != trimChunkRows {
		t.Errorf("TrimBefore() = %d before the failure, want the first chunk (%d)", trimmed, trimChunkRows)
	}

	if left := countRows(t, db, "metrics", ""); left != seeded-trimChunkRows {
		t.Errorf("rows left after the failed pass = %d, want %d (first chunk stays deleted)", left, seeded-trimChunkRows)
	}

	if _, err := db.sql.ExecContext(t.Context(), "DROP TRIGGER block_trim"); err != nil {
		t.Fatalf("dropping blocker trigger: %v", err)
	}

	trimmed, err = metrics.TrimBefore(t.Context(), cutoff)
	if err != nil {
		t.Fatalf("resumed TrimBefore() error = %v, want nil", err)
	}

	if trimmed != seeded-trimChunkRows {
		t.Errorf("resumed TrimBefore() = %d, want the remaining %d", trimmed, seeded-trimChunkRows)
	}

	if left := countRows(t, db, "metrics", ""); left != 0 {
		t.Errorf("rows left after resuming = %d, want 0", left)
	}
}

func TestTrimStopsOnCancelledContext(t *testing.T) {
	t.Parallel()

	db := openGenericTestDB(t)
	cutoff := time.Now().UTC().Truncate(time.Second)
	seedMetrics(t, db, 20, cutoff.Add(-time.Hour))

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if _, err := NewMetricStore(db).TrimBefore(ctx, cutoff); err == nil {
		t.Fatal("TrimBefore() on a cancelled context error = nil, want an error")
	}

	if left := countRows(t, db, "metrics", ""); left != 20 {
		t.Errorf("rows left = %d, want 20 untouched", left)
	}
}

func TestFleetStoreInsertContainers(t *testing.T) {
	t.Parallel()

	db := openGenericTestDB(t)
	fleet := NewFleetStore(db)
	sampledAt := time.Now().UTC().Truncate(time.Second)

	if err := fleet.InsertContainers(t.Context(), nil); err != nil {
		t.Fatalf("InsertContainers(nil) error = %v, want nil", err)
	}

	samples := []model.ContainerSample{
		{SampledAt: sampledAt, ContainerName: "billing-worker-1", ServiceID: testfixture.GenericGranted, Managed: true, State: testFleetState},
		{SampledAt: sampledAt, ContainerName: "docs-web-1", ServiceID: testfixture.GenericOther, Managed: true, State: testFleetState},
		{SampledAt: sampledAt, ContainerName: "sidecar-1", State: testFleetState},
	}

	if err := fleet.InsertContainers(t.Context(), samples); err != nil {
		t.Fatalf("InsertContainers() error = %v, want nil", err)
	}

	latest, err := fleet.LatestContainers(t.Context())
	if err != nil {
		t.Fatalf("LatestContainers() error = %v, want nil", err)
	}

	if len(latest) != len(samples) {
		t.Fatalf("LatestContainers() returned %d containers, want %d", len(latest), len(samples))
	}
}
