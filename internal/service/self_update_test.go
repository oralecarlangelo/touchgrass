package service

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
)

const candidateTag = "v0.8.0"

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
	if !status.CanInstall || status.Release == nil || status.Release.Tag != candidateTag || status.Job == nil {
		t.Fatalf("Check() = %+v, want installable v0.8.0 and persisted job", status)
	}

	if err := os.WriteFile(statusPath, []byte(`{"tag":"v0.8.0","state":"running"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := updater.Start(context.Background(), candidateTag); !errors.Is(err, ErrUpdateBusy) {
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
		{candidate: candidateTag, installed: "0.7.9", want: true},
		{candidate: "v1.0.0", installed: "0.9.9", want: true},
		{candidate: candidateTag, installed: "0.8.0", want: false},
		{candidate: candidateTag, installed: "dev", want: false},
		{candidate: "v0.10.0", installed: "0.9.0-fleet.1", want: true},
		{candidate: "v0.9.0", installed: "0.9.0-fleet.1", want: false},
		{candidate: "v0.8.0", installed: "0.9.0-fleet.1", want: false},
		{candidate: "v0.10.0", installed: "0.9.0+build.123", want: true},
		{candidate: "v0.8.0-beta", installed: "0.7.0", want: false},
	} {
		if got := newer(tt.candidate, tt.installed); got != tt.want {
			t.Errorf("newer(%q, %q) = %t, want %t", tt.candidate, tt.installed, got, tt.want)
		}
	}
}

func TestSelfUpdateDiscoversReleaseWithoutHostHelper(t *testing.T) {
	t.Parallel()

	asset := "touchgrass_0.8.0_linux_" + runtime.GOARCH + ".tar.gz"

	fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if _, err := w.Write([]byte(`{"tag_name":"v0.8.0","html_url":"https://github.com/oralecarlangelo/touchgrass/releases/tag/v0.8.0","assets":[{"name":"` + asset + `"},{"name":"checksums.txt"}]}`)); err != nil {
			t.Error(err)
		}
	}))
	defer fixture.Close()

	updater := NewSelfUpdate("", "", "", "0.7.0")
	updater.releaseURL = fixture.URL

	status := updater.Check(t.Context())
	if status.Configured || status.CanInstall || status.Release == nil || status.Release.Tag != candidateTag {
		t.Fatalf("Check() = %+v, want visible release with installation disabled", status)
	}
}

func TestSelfUpdateUsesStandardHostPaths(t *testing.T) {
	t.Parallel()

	u := NewSelfUpdate("", "", "", "0.9.0")
	if u.helper != "/usr/local/libexec/touchgrass-updater/request" || u.statusPath != "/var/lib/touchgrass-updater/status.json" {
		t.Fatalf("unexpected defaults: %s %s", u.helper, u.statusPath)
	}
}

func TestSelfUpdateExplicitRefreshBypassesCache(t *testing.T) {
	t.Parallel()

	var requests atomic.Int32

	fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)

		body := `{"tag_name":"v0.8.0","html_url":"https://github.com/oralecarlangelo/touchgrass/releases/tag/v0.8.0","assets":[{"name":"touchgrass_0.8.0_linux_` + runtime.GOARCH + `.tar.gz"},{"name":"checksums.txt"}]}`
		if _, err := w.Write([]byte(body)); err != nil {
			t.Error(err)
		}
	}))
	defer fixture.Close()

	u := NewSelfUpdate("", "", "", "0.7.0")
	u.releaseURL = fixture.URL
	u.Check(t.Context())
	u.Check(t.Context())
	u.CheckNow(t.Context())

	if got := requests.Load(); got != 2 {
		t.Fatalf("release requests = %d, want 2", got)
	}
}

func TestSelfUpdateReconcilesStoppedHostJob(t *testing.T) {
	t.Parallel()

	for _, active := range []bool{true, false} {
		t.Run(strconv.FormatBool(active), func(t *testing.T) {
			t.Parallel()

			path := filepath.Join(t.TempDir(), "status.json")

			fixture := `{
				"tag":"v0.10.0","state":"running","unit":"touchgrass-update-123.service",
				"phase":"backing_up","backup_percent":0,"updated_at":"2026-11-28T02:42:05Z"
			}`
			if err := os.WriteFile(path, []byte(fixture), 0o600); err != nil {
				t.Fatal(err)
			}

			u := NewSelfUpdate("/usr/bin/true", path, "", "0.9.0")
			u.jobActive = func(context.Context, string) (bool, error) { return active, nil }

			job, err := u.readJob(t.Context())
			if err != nil {
				t.Fatal(err)
			}

			want := updateStateFailed
			if active {
				want = updateStateRunning
			}

			if job.State != want {
				t.Fatalf("state = %s, want %s", job.State, want)
			}

			if job.Phase != "backing_up" || job.BackupPercent == nil || *job.BackupPercent != 0 || job.UpdatedAt == "" {
				t.Fatalf("job progress = %+v, want the backup report", job)
			}
		})
	}
}
