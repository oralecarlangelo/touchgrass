// Package store owns SQLite persistence.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"

	// The pure-Go SQLite driver. PROJECT_STRUCTURE.md assigns SQL driver
	// ownership to internal/store, so the import lives here.
	"modernc.org/sqlite"
)

// memDBCounter keeps test databases isolated: every :memory: open gets a
// unique shared-cache name so parallel tests never see each other's rows.
var memDBCounter atomic.Int64

const (
	// maxMemoryConns is the pool size of a transient :memory: database, which
	// serves reads and writes from one shared-cache pool.
	maxMemoryConns = 4
	// maxWriteConns is one on purpose: SQLite has a single writer, so writes
	// queue in the Go pool (bounded by the caller's context) instead of
	// colliding inside SQLite as SQLITE_BUSY.
	maxWriteConns = 1
	// maxReadConns sizes the read-only pool. WAL readers never block the
	// writer or each other, so slow scans cannot starve interactive reads.
	maxReadConns = 8
	pingTimeout  = 5 * time.Second

	// busyTimeoutMillis is how long a connection waits for a lock held by
	// another OS process (a CLI subcommand next to the server). Writers in one
	// process never reach it: they queue on the single write connection.
	busyTimeoutMillis = 30000
	// walAutocheckpointPages checkpoints after ~4 MiB of WAL (4 KiB pages).
	walAutocheckpointPages = 1000
	// journalSizeLimitBytes truncates the WAL file back to this size after a
	// checkpoint resets it, so one long reader cannot leave a huge WAL behind.
	journalSizeLimitBytes = 64 << 20

	sqliteBusy     = 5
	sqlitePrimary  = 0xff
	busyExplainFmt = "%w: another touchgrass process held the SQLite write lock for more than %s " +
		"(a running server, a migration, or another CLI command on the same database file); " +
		"wait for it to finish and retry"
)

// ErrBusy reports that another process held the database write lock for
// longer than the busy timeout.
var ErrBusy = errors.New("store: database is busy")

// DB is a configured SQLite handle. Writes go through sql, a single
// connection whose transactions begin IMMEDIATE; reads go through read, a
// read-only pool. For a :memory: database both name the same pool.
type DB struct {
	sql  *sql.DB
	read *sql.DB
	// path is the database file, empty for a :memory: database.
	path string
}

// Open connects to the SQLite file at path, or a transient database when
// path is ":memory:". The caller owns migrations via MigrateUp.
func Open(path string) (*DB, error) {
	if path == ":memory:" {
		pool, err := openPool(memoryDSN(), maxMemoryConns)
		if err != nil {
			return nil, err
		}

		return &DB{sql: pool, read: pool}, nil
	}

	// The writer opens first: it creates the file and switches it to WAL
	// before any read-only connection looks at it.
	writer, err := openPool(writeDSN(path), maxWriteConns)
	if err != nil {
		return nil, err
	}

	reader, err := openPool(readDSN(path), maxReadConns)
	if err != nil {
		_ = writer.Close()

		return nil, err
	}

	return &DB{sql: writer, read: reader, path: path}, nil
}

// openPool opens and pings one connection pool.
func openPool(dsn string, conns int) (*sql.DB, error) {
	pool, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("opening database: %w", err)
	}

	pool.SetMaxOpenConns(conns)
	pool.SetMaxIdleConns(conns)

	ctx, cancel := context.WithTimeout(context.Background(), pingTimeout)
	defer cancel()

	if err := pool.PingContext(ctx); err != nil {
		_ = pool.Close()

		return nil, fmt.Errorf("pinging database: %w", err)
	}

	return pool, nil
}

// Close releases the database handle.
func (db *DB) Close() error {
	var readErr error
	if db.read != db.sql {
		readErr = db.read.Close()
	}

	if err := errors.Join(db.sql.Close(), readErr); err != nil {
		return fmt.Errorf("closing database: %w", err)
	}

	return nil
}

// IsBusy reports whether err is SQLite refusing a lock that another process
// held past the busy timeout (SQLITE_BUSY and its extended codes).
func IsBusy(err error) bool {
	if errors.Is(err, ErrBusy) {
		return true
	}

	var sqliteErr *sqlite.Error

	return errors.As(err, &sqliteErr) && sqliteErr.Code()&sqlitePrimary == sqliteBusy
}

// ExplainBusy rewrites a busy error into an operator-facing explanation and
// returns every other error unchanged.
func ExplainBusy(err error) error {
	if err == nil || !IsBusy(err) || errors.Is(err, ErrBusy) {
		return err
	}

	return fmt.Errorf(busyExplainFmt+": %w", ErrBusy, time.Duration(busyTimeoutMillis)*time.Millisecond, err)
}

// Backup writes a consistent SQLite snapshot to a new destination file.
// The service layer owns directory allowlisting; this method refuses to
// overwrite an existing file. The snapshot only reads this database, so
// writers keep working while it runs; the WAL cannot be reset until it
// finishes.
func (db *DB) Backup(ctx context.Context, destination string) error {
	if destination == "" {
		return fmt.Errorf("backing up database: destination is required")
	}
	if _, err := os.Stat(destination); err == nil {
		return fmt.Errorf("backing up database: destination already exists")
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("checking backup destination: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		return fmt.Errorf("creating backup directory: %w", err)
	}

	if err := db.vacuumInto(ctx, destination); err != nil {
		return err
	}
	if err := os.Chmod(destination, 0o600); err != nil {
		return fmt.Errorf("securing database backup: %w", err)
	}

	return nil
}

// vacuumInto snapshots the database into destination. A database file gets a
// connection of its own: the read pool is query_only and refuses to create the
// destination file, and the write connection must stay free for writers.
func (db *DB) vacuumInto(ctx context.Context, destination string) error {
	pool := db.sql
	if db.path != "" {
		snapshot, err := sql.Open("sqlite", snapshotDSN(db.path))
		if err != nil {
			return fmt.Errorf("opening backup connection: %w", err)
		}
		defer func() { _ = snapshot.Close() }()

		snapshot.SetMaxOpenConns(1)
		pool = snapshot
	}

	if _, err := pool.ExecContext(ctx, "VACUUM INTO ?", destination); err != nil {
		return fmt.Errorf("backing up database: %w", err)
	}

	return nil
}

// memoryDSN names a unique shared-cache in-memory database. It keeps the
// driver's deferred transactions: a shared cache locks per table, and the
// write model below only applies to a database file.
func memoryDSN() string {
	id := memDBCounter.Add(1)

	// auto_vacuum matches writeDSN so a test exercises the same freelist
	// behaviour as a file; a fresh memory database is empty, so it takes.
	return fmt.Sprintf(
		"file:tgmem%d?mode=memory&cache=shared&_pragma=foreign_keys(1)&_pragma=auto_vacuum(incremental)",
		id,
	)
}

// commonPragmas apply to every connection of a database file:
//
//   - busy_timeout: wait for a lock another process holds instead of failing
//     at once. Set first so the pragmas after it wait too.
//   - foreign_keys: enforce declared references (off by default in SQLite).
//   - temp_store(MEMORY): sort and GROUP BY spill into memory, not temp files.
//   - synchronous(NORMAL): in WAL mode this syncs at checkpoints instead of at
//     every commit. A power loss can drop the last commits but never corrupts
//     the file; a process crash loses nothing.
func commonPragmas() string {
	return fmt.Sprintf(
		"_pragma=busy_timeout(%d)&_pragma=foreign_keys(1)&_pragma=temp_store(MEMORY)&_pragma=synchronous(NORMAL)",
		busyTimeoutMillis,
	)
}

// writeDSN configures the single write connection:
//
//   - _txlock=immediate: every transaction takes the write lock at BEGIN. A
//     deferred transaction that reads and then writes fails with SQLITE_BUSY
//     without consulting busy_timeout when another writer got in between.
//   - journal_mode(WAL): readers and the writer do not block each other.
//   - wal_autocheckpoint: move WAL pages into the main file every ~4 MiB.
//   - journal_size_limit: shrink the WAL file after a checkpoint resets it.
//   - auto_vacuum(incremental): keep a freelist the maintenance sweep can hand
//     back to the filesystem with PRAGMA incremental_vacuum. Without it a
//     deleted row's pages are reused but never released, so the file is a
//     permanent high-water mark: a GRID host reached 935 MB and stayed there
//     even once retention worked again. ⚠ SQLite only honours this on an EMPTY
//     database. An existing file keeps auto_vacuum=none until a full VACUUM
//     rewrites it, so the sweep reports the mode rather than assuming it.
func writeDSN(path string) string {
	return fmt.Sprintf(
		"file:%s?_txlock=immediate&%s&_pragma=journal_mode(WAL)&_pragma=wal_autocheckpoint(%d)"+
			"&_pragma=journal_size_limit(%d)&_pragma=auto_vacuum(incremental)",
		path, commonPragmas(), walAutocheckpointPages, journalSizeLimitBytes,
	)
}

// readDSN configures the read pool. query_only makes a stray write on a read
// connection fail loudly instead of joining the fight for the write lock.
func readDSN(path string) string {
	return fmt.Sprintf("file:%s?%s&_pragma=query_only(1)", path, commonPragmas())
}

// snapshotDSN configures the one-shot connection a backup reads through.
func snapshotDSN(path string) string {
	return fmt.Sprintf("file:%s?%s", path, commonPragmas())
}
