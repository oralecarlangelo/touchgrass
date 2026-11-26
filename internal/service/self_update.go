package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

const touchGrassReleaseURL = "https://api.github.com/repos/oralecarlangelo/touchgrass/releases/latest"

var updateTagPattern = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+$`)

var (
	ErrUpdateUnavailable = errors.New("update unavailable")
	ErrUpdateBusy        = errors.New("update already running")
	ErrUpdateInvalid     = errors.New("invalid update tag")
)

// UpdateJob is the durable status written by the external host job.
type UpdateJob struct {
	Tag        string `json:"tag"`
	State      string `json:"state"`
	StartedAt  string `json:"started_at,omitempty"`
	FinishedAt string `json:"finished_at,omitempty"`
}

// UpdateRelease is one published, architecture-compatible release.
type UpdateRelease struct {
	Tag         string `json:"tag"`
	URL         string `json:"url"`
	PublishedAt string `json:"published_at"`
}

// UpdateStatus is the admin-facing installed and available version state.
type UpdateStatus struct {
	Installed  string         `json:"installed"`
	Configured bool           `json:"configured"`
	Release    *UpdateRelease `json:"release,omitempty"`
	Job        *UpdateJob     `json:"job,omitempty"`
	CheckError string         `json:"check_error,omitempty"`
	CanInstall bool           `json:"can_install"`
}

// SelfUpdate checks releases and requests a host-owned update job.
type SelfUpdate struct {
	helper     string
	statusPath string
	origin     string
	version    string
	platform   string
	releaseURL string
	client     *http.Client
	mu         sync.Mutex
	cached     *UpdateRelease
	cachedAt   time.Time
}

// NewSelfUpdate configures the optional console updater.
func NewSelfUpdate(helper, statusPath, origin, version string) *SelfUpdate {
	return &SelfUpdate{
		helper: helper, statusPath: statusPath, origin: origin, version: version,
		platform:   runtime.GOOS,
		releaseURL: touchGrassReleaseURL,
		client:     &http.Client{Timeout: 5 * time.Second},
	}
}

// Origin returns the single browser origin allowed to start an update.
func (u *SelfUpdate) Origin() string { return u.origin }

// Check returns job status even when upstream release discovery fails.
func (u *SelfUpdate) Check(ctx context.Context) UpdateStatus {
	status := UpdateStatus{Installed: u.version, Configured: u.configured()}
	if !status.Configured {
		return status
	}

	job, err := u.readJob()
	if err != nil {
		status.CheckError = "Update status is unavailable."
		return status
	}
	status.Job = job

	release, err := u.latest(ctx, false)
	if err != nil {
		status.CheckError = "Could not check GitHub releases. Retry shortly."
		return status
	}
	status.Release = release
	status.CanInstall = newer(release.Tag, u.version) && (job == nil || job.State == "succeeded" || job.State == "failed")
	return status
}

// Start requests one exact published release through the host helper.
func (u *SelfUpdate) Start(ctx context.Context, tag string) error {
	if !u.configured() {
		return ErrUpdateUnavailable
	}
	if !updateTagPattern.MatchString(tag) {
		return ErrUpdateInvalid
	}
	job, err := u.readJob()
	if err != nil {
		return fmt.Errorf("reading update status: %w", err)
	}
	if job != nil && (job.State == "pending" || job.State == "running") {
		return ErrUpdateBusy
	}
	release, err := u.latest(ctx, true)
	if err != nil {
		return fmt.Errorf("checking latest release: %w", err)
	}
	if release.Tag != tag || !newer(tag, u.version) {
		return ErrUpdateInvalid
	}
	command := exec.CommandContext(ctx, "sudo", "-n", u.helper, tag)
	if err := command.Run(); err != nil {
		return fmt.Errorf("starting update helper: %w", err)
	}
	return nil
}

func (u *SelfUpdate) configured() bool {
	if u.platform != "linux" || !filepath.IsAbs(u.helper) || !filepath.IsAbs(u.statusPath) || u.origin == "" {
		return false
	}
	info, err := os.Stat(u.helper)
	return err == nil && info.Mode().IsRegular() && info.Mode()&0o111 != 0
}

func (u *SelfUpdate) readJob() (*UpdateJob, error) {
	contents, err := os.ReadFile(u.statusPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading job file: %w", err)
	}
	var job UpdateJob
	if err := json.Unmarshal(contents, &job); err != nil {
		return nil, fmt.Errorf("decoding job file: %w", err)
	}
	if !updateTagPattern.MatchString(job.Tag) {
		return nil, ErrUpdateInvalid
	}
	switch job.State {
	case "pending", "running", "succeeded", "failed":
		return &job, nil
	default:
		return nil, fmt.Errorf("unknown update state")
	}
}

func (u *SelfUpdate) latest(ctx context.Context, force bool) (*UpdateRelease, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if !force && u.cached != nil && time.Since(u.cachedAt) < 2*time.Minute {
		return u.cached, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.releaseURL, nil)
	if err != nil {
		return nil, fmt.Errorf("creating release request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := u.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching release: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("release lookup returned %d", resp.StatusCode)
	}
	var body struct {
		Tag         string `json:"tag_name"`
		URL         string `json:"html_url"`
		PublishedAt string `json:"published_at"`
		Draft       bool   `json:"draft"`
		Prerelease  bool   `json:"prerelease"`
		Assets      []struct {
			Name string `json:"name"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&body); err != nil {
		return nil, fmt.Errorf("decoding release: %w", err)
	}
	if body.Draft || body.Prerelease || !updateTagPattern.MatchString(body.Tag) {
		return nil, ErrUpdateUnavailable
	}
	asset := "touchgrass_" + strings.TrimPrefix(body.Tag, "v") + "_linux_" + runtime.GOARCH + ".tar.gz"
	foundAsset, foundChecksum := false, false
	for _, item := range body.Assets {
		foundAsset = foundAsset || item.Name == asset
		foundChecksum = foundChecksum || item.Name == "checksums.txt"
	}
	if !foundAsset || !foundChecksum {
		return nil, ErrUpdateUnavailable
	}
	wantURL := "https://github.com/oralecarlangelo/touchgrass/releases/tag/" + body.Tag
	if body.URL != wantURL {
		return nil, ErrUpdateUnavailable
	}
	u.cached = &UpdateRelease{Tag: body.Tag, URL: body.URL, PublishedAt: body.PublishedAt}
	u.cachedAt = time.Now()
	return u.cached, nil
}

func newer(tag, installed string) bool {
	if !strings.HasPrefix(installed, "v") {
		installed = "v" + installed
	}
	if !updateTagPattern.MatchString(tag) || !updateTagPattern.MatchString(installed) {
		return false
	}
	left := strings.Split(strings.TrimPrefix(tag, "v"), ".")
	right := strings.Split(strings.TrimPrefix(installed, "v"), ".")
	for index := range left {
		a, errA := strconv.ParseUint(left[index], 10, 64)
		b, errB := strconv.ParseUint(right[index], 10, 64)
		if errA != nil || errB != nil {
			return false
		}
		if a != b {
			return a > b
		}
	}
	return false
}
