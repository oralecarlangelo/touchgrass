package securitylog

import (
	"strings"
	"testing"
	"time"

	"github.com/oralecarlangelo/touchgrass/internal/model"
)

const validSecurityEvent = `{"timestamp":"2026-11-02T05:04:53Z","event_type":"security_probe","rule_id":"edge.malformed_server_action","category":"traffic","severity":"medium","confidence":"high","source":"nginx","action":"blocked","method":"POST","status":"404"}`

func TestParserValidSecurityEvent(t *testing.T) {
	t.Parallel()

	got, err := (Parser{}).Parse([]byte(validSecurityEvent))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	wantTime := time.Date(2026, 11, 2, 5, 4, 53, 0, time.UTC)
	if !got.Timestamp.Equal(wantTime) || got.EventType != "security_probe" ||
		got.RuleID != "edge.malformed_server_action" || got.Category != model.SecurityCategoryTraffic ||
		got.Severity != model.SecuritySeverityMedium || got.Confidence != model.SecurityConfidenceHigh ||
		got.Source != "nginx" || got.Action != "blocked" || got.Method != "POST" || got.Status != 404 {
		t.Fatalf("Parse() = %+v", got)
	}
}

func TestParserBoundsAndRedactsSecurityEvent(t *testing.T) {
	t.Parallel()

	line := strings.TrimSuffix(validSecurityEvent, "}") + `,"uri":"/private?token=secret","remote_addr":"203.0.113.5","user_agent":"scanner"}`
	got, err := (Parser{}).Parse([]byte(line))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	dump := got.String()
	for _, forbidden := range []string{"/private", "token=secret", "203.0.113.5", "scanner"} {
		if strings.Contains(dump, forbidden) {
			t.Errorf("event leaked %q: %s", forbidden, dump)
		}
	}
}

func TestParserRejectsMalformedSecurityEvents(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		line string
	}{
		{name: "malformed json", line: `{"timestamp":"broken"`},
		{name: "unknown category", line: strings.ReplaceAll(validSecurityEvent, `"category":"traffic"`, `"category":"other"`)},
		{name: "invalid rule id", line: strings.ReplaceAll(validSecurityEvent, `edge.malformed_server_action`, "edge.bad rule")},
		{name: "invalid status", line: strings.ReplaceAll(validSecurityEvent, `"status":"404"`, `"status":"999"`)},
		{name: "trailing data", line: validSecurityEvent + ` {}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if _, err := (Parser{}).Parse([]byte(tt.line)); err == nil {
				t.Errorf("Parse(%q) error = nil", tt.line)
			}
		})
	}
}
