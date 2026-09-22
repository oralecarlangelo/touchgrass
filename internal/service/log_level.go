package service

import (
	"fmt"
	"strings"

	"github.com/oralecarlangelo/touchgrass/internal/model"
)

// ParseLogLevel classifies a line from its stream and message. Error
// markers win, then warn, then debug; stderr with no markers is warn
// (conventionally warnings and errors), everything else is info. The
// match is a case-insensitive substring on purpose: container logs are
// free text (NestJS `[Nest] ... ERROR [ctx]`, Go `level=error`, plain
// prose), and a word-boundary parser would miss half of them.
func ParseLogLevel(stream, line string) string {
	lowered := strings.ToLower(line)

	for _, marker := range errorMarkers {
		if strings.Contains(lowered, marker) {
			return model.LogLevelError
		}
	}

	for _, marker := range warnMarkers {
		if strings.Contains(lowered, marker) {
			return model.LogLevelWarn
		}
	}

	for _, marker := range debugMarkers {
		if strings.Contains(lowered, marker) {
			return model.LogLevelDebug
		}
	}

	if stream == model.LogStderr {
		return model.LogLevelWarn
	}

	return model.LogLevelInfo
}

// Level markers, ordered by severity. Short stems ("fail", "deprecat")
// cover inflections; priority order keeps "stack trace" on an error
// line at error, not debug.
var (
	errorMarkers = []string{
		"error", "exception", "fail", "fatal",
		"critical", "panic", "traceback", "unhandled",
	}
	warnMarkers  = []string{"warn", "deprecat"}
	debugMarkers = []string{"debug", "trace"}
)

// NormalizeLogLevels parses a comma-separated level filter into a set,
// rejecting unknown levels. Empty input selects everything (nil set).
func NormalizeLogLevels(raw string) (map[string]bool, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}

	levels := map[string]bool{}

	for part := range strings.SplitSeq(raw, ",") {
		level := strings.ToLower(strings.TrimSpace(part))

		switch level {
		case model.LogLevelError,
			model.LogLevelWarn,
			model.LogLevelInfo,
			model.LogLevelDebug:
			levels[level] = true
		case "":
			continue
		default:
			return nil, fmt.Errorf("%w: level %q (want error, warn, info, or debug)", ErrInvalidInput, part)
		}
	}

	return levels, nil
}

// withLevels stamps the parsed level on every line.
func withLevels(lines []model.LogLine) []model.LogLine {
	for i := range lines {
		lines[i].Level = ParseLogLevel(lines[i].Stream, lines[i].Line)
	}

	return lines
}
