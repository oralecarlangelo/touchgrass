package store

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/oralecarlangelo/touchgrass/internal/model"
)

// migrationTestService is a seeded service the tests attach rows to.
const migrationTestService = "tn-fe"

func testMigrationRow() MigrationConfigRow {
	return MigrationConfigRow{
		MigrationConfig: model.MigrationConfig{
			ServiceID:     migrationTestService,
			Tool:          "prisma",
			Database:      "db:5432/shop (schema public)",
			Container:     "api",
			Workdir:       "/srv/app",
			MigrationsDir: "db/migrations",
			Command:       []string{"pnpm", "exec", "prisma"},
			UpdatedBy:     "owner",
			UpdatedAt:     time.Date(2026, 11, 13, 9, 0, 0, 0, time.UTC),
		},
		URLCiphertext: []byte{1, 2, 3},
		URLNonce:      []byte{4, 5, 6},
		URLKeyID:      "abcd1234",
	}
}

func TestMigrationConfigRoundTrip(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	configs := NewMigrationConfigStore(openTestDB(t))

	if _, err := configs.Get(ctx, migrationTestService); !errors.Is(err, ErrMigrationConfigNotFound) {
		t.Fatalf("Get() before Put error = %v, want ErrMigrationConfigNotFound", err)
	}

	want := testMigrationRow()

	if err := configs.Put(ctx, want); err != nil {
		t.Fatalf("Put() error = %v, want nil", err)
	}

	got, err := configs.Get(ctx, migrationTestService)
	if err != nil {
		t.Fatalf("Get() error = %v, want nil", err)
	}

	if !sameMigrationRow(got, want) {
		t.Errorf("Get() = %+v, want %+v", got, want)
	}
}

// sameMigrationRow compares every stored field of two setups.
func sameMigrationRow(got, want MigrationConfigRow) bool {
	sameEnvelope := slices.Equal(got.URLCiphertext, want.URLCiphertext) &&
		slices.Equal(got.URLNonce, want.URLNonce) && got.URLKeyID == want.URLKeyID
	sameLayout := got.Container == want.Container && got.Workdir == want.Workdir &&
		got.MigrationsDir == want.MigrationsDir && slices.Equal(got.Command, want.Command)
	sameIdentity := got.ServiceID == want.ServiceID && got.Tool == want.Tool && got.Database == want.Database &&
		got.UpdatedBy == want.UpdatedBy && got.UpdatedAt.Equal(want.UpdatedAt)

	return sameEnvelope && sameLayout && sameIdentity
}

func TestMigrationConfigPutReplacesAndDeleteReports(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	configs := NewMigrationConfigStore(openTestDB(t))

	row := testMigrationRow()
	if err := configs.Put(ctx, row); err != nil {
		t.Fatalf("Put() error = %v, want nil", err)
	}

	row.Container = ""
	row.Command = nil
	row.URLCiphertext = []byte{9}

	if err := configs.Put(ctx, row); err != nil {
		t.Fatalf("second Put() error = %v, want nil", err)
	}

	got, err := configs.Get(ctx, migrationTestService)
	if err != nil {
		t.Fatalf("Get() error = %v, want nil", err)
	}

	if got.Container != "" || got.Command == nil || len(got.Command) != 0 || !slices.Equal(got.URLCiphertext, []byte{9}) {
		t.Errorf("Get() after replace = %+v, want the second row with an empty command", got)
	}

	for _, want := range []bool{true, false} {
		removed, err := configs.Delete(ctx, migrationTestService)
		if err != nil || removed != want {
			t.Errorf("Delete() = %t, %v, want %t", removed, err, want)
		}
	}
}

func startTestRun(t *testing.T, runs *MigrationRunStore, serviceID, kind string) int64 {
	t.Helper()

	id, err := runs.Create(context.Background(), model.MigrationRun{
		ServiceID: serviceID, Kind: kind, Tool: "prisma", Detail: "npx prisma " + kind,
		Actor: "owner", Container: "shop-api-1", Image: "shop/api:1", StartedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("Create() error = %v, want nil", err)
	}

	return id
}

func TestMigrationRunLifecycle(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	runs := NewMigrationRunStore(openTestDB(t))
	id := startTestRun(t, runs, migrationTestService, model.MigrationRunSeed)

	if busy, err := runs.Running(ctx, migrationTestService); err != nil || !busy {
		t.Errorf("Running() = %t, %v, want true", busy, err)
	}

	exitCode := 3

	if err := runs.Finish(ctx, id, MigrationRunResult{
		State: model.MigrationRunFailed, ExitCode: &exitCode, Output: "seed blew up", Truncated: true,
		FinishedAt: time.Now(),
	}); err != nil {
		t.Fatalf("Finish() error = %v, want nil", err)
	}

	got, err := runs.Get(ctx, migrationTestService, id)
	if err != nil {
		t.Fatalf("Get() error = %v, want nil", err)
	}

	if !failedWithOutput(got, 3, "seed blew up") {
		t.Errorf("Get() = %+v, want the finished run with its output", got)
	}

	listed, err := runs.List(ctx, migrationTestService, 10)
	if err != nil || len(listed) != 1 {
		t.Fatalf("List() = %v, %v, want one run", listed, err)
	}

	if listed[0].Output != "" || listed[0].Kind != model.MigrationRunSeed {
		t.Errorf("List() = %+v, want the seed run without output", listed[0])
	}
}

// failedWithOutput reports a run finished as failed with exitCode and
// output, marked truncated.
func failedWithOutput(run model.MigrationRun, exitCode int, output string) bool {
	return run.State == model.MigrationRunFailed && run.ExitCode != nil && *run.ExitCode == exitCode &&
		run.Output == output && run.Truncated && run.FinishedAt != nil
}

func TestMigrationRunBelongsToItsService(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	runs := NewMigrationRunStore(openTestDB(t))
	id := startTestRun(t, runs, migrationTestService, model.MigrationRunSeed)

	// A run id is only valid under its own service.
	if _, err := runs.Get(ctx, "tn-api", id); !errors.Is(err, ErrMigrationRunNotFound) {
		t.Errorf("Get() under another service error = %v, want ErrMigrationRunNotFound", err)
	}

	if err := runs.Finish(ctx, id+100, MigrationRunResult{State: model.MigrationRunFailed, FinishedAt: time.Now()}); !errors.Is(err, ErrMigrationRunNotFound) {
		t.Errorf("Finish() of an unknown run error = %v, want ErrMigrationRunNotFound", err)
	}
}

func TestMigrationRunsInterruptedByARestart(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	runs := NewMigrationRunStore(openTestDB(t))

	done := startTestRun(t, runs, migrationTestService, model.MigrationRunDeploy)
	if err := runs.Finish(ctx, done, MigrationRunResult{State: model.MigrationRunSucceeded, FinishedAt: time.Now()}); err != nil {
		t.Fatalf("Finish() error = %v, want nil", err)
	}

	stuck := startTestRun(t, runs, "tn-api", model.MigrationRunDeploy)

	running, err := runs.ListRunning(ctx)
	if err != nil || len(running) != 1 || running[0].ID != stuck {
		t.Fatalf("ListRunning() = %v, %v, want only run %d", running, err, stuck)
	}

	if failed, err := runs.FailRunning(ctx, "interrupted"); err != nil || failed != 1 {
		t.Errorf("FailRunning() = %d, %v, want 1", failed, err)
	}

	got, err := runs.Get(ctx, "tn-api", stuck)
	if err != nil || got.State != model.MigrationRunFailed || got.Output != "interrupted" || got.FinishedAt == nil {
		t.Errorf("Get() = %+v, %v, want the run failed as interrupted", got, err)
	}

	if kept, err := runs.Get(ctx, migrationTestService, done); err != nil || kept.State != model.MigrationRunSucceeded {
		t.Errorf("finished run = %+v, %v, want it left succeeded", kept, err)
	}
}

func TestMigrationRunsAreTrimmedPerService(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	runs := NewMigrationRunStore(openTestDB(t))
	other := startTestRun(t, runs, "tn-api", model.MigrationRunDeploy)

	var last int64

	for range maxMigrationRuns + 5 {
		last = startTestRun(t, runs, migrationTestService, model.MigrationRunDeploy)
	}

	listed, err := runs.List(ctx, migrationTestService, maxMigrationRuns*2)
	if err != nil {
		t.Fatalf("List() error = %v, want nil", err)
	}

	if len(listed) != maxMigrationRuns || listed[0].ID != last {
		t.Errorf("List() = %d runs newest %d, want %d runs newest %d", len(listed), listed[0].ID, maxMigrationRuns, last)
	}

	if _, err := runs.Get(ctx, "tn-api", other); err != nil {
		t.Errorf("another service's run was trimmed: %v", err)
	}
}

// Deleting a service must clear its migration rows, or the foreign
// keys refuse the delete.
func TestDeletingAServiceRemovesItsMigrationRows(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openTestDB(t)
	configs := NewMigrationConfigStore(db)
	runs := NewMigrationRunStore(db)

	if err := configs.Put(ctx, testMigrationRow()); err != nil {
		t.Fatalf("Put() error = %v, want nil", err)
	}

	startTestRun(t, runs, migrationTestService, model.MigrationRunSeed)

	if err := NewServiceStore(db).Delete(ctx, migrationTestService); err != nil {
		t.Fatalf("Delete() error = %v, want nil", err)
	}

	if _, err := configs.Get(ctx, migrationTestService); !errors.Is(err, ErrMigrationConfigNotFound) {
		t.Errorf("config after service delete error = %v, want ErrMigrationConfigNotFound", err)
	}

	if listed, err := runs.List(ctx, migrationTestService, 10); err != nil || len(listed) != 0 {
		t.Errorf("runs after service delete = %v, %v, want none", listed, err)
	}
}
