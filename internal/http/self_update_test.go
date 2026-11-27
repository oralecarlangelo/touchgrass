package http

import (
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	nethttp "net/http"

	"github.com/oralecarlangelo/touchgrass/internal/service"
)

func TestSelfUpdateRequiresAdminAndConfiguredOrigin(t *testing.T) {
	t.Parallel()
	server, _ := fullTestServer(t)
	server.selfUpdate = service.NewSelfUpdate(
		"", filepath.Join(t.TempDir(), "status.json"), "https://console.example.com", "0.7.0",
	)
	viewer := viewerCookie(t, server)
	admin := authCookie(t, server)

	for _, tt := range []struct {
		name, method, origin, body string
		cookie                     *nethttp.Cookie
		want                       int
	}{
		{name: "viewer read", method: nethttp.MethodGet, cookie: viewer, want: nethttp.StatusForbidden},
		{name: "viewer write", method: nethttp.MethodPost, cookie: viewer, want: nethttp.StatusForbidden},
		{name: "admin read", method: nethttp.MethodGet, cookie: admin, want: nethttp.StatusOK},
		{name: "cross origin", method: nethttp.MethodPost, origin: "https://other.example.com", cookie: admin, want: nethttp.StatusForbidden},
		{name: "missing origin", method: nethttp.MethodPost, cookie: admin, want: nethttp.StatusForbidden},
		{name: "confirmation required", method: nethttp.MethodPost, origin: "https://console.example.com", body: `{"tag":"v0.8.0","confirm":false}`, cookie: admin, want: nethttp.StatusBadRequest},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequestWithContext(t.Context(), tt.method, "/api/self-update", strings.NewReader(tt.body))
			req.AddCookie(tt.cookie)

			if tt.origin != "" {
				req.Header.Set("Origin", tt.origin)
			}

			rec := httptest.NewRecorder()
			server.handler.ServeHTTP(rec, req)

			if rec.Code != tt.want {
				t.Fatalf("status = %d, want %d", rec.Code, tt.want)
			}
		})
	}
}

func TestSelfUpdateOriginWithoutManualConfiguration(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name, origin, site string
		want               bool
	}{
		{name: "same host", origin: "https://console.example.com", want: true},
		{name: "cross host", origin: "https://attacker.example.com"},
		{name: "missing"},
		{name: "opaque", origin: "null"},
		{name: "userinfo", origin: "https://attacker@console.example.com"},
		{name: "path", origin: "https://console.example.com/path"},
		{name: "cross site", origin: "https://console.example.com", site: "cross-site"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			r := httptest.NewRequestWithContext(t.Context(), nethttp.MethodPost, "https://console.example.com/api/self-update", nil)
			r.Header.Set("Origin", tt.origin)
			r.Header.Set("Sec-Fetch-Site", tt.site)

			if got := validSelfUpdateOrigin(r, ""); got != tt.want {
				t.Fatalf("validSelfUpdateOrigin = %t, want %t", got, tt.want)
			}
		})
	}
}
