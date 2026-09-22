package service

import (
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/oralecarlangelo/touchgrass/internal/docker"
	"github.com/oralecarlangelo/touchgrass/internal/model"
	"github.com/oralecarlangelo/touchgrass/internal/store"
)

// fakeLogLister emulates daemon log tailing over scripted lines.
type fakeLogLister struct {
	containers []docker.Container
	lines      map[string][]docker.LogLine
	inclusive  bool
	listErr    error
	logsErr    error
}

func (f *fakeLogLister) List(_ context.Context) ([]docker.Container, error) {
	return f.containers, f.listErr
}

func (f *fakeLogLister) Logs(
	_ context.Context,
	containerID, since string,
	tail int,
) ([]docker.LogLine, error) {
	if f.logsErr != nil {
		return nil, f.logsErr
	}

	lines := f.lines[containerID]

	if since != "" {
		kept := []docker.LogLine{}

		for _, line := range lines {
			stamp := line.Timestamp.UTC().Format(time.RFC3339Nano)

			if stamp > since || (f.inclusive && stamp == since) {
				kept = append(kept, line)
			}
		}

		lines = kept
	}

	if len(lines) > tail {
		lines = lines[len(lines)-tail:]
	}

	return lines, nil
}

// testLogContainer builds a managed tn-api container.
func testLogContainer() docker.Container {
	return docker.Container{
		ID: testBlueContainerID, Name: testBlueContainerName,
		Image: "tn-api:latest", ImageID: "sha256:blue", State: testRunningState,
		Labels: map[string]string{
			docker.LabelComposeProject: testComposeProject,
			docker.LabelComposeService: testBlueService,
		},
	}
}

// testLogCollector builds a collector on the seeded store.
func testLogCollector(t *testing.T, lister LogLister, maxLines int) (*LogCollector, *store.DB) {
	t.Helper()

	db := openInventoryDB(t)

	collector := NewLogCollector(LogCollectorConfig{
		Services:           store.NewServiceStore(db),
		Docker:             lister,
		Logs:               store.NewLogStore(db),
		Interval:           time.Hour,
		MaxLinesPerService: maxLines,
		Logger:             slog.New(slog.DiscardHandler),
	})

	return collector, db
}

// scriptLines builds n.stderr/stdout lines from base, one second apart.
func scriptLines(base time.Time, n int) []docker.LogLine {
	lines := make([]docker.LogLine, 0, n)

	for i := range n {
		lines = append(lines, docker.LogLine{
			Timestamp: base.Add(time.Duration(i) * time.Second),
			Stream:    model.LogStdout,
			Message:   "log line",
		})
	}

	return lines
}

func TestCollectStoresLines(t *testing.T) {
	t.Parallel()

	base := time.Date(2026, 9, 21, 10, 0, 0, 0, time.UTC)
	lister := &fakeLogLister{
		containers: []docker.Container{testLogContainer()},
		lines: map[string][]docker.LogLine{
			testBlueContainerID: {
				{Timestamp: base, Stream: model.LogStdout, Message: "listening on :4101"},
				{Timestamp: base.Add(time.Second), Stream: "stderr", Message: "boom failed"},
			},
		},
	}

	collector, _ := testLogCollector(t, lister, 1000)
	ctx := context.Background()

	if err := collector.collect(ctx); err != nil {
		t.Fatalf("collect() error = %v, want nil", err)
	}

	found, err := collector.Search(ctx, LogSearch{ServiceID: testServiceAPI, Query: "boom", Limit: 10})
	if err != nil {
		t.Fatalf("Search() error = %v, want nil", err)
	}

	if len(found) != 1 || found[0].Stream != "stderr" {
		t.Fatalf("Search() = %+v, want the stderr line", found)
	}

	if found[0].Container != testBlueContainerName {
		t.Errorf("container = %q, want blue container", found[0].Container)
	}

	// Second poll advances the cursor: nothing new, no duplicates.
	if err := collector.collect(ctx); err != nil {
		t.Fatalf("collect() error = %v, want nil", err)
	}

	all, err := collector.Search(ctx, LogSearch{ServiceID: testServiceAPI, Limit: 10})
	if err != nil {
		t.Fatalf("Search() error = %v, want nil", err)
	}

	if len(all) != 2 {
		t.Errorf("Search() = %d lines, want 2 without duplicates", len(all))
	}
}

func TestCollectSkipsUnmanaged(t *testing.T) {
	t.Parallel()

	stranger := testLogContainer()
	stranger.ID = "stranger-id"
	stranger.Labels = map[string]string{docker.LabelComposeProject: testOtherProject}

	lister := &fakeLogLister{
		containers: []docker.Container{stranger},
		lines:      map[string][]docker.LogLine{"stranger-id": scriptLines(time.Now(), 3)},
	}

	collector, db := testLogCollector(t, lister, 1000)

	if err := collector.collect(context.Background()); err != nil {
		t.Fatalf("collect() error = %v, want nil", err)
	}

	count, err := store.NewLogStore(db).CountByService(context.Background(), testServiceAPI)
	if err != nil {
		t.Fatalf("CountByService() error = %v, want nil", err)
	}

	if count != 0 {
		t.Errorf("CountByService() = %d, want 0 for unmanaged containers", count)
	}
}

func TestCollectDedupesInclusiveBoundary(t *testing.T) {
	t.Parallel()

	base := time.Date(2026, 9, 21, 10, 0, 0, 0, time.UTC)
	lister := &fakeLogLister{
		containers: []docker.Container{testLogContainer()},
		lines:      map[string][]docker.LogLine{testBlueContainerID: scriptLines(base, 2)},
		inclusive:  true,
	}

	collector, db := testLogCollector(t, lister, 1000)
	ctx := context.Background()

	if err := collector.collect(ctx); err != nil {
		t.Fatalf("collect() error = %v, want nil", err)
	}

	// The boundary line repeats inclusively; the new line appends.
	lister.lines[testBlueContainerID] = append(lister.lines[testBlueContainerID], docker.LogLine{
		Timestamp: base.Add(2 * time.Second), Stream: model.LogStdout, Message: "log line",
	})

	if err := collector.collect(ctx); err != nil {
		t.Fatalf("collect() error = %v, want nil", err)
	}

	count, err := store.NewLogStore(db).CountByService(ctx, testServiceAPI)
	if err != nil {
		t.Fatalf("CountByService() error = %v, want nil", err)
	}

	if count != 3 {
		t.Errorf("CountByService() = %d, want 3 without boundary duplicate", count)
	}
}

func TestCollectNonChronologicalBatch(t *testing.T) {
	t.Parallel()

	base := time.Date(2026, 9, 21, 10, 0, 0, 0, time.UTC)
	// The daemon merges stdout-then-stderr blocks: the oldest line
	// arrives last. The cursor must still advance to the max stamp.
	lister := &fakeLogLister{
		containers: []docker.Container{testLogContainer()},
		lines: map[string][]docker.LogLine{
			testBlueContainerID: {
				{Timestamp: base.Add(2 * time.Second), Stream: model.LogStdout, Message: "new2"},
				{Timestamp: base.Add(time.Second), Stream: model.LogStdout, Message: "new1"},
				{Timestamp: base.Add(-time.Hour), Stream: model.LogStderr, Message: "ancient"},
			},
		},
	}

	collector, db := testLogCollector(t, lister, 1000)
	ctx := context.Background()

	if err := collector.collect(ctx); err != nil {
		t.Fatalf("collect() error = %v, want nil", err)
	}

	lister.lines[testBlueContainerID] = append(lister.lines[testBlueContainerID], docker.LogLine{
		Timestamp: base.Add(3 * time.Second), Stream: model.LogStdout, Message: "new3",
	})

	if err := collector.collect(ctx); err != nil {
		t.Fatalf("collect() error = %v, want nil", err)
	}

	count, err := store.NewLogStore(db).CountByService(ctx, testServiceAPI)
	if err != nil {
		t.Fatalf("CountByService() error = %v, want nil", err)
	}

	if count != 4 {
		t.Errorf("CountByService() = %d, want 4 without re-ingestion", count)
	}
}

// staticLogLister ignores since, emulating a daemon that returns the
// full tail window on every poll.
type staticLogLister struct {
	inner *fakeLogLister
}

func (s *staticLogLister) List(ctx context.Context) ([]docker.Container, error) {
	return s.inner.List(ctx)
}

func (s *staticLogLister) Logs(
	_ context.Context,
	containerID, _ string,
	_ int,
) ([]docker.LogLine, error) {
	return s.inner.lines[containerID], nil
}

func TestCollectSkipsAlreadySeen(t *testing.T) {
	t.Parallel()

	base := time.Date(2026, 9, 21, 10, 0, 0, 0, time.UTC)
	inner := &fakeLogLister{
		containers: []docker.Container{testLogContainer()},
		lines:      map[string][]docker.LogLine{testBlueContainerID: scriptLines(base, 5)},
	}

	collector, db := testLogCollector(t, &staticLogLister{inner: inner}, 1000)
	ctx := context.Background()

	if err := collector.collect(ctx); err != nil {
		t.Fatalf("collect() error = %v, want nil", err)
	}

	if err := collector.collect(ctx); err != nil {
		t.Fatalf("collect() error = %v, want nil", err)
	}

	count, err := store.NewLogStore(db).CountByService(ctx, testServiceAPI)
	if err != nil {
		t.Fatalf("CountByService() error = %v, want nil", err)
	}

	if count != 5 {
		t.Errorf("CountByService() = %d, want 5 without duplicates", count)
	}
}

func TestCollectDropsBeyondPollCap(t *testing.T) {
	t.Parallel()

	base := time.Date(2026, 9, 21, 10, 0, 0, 0, time.UTC)

	rogue := &fakeLogLister{
		containers: []docker.Container{testLogContainer()},
		lines:      map[string][]docker.LogLine{testBlueContainerID: scriptLines(base, maxLinesPerPoll+10)},
	}
	// The rogue source ignores tail; the collector must still bound.
	collector, db := testLogCollector(t, &uncappedLogLister{inner: rogue}, 100000)
	ctx := context.Background()

	if err := collector.collect(ctx); err != nil {
		t.Fatalf("collect() error = %v, want nil", err)
	}

	if got := collector.Drops(); got != 10 {
		t.Errorf("Drops() = %d, want 10", got)
	}

	count, err := store.NewLogStore(db).CountByService(ctx, testServiceAPI)
	if err != nil {
		t.Fatalf("CountByService() error = %v, want nil", err)
	}

	if count != int64(maxLinesPerPoll) {
		t.Errorf("CountByService() = %d, want capped %d", count, maxLinesPerPoll)
	}
}

// uncappedLogLister ignores the tail hint, emulating a rogue source.
type uncappedLogLister struct {
	inner *fakeLogLister
}

func (u *uncappedLogLister) List(ctx context.Context) ([]docker.Container, error) {
	return u.inner.List(ctx)
}

func (u *uncappedLogLister) Logs(
	ctx context.Context,
	containerID, since string,
	_ int,
) ([]docker.LogLine, error) {
	return u.inner.Logs(ctx, containerID, since, len(u.inner.lines[containerID]))
}

func TestCollectTrimsToServiceCap(t *testing.T) {
	t.Parallel()

	base := time.Date(2026, 9, 21, 10, 0, 0, 0, time.UTC)
	lister := &fakeLogLister{
		containers: []docker.Container{testLogContainer()},
		lines:      map[string][]docker.LogLine{testBlueContainerID: scriptLines(base, 8)},
	}

	collector, db := testLogCollector(t, lister, 5)
	ctx := context.Background()

	if err := collector.collect(ctx); err != nil {
		t.Fatalf("collect() error = %v, want nil", err)
	}

	count, err := store.NewLogStore(db).CountByService(ctx, testServiceAPI)
	if err != nil {
		t.Fatalf("CountByService() error = %v, want nil", err)
	}

	if count != 5 {
		t.Errorf("CountByService() = %d, want 5 under the cap", count)
	}
}

func TestCollectPrunesCursors(t *testing.T) {
	t.Parallel()

	base := time.Date(2026, 9, 21, 10, 0, 0, 0, time.UTC)
	lister := &fakeLogLister{
		containers: []docker.Container{testLogContainer()},
		lines:      map[string][]docker.LogLine{testBlueContainerID: scriptLines(base, 2)},
	}

	collector, db := testLogCollector(t, lister, 1000)
	ctx := context.Background()

	if err := collector.collect(ctx); err != nil {
		t.Fatalf("collect() error = %v, want nil", err)
	}

	lister.containers = []docker.Container{}

	if err := collector.collect(ctx); err != nil {
		t.Fatalf("collect() error = %v, want nil", err)
	}

	// The container returns with a fresh id: the pruned cursor retails.
	lister.containers = []docker.Container{testLogContainer()}

	if err := collector.collect(ctx); err != nil {
		t.Fatalf("collect() error = %v, want nil", err)
	}

	count, err := store.NewLogStore(db).CountByService(ctx, testServiceAPI)
	if err != nil {
		t.Fatalf("CountByService() error = %v, want nil", err)
	}

	if count != 4 {
		t.Errorf("CountByService() = %d, want 4 after cursor prune retally", count)
	}
}

func TestCollectTruncatesLongLines(t *testing.T) {
	t.Parallel()

	base := time.Date(2026, 9, 21, 10, 0, 0, 0, time.UTC)
	lister := &fakeLogLister{
		containers: []docker.Container{testLogContainer()},
		lines: map[string][]docker.LogLine{
			testBlueContainerID: {{Timestamp: base, Stream: model.LogStdout, Message: strings.Repeat("x", maxLogLineBytes+100)}},
		},
	}

	collector, _ := testLogCollector(t, lister, 1000)
	ctx := context.Background()

	if err := collector.collect(ctx); err != nil {
		t.Fatalf("collect() error = %v, want nil", err)
	}

	found, err := collector.Search(ctx, LogSearch{ServiceID: testServiceAPI, Limit: 10})
	if err != nil {
		t.Fatalf("Search() error = %v, want nil", err)
	}

	if len(found) != 1 || len(found[0].Line) != maxLogLineBytes {
		t.Fatalf("Search() line len = %d, want truncated %d", len(found[0].Line), maxLogLineBytes)
	}
}

func TestLogSearchRejects(t *testing.T) {
	t.Parallel()

	collector, _ := testLogCollector(t, &fakeLogLister{}, 1000)
	ctx := context.Background()

	if _, err := collector.Search(ctx, LogSearch{ServiceID: testUnknownServiceID, Limit: 10}); err == nil {
		t.Error("Search() error = nil, want unknown service error")
	}

	if _, err := collector.Search(ctx, LogSearch{ServiceID: testServiceAPI, Stream: "bogus", Limit: 10}); err == nil {
		t.Error("Search() error = nil, want bad stream error")
	}

	if _, err := collector.Search(ctx, LogSearch{ServiceID: testServiceAPI, Level: "bogus", Limit: 10}); err == nil {
		t.Error("Search() error = nil, want bad level error")
	}

	if _, err := collector.Context(ctx, 9999, 5, 5); err == nil {
		t.Error("Context() error = nil, want unknown line error")
	}
}

// testLevelLines is the mixed-level corpus, oldest first.
var testLevelLines = []struct {
	stream string
	line   string
}{
	{model.LogStdout, "boot ok"},
	{model.LogStderr, "ERROR disk full"},
	{model.LogStdout, "WARN slow query"},
	{model.LogStdout, "ERROR retry failed"},
	{model.LogStdout, "shutdown ok"},
}

// seedLevelLines stores the mixed-level corpus one second apart.
func seedLevelLines(t *testing.T, db *store.DB, base time.Time) {
	t.Helper()

	lines := make([]model.LogLine, 0, len(testLevelLines))

	for i, entry := range testLevelLines {
		lines = append(lines, model.LogLine{
			ServiceID: testServiceAPI, Container: "c", Stream: entry.stream,
			Line: entry.line, Ts: base.Add(time.Duration(i) * time.Second),
		})
	}

	if _, err := store.NewLogStore(db).InsertBatch(context.Background(), lines); err != nil {
		t.Fatalf("InsertBatch() error = %v, want nil", err)
	}
}

func TestLogSearchLevels(t *testing.T) {
	t.Parallel()

	collector, db := testLogCollector(t, &fakeLogLister{}, 1000)
	ctx := context.Background()
	base := time.Date(2026, 9, 21, 10, 0, 0, 0, time.UTC)

	seedLevelLines(t, db, base)

	errors, err := collector.Search(ctx, LogSearch{ServiceID: testServiceAPI, Level: "error", Limit: 10})
	if err != nil {
		t.Fatalf("Search() error = %v, want nil", err)
	}

	if len(errors) != 2 || errors[0].Line != "ERROR retry failed" || errors[1].Line != "ERROR disk full" {
		t.Fatalf("level filter = %+v, want newest-first errors", errors)
	}

	if errors[0].Level != model.LogLevelError || errors[1].Level != model.LogLevelError {
		t.Errorf("levels = %q/%q, want error stamped", errors[0].Level, errors[1].Level)
	}

	multi, err := collector.Search(ctx, LogSearch{ServiceID: testServiceAPI, Level: "error,warn", Limit: 2})
	if err != nil {
		t.Fatalf("Search() error = %v, want nil", err)
	}

	if len(multi) != 2 || multi[0].Line != "ERROR retry failed" || multi[1].Line != "WARN slow query" {
		t.Fatalf("level set = %+v, want newest two of error+warn", multi)
	}
}

func TestLogSearchStream(t *testing.T) {
	t.Parallel()

	collector, db := testLogCollector(t, &fakeLogLister{}, 1000)
	ctx := context.Background()
	base := time.Date(2026, 9, 21, 10, 0, 0, 0, time.UTC)

	seedLevelLines(t, db, base)

	stderr, err := collector.Search(ctx, LogSearch{ServiceID: testServiceAPI, Stream: model.LogStderr, Limit: 10})
	if err != nil {
		t.Fatalf("Search() error = %v, want nil", err)
	}

	if len(stderr) != 1 || stderr[0].Line != "ERROR disk full" {
		t.Fatalf("stream filter = %+v, want the stderr line", stderr)
	}
}

func TestLogContextLevels(t *testing.T) {
	t.Parallel()

	collector, db := testLogCollector(t, &fakeLogLister{}, 1000)
	ctx := context.Background()
	base := time.Date(2026, 9, 21, 10, 0, 0, 0, time.UTC)

	seedLevelLines(t, db, base)

	errors, err := collector.Search(ctx, LogSearch{ServiceID: testServiceAPI, Level: "error", Limit: 1})
	if err != nil {
		t.Fatalf("Search() error = %v, want nil", err)
	}

	got, err := collector.Context(ctx, errors[0].ID, 1, 1)
	if err != nil {
		t.Fatalf("Context() error = %v, want nil", err)
	}

	if got.Anchor.Level != model.LogLevelError || len(got.Before) != 1 || len(got.After) != 1 {
		t.Fatalf("context = %+v, want stamped anchor with neighbors", got)
	}

	if got.Before[0].Level == "" || got.After[0].Level == "" {
		t.Error("context neighbors lack stamped levels")
	}
}

func TestLogContextClamps(t *testing.T) {
	t.Parallel()

	base := time.Date(2026, 9, 21, 10, 0, 0, 0, time.UTC)
	lister := &fakeLogLister{
		containers: []docker.Container{testLogContainer()},
		lines:      map[string][]docker.LogLine{testBlueContainerID: scriptLines(base, 3)},
	}

	collector, _ := testLogCollector(t, lister, 1000)
	ctx := context.Background()

	if err := collector.collect(ctx); err != nil {
		t.Fatalf("collect() error = %v, want nil", err)
	}

	all, err := collector.Search(ctx, LogSearch{ServiceID: testServiceAPI, Limit: 10})
	if err != nil {
		t.Fatalf("Search() error = %v, want nil", err)
	}

	got, err := collector.Context(ctx, all[1].ID, -5, 5000)
	if err != nil {
		t.Fatalf("Context() error = %v, want nil", err)
	}

	if len(got.Before) != 0 || len(got.After) != 1 {
		t.Errorf("Context() = %d before %d after, want 0/1 clamped", len(got.Before), len(got.After))
	}
}
