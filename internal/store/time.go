package store

import (
	"database/sql"
	"fmt"
	"time"
)

// parseTime parses an ISO8601 timestamp column.
func parseTime(raw, column string) (time.Time, error) {
	parsed, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}, fmt.Errorf("parsing %s %q: %w", column, raw, err)
	}

	return parsed, nil
}

// parseNullTime parses a nullable ISO8601 timestamp column.
func parseNullTime(raw sql.NullString, column string) (*time.Time, error) {
	if !raw.Valid {
		return nil, nil
	}

	parsed, err := parseTime(raw.String, column)
	if err != nil {
		return nil, err
	}

	return &parsed, nil
}

// formatTime renders a timestamp for storage.
func formatTime(moment time.Time) string {
	return moment.UTC().Format(time.RFC3339Nano)
}

// fixedTimeLayout renders UTC timestamps at full nanosecond precision so
// stored values compare lexicographically across mixed input precisions.
const fixedTimeLayout = "2006-01-02T15:04:05.000000000Z07:00"

// formatFixedTime renders a timestamp for ordered storage and bounds.
func formatFixedTime(moment time.Time) string {
	return moment.UTC().Format(fixedTimeLayout)
}

// formatNullTime renders a nullable timestamp for storage.
func formatNullTime(moment *time.Time) any {
	if moment == nil {
		return nil
	}

	return formatTime(*moment)
}
