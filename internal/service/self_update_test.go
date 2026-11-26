package service

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestSelfUpdateChecksCompatibleReleaseAndDurableJob(t *testing.T) {
	t.Parallel()
	asset := "touchgrass_0.8.0_linux_" + runtime.GOARCH + ".tar.gz"
	fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"tag_name":"v0.8.0","html_url":"https://github.com/oralecarlangelo/touchgrass/releases/tag/v0.8.0","published_at":"2026-11-27T00:00:00Z","assets":[{"name":"` + asset + `"},{"name":"checksums.txt"}]}`))
	}))
	defer fixture.Close()
	statusPath := filepath.Join(t.TempDir(), "status.json")
	helper := "/usr/bin/true"
	if err := os.WriteFile(statusPath, []byte(`{"tag":"v0.7.0","state":"succeeded"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	updater := NewSelfUpdate(helper, statusPath, "https://console.example.com", "0.7.0")
	updater.platform = "linux"
	updater.releaseURL = fixture.URL
	status := updater.Check(context.Background())
	if !status.CanInstall || status.Release == nil || status.Release.Tag != "v0.8.0" || status.Job == nil {
		t.Fatalf("Check() = %+v, want installable v0.8.0 and persisted job", status)
	}
	if err := os.WriteFile(statusPath, []byte(`{"tag":"v0.8.0","state":"running"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := updater.Start(context.Background(), "v0.8.0"); !errors.Is(err, ErrUpdateBusy) {
		t.Fatalf("Start() = %v, want ErrUpdateBusy", err)
	}
}

func TestSelfUpdateRefusesMissingAsset(t *testing.T) {
	t.Parallel()
	fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"tag_name":"v0.8.0","html_url":"https://github.com/oralecarlangelo/touchgrass/releases/tag/v0.8.0","assets":[{"name":"checksums.txt"}]}`))
	}))
	defer fixture.Close()

	helper := "/usr/bin/true"
	updater := NewSelfUpdate(helper, filepath.Join(t.TempDir(), "status.json"), "https://console.example.com", "0.7.0")
	updater.platform = "linux"
	updater.releaseURL = fixture.URL
	status := updater.Check(context.Background())
	if status.CanInstall || !strings.Contains(status.CheckError, "GitHub") {
		t.Fatalf("Check() = %+v, want unavailable release", status)
	}
}

func TestUpdateVersionOrdering(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		candidate, installed string
		want                 bool
	}{
		{candidate: "v0.8.0", installed: "0.7.9", want: true},
		{candidate: "v1.0.0", installed: "0.9.9", want: true},
		{candidate: "v0.8.0", installed: "0.8.0", want: false},
		{candidate: "v0.8.0", installed: "dev", want: false},
		{candidate: "v0.8.0-beta", installed: "0.7.0", want: false},
	} {
		if got := newer(tt.candidate, tt.installed); got != tt.want {
			t.Errorf("newer(%q, %q) = %t, want %t", tt.candidate, tt.installed, got, tt.want)
		}
	}
}
