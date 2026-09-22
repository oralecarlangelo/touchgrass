package service

import (
	"testing"

	"github.com/oralecarlangelo/touchgrass/internal/model"
)

func TestParseLogLevel(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		stream string
		line   string
		want   string
	}{
		{name: "plain info", stream: model.LogStdout, line: "listening on :4101", want: model.LogLevelInfo},
		{name: "nest error", stream: model.LogStderr, line: "[Nest] 1 - 09/22 ERROR [Bootstrap] connect ECONNREFUSED", want: model.LogLevelError},
		{name: "lowercase error", stream: model.LogStdout, line: "request failed: timeout", want: model.LogLevelError},
		{name: "exception", stream: model.LogStdout, line: "UnhandledPromiseRejection: exception in handler", want: model.LogLevelError},
		{name: "go level error", stream: model.LogStderr, line: `level=error msg="dial failed"`, want: model.LogLevelError},
		{name: "panic", stream: model.LogStderr, line: "panic: nil map write", want: model.LogLevelError},
		{name: "traceback", stream: model.LogStderr, line: "Traceback (most recent call last):", want: model.LogLevelError},
		{name: "warn", stream: model.LogStdout, line: "WARN deprecated endpoint /v1 used", want: model.LogLevelWarn},
		{name: "deprecation", stream: model.LogStdout, line: "DeprecationWarning: Buffer() is deprecated", want: model.LogLevelWarn},
		{name: "stderr default", stream: model.LogStderr, line: "some unstructured output", want: model.LogLevelWarn},
		{name: "debug", stream: model.LogStdout, line: "DEBUG cache miss for key x", want: model.LogLevelDebug},
		{name: "error beats trace", stream: model.LogStderr, line: "Error: boom; stack trace follows", want: model.LogLevelError},
		{name: "warn beats debug", stream: model.LogStdout, line: "warning: tracing disabled, debugging blind", want: model.LogLevelWarn},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := ParseLogLevel(tt.stream, tt.line); got != tt.want {
				t.Errorf("ParseLogLevel() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestNormalizeLogLevels(t *testing.T) {
	t.Parallel()

	levels, err := NormalizeLogLevels("error, warn")
	if err != nil {
		t.Fatalf("NormalizeLogLevels() error = %v, want nil", err)
	}

	if len(levels) != 2 || !levels["error"] || !levels["warn"] {
		t.Errorf("NormalizeLogLevels() = %v, want error+warn set", levels)
	}

	levels, err = NormalizeLogLevels("  ")
	if err != nil || levels != nil {
		t.Errorf("NormalizeLogLevels(blank) = (%v, %v), want (nil, nil)", levels, err)
	}

	if _, err := NormalizeLogLevels("error,bogus"); err == nil {
		t.Error("NormalizeLogLevels(bogus) error = nil, want invalid input")
	}
}
