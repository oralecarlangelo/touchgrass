package dbmigrate

import (
	"testing"
	"time"

	"github.com/oralecarlangelo/touchgrass/internal/model"
)

// Migration names the status and adapter tests share.
const (
	testInit       = "001_init"
	testRetried    = "005_retried"
	testPrismaInit = "20261101100000_init"
)

var testStart = time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)

// at returns a time minutes after the fixture start.
func at(minutes int) time.Time {
	return testStart.Add(time.Duration(minutes) * time.Minute)
}

// finished returns a pointer to at(minutes).
func finished(minutes int) *time.Time {
	moment := at(minutes)

	return &moment
}

// states maps each migration name to its state.
func states(migrations []model.Migration) map[string]string {
	result := make(map[string]string, len(migrations))

	for _, migration := range migrations {
		result[migration.Name] = migration.State
	}

	return result
}

func TestCompareClassifiesEachMigration(t *testing.T) {
	t.Parallel()

	records := []Record{
		{Name: testInit, Checksum: "aaa", StartedAt: at(0), FinishedAt: finished(1), Steps: 1},
		{Name: "002_users", Checksum: "bbb", StartedAt: at(2), FinishedAt: finished(3), Steps: 1},
		{Name: "003_gone", Checksum: "ccc", StartedAt: at(4), FinishedAt: finished(5), Steps: 1},
		{Name: "004_broken", Checksum: "ddd", StartedAt: at(6), Logs: "column exists"},
		{Name: testRetried, Checksum: "eee", StartedAt: at(7), RolledBackAt: finished(8), Logs: "first try"},
		{Name: testRetried, Checksum: "eee", StartedAt: at(9), FinishedAt: finished(10), Steps: 2},
		{Name: "006_abandoned", Checksum: "fff", StartedAt: at(11), RolledBackAt: finished(12)},
		{Name: "007_rolled_then_shipped", Checksum: "ggg", StartedAt: at(13), RolledBackAt: finished(14)},
	}
	shipped := []Shipped{
		{Name: testInit, Checksum: "aaa"},
		{Name: "002_users", Checksum: "EDITED"},
		{Name: "004_broken", Checksum: "ddd"},
		{Name: testRetried, Checksum: "eee"},
		{Name: "007_rolled_then_shipped", Checksum: "ggg"},
		{Name: "008_new", Checksum: "hhh"},
	}

	got := Compare(records, shipped, true)

	want := map[string]string{
		testInit:                  model.MigrationApplied,
		"002_users":               model.MigrationModified,
		"003_gone":                model.MigrationMissing,
		"004_broken":              model.MigrationFailed,
		testRetried:               model.MigrationApplied,
		"006_abandoned":           model.MigrationRolledBack,
		"007_rolled_then_shipped": model.MigrationPending,
		"008_new":                 model.MigrationPending,
	}

	if len(got) != len(want) {
		t.Fatalf("Compare() returned %d migrations, want %d", len(got), len(want))
	}

	for name, state := range states(got) {
		if state != want[name] {
			t.Errorf("Compare() state of %s = %q, want %q", name, state, want[name])
		}
	}

	for index := 1; index < len(got); index++ {
		if got[index-1].Name >= got[index].Name {
			t.Errorf("Compare() order: %s before %s, want name order", got[index-1].Name, got[index].Name)
		}
	}

	counts := Count(got)
	wantCounts := model.MigrationCounts{Applied: 2, Pending: 2, Failed: 1, Modified: 1, Missing: 1, RolledBack: 1}

	if counts != wantCounts {
		t.Errorf("Count() = %+v, want %+v", counts, wantCounts)
	}
}

func TestCompareKeepsTheFactsOfTheActiveRow(t *testing.T) {
	t.Parallel()

	got := Compare([]Record{
		{Name: testRetried, Checksum: "old", StartedAt: at(7), RolledBackAt: finished(8), Logs: "first try"},
		{Name: testRetried, Checksum: "new", StartedAt: at(9), FinishedAt: finished(11), Steps: 2},
		{Name: "006_broken", Checksum: "bad", StartedAt: at(12), Logs: "relation exists"},
	}, nil, false)

	retried, broken := got[0], got[1]

	if retried.Attempts != 2 || retried.ChecksumApplied != "new" || retried.Steps != 2 || retried.Logs != "" {
		t.Errorf("retried = %+v, want 2 attempts and the second row's facts with no logs", retried)
	}

	if retried.DurationMS == nil || *retried.DurationMS != (2*time.Minute).Milliseconds() {
		t.Errorf("retried duration = %v, want 120000ms", retried.DurationMS)
	}

	if broken.State != model.MigrationFailed || broken.Logs != "relation exists" || broken.DurationMS != nil {
		t.Errorf("broken = %+v, want failed with its logs and no duration", broken)
	}
}

// Without the build's files nothing can be called pending, modified,
// or missing: the history is reported as it stands.
func TestCompareWithoutShippedFilesReportsHistoryOnly(t *testing.T) {
	t.Parallel()

	got := Compare([]Record{
		{Name: testInit, Checksum: "aaa", StartedAt: at(0), FinishedAt: finished(1)},
		{Name: "002_broken", Checksum: "bbb", StartedAt: at(2)},
		{Name: "003_undone", Checksum: "ccc", StartedAt: at(3), RolledBackAt: finished(4)},
	}, nil, false)

	want := map[string]string{
		testInit:     model.MigrationApplied,
		"002_broken": model.MigrationFailed,
		"003_undone": model.MigrationRolledBack,
	}

	for name, state := range states(got) {
		if state != want[name] {
			t.Errorf("state of %s = %q, want %q", name, state, want[name])
		}
	}
}

func TestCompareOfNothingIsEmptyNotNil(t *testing.T) {
	t.Parallel()

	if got := Compare(nil, nil, true); got == nil || len(got) != 0 {
		t.Errorf("Compare(nil, nil) = %v, want an empty non-nil slice", got)
	}
}

func TestVerdictReportsTheWorstCondition(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		counts    model.MigrationCounts
		historyOK bool
		want      string
	}{
		{name: "unreadable history wins over everything", counts: model.MigrationCounts{Failed: 1}, want: model.MigrationVerdictUnknown},
		{name: "failed", counts: model.MigrationCounts{Failed: 1, Modified: 1, Pending: 1}, historyOK: true, want: model.MigrationVerdictFailed},
		{name: "modified is drift", counts: model.MigrationCounts{Modified: 1, Pending: 1}, historyOK: true, want: model.MigrationVerdictDrift},
		{name: "missing is drift", counts: model.MigrationCounts{Missing: 1}, historyOK: true, want: model.MigrationVerdictDrift},
		{name: "pending", counts: model.MigrationCounts{Applied: 3, Pending: 1}, historyOK: true, want: model.MigrationVerdictPending},
		{name: "rolled back alone is in sync", counts: model.MigrationCounts{Applied: 3, RolledBack: 1}, historyOK: true, want: model.MigrationVerdictInSync},
		{name: "empty database and empty build", historyOK: true, want: model.MigrationVerdictInSync},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			if got := Verdict(test.counts, test.historyOK); got != test.want {
				t.Errorf("Verdict() = %q, want %q", got, test.want)
			}
		})
	}
}
