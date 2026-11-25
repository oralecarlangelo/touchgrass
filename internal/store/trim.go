package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// trimChunkRows bounds one retention DELETE. Each chunk is its own short
// transaction, so the write connection is handed to any waiting writer
// between chunks instead of being held for the whole trim.
const trimChunkRows = 500

// Tables and timestamp columns the trims name.
const (
	tableDeployProbes  = "deploy_probes"
	tableDeploys       = "deploys"
	tableLogLines      = "log_lines"
	tableMetrics       = "metrics"
	tableNotifications = "notifications"
	tableOccurrences   = "occurrences"
	tableSDKLogs       = "sdk_logs"
	columnCreatedAt    = "created_at"
	columnLastSeen     = "last_seen"
	columnSampledAt    = "sampled_at"
)

// ageTrim names one table trimmed by a timestamp column. what labels errors
// and defaults to the table name.
type ageTrim struct {
	table  string
	column string
	what   string
}

// label names the trimmed rows in errors.
func (t ageTrim) label() string {
	if t.what == "" {
		return t.table
	}

	return t.what
}

// deleteInChunks repeats a bounded DELETE until a pass removes fewer rows
// than the chunk size. statement must end in "LIMIT ?"; the chunk size is
// appended to args. Every chunk commits on its own, so a cancelled or failed
// trim keeps its progress (reported alongside the error) and the next call
// resumes where it stopped.
func (db *DB) deleteInChunks(ctx context.Context, what, statement string, args ...any) (int64, error) {
	bound := make([]any, 0, len(args)+1)
	bound = append(bound, args...)
	bound = append(bound, trimChunkRows)

	var total int64

	for {
		res, err := db.sql.ExecContext(ctx, statement, bound...)
		if err != nil {
			return total, fmt.Errorf("trimming %s: %w", what, err)
		}

		deleted, err := res.RowsAffected()
		if err != nil {
			return total, fmt.Errorf("counting trimmed %s: %w", what, err)
		}

		total += deleted

		if deleted < trimChunkRows {
			return total, nil
		}
	}
}

// trimOlderThan deletes rows whose timestamp column sorts before cutoff, in
// chunks, oldest first.
func (db *DB) trimOlderThan(ctx context.Context, trim ageTrim, cutoff string) (int64, error) {
	// table and column are fixed internal metadata, never request input.
	statement := fmt.Sprintf(
		`DELETE FROM %[1]s WHERE id IN (SELECT id FROM %[1]s WHERE %[2]s < ? ORDER BY %[2]s LIMIT ?)`,
		trim.table, trim.column,
	)

	return db.deleteInChunks(ctx, trim.label(), statement, cutoff)
}

// trimBeyondCap deletes one service's rows older than its newest maximum
// rows, in chunks, oldest first. The boundary is found on a read connection,
// so a service at or under its cap never takes the write lock.
func (db *DB) trimBeyondCap(ctx context.Context, trim ageTrim, serviceID string, maximum int) (int64, error) {
	if maximum < 0 {
		maximum = 0
	}

	//nolint:gosec // table is fixed internal metadata, never request input.
	boundary := fmt.Sprintf(
		`SELECT id FROM %s WHERE service_id = ? ORDER BY id DESC LIMIT 1 OFFSET ?`, trim.table)

	var newestExcess int64

	err := db.read.QueryRowContext(ctx, boundary, serviceID, maximum).Scan(&newestExcess)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}

	if err != nil {
		return 0, fmt.Errorf("finding %s cap boundary: %w", trim.label(), err)
	}

	// table is fixed internal metadata, never request input.
	statement := fmt.Sprintf(
		`DELETE FROM %[1]s WHERE id IN (
		   SELECT id FROM %[1]s WHERE service_id = ? AND id <= ? ORDER BY id LIMIT ?
		 )`, trim.table)

	return db.deleteInChunks(ctx, trim.label(), statement, serviceID, newestExcess)
}

// placeholders renders count comma-separated bind markers.
func placeholders(count int) string {
	if count <= 0 {
		return ""
	}

	return strings.Repeat("?,", count-1) + "?"
}
