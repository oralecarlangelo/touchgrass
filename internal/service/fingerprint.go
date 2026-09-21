package service

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strings"

	"github.com/oralecarlangelo/touchgrass/internal/model"
)

// Fingerprint input shaping.
const (
	fingerprintLen = 32
	fingerprintTop = 3
)

// templatePatterns normalize volatile message parts to fixed tokens so
// same-bug reports group. Order matters: quoted first (may contain ids),
// then uuid, email, long hex, and finally bare numbers.
var templatePatterns = []struct {
	pattern *regexp.Regexp
	token   string
}{
	{regexp.MustCompile(`'[^']*'`), "'?'"},
	{regexp.MustCompile(`[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}`), "#uuid"},
	{regexp.MustCompile(`[\w.+-]+@[\w-]+\.[\w.]+`), "#email"},
	{regexp.MustCompile(`\b[0-9a-fA-F]{8,}\b`), "#hex"},
	{regexp.MustCompile(`\d+`), "#"},
}

// Fingerprint groups one report: same bug, same 32 hex chars, stable
// across machines. Releases stay out so one issue tracks many releases;
// only the top frames count so deep-trace churn doesn't split groups.
func Fingerprint(
	serviceID, reportType, message string,
	frames []model.StackFrame,
) string {
	parts := []string{serviceID, reportType, templateMessage(message)}

	for _, frame := range frames[:min(len(frames), fingerprintTop)] {
		parts = append(parts, frame.Function, frame.File)
	}

	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))

	return hex.EncodeToString(sum[:])[:fingerprintLen]
}

// templateMessage replaces volatile parts with fixed tokens.
func templateMessage(message string) string {
	templated := message

	for _, replacement := range templatePatterns {
		templated = replacement.pattern.ReplaceAllString(templated, replacement.token)
	}

	return strings.Join(strings.Fields(templated), " ")
}
