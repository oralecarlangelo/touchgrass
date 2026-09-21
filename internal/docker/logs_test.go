package docker

import (
	"bytes"
	"encoding/binary"
	"strings"
	"testing"
	"time"
)

// multiplexFrame builds one Docker log stream frame.
func multiplexFrame(stream byte, payload string) []byte {
	frame := []byte{stream, 0, 0, 0, 0, 0, 0, 0}
	//nolint:gosec // test payloads are short literals; the length always fits uint32.
	binary.BigEndian.PutUint32(frame[4:], uint32(len(payload)))

	return append(frame, payload...)
}

func TestParseLogStream(t *testing.T) {
	t.Parallel()

	var raw bytes.Buffer

	raw.Write(multiplexFrame(1, "2026-09-21T10:00:00.123456789Z hello stdout\n"))
	raw.Write(multiplexFrame(2, "2026-09-21T10:00:01Z hello stderr\n"))

	lines, err := parseLogStream(&raw)
	if err != nil {
		t.Fatalf("parseLogStream() error = %v, want nil", err)
	}

	if len(lines) != 2 {
		t.Fatalf("parseLogStream() = %d lines, want 2", len(lines))
	}

	first := lines[0]
	if first.Stream != "stdout" || first.Message != "hello stdout" {
		t.Errorf("first = %+v, want stdout hello", first)
	}

	want, _ := time.Parse(time.RFC3339Nano, "2026-09-21T10:00:00.123456789Z")
	if !first.Timestamp.Equal(want) {
		t.Errorf("first ts = %v, want %v", first.Timestamp, want)
	}

	if lines[1].Stream != "stderr" || lines[1].Message != "hello stderr" {
		t.Errorf("second = %+v, want stderr hello", lines[1])
	}
}

func TestParseLogStreamTTY(t *testing.T) {
	t.Parallel()

	raw := strings.NewReader("2026-09-21T10:00:00Z plain tty line\nsecond without stamp\n")

	lines, err := parseLogStream(raw)
	if err != nil {
		t.Fatalf("parseLogStream() error = %v, want nil", err)
	}

	if len(lines) != 2 {
		t.Fatalf("parseLogStream() = %d lines, want 2", len(lines))
	}

	if lines[0].Stream != "stdout" || lines[0].Message != "plain tty line" {
		t.Errorf("first = %+v, want stdout fallback", lines[0])
	}

	if !lines[1].Timestamp.IsZero() || lines[1].Message != "second without stamp" {
		t.Errorf("second = %+v, want zero ts with full text", lines[1])
	}
}

func TestParseLogStreamSkipsBlanks(t *testing.T) {
	t.Parallel()

	raw := strings.NewReader("\n\n2026-09-21T10:00:00Z only\n\n")

	lines, err := parseLogStream(raw)
	if err != nil {
		t.Fatalf("parseLogStream() error = %v, want nil", err)
	}

	if len(lines) != 1 || lines[0].Message != "only" {
		t.Errorf("parseLogStream() = %+v, want the one line", lines)
	}
}
