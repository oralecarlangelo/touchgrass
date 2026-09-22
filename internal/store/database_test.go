package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/oralecarlangelo/touchgrass/internal/model"
)

func TestDBJobCreateGet(t *testing.T) {
	t.Parallel()

	db := openTestDB(t)
	ctx := context.Background()
	jobs := NewDBJobStore(db)

	id, err := jobs.Create(ctx, model.DBJob{
		Kind: model.DBJobBackup, Target: "ticketnation-20260922T120000Z.dump",
		Status: model.DBJobRunning, StartedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("Create() error = %v, want nil", err)
	}

	if id == 0 {
		t.Fatal("Create() id = 0, want non-zero")
	}

	got, err := jobs.Get(ctx, id)
	if err != nil {
		t.Fatalf("Get() error = %v, want nil", err)
	}

	if got.Kind != model.DBJobBackup || got.Status != model.DBJobRunning {
		t.Errorf("Get() = %+v, want running backup", got)
	}

	if got.Target != "ticketnation-20260922T120000Z.dump" {
		t.Errorf("Get() target = %q, want the backup name", got.Target)
	}

	if got.StartedAt.IsZero() || got.FinishedAt != nil {
		t.Errorf("Get() times = (%v, %v), want started set, finished nil", got.StartedAt, got.FinishedAt)
	}
}

func TestDBJobGetUnknown(t *testing.T) {
	t.Parallel()

	db := openTestDB(t)

	if _, err := NewDBJobStore(db).Get(context.Background(), 999); !errors.Is(err, ErrDBJobNotFound) {
		t.Errorf("Get() error = %v, want ErrDBJobNotFound", err)
	}
}

func TestDBJobFinish(t *testing.T) {
	t.Parallel()

	db := openTestDB(t)
	ctx := context.Background()
	jobs := NewDBJobStore(db)

	id, err := jobs.Create(ctx, model.DBJob{
		Kind: model.DBJobRestore, Target: "old.dump",
		Status: model.DBJobRunning, StartedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("Create() error = %v, want nil", err)
	}

	finished := time.Now()

	if err := jobs.Finish(ctx, id, model.DBJobSuccess, "old.dump restored", finished); err != nil {
		t.Fatalf("Finish() error = %v, want nil", err)
	}

	got, err := jobs.Get(ctx, id)
	if err != nil {
		t.Fatalf("Get() error = %v, want nil", err)
	}

	if got.Status != model.DBJobSuccess || got.Detail != "old.dump restored" {
		t.Errorf("Get() = %+v, want success with detail", got)
	}

	if got.FinishedAt == nil || !got.FinishedAt.Equal(finished) {
		t.Errorf("Get() finished_at = %v, want %v", got.FinishedAt, finished)
	}

	if err := jobs.Finish(ctx, 999, model.DBJobFailed, "nope", time.Now()); !errors.Is(
		err, ErrDBJobNotFound,
	) {
		t.Errorf("Finish() error = %v, want ErrDBJobNotFound", err)
	}
}

func TestDBJobList(t *testing.T) {
	t.Parallel()

	db := openTestDB(t)
	ctx := context.Background()
	jobs := NewDBJobStore(db)

	targets := []string{"a.dump", "a.dump", "b.dump"}

	for _, target := range targets {
		if _, err := jobs.Create(ctx, model.DBJob{
			Kind: model.DBJobBackup, Target: target,
			Status: model.DBJobSuccess, StartedAt: time.Now(),
		}); err != nil {
			t.Fatalf("Create() error = %v, want nil", err)
		}
	}

	got, err := jobs.List(ctx, 20)
	if err != nil {
		t.Fatalf("List() error = %v, want nil", err)
	}

	if len(got) != len(targets) {
		t.Fatalf("List() = %d jobs, want %d", len(got), len(targets))
	}

	for i := 1; i < len(got); i++ {
		if got[i-1].ID <= got[i].ID {
			t.Fatalf("List() ids not newest first: %+v", got)
		}
	}

	if got[0].Target != "b.dump" {
		t.Errorf("List()[0] target = %q, want b.dump", got[0].Target)
	}
}

func TestDBJobCreateTrimsToFifty(t *testing.T) {
	t.Parallel()

	db := openTestDB(t)
	ctx := context.Background()
	jobs := NewDBJobStore(db)

	for range 55 {
		if _, err := jobs.Create(ctx, model.DBJob{
			Kind: model.DBJobBackup, Target: "a.dump",
			Status: model.DBJobSuccess, StartedAt: time.Now(),
		}); err != nil {
			t.Fatalf("Create() error = %v, want nil", err)
		}
	}

	got, err := jobs.List(ctx, 100)
	if err != nil {
		t.Fatalf("List() error = %v, want nil", err)
	}

	if len(got) != 50 {
		t.Fatalf("List() = %d jobs, want 50", len(got))
	}

	if got[49].ID != got[0].ID-49 {
		t.Errorf("List() oldest id = %d, want %d", got[49].ID, got[0].ID-49)
	}
}

func TestDBJobFailRunning(t *testing.T) {
	t.Parallel()

	db := openTestDB(t)
	ctx := context.Background()
	jobs := NewDBJobStore(db)

	running, err := jobs.Create(ctx, model.DBJob{
		Kind: model.DBJobBackup, Target: "stale.dump",
		Status: model.DBJobRunning, StartedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("Create() error = %v, want nil", err)
	}

	done, err := jobs.Create(ctx, model.DBJob{
		Kind: model.DBJobBackup, Target: "done.dump",
		Status: model.DBJobSuccess, StartedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("Create() error = %v, want nil", err)
	}

	failed, err := jobs.FailRunning(ctx, "interrupted by restart")
	if err != nil {
		t.Fatalf("FailRunning() error = %v, want nil", err)
	}

	if failed != 1 {
		t.Errorf("FailRunning() = %d, want 1", failed)
	}

	got, err := jobs.Get(ctx, running)
	if err != nil {
		t.Fatalf("Get() error = %v, want nil", err)
	}

	if got.Status != model.DBJobFailed || got.Detail != "interrupted by restart" {
		t.Errorf("Get() = %+v, want failed with detail", got)
	}

	if got.FinishedAt == nil {
		t.Error("Get() finished_at is nil, want stamped")
	}

	untouched, err := jobs.Get(ctx, done)
	if err != nil {
		t.Fatalf("Get() error = %v, want nil", err)
	}

	if untouched.Status != model.DBJobSuccess {
		t.Errorf("Get() status = %q, want success untouched", untouched.Status)
	}
}

func TestSQLiteHealth(t *testing.T) {
	t.Parallel()

	db := openTestDB(t)

	health, err := db.SQLiteHealth(context.Background())
	if err != nil {
		t.Fatalf("SQLiteHealth() error = %v, want nil", err)
	}

	if health.Version == "" {
		t.Error("SQLiteHealth() version is empty, want sqlite_version()")
	}

	if health.Integrity != "ok" {
		t.Errorf("SQLiteHealth() integrity = %q, want ok", health.Integrity)
	}
}
