package nginxlog

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/oralecarlangelo/touchgrass/internal/model"
)

func TestTailerResumesFromPersistedOffset(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "timing.jsonl")
	first := validTimingLine + "\n"
	second := replaceStatus(validTimingLine, "201") + "\n"
	if err := os.WriteFile(path, []byte(first+second), 0o600); err != nil {
		t.Fatal(err)
	}

	tailer := NewTailer(path, Parser{}, "nginx", model.CollectorCursor{Source: "nginx", Offset: int64(len(first))})
	got, cursor, err := tailer.Read(context.Background())
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if len(got) != 1 || got[0].Status != 201 {
		t.Fatalf("Read() = %+v, want second record", got)
	}
	if cursor.Offset != int64(len(first+second)) || cursor.Identity == "" {
		t.Fatalf("cursor = %+v", cursor)
	}
}

func TestTailerRetriesUntilCursorIsAcknowledged(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "timing.jsonl")

	body := validTimingLine + "\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	tailer := NewTailer(path, Parser{}, "nginx", model.CollectorCursor{Source: "nginx"})

	first, cursor, err := tailer.Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	second, _, err := tailer.Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	if len(first) != 1 || len(second) != 1 {
		t.Fatalf("unacknowledged reads = (%d, %d), want the record retried", len(first), len(second))
	}

	tailer.Acknowledge(cursor)

	afterAck, _, err := tailer.Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	if len(afterAck) != 0 {
		t.Fatalf("read after acknowledgement = %d records, want 0", len(afterAck))
	}
}

func TestTailerRotationAndTruncationAreVisible(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "timing.jsonl")
	if err := os.WriteFile(path, []byte(validTimingLine+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	first := NewTailer(path, Parser{}, "nginx", model.CollectorCursor{Source: "nginx"})
	_, persisted, err := first.Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	if err := os.Rename(path, path+".1"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(replaceStatus(validTimingLine, "202")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	rotated := NewTailer(path, Parser{}, "nginx", persisted)
	got, afterRotation, err := rotated.Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Status != 202 || afterRotation.DroppedRecords != 1 {
		t.Fatalf("rotation result = (%+v, %+v)", got, afterRotation)
	}

	if err := os.Truncate(path, 0); err != nil {
		t.Fatal(err)
	}
	shortLine := `{"timestamp":"2026-10-16T01:02:03Z","method":"GET","status":"203","route_family":"health","request_time":"0.001","upstream_connect_time":"-","upstream_header_time":"-","upstream_response_time":"-","bytes_sent":"1","request_length":"1","connection_requests":"1"}`
	if err := os.WriteFile(path, []byte(shortLine+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	truncated := NewTailer(path, Parser{}, "nginx", afterRotation)
	got, afterTruncation, err := truncated.Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Status != 203 || afterTruncation.DroppedRecords != 2 {
		t.Fatalf("truncation result = (%+v, %+v)", got, afterTruncation)
	}
}

func TestTailerCountsMalformedAndIncompleteRecords(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "timing.jsonl")
	body := "not-json\n" + validTimingLine
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	tailer := NewTailer(path, Parser{}, "nginx", model.CollectorCursor{Source: "nginx"})
	got, cursor, err := tailer.Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 || cursor.DroppedRecords != 1 || cursor.Offset != int64(len("not-json\n")) {
		t.Fatalf("partial read = (%+v, %+v)", got, cursor)
	}
}

func TestTailerBoundsEachReadBatch(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "timing.jsonl")

	body := strings.Repeat(validTimingLine+"\n", maxRecordsPerRead+1)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	first := NewTailer(path, Parser{}, "nginx", model.CollectorCursor{Source: "nginx"})

	got, cursor, err := first.Read(context.Background())
	if err != nil {
		t.Fatalf("first Read() error = %v", err)
	}

	if len(got) != maxRecordsPerRead || cursor.Offset >= int64(len(body)) {
		t.Fatalf("first Read() = (%d records, offset %d), want bounded batch before EOF", len(got), cursor.Offset)
	}

	second := NewTailer(path, Parser{}, "nginx", cursor)

	got, cursor, err = second.Read(context.Background())
	if err != nil {
		t.Fatalf("second Read() error = %v", err)
	}

	if len(got) != 1 || cursor.Offset != int64(len(body)) {
		t.Fatalf("second Read() = (%d records, offset %d), want final record and EOF", len(got), cursor.Offset)
	}
}

func TestTailerDropsOversizedLineAndContinues(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "timing.jsonl")
	oversized := strings.Repeat("x", maxTimingLineBytes+1) + "\n"

	body := oversized + validTimingLine + "\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	tailer := NewTailer(path, Parser{}, "nginx", model.CollectorCursor{Source: "nginx"})

	got, cursor, err := tailer.Read(context.Background())
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}

	if len(got) != 1 || cursor.DroppedRecords != 1 || cursor.Offset != int64(len(body)) {
		t.Fatalf("Read() = (%+v, %+v), want oversized line dropped and valid record retained", got, cursor)
	}
}

func TestTailerLeavesOversizedIncompleteLineUnread(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "timing.jsonl")

	body := strings.Repeat("x", maxTimingLineBytes+1)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	tailer := NewTailer(path, Parser{}, "nginx", model.CollectorCursor{Source: "nginx"})

	got, cursor, err := tailer.Read(context.Background())
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}

	if len(got) != 0 || cursor.DroppedRecords != 0 || cursor.Offset != 0 {
		t.Fatalf("Read() = (%+v, %+v), want incomplete line left unread", got, cursor)
	}
}

func replaceStatus(line, status string) string {
	return stringReplace(line, `"status":"200"`, `"status":"`+status+`"`)
}

func stringReplace(value, old, replacement string) string {
	for index := 0; index+len(old) <= len(value); index++ {
		if value[index:index+len(old)] == old {
			return value[:index] + replacement + value[index+len(old):]
		}
	}
	return value
}
