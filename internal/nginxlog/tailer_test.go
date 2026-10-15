package nginxlog

import (
	"context"
	"os"
	"path/filepath"
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
