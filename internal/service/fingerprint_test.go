package service

import (
	"strings"
	"testing"

	"github.com/oralecarlangelo/touchgrass/internal/model"
)

// testFrames builds a two-frame stack.
func testFrames() []model.StackFrame {
	return []model.StackFrame{
		{Function: "handler", File: "app.js", Line: 10, Column: 3},
		{Function: "serve", File: "server.js", Line: 44, Column: 1},
	}
}

func TestFingerprintGroupsSameBug(t *testing.T) {
	t.Parallel()

	frames := testFrames()

	pairs := [][2]string{
		{"user 123 not found", "user 456 not found"},
		{
			"user [UUID_PLACEHOLDER_1] not found",
			"user [UUID_PLACEHOLDER_2] not found",
		},
		{"user 'alice' not found", "user 'bob' not found"},
		{"request deadbeef42 failed", "request 0102030405 failed"},
		{"mail bob@example.com bounced", "mail Ann@Example.COM bounced"},
	}

	for _, pair := range pairs {
		first := Fingerprint(testServiceAPI, model.OccurrenceException, pair[0], frames)
		second := Fingerprint(testServiceAPI, model.OccurrenceException, pair[1], frames)

		if first != second {
			t.Errorf("Fingerprint(%q) = %q, want same-bug group %q", pair[1], second, first)
		}
	}
}

func TestFingerprintSplitsDistinctBugs(t *testing.T) {
	t.Parallel()

	frames := testFrames()
	base := Fingerprint(testServiceAPI, model.OccurrenceException, "user 123 not found", frames)

	others := map[string]string{
		"other":   Fingerprint(testServiceAPI, model.OccurrenceException, "order 123 not found", frames),
		"frames":  Fingerprint(testServiceAPI, model.OccurrenceException, "user 123 not found", []model.StackFrame{{Function: "other", File: "o.js"}}),
		"type":    Fingerprint(testServiceAPI, model.OccurrenceMessage, "user 123 not found", frames),
		"service": Fingerprint(testServiceFE, model.OccurrenceException, "user 123 not found", frames),
	}

	for name, got := range others {
		if got == base {
			t.Errorf("Fingerprint(%s) = %q, want distinct from base", name, got)
		}
	}
}

func TestFingerprintIgnoresReleaseAndTail(t *testing.T) {
	t.Parallel()

	// Releases never enter the fingerprint: one issue tracks many releases.
	// Deep frames past the top three don't split groups either.
	short := []model.StackFrame{
		{Function: "a", File: "a.js"},
		{Function: "b", File: "b.js"},
		{Function: "c", File: "c.js"},
	}
	deep := append(append([]model.StackFrame{}, short...),
		model.StackFrame{Function: "tail", File: "tail.js", Line: 1},
		model.StackFrame{Function: "tail2", File: "tail.js", Line: 2},
	)

	first := Fingerprint(testServiceAPI, model.OccurrenceException, "boom", short)
	if got := Fingerprint(testServiceAPI, model.OccurrenceException, "boom", deep); got != first {
		t.Errorf("Fingerprint(deep) = %q, want tail-insensitive %q", got, first)
	}
}

func TestFingerprintShape(t *testing.T) {
	t.Parallel()

	got := Fingerprint(testServiceAPI, model.OccurrenceException, "boom", testFrames())

	if len(got) != fingerprintLen {
		t.Fatalf("Fingerprint() len = %d, want %d", len(got), fingerprintLen)
	}

	for _, c := range got {
		if !strings.ContainsRune("0123456789abcdef", c) {
			t.Fatalf("Fingerprint() = %q, want lowercase hex", got)
		}
	}
}

// FuzzFingerprint proves the grouper never panics, stays deterministic,
// and always emits a 32-char hex id over arbitrary inputs.
func FuzzFingerprint(f *testing.F) {
	f.Add("tn-api", "exception", "user 123 not found", "handler|app.js;serve|server.js")
	f.Add("", "", "", "")
	f.Add("s", "message", "'quoted' a@b.co 0123 deadBEEF [UUID_PLACEHOLDER_1]", "a|b")

	f.Fuzz(func(t *testing.T, serviceID, reportType, message, frameSpec string) {
		frames := parseFuzzFrames(frameSpec)

		first := Fingerprint(serviceID, reportType, message, frames)
		second := Fingerprint(serviceID, reportType, message, frames)

		if first != second {
			t.Fatalf("Fingerprint() nondeterministic: %q vs %q", first, second)
		}

		if len(first) != fingerprintLen {
			t.Fatalf("Fingerprint() len = %d, want %d", len(first), fingerprintLen)
		}

		for _, c := range first {
			if !strings.ContainsRune("0123456789abcdef", c) {
				t.Fatalf("Fingerprint() = %q, want lowercase hex", first)
			}
		}
	})
}

// parseFuzzFrames builds frames from "fn|file;fn|file" specs.
func parseFuzzFrames(spec string) []model.StackFrame {
	if spec == "" {
		return []model.StackFrame{}
	}

	frames := []model.StackFrame{}

	for frame := range strings.SplitSeq(spec, ";") {
		name, file, _ := strings.Cut(frame, "|")
		frames = append(frames, model.StackFrame{Function: name, File: file})
	}

	return frames
}
