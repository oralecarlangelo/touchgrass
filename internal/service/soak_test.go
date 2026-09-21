package service

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/oralecarlangelo/touchgrass/internal/docker"
	"github.com/oralecarlangelo/touchgrass/internal/store"
)

// soak caps stay small so the proof runs in milliseconds.
const (
	soakOccurrenceCap = 50
	soakLogCap        = 50
	soakErrorVolume   = 150
	soakLogVolume     = 1500
	soakLongLines     = 5
)

// TestSoakHoldsCaps floods errors and logs past their caps and proves
// stored totals stay under cap while every loss stays queryable.
func TestSoakHoldsCaps(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	ingestor, db := testIngestor(t, soakOccurrenceCap)
	plaintext := mintKey(t, ingestor, testServiceAPI, 1)

	for i := range soakErrorVolume {
		report := testReport()
		report.Message = fmt.Sprintf("soak failure %d", i)

		if _, err := ingestor.Ingest(ctx, plaintext, report); err != nil {
			t.Fatalf("Ingest() error = %v, want nil", err)
		}
	}

	errors, err := store.NewOccurrenceStore(db).CountByService(ctx, testServiceAPI)
	if err != nil {
		t.Fatalf("CountByService() error = %v, want nil", err)
	}

	if errors != soakOccurrenceCap {
		t.Errorf("occurrences = %d, want cap %d", errors, soakOccurrenceCap)
	}

	base := time.Date(2026, 9, 21, 10, 0, 0, 0, time.UTC)
	lines := scriptLines(base, soakLogVolume)

	for i := range soakLongLines {
		lines[i].Message = strings.Repeat("x", maxLogLineBytes+10)
	}

	lister := &fakeLogLister{
		containers: []docker.Container{testLogContainer()},
		lines:      map[string][]docker.LogLine{testBlueContainerID: lines},
	}
	// The rogue source ignores tail, so the poll cap drops the overflow.
	collector, logDB := testLogCollector(t, &uncappedLogLister{inner: lister}, soakLogCap)

	if err := collector.collect(ctx); err != nil {
		t.Fatalf("collect() error = %v, want nil", err)
	}

	logs, err := store.NewLogStore(logDB).CountByService(ctx, testServiceAPI)
	if err != nil {
		t.Fatalf("CountByService() error = %v, want nil", err)
	}

	if logs != soakLogCap {
		t.Errorf("log lines = %d, want cap %d", logs, soakLogCap)
	}

	stats, err := collector.Stats(ctx, testServiceAPI)
	if err != nil {
		t.Fatalf("Stats() error = %v, want nil", err)
	}

	if stats.Drops != soakLogVolume-maxLinesPerPoll {
		t.Errorf("drops = %d, want %d", stats.Drops, soakLogVolume-maxLinesPerPoll)
	}

	if stats.Truncations != soakLongLines {
		t.Errorf("truncations = %d, want %d", stats.Truncations, soakLongLines)
	}

	if total := errors + logs; total != soakOccurrenceCap+soakLogCap {
		t.Errorf("stored total = %d, want %d under caps", total, soakOccurrenceCap+soakLogCap)
	}
}
