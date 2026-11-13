package dbmigrate

import (
	"slices"

	"github.com/oralecarlangelo/touchgrass/internal/model"
)

// Compare merges a history with the migrations a build ships into one
// row per migration name, in name order (the order tools apply them).
//
// shippedKnown is false when the build's files could not be read. The
// history then stands alone: nothing is reported pending, modified, or
// missing, because none of those can be told without the files.
func Compare(records []Record, shipped []Shipped, shippedKnown bool) []model.Migration {
	byName := map[string][]Record{}

	for _, record := range records {
		byName[record.Name] = append(byName[record.Name], record)
	}

	shippedSums := make(map[string]string, len(shipped))

	for _, migration := range shipped {
		shippedSums[migration.Name] = migration.Checksum
	}

	names := make([]string, 0, len(byName)+len(shipped))

	for name := range byName {
		names = append(names, name)
	}

	for name := range shippedSums {
		if _, recorded := byName[name]; !recorded {
			names = append(names, name)
		}
	}

	slices.Sort(names)

	migrations := make([]model.Migration, 0, len(names))

	for _, name := range names {
		checksum, isShipped := shippedSums[name]

		migrations = append(migrations, classify(name, byName[name], file{
			checksum: checksum,
			shipped:  isShipped,
			known:    shippedKnown,
		}))
	}

	return migrations
}

// file is what the running build says about one migration name.
type file struct {
	checksum string
	shipped  bool
	known    bool
}

// classify decides the state of one migration from its history rows
// and its shipped file.
func classify(name string, rows []Record, shipped file) model.Migration {
	migration := model.Migration{
		Name:            name,
		ChecksumShipped: shipped.checksum,
		Attempts:        len(rows),
	}

	active, hasActive := activeRecord(rows)
	if !hasActive {
		return withoutActive(migration, rows, shipped)
	}

	fill(&migration, active)

	switch {
	case active.FinishedAt == nil:
		migration.State = model.MigrationFailed
		migration.Logs = active.Logs
	case !shipped.known:
		migration.State = model.MigrationApplied
	case !shipped.shipped:
		migration.State = model.MigrationMissing
	case active.Checksum != "" && shipped.checksum != "" && shipped.checksum != active.Checksum:
		// A side with no checksum (a tool or a setup that records
		// none) cannot show an edit, so it never reads as modified.
		migration.State = model.MigrationModified
	default:
		migration.State = model.MigrationApplied
	}

	return migration
}

// withoutActive classifies a name with no live history row: it is
// pending when the build ships it, and otherwise only a rolled-back
// record remains.
func withoutActive(migration model.Migration, rows []Record, shipped file) model.Migration {
	if shipped.shipped {
		migration.State = model.MigrationPending

		return migration
	}

	last := rows[len(rows)-1]
	fill(&migration, last)

	migration.State = model.MigrationRolledBack
	migration.Logs = last.Logs

	return migration
}

// activeRecord returns the latest row that was not rolled back. Rows
// arrive in start order, so the last one wins.
func activeRecord(rows []Record) (Record, bool) {
	for _, row := range slices.Backward(rows) {
		if row.RolledBackAt == nil {
			return row, true
		}
	}

	return Record{}, false
}

// fill copies a history row's facts onto a migration.
func fill(migration *model.Migration, record Record) {
	started := record.StartedAt

	migration.ChecksumApplied = record.Checksum
	migration.StartedAt = &started
	migration.FinishedAt = record.FinishedAt
	migration.RolledBackAt = record.RolledBackAt
	migration.Steps = record.Steps

	// Equal times mean the tool records one instant, not a duration.
	if record.FinishedAt != nil && !record.FinishedAt.Equal(record.StartedAt) {
		duration := record.FinishedAt.Sub(record.StartedAt).Milliseconds()
		migration.DurationMS = &duration
	}
}

// Count tallies migrations by state.
func Count(migrations []model.Migration) model.MigrationCounts {
	counts := model.MigrationCounts{}

	for _, migration := range migrations {
		switch migration.State {
		case model.MigrationApplied:
			counts.Applied++
		case model.MigrationPending:
			counts.Pending++
		case model.MigrationFailed:
			counts.Failed++
		case model.MigrationModified:
			counts.Modified++
		case model.MigrationMissing:
			counts.Missing++
		case model.MigrationRolledBack:
			counts.RolledBack++
		}
	}

	return counts
}

// Verdict is the one-word state of a service, worst condition first.
// A history that could not be read is unknown whatever the files say.
func Verdict(counts model.MigrationCounts, historyOK bool) string {
	switch {
	case !historyOK:
		return model.MigrationVerdictUnknown
	case counts.Failed > 0:
		return model.MigrationVerdictFailed
	case counts.Modified > 0 || counts.Missing > 0:
		return model.MigrationVerdictDrift
	case counts.Pending > 0:
		return model.MigrationVerdictPending
	default:
		return model.MigrationVerdictInSync
	}
}
