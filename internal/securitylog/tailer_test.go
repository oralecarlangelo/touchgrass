package securitylog

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/oralecarlangelo/touchgrass/internal/model"
)

func TestTailerResumesAndAcknowledges(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "events.jsonl")
	first := validSecurityEvent + "\n"
	second := stringReplace(validSecurityEvent, `"status":"404"`, `"status":"429"`) + "\n"
	if err := os.WriteFile(path, []byte(first+second), 0o600); err != nil {
		t.Fatal(err)
	}

	tailer := NewTailer(path, Parser{}, "security_events", model.CollectorCursor{Source: "security_events", Offset: int64(len(first))})
	got, cursor, err := tailer.Read(context.Background())
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if len(got) != 1 || got[0].Status != 429 || cursor.Offset != int64(len(first+second)) {
		t.Fatalf("Read() = (%+v, %+v)", got, cursor)
	}

	tailer.Acknowledge(cursor)
	after, _, err := tailer.Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != 0 {
		t.Fatalf("read after acknowledgement = %d events, want 0", len(after))
	}
}

func TestTailerDropsMalformedAndLeavesIncompleteRecord(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "events.jsonl")
	body := "not-json\n" + validSecurityEvent
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	tailer := NewTailer(path, Parser{}, "security_events", model.CollectorCursor{Source: "security_events"})
	got, cursor, err := tailer.Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 || cursor.DroppedRecords != 1 || cursor.Offset != int64(len("not-json\n")) {
		t.Fatalf("Read() = (%+v, %+v)", got, cursor)
	}
}

func stringReplace(value, old, replacement string) string {
	for index := 0; index+len(old) <= len(value); index++ {
		if value[index:index+len(old)] == old {
			return value[:index] + replacement + value[index+len(old):]
		}
	}
	return value
}
