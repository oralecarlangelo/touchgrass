package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/oralecarlangelo/touchgrass/internal/model"
)

const (
	testEnvKeyID = "deadbeef"
	testEnvActor = "admin"
)

// envEntry builds a stored entry with opaque ciphertext for store tests.
func envEntry(key, state string, secret bool) model.EnvEntry {
	return model.EnvEntry{
		ServiceID: testServiceAPI, Key: key, Ciphertext: []byte("ct-" + key), Nonce: []byte("nonce-" + key),
		KeyID: testEnvKeyID, IsSecret: secret, State: state, UpdatedBy: testEnvActor, UpdatedAt: time.Now(),
	}
}

// mustUpsert writes one entry or fails the test.
func mustUpsert(t *testing.T, env *EnvStore, entry model.EnvEntry) {
	t.Helper()

	if err := env.UpsertEntry(context.Background(), entry); err != nil {
		t.Fatalf("UpsertEntry(%s) error = %v", entry.Key, err)
	}
}

// mustRevision inserts an applied revision snapshot or fails the test.
func mustRevision(t *testing.T, env *EnvStore, summary, sha string, entries []model.EnvRevisionEntry) int64 {
	t.Helper()

	id, err := env.InsertRevision(context.Background(), model.EnvRevision{
		ServiceID: testServiceAPI, CreatedBy: testEnvActor, Summary: summary, RenderedSHA256: sha, Applied: true,
	}, entries)
	if err != nil {
		t.Fatalf("InsertRevision(%s) error = %v", summary, err)
	}

	return id
}

func TestEnvEntriesList(t *testing.T) {
	t.Parallel()

	db := openTestDB(t)
	env := NewEnvStore(db)
	ctx := context.Background()

	mustUpsert(t, env, envEntry("B_KEY", model.EnvStateApplied, true))
	mustUpsert(t, env, envEntry("A_KEY", model.EnvStateAdded, false))

	entries, err := env.ListEntries(ctx, testServiceAPI)
	if err != nil {
		t.Fatalf("ListEntries() error = %v", err)
	}

	if len(entries) != 2 || entries[0].Key != "A_KEY" || entries[1].Key != "B_KEY" {
		t.Fatalf("ListEntries() = %v, want A_KEY then B_KEY", entries)
	}

	if !entries[1].IsSecret || string(entries[1].Ciphertext) != "ct-B_KEY" || entries[1].KeyID != testEnvKeyID {
		t.Errorf("B_KEY = %+v, want secret with stored ciphertext and key id", entries[1])
	}

	other, err := env.ListEntries(ctx, "tn-fe")
	if err != nil {
		t.Fatalf("ListEntries(other) error = %v", err)
	}

	if len(other) != 0 {
		t.Errorf("ListEntries(other) = %v, want none", other)
	}
}

func TestEnvEntriesUpsertReplaces(t *testing.T) {
	t.Parallel()

	db := openTestDB(t)
	env := NewEnvStore(db)
	ctx := context.Background()

	mustUpsert(t, env, envEntry("B_KEY", model.EnvStateApplied, true))

	changed := envEntry("B_KEY", model.EnvStateChanged, true)
	changed.Ciphertext = []byte("ct-new")
	mustUpsert(t, env, changed)

	got, err := env.GetEntry(ctx, testServiceAPI, "B_KEY")
	if err != nil {
		t.Fatalf("GetEntry() error = %v", err)
	}

	if got.State != model.EnvStateChanged || string(got.Ciphertext) != "ct-new" {
		t.Errorf("GetEntry() = %+v, want changed state with new ciphertext", got)
	}
}

func TestEnvEntriesDelete(t *testing.T) {
	t.Parallel()

	db := openTestDB(t)
	env := NewEnvStore(db)
	ctx := context.Background()

	mustUpsert(t, env, envEntry("A_KEY", model.EnvStateAdded, false))

	if err := env.DeleteEntry(ctx, testServiceAPI, "A_KEY"); err != nil {
		t.Fatalf("DeleteEntry() error = %v", err)
	}

	if err := env.DeleteEntry(ctx, testServiceAPI, "A_KEY"); !errors.Is(err, ErrEnvEntryNotFound) {
		t.Errorf("second DeleteEntry() error = %v, want ErrEnvEntryNotFound", err)
	}

	if _, err := env.GetEntry(ctx, testServiceAPI, "A_KEY"); !errors.Is(err, ErrEnvEntryNotFound) {
		t.Errorf("GetEntry(deleted) error = %v, want ErrEnvEntryNotFound", err)
	}
}

func TestEnvMarkApplied(t *testing.T) {
	t.Parallel()

	db := openTestDB(t)
	ctx := context.Background()
	env := NewEnvStore(db)

	seed := []model.EnvEntry{
		envEntry("KEEP", model.EnvStateApplied, false),
		envEntry("NEW", model.EnvStateAdded, false),
		envEntry("EDIT", model.EnvStateChanged, true),
		envEntry("GONE", model.EnvStateRemoved, false),
	}

	if err := env.ReplaceEntries(ctx, testServiceAPI, seed); err != nil {
		t.Fatalf("ReplaceEntries() error = %v", err)
	}

	if err := env.MarkApplied(ctx, testServiceAPI, time.Now()); err != nil {
		t.Fatalf("MarkApplied() error = %v", err)
	}

	entries, err := env.ListEntries(ctx, testServiceAPI)
	if err != nil {
		t.Fatalf("ListEntries() error = %v", err)
	}

	want := map[string]bool{"KEEP": true, "NEW": true, "EDIT": true}

	if len(entries) != len(want) {
		t.Fatalf("entries after apply = %d, want %d", len(entries), len(want))
	}

	for _, entry := range entries {
		if !want[entry.Key] {
			t.Errorf("unexpected entry %s after apply", entry.Key)
		}

		if entry.State != model.EnvStateApplied {
			t.Errorf("%s state = %s, want applied", entry.Key, entry.State)
		}
	}
}

func TestEnvAppliedRevisionMoves(t *testing.T) {
	t.Parallel()

	db := openTestDB(t)
	ctx := context.Background()
	env := NewEnvStore(db)

	if _, found, err := env.AppliedRevision(ctx, testServiceAPI); err != nil || found {
		t.Fatalf("AppliedRevision(empty) = found %t, err %v; want false, nil", found, err)
	}

	first := mustRevision(t, env, "import", "aaa", []model.EnvRevisionEntry{
		{Key: "A", Ciphertext: []byte("a"), Nonce: []byte("n"), KeyID: testEnvKeyID, IsSecret: true},
	})
	second := mustRevision(t, env, "second", "bbb", nil)

	applied, found, err := env.AppliedRevision(ctx, testServiceAPI)
	if err != nil || !found || applied.ID != second {
		t.Fatalf("AppliedRevision() = %+v, %t, %v; want revision %d", applied, found, err, second)
	}

	previous, err := env.GetRevision(ctx, testServiceAPI, first)
	if err != nil {
		t.Fatalf("GetRevision(first) error = %v", err)
	}

	if previous.Applied {
		t.Error("first revision still applied after second insert")
	}

	if _, err := env.GetRevision(ctx, "tn-fe", first); !errors.Is(err, ErrEnvRevisionNotFound) {
		t.Errorf("GetRevision(other service) error = %v, want ErrEnvRevisionNotFound", err)
	}
}

func TestEnvRevisionListAndSnapshot(t *testing.T) {
	t.Parallel()

	db := openTestDB(t)
	ctx := context.Background()
	env := NewEnvStore(db)

	first := mustRevision(t, env, "import", "aaa", nil)
	second := mustRevision(t, env, "second", "bbb", []model.EnvRevisionEntry{
		{Key: "A", Ciphertext: []byte("a2"), Nonce: []byte("n2"), KeyID: testEnvKeyID},
		{Key: "B", Ciphertext: []byte("b"), Nonce: []byte("n3"), KeyID: testEnvKeyID},
	})

	list, err := env.ListRevisions(ctx, testServiceAPI, 10)
	if err != nil {
		t.Fatalf("ListRevisions() error = %v", err)
	}

	if len(list) != 2 || list[0].ID != second || list[1].ID != first {
		t.Errorf("ListRevisions() = %v, want newest first", list)
	}

	entries, err := env.RevisionEntries(ctx, second)
	if err != nil {
		t.Fatalf("RevisionEntries() error = %v", err)
	}

	if len(entries) != 2 || entries[0].Key != "A" || string(entries[0].Ciphertext) != "a2" || entries[1].Key != "B" {
		t.Errorf("RevisionEntries() = %v, want A(a2), B", entries)
	}
}

func TestEnvRunInsertAndGet(t *testing.T) {
	t.Parallel()

	db := openTestDB(t)
	ctx := context.Background()
	env := NewEnvStore(db)
	started := time.Now().UTC().Truncate(time.Second)

	if err := env.InsertRun(ctx, model.EnvRun{
		ID: "run-1", ServiceID: testServiceAPI, State: model.EnvRunRunning, StartedAt: started,
		Steps: []model.EnvRunStep{{Step: model.EnvStepRender, Message: "rendered", At: started}},
	}); err != nil {
		t.Fatalf("InsertRun() error = %v", err)
	}

	got, err := env.GetRun(ctx, testServiceAPI, "run-1")
	if err != nil {
		t.Fatalf("GetRun() error = %v", err)
	}

	if got.State != model.EnvRunRunning || len(got.Steps) != 1 || got.FinishedAt != nil || got.RevisionID != nil {
		t.Errorf("GetRun() = %+v, want running with one step", got)
	}

	if _, err := env.GetRun(ctx, testServiceAPI, "nope"); !errors.Is(err, ErrEnvRunNotFound) {
		t.Errorf("GetRun(unknown) error = %v, want ErrEnvRunNotFound", err)
	}
}

func TestEnvRunUpdate(t *testing.T) {
	t.Parallel()

	db := openTestDB(t)
	ctx := context.Background()
	env := NewEnvStore(db)
	started := time.Now().UTC().Truncate(time.Second)
	run := model.EnvRun{ID: "run-1", ServiceID: testServiceAPI, State: model.EnvRunRunning, StartedAt: started}

	if err := env.InsertRun(ctx, run); err != nil {
		t.Fatalf("InsertRun() error = %v", err)
	}

	revision := mustRevision(t, env, "apply", "x", nil)
	finished := started.Add(time.Minute)
	run.State = model.EnvRunSucceeded
	run.FinishedAt = &finished
	run.RevisionID = &revision
	run.Steps = []model.EnvRunStep{{Step: model.EnvStepDone, Message: "ok", At: finished}}

	if err := env.UpdateRun(ctx, run); err != nil {
		t.Fatalf("UpdateRun() error = %v", err)
	}

	got, err := env.GetRun(ctx, testServiceAPI, "run-1")
	if err != nil {
		t.Fatalf("GetRun(updated) error = %v", err)
	}

	if got.State != model.EnvRunSucceeded || len(got.Steps) != 1 || got.RevisionID == nil || *got.RevisionID != revision {
		t.Errorf("GetRun(updated) = %+v, want succeeded with one step and revision", got)
	}

	if got.FinishedAt == nil || !got.FinishedAt.Equal(finished) {
		t.Errorf("finished_at = %v, want %v", got.FinishedAt, finished)
	}

	if err := env.UpdateRun(ctx, model.EnvRun{ID: "nope", State: model.EnvRunFailed}); !errors.Is(err, ErrEnvRunNotFound) {
		t.Errorf("UpdateRun(unknown) error = %v, want ErrEnvRunNotFound", err)
	}
}

func TestEnvFailRunningRuns(t *testing.T) {
	t.Parallel()

	db := openTestDB(t)
	ctx := context.Background()
	env := NewEnvStore(db)
	now := time.Now()

	for _, run := range []model.EnvRun{
		{ID: "orphan", ServiceID: testServiceAPI, State: model.EnvRunRunning, StartedAt: now},
		{ID: "done", ServiceID: testServiceAPI, State: model.EnvRunSucceeded, StartedAt: now, FinishedAt: &now},
	} {
		if err := env.InsertRun(ctx, run); err != nil {
			t.Fatalf("InsertRun(%s) error = %v", run.ID, err)
		}
	}

	failed, err := env.FailRunningRuns(ctx, now, "restarted")
	if err != nil {
		t.Fatalf("FailRunningRuns() error = %v", err)
	}

	if failed != 1 {
		t.Errorf("FailRunningRuns() = %d, want 1", failed)
	}

	orphan, err := env.GetRun(ctx, testServiceAPI, "orphan")
	if err != nil {
		t.Fatalf("GetRun(orphan) error = %v", err)
	}

	if orphan.State != model.EnvRunFailed || len(orphan.Steps) != 1 || orphan.Steps[0].Message != "restarted" {
		t.Errorf("orphan = %+v, want failed with the restart note", orphan)
	}

	done, err := env.GetRun(ctx, testServiceAPI, "done")
	if err != nil {
		t.Fatalf("GetRun(done) error = %v", err)
	}

	if done.State != model.EnvRunSucceeded {
		t.Errorf("done state = %s, want succeeded untouched", done.State)
	}
}

func TestEnvValidation(t *testing.T) {
	t.Parallel()

	db := openTestDB(t)
	ctx := context.Background()
	env := NewEnvStore(db)

	if _, found, err := env.GetValidation(ctx, testServiceAPI); err != nil || found {
		t.Fatalf("GetValidation(empty) = %t, %v; want false, nil", found, err)
	}

	if err := env.UpsertValidation(ctx, model.EnvValidation{
		ServiceID: testServiceAPI, OK: false, Lines: []string{"INVALID tn-api:DB_URL: missing"},
	}); err != nil {
		t.Fatalf("UpsertValidation() error = %v", err)
	}

	if err := env.UpsertValidation(ctx, model.EnvValidation{ServiceID: testServiceAPI, OK: true}); err != nil {
		t.Fatalf("UpsertValidation(second) error = %v", err)
	}

	got, found, err := env.GetValidation(ctx, testServiceAPI)
	if err != nil || !found {
		t.Fatalf("GetValidation() = %t, %v; want found", found, err)
	}

	if !got.OK || got.Lines == nil || len(got.Lines) != 0 || got.At.IsZero() {
		t.Errorf("GetValidation() = %+v, want ok with empty non-nil lines", got)
	}
}

func TestServiceDeleteClearsEnv(t *testing.T) {
	t.Parallel()

	db := openTestDB(t)
	ctx := context.Background()
	env := NewEnvStore(db)

	mustUpsert(t, env, envEntry("A", model.EnvStateApplied, false))

	revision := mustRevision(t, env, "import", "x", []model.EnvRevisionEntry{
		{Key: "A", Ciphertext: []byte("a"), Nonce: []byte("n"), KeyID: testEnvKeyID},
	})

	if err := env.InsertRun(ctx, model.EnvRun{
		ID: "r", ServiceID: testServiceAPI, RevisionID: &revision, State: model.EnvRunSucceeded, StartedAt: time.Now(),
	}); err != nil {
		t.Fatalf("InsertRun() error = %v", err)
	}

	if err := env.UpsertValidation(ctx, model.EnvValidation{ServiceID: testServiceAPI, OK: true}); err != nil {
		t.Fatalf("UpsertValidation() error = %v", err)
	}

	if err := NewServiceStore(db).Delete(ctx, testServiceAPI); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}

	for _, table := range []string{"env_entries", "env_revisions", "env_runs", "env_validations"} {
		var count int

		if err := db.sql.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table+" WHERE service_id = ?", testServiceAPI).
			Scan(&count); err != nil {
			t.Fatalf("counting %s: %v", table, err)
		}

		if count != 0 {
			t.Errorf("%s rows after delete = %d, want 0", table, count)
		}
	}

	entries, err := env.RevisionEntries(ctx, revision)
	if err != nil {
		t.Fatalf("RevisionEntries() error = %v", err)
	}

	if len(entries) != 0 {
		t.Errorf("revision entries after delete = %d, want 0 (cascade)", len(entries))
	}
}
