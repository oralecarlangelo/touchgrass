package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/oralecarlangelo/touchgrass/internal/model"
	"github.com/oralecarlangelo/touchgrass/internal/testfixture"
)

const (
	contentionBackgroundWriters = 4
	contentionInteractiveRounds = 20
	contentionLogCap            = 60
	// contentionLatencyBudget bounds one interactive round (four writes). It
	// is wall-clock, so it is set to the busy timeout rather than to what a
	// quiet machine needs (about a second under the race detector): a round
	// slower than this would have failed outright before writes were queued.
	contentionLatencyBudget = busyTimeoutMillis * time.Millisecond
)

// openGenericFileDB returns a migrated database file holding the generic
// service fixture, and its path.
func openGenericFileDB(t *testing.T) (*DB, string) {
	t.Helper()

	path := filepath.Join(t.TempDir(), "touchgrass.db")

	db, err := Open(path)
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

	return db, path
}

// writeErrors collects failures from concurrent writers.
type writeErrors struct {
	mu   sync.Mutex
	all  []error
	busy int
}

func (w *writeErrors) add(err error) {
	if err == nil || errors.Is(err, context.Canceled) {
		return
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	w.all = append(w.all, err)
	if IsBusy(err) {
		w.busy++
	}
}

func (w *writeErrors) report(t *testing.T) {
	t.Helper()

	w.mu.Lock()
	defer w.mu.Unlock()

	if len(w.all) > 0 {
		t.Fatalf("writers failed %d times (%d SQLITE_BUSY), want 0; first error: %v", len(w.all), w.busy, w.all[0])
	}
}

// contentionLines builds one collector batch for a service.
func contentionLines(serviceID string, writer, round int) []model.LogLine {
	lines := make([]model.LogLine, 0, 20)
	for i := range 20 {
		lines = append(lines, model.LogLine{
			ServiceID: serviceID, Container: fmt.Sprintf("worker-%d", writer), Stream: model.LogStdout,
			Line: fmt.Sprintf("writer %d round %d line %d", writer, round, i), Ts: time.Now(),
		})
	}

	return lines
}

// readThenWrite is the transaction shape that a deferred BEGIN turns into an
// instant SQLITE_BUSY under concurrent writers: read first, then write.
func readThenWrite(ctx context.Context, db *DB, actor string) error {
	tx, err := db.sql.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("beginning read-then-write: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var count int
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM audit").Scan(&count); err != nil {
		return fmt.Errorf("reading before write: %w", err)
	}

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO audit (actor, action, result, detail, created_at) VALUES (?, 'login', 'success', ?, ?)`,
		actor, fmt.Sprintf("saw %d", count), formatTime(time.Now()),
	); err != nil {
		return fmt.Errorf("writing after read: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing read-then-write: %w", err)
	}

	return nil
}

// runBackgroundWriter mimics a collector until ctx is cancelled: batched
// inserts, a cap trim, a cursor upsert, and a read-then-write transaction.
func runBackgroundWriter(ctx context.Context, db *DB, writer int, failures *writeErrors) {
	logs := NewLogStore(db)
	cursors := NewCursorStore(db)
	metrics := NewMetricStore(db)
	serviceID := testfixture.GenericServices()[writer%2].ID

	for round := 0; ctx.Err() == nil; round++ {
		_, err := logs.InsertBatch(ctx, contentionLines(serviceID, writer, round))
		failures.add(err)

		_, err = logs.TrimBeyondCap(ctx, serviceID, contentionLogCap)
		failures.add(err)

		failures.add(metrics.Insert(ctx, model.Metric{ServiceID: serviceID, ContainerName: "worker"}))
		_, err = metrics.TrimBefore(ctx, time.Now().Add(-time.Second))
		failures.add(err)

		failures.add(cursors.Upsert(ctx, model.CollectorCursor{
			Source: fmt.Sprintf("generic:%d", writer), Offset: int64(round),
		}))
		failures.add(readThenWrite(ctx, db, fmt.Sprintf("collector-%d", writer)))
	}
}

// interactiveEnvWrite performs the writes behind one env edit: replace the
// working set, stage one entry, start a run, and audit it.
func interactiveEnvWrite(ctx context.Context, db *DB, round int) error {
	env := NewEnvStore(db)
	audit := NewAuditStore(db)
	serviceID := testfixture.GenericGranted

	entries := make([]model.EnvEntry, 0, 5)
	for i := range 5 {
		entries = append(entries, model.EnvEntry{
			ServiceID: serviceID, Key: fmt.Sprintf("KEY_%d", i), Ciphertext: []byte("ciphertext"),
			Nonce: []byte("nonce-nonce-"), KeyID: "k1", State: model.EnvStateApplied, UpdatedBy: "operator",
		})
	}

	if err := env.ReplaceEntries(ctx, serviceID, entries); err != nil {
		return err
	}

	if err := env.UpsertEntry(ctx, model.EnvEntry{
		ServiceID: serviceID, Key: "STAGED", Ciphertext: []byte("ciphertext"),
		Nonce: []byte("nonce-nonce-"), KeyID: "k1", State: model.EnvStateAdded, UpdatedBy: "operator",
	}); err != nil {
		return err
	}

	if err := env.InsertRun(ctx, model.EnvRun{
		ID: fmt.Sprintf("run-%d-%d", time.Now().UnixNano(), round), ServiceID: serviceID,
		State: model.EnvRunRunning, StartedAt: time.Now(),
	}); err != nil {
		return err
	}

	_, err := audit.Insert(ctx, model.Audit{
		ServiceID: &serviceID, Actor: "operator", Action: model.AuditEnvReveal, Result: "success",
	})

	return err
}

func TestInteractiveWritesSurviveBackgroundWriters(t *testing.T) {
	t.Parallel()

	db, _ := openGenericFileDB(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	background, stop := context.WithCancel(ctx)
	failures := &writeErrors{}

	var group sync.WaitGroup
	for writer := range contentionBackgroundWriters {
		group.Go(func() { runBackgroundWriter(background, db, writer, failures) })
	}

	var slowest time.Duration

	for round := range contentionInteractiveRounds {
		started := time.Now()

		if err := interactiveEnvWrite(ctx, db, round); err != nil {
			failures.add(fmt.Errorf("interactive round %d: %w", round, err))

			break
		}

		slowest = max(slowest, time.Since(started))
	}

	stop()
	group.Wait()
	failures.report(t)

	if slowest > contentionLatencyBudget {
		t.Fatalf("slowest interactive write = %s, want under %s", slowest, contentionLatencyBudget)
	}

	t.Logf("slowest interactive write: %s", slowest)
}

func TestSecondHandleWritesWhileFirstIsBusy(t *testing.T) {
	t.Parallel()

	server, path := openGenericFileDB(t)

	// The CLI subcommands are a second process: a second Open on the file.
	cli, err := Open(path)
	if err != nil {
		t.Fatalf("second Open() error = %v, want nil", err)
	}

	t.Cleanup(func() { _ = cli.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	background, stop := context.WithCancel(ctx)
	failures := &writeErrors{}

	var group sync.WaitGroup
	for writer := range contentionBackgroundWriters {
		group.Go(func() { runBackgroundWriter(background, server, writer, failures) })
	}

	for round := range contentionInteractiveRounds {
		if err := interactiveEnvWrite(ctx, cli, round); err != nil {
			failures.add(fmt.Errorf("second handle round %d: %w", round, err))

			break
		}
	}

	stop()
	group.Wait()
	failures.report(t)
}

func TestSecondHandleWaitsForAHeldWriteLock(t *testing.T) {
	t.Parallel()

	server, path := openGenericFileDB(t)

	cli, err := Open(path)
	if err != nil {
		t.Fatalf("second Open() error = %v, want nil", err)
	}

	t.Cleanup(func() { _ = cli.Close() })

	ctx := t.Context()

	held, err := server.sql.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("BeginTx() error = %v, want nil", err)
	}

	const hold = 400 * time.Millisecond

	released := make(chan error, 1)

	go func() {
		time.Sleep(hold)

		released <- held.Commit()
	}()

	started := time.Now()

	if err := NewCursorStore(cli).Upsert(ctx, model.CollectorCursor{Source: "generic:cli"}); err != nil {
		t.Fatalf("Upsert() on second handle error = %v, want nil after the lock is released", err)
	}

	if waited := time.Since(started); waited < hold/2 {
		t.Errorf("second handle waited %s, want it to wait for the held write lock (~%s)", waited, hold)
	}

	if err := <-released; err != nil {
		t.Fatalf("Commit() error = %v, want nil", err)
	}
}

func TestWriteWaitIsBoundedByContext(t *testing.T) {
	t.Parallel()

	db, _ := openGenericFileDB(t)

	held, err := db.sql.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatalf("BeginTx() error = %v, want nil", err)
	}

	defer func() { _ = held.Rollback() }()

	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()

	err = NewCursorStore(db).Upsert(ctx, model.CollectorCursor{Source: "generic:waiter"})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Upsert() while the write connection is held error = %v, want context.DeadlineExceeded", err)
	}

	if IsBusy(err) {
		t.Errorf("IsBusy(%v) = true, want a context error rather than SQLITE_BUSY", err)
	}
}

func TestReadPoolRefusesWrites(t *testing.T) {
	t.Parallel()

	db, _ := openGenericFileDB(t)

	if _, err := db.read.ExecContext(t.Context(), "DELETE FROM audit"); err == nil {
		t.Fatal("write on the read pool error = nil, want a read-only refusal")
	}
}

func TestExplainBusy(t *testing.T) {
	t.Parallel()

	db, path := openGenericFileDB(t)

	held, err := db.sql.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatalf("BeginTx() error = %v, want nil", err)
	}

	defer func() { _ = held.Rollback() }()

	// A connection with a 1ms busy timeout stands in for one that waited the
	// full timeout behind another process.
	impatient, err := sql.Open("sqlite", "file:"+path+"?_pragma=busy_timeout(1)")
	if err != nil {
		t.Fatalf("sql.Open() error = %v, want nil", err)
	}

	defer func() { _ = impatient.Close() }()

	_, busyErr := impatient.ExecContext(t.Context(), "DELETE FROM audit")
	if !IsBusy(busyErr) {
		t.Fatalf("IsBusy(%v) = false, want true for a write behind a held lock", busyErr)
	}

	tests := []struct {
		name     string
		err      error
		wantBusy bool
	}{
		{name: "nil stays nil", err: nil, wantBusy: false},
		{name: "other error is unchanged", err: sql.ErrNoRows, wantBusy: false},
		{name: "busy is explained", err: fmt.Errorf("upserting env entry: %w", busyErr), wantBusy: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := ExplainBusy(tt.err)

			if errors.Is(got, ErrBusy) != tt.wantBusy {
				t.Fatalf("errors.Is(ExplainBusy(%v), ErrBusy) = %t, want %t", tt.err, !tt.wantBusy, tt.wantBusy)
			}

			if !tt.wantBusy && !errors.Is(got, tt.err) {
				t.Errorf("ExplainBusy(%v) = %v, want the error unchanged", tt.err, got)
			}

			if tt.wantBusy && !errors.Is(got, tt.err) {
				t.Errorf("ExplainBusy(%v) = %v, want the cause kept in the chain", tt.err, got)
			}
		})
	}
}
