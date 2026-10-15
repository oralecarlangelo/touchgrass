package nginxlog

import (
	"strings"
	"testing"
	"time"
)

const validTimingLine = `{"timestamp":"2026-10-16T01:02:03+08:00","request_id":"opaque","method":"GET","status":"200","route_family":"account_orders","request_time":"0.125","upstream_connect_time":"0.005","upstream_header_time":"0.020","upstream_response_time":"0.120","upstream_status":"200","upstream_addr":"127.0.0.1:4101","bytes_sent":"2048","request_length":"512","connection_requests":"3"}`

func TestParserValidTimingRecord(t *testing.T) {
	t.Parallel()

	got, err := (Parser{}).Parse([]byte(validTimingLine))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	wantTime := time.Date(2026, 10, 15, 17, 2, 3, 0, time.UTC)
	if !got.Timestamp.Equal(wantTime) || got.Method != "GET" || got.RouteFamily != "account_orders" || got.Status != 200 {
		t.Fatalf("Parse() identity fields = %+v", got)
	}
	if got.DurationMS != 125 || got.UpstreamConnectMS != 5 || got.UpstreamHeaderMS != 20 || got.UpstreamResponseMS != 120 {
		t.Fatalf("Parse() timing fields = %+v", got)
	}
	if got.BytesSent != 2048 || got.RequestBytes != 512 || got.Upstream != "127.0.0.1:4101" {
		t.Fatalf("Parse() bounded fields = %+v", got)
	}
}

func TestParserPrivacyAndRouteAllowlist(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		route  string
		extra  string
		wanted string
	}{
		{name: "known", route: "checkout", wanted: "checkout"},
		{name: "static producer alias", route: "static_assets", wanted: "static"},
		{name: "unknown", route: "customer_profile", wanted: "other"},
		{name: "encoded email", route: "person%40example.com", wanted: "other"},
		{name: "encoded token", route: "token%2Fsecret", wanted: "other"},
		{name: "query fixture ignored", route: "other", extra: `,"uri":"/events?email=person@example.com&token=secret"`, wanted: "other"},
		{name: "control character", route: "admin\\nsecret", wanted: "other"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			line := `{"timestamp":"2026-10-16T01:02:03Z","method":"GET","status":"200","route_family":"` + tt.route + `","request_time":"0.001","upstream_connect_time":"-","upstream_header_time":"-","upstream_response_time":"-","upstream_status":"-","upstream_addr":"-","bytes_sent":"1","request_length":"2","connection_requests":"1"` + tt.extra + `}`
			got, err := (Parser{}).Parse([]byte(line))
			if err != nil {
				t.Fatalf("Parse() error = %v", err)
			}
			if got.RouteFamily != tt.wanted {
				t.Errorf("RouteFamily = %q, want %q", got.RouteFamily, tt.wanted)
			}

			dump := got.String()
			for _, forbidden := range []string{"person@example.com", "token=secret", "/events?"} {
				if strings.Contains(dump, forbidden) {
					t.Errorf("observation leaked %q: %s", forbidden, dump)
				}
			}
		})
	}
}

func TestParserPartialUpstreamTimings(t *testing.T) {
	t.Parallel()

	line := strings.ReplaceAll(validTimingLine, `"upstream_connect_time":"0.005"`, `"upstream_connect_time":"0.005, -"`)
	line = strings.ReplaceAll(line, `"upstream_header_time":"0.020"`, `"upstream_header_time":"-"`)
	got, err := (Parser{}).Parse([]byte(line))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if got.UpstreamConnectMS != 5 || got.UpstreamHeaderMS != 0 {
		t.Fatalf("partial upstream timings = (%v, %v)", got.UpstreamConnectMS, got.UpstreamHeaderMS)
	}
}

func TestParserRejectsMalformedRecords(t *testing.T) {
	t.Parallel()

	for _, line := range []string{
		`{"timestamp":"2026-10-16T01:02:03Z","route_family":"health`,
		validTimingLine + ` {}`,
		strings.ReplaceAll(validTimingLine, `"request_time":"0.125"`, `"request_time":"slow"`),
		strings.ReplaceAll(validTimingLine, `"status":"200"`, `"status":"999"`),
		strings.ReplaceAll(validTimingLine, `"bytes_sent":"2048"`, `"bytes_sent":"-1"`),
	} {
		if _, err := (Parser{}).Parse([]byte(line)); err == nil {
			t.Errorf("Parse(%q) error = nil", line)
		}
	}
}
