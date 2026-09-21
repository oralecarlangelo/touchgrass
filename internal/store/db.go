// Package store owns SQLite persistence.
package store

import (
	"context"
	"database/sql"
	"fmt"
	"sync/atomic"
	"time"

	// Register the pure-Go SQLite driver. PROJECT_STRUCTURE.md assigns SQL
	// driver ownership to internal/store, so the blank import lives here.
	_ "modernc.org/sqlite"
)

// memDBCounter keeps test databases isolated: every :memory: open gets a
// unique shared-cache name so parallel tests never see each other's rows.
var memDBCounter atomic.Int64

const (
	maxOpenConns = 1
	pingTimeout  = 5 * time.Second
)

// DB is a configured SQLite handle.
type DB struct {
	sql *sql.DB
}

// Open connects to the SQLite file at path, or a transient database when
// path is ":memory:". The caller owns migrations via MigrateUp.
func Open(path string) (*DB, error) {
	db, err := sql.Open("sqlite", dsn(path))
	if err != nil {
		return nil, fmt.Errorf("opening database: %w", err)
	}

	db.SetMaxOpenConns(maxOpenConns)
	db.SetMaxIdleConns(maxOpenConns)

	ctx, cancel := context.WithTimeout(context.Background(), pingTimeout)
	defer cancel()

	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()

		return nil, fmt.Errorf("pinging database: %w", err)
	}

	return &DB{sql: db}, nil
}

// Close releases the database handle.
func (db *DB) Close() error {
	if err := db.sql.Close(); err != nil {
		return fmt.Errorf("closing database: %w", err)
	}

	return nil
}

// dsn builds a modernc.org/sqlite data source name with the v1 pragmas:
// WAL for concurrent readers, a busy timeout for the single writer, and
// enforced foreign keys.
func dsn(path string) string {
	if path == ":memory:" {
		id := memDBCounter.Add(1)

		return fmt.Sprintf("file:tgmem%d?mode=memory&cache=shared&_pragma=foreign_keys(1)", id)
	}

	return "file:" + path + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)"
}
