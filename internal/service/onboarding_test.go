package service

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"

	"github.com/oralecarlangelo/touchgrass/internal/docker"
	"github.com/oralecarlangelo/touchgrass/internal/model"
	"github.com/oralecarlangelo/touchgrass/internal/store"
)

// Repeated onboarding fixtures.
const (
	testOnboardWildcard = "0.0.0.0"
	testOnboardTarget   = "shop-web-1"
	testOnboardRootURL  = "http://127.0.0.1:4201/"
	testOnboardProject  = "shop"
	testOnboardHealth   = "http://127.0.0.1:4201/health"
	testOnboardScripts  = "/opt/touchgrass/scripts"
	testOnboardProto    = "tcp"
)

// suggestContainer builds a daemon container for onboarding tests.
func suggestContainer(
	name string,
	labels map[string]string,
	ports ...docker.PublishedPort,
) docker.Container {
	published := []docker.PublishedPort{}
	published = append(published, ports...)

	return docker.Container{
		ID:        name + "-id",
		Name:      name,
		Image:     "shop:test",
		State:     testRunningState,
		Published: published,
		Labels:    labels,
	}
}

// composeLabels builds project/service/working-dir labels.
func composeLabels(project, service, dir string) map[string]string {
	return map[string]string{
		docker.LabelComposeProject:    project,
		docker.LabelComposeService:    service,
		docker.LabelComposeWorkingDir: dir,
	}
}

// stubDirEntry is a fake fs.DirEntry for nginx scans.
type stubDirEntry struct {
	name  string
	isDir bool
}

func (e stubDirEntry) Name() string {
	return e.name
}

func (e stubDirEntry) IsDir() bool {
	return e.isDir
}

func (e stubDirEntry) Type() fs.FileMode {
	return 0
}

func (e stubDirEntry) Info() (fs.FileInfo, error) {
	return nil, errors.New("no info")
}

// stubNginx wires fake nginx dir readers over files.
func stubNginx(files map[string]string, readErr error) (func(string) ([]fs.DirEntry, error), func(string) ([]byte, error)) {
	readDir := func(_ string) ([]fs.DirEntry, error) {
		if readErr != nil {
			return nil, readErr
		}

		entries := []fs.DirEntry{}

		for name := range files {
			entries = append(entries, stubDirEntry{name: name})
		}

		return entries, nil
	}

	readFile := func(path string) ([]byte, error) {
		content, ok := files[filepath.Base(path)]
		if !ok {
			return nil, errors.New("no such file")
		}

		return []byte(content), nil
	}

	return readDir, readFile
}

// suggestCase is one Suggest scenario.
type suggestCase struct {
	name           string
	containers     []docker.Container
	suggestName    string
	healthy        map[string]bool
	nginxFiles     map[string]string
	nginxErr       error
	seedExtra      []model.Service
	wantErr        error
	wantStrategy   model.Strategy
	wantID         string
	wantHealth     string
	wantConfidence string
	wantWarnings   []string
	wantReasons    []string
}

func TestOnboardingSuggestDraft(t *testing.T) {
	t.Parallel()

	shopLabels := func() map[string]string { return composeLabels(testOnboardProject, "web", "/opt/shop") }

	tests := []suggestCase{
		{
			name: "recreate happy path on root",
			containers: []docker.Container{
				suggestContainer(testOnboardTarget, shopLabels(),
					docker.PublishedPort{HostIP: testOnboardWildcard, HostPort: 4201, ContainerPort: 80, Proto: testOnboardProto}),
			},
			suggestName:    testOnboardTarget,
			healthy:        map[string]bool{testOnboardRootURL: true},
			wantStrategy:   model.StrategyRecreate,
			wantID:         testOnboardProject,
			wantHealth:     testOnboardRootURL,
			wantConfidence: model.ConfidenceHigh,
			wantReasons:    []string{`service id "shop" is available`, "probed http://127.0.0.1:4201/ (2xx)"},
		},
		{
			name: "health path fallback",
			containers: []docker.Container{
				suggestContainer(testOnboardTarget, shopLabels(),
					docker.PublishedPort{HostIP: "127.0.0.1", HostPort: 4201, ContainerPort: 80, Proto: testOnboardProto}),
			},
			suggestName:    testOnboardTarget,
			healthy:        map[string]bool{testOnboardHealth: true},
			wantStrategy:   model.StrategyRecreate,
			wantID:         testOnboardProject,
			wantHealth:     testOnboardHealth,
			wantConfidence: model.ConfidenceHigh,
		},
		{
			name: "lowest published port wins",
			containers: []docker.Container{
				suggestContainer(testOnboardTarget, shopLabels(),
					docker.PublishedPort{HostIP: testOnboardWildcard, HostPort: 4300, ContainerPort: 81, Proto: testOnboardProto},
					docker.PublishedPort{HostIP: testOnboardWildcard, HostPort: 4201, ContainerPort: 80, Proto: testOnboardProto}),
			},
			suggestName:    testOnboardTarget,
			healthy:        map[string]bool{testOnboardRootURL: true},
			wantStrategy:   model.StrategyRecreate,
			wantID:         testOnboardProject,
			wantHealth:     testOnboardRootURL,
			wantConfidence: model.ConfidenceHigh,
		},
		{
			name: "id falls back to project-service",
			containers: []docker.Container{
				suggestContainer(testOnboardTarget, composeLabels("tn-fe", "web", "/opt/shop")),
			},
			suggestName:    testOnboardTarget,
			wantStrategy:   model.StrategyRecreate,
			wantID:         "tn-fe-web",
			wantConfidence: model.ConfidenceMedium,
		},
		{
			name: "id sanitized to id shape",
			containers: []docker.Container{
				suggestContainer(testOnboardTarget, composeLabels("Shop_App", "web", "/opt/shop")),
			},
			suggestName:    testOnboardTarget,
			wantStrategy:   model.StrategyRecreate,
			wantID:         "shop-app",
			wantConfidence: model.ConfidenceMedium,
		},
		{
			name: "no published ports",
			containers: []docker.Container{
				suggestContainer(testOnboardTarget, shopLabels()),
			},
			suggestName:    testOnboardTarget,
			wantStrategy:   model.StrategyRecreate,
			wantID:         testOnboardProject,
			wantHealth:     "",
			wantConfidence: model.ConfidenceMedium,
			wantWarnings:   []string{"publishes no host ports"},
		},
		{
			name: "probe misses",
			containers: []docker.Container{
				suggestContainer(testOnboardTarget, shopLabels(),
					docker.PublishedPort{HostIP: testOnboardWildcard, HostPort: 4201, ContainerPort: 80, Proto: testOnboardProto}),
			},
			suggestName:    testOnboardTarget,
			healthy:        map[string]bool{},
			wantStrategy:   model.StrategyRecreate,
			wantID:         testOnboardProject,
			wantHealth:     "",
			wantConfidence: model.ConfidenceMedium,
			wantWarnings:   []string{"no 2xx from port 4201"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			runSuggestCase(t, tt)
		})
	}
}

func TestOnboardingSuggestEdges(t *testing.T) {
	t.Parallel()

	shopLabels := func() map[string]string { return composeLabels(testOnboardProject, "web", "/opt/shop") }

	tests := []suggestCase{
		{
			name: "both ids taken",
			containers: []docker.Container{
				suggestContainer(testOnboardTarget, shopLabels()),
			},
			suggestName: testOnboardTarget,
			seedExtra: []model.Service{
				{ID: testOnboardProject, Name: testOnboardProject, Strategy: model.StrategyRecreate},
				{ID: "shop-web", Name: "shop-web", Strategy: model.StrategyRecreate},
			},
			wantStrategy:   model.StrategyRecreate,
			wantID:         "",
			wantConfidence: model.ConfidenceLow,
			wantWarnings:   []string{`"shop" and "shop-web" are taken`},
		},
		{
			name:        "unknown container",
			containers:  []docker.Container{suggestContainer(testOnboardTarget, shopLabels())},
			suggestName: "nope",
			wantErr:     ErrInvalidInput,
		},
		{
			name:        "empty container name",
			containers:  []docker.Container{suggestContainer(testOnboardTarget, shopLabels())},
			suggestName: "",
			wantErr:     ErrInvalidInput,
		},
		{
			name: "already managed blue-green",
			containers: []docker.Container{
				suggestContainer("ticketnation-api-blue-1",
					composeLabels("ticketnation", "api-blue", "/opt/ticketnation")),
			},
			suggestName: "ticketnation-api-blue-1",
			wantErr:     ErrConflict,
		},
		{
			name: "already managed recreate",
			containers: []docker.Container{
				suggestContainer("fe-1", composeLabels("ticketnation-fe", "fe", "/opt/ticketnation-fe")),
			},
			suggestName: "fe-1",
			wantErr:     ErrConflict,
		},
		{
			name: "missing labels",
			containers: []docker.Container{
				suggestContainer("lonely-1", map[string]string{}),
			},
			suggestName:    "lonely-1",
			wantStrategy:   model.StrategyRecreate,
			wantID:         "",
			wantConfidence: model.ConfidenceLow,
			wantWarnings: []string{
				"no compose project label",
				"no compose service label",
				"no compose working-dir label",
				"cannot draft a service id",
			},
		},
		{
			name: "lone blue without mate stays recreate",
			containers: []docker.Container{
				suggestContainer("shop-fe-blue-1", composeLabels(testOnboardProject, "fe-blue", "/opt/shop"),
					docker.PublishedPort{HostIP: testOnboardWildcard, HostPort: 4201, ContainerPort: 80, Proto: testOnboardProto}),
			},
			suggestName:    "shop-fe-blue-1",
			healthy:        map[string]bool{testOnboardHealth: true},
			wantStrategy:   model.StrategyRecreate,
			wantID:         testOnboardProject,
			wantHealth:     testOnboardHealth,
			wantConfidence: model.ConfidenceHigh,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			runSuggestCase(t, tt)
		})
	}
}

// runSuggestCase runs one Suggest scenario.
func runSuggestCase(t *testing.T, tt suggestCase) {
	t.Helper()

	db := openInventoryDB(t)
	ctx := context.Background()
	services := store.NewServiceStore(db)

	for _, extra := range tt.seedExtra {
		if err := services.Create(ctx, extra); err != nil {
			t.Fatalf("Create(seed) error = %v, want nil", err)
		}
	}

	readDir, readFile := stubNginx(tt.nginxFiles, tt.nginxErr)

	onboarding := NewOnboarding(OnboardingConfig{
		Services:   services,
		Docker:     stubLister{containers: tt.containers},
		Prober:     stubProber{healthy: tt.healthy},
		Audit:      NewAudit(services, store.NewAuditStore(db)),
		ScriptsDir: testOnboardScripts,
		NginxDir:   "/etc/nginx/sites-enabled",
		ReadDir:    readDir,
		ReadFile:   readFile,
	})

	got, err := onboarding.Suggest(ctx, tt.suggestName)
	if tt.wantErr != nil {
		if !errors.Is(err, tt.wantErr) {
			t.Fatalf("Suggest() error = %v, want %v", err, tt.wantErr)
		}

		return
	}

	if err != nil {
		t.Fatalf("Suggest() error = %v, want nil", err)
	}

	if got.Strategy != tt.wantStrategy {
		t.Errorf("Suggest() strategy = %q, want %q", got.Strategy, tt.wantStrategy)
	}

	if got.ServiceID != tt.wantID {
		t.Errorf("Suggest() service_id = %q, want %q", got.ServiceID, tt.wantID)
	}

	if got.HealthURL != tt.wantHealth {
		t.Errorf("Suggest() health_url = %q, want %q", got.HealthURL, tt.wantHealth)
	}

	if got.Confidence != tt.wantConfidence {
		t.Errorf("Suggest() confidence = %q, want %q", got.Confidence, tt.wantConfidence)
	}

	if got.Reasons == nil || got.Warnings == nil {
		t.Error("Suggest() has nil reasons or warnings (must never be null)")
	}

	requireContains(t, got.Warnings, tt.wantWarnings)
	requireContains(t, got.Reasons, tt.wantReasons)
}

func TestOnboardingSuggestRecreateScripts(t *testing.T) {
	t.Parallel()

	db := openInventoryDB(t)
	ctx := context.Background()
	services := store.NewServiceStore(db)
	readDir, readFile := stubNginx(map[string]string{}, nil)

	onboarding := NewOnboarding(OnboardingConfig{
		Services: services,
		Docker: stubLister{containers: []docker.Container{
			suggestContainer(testOnboardTarget, composeLabels(testOnboardProject, "web", "/opt/shop")),
		}},
		Prober:     stubProber{},
		Audit:      NewAudit(services, store.NewAuditStore(db)),
		ScriptsDir: testOnboardScripts,
		ReadDir:    readDir,
		ReadFile:   readFile,
	})

	got, err := onboarding.Suggest(ctx, testOnboardTarget)
	if err != nil {
		t.Fatalf("Suggest() error = %v, want nil", err)
	}

	if got.DeployScript != "/opt/touchgrass/scripts/recreate-deploy.sh" {
		t.Errorf("Suggest() deploy_script = %q, want default recreate script", got.DeployScript)
	}

	if got.RollbackScript != "/opt/touchgrass/scripts/recreate-rollback.sh" {
		t.Errorf("Suggest() rollback_script = %q, want default recreate script", got.RollbackScript)
	}

	if got.PublicURL != "" {
		t.Errorf("Suggest() public_url = %q, want blank for the operator", got.PublicURL)
	}
}

func TestOnboardingSuggestBlueGreen(t *testing.T) {
	t.Parallel()

	containers := []docker.Container{
		suggestContainer("shop-fe-blue-1", composeLabels(testOnboardProject, "fe-blue", "/opt/shop"),
			docker.PublishedPort{HostIP: testOnboardWildcard, HostPort: 4201, ContainerPort: 80, Proto: testOnboardProto}),
		suggestContainer("shop-fe-green-1", composeLabels(testOnboardProject, "fe-green", "/opt/shop"),
			docker.PublishedPort{HostIP: testOnboardWildcard, HostPort: 4202, ContainerPort: 80, Proto: testOnboardProto}),
	}
	healthy := map[string]bool{
		testOnboardHealth:              true,
		"http://127.0.0.1:4202/health": true,
	}
	nginxFiles := map[string]string{
		"shop": "upstream fe {\n    server 127.0.0.1:4201; # BLUEGREEN-ACTIVE\n}\n",
	}

	db := openInventoryDB(t)
	ctx := context.Background()
	services := store.NewServiceStore(db)
	readDir, readFile := stubNginx(nginxFiles, nil)

	onboarding := NewOnboarding(OnboardingConfig{
		Services:   services,
		Docker:     stubLister{containers: containers},
		Prober:     stubProber{healthy: healthy},
		Audit:      NewAudit(services, store.NewAuditStore(db)),
		ScriptsDir: testOnboardScripts,
		NginxDir:   "/etc/nginx/sites-enabled",
		ReadDir:    readDir,
		ReadFile:   readFile,
	})

	got, err := onboarding.Suggest(ctx, "shop-fe-blue-1")
	if err != nil {
		t.Fatalf("Suggest() error = %v, want nil", err)
	}

	if got.Strategy != model.StrategyBlueGreen {
		t.Fatalf("Suggest() strategy = %q, want bluegreen", got.Strategy)
	}

	if got.BlueService != "fe-blue" || got.GreenService != "fe-green" {
		t.Errorf("Suggest() services = (%q, %q), want (fe-blue, fe-green)", got.BlueService, got.GreenService)
	}

	if got.BlueTarget != "127.0.0.1:4201" || got.GreenTarget != "127.0.0.1:4202" {
		t.Errorf("Suggest() targets = (%q, %q), want color host ports", got.BlueTarget, got.GreenTarget)
	}

	if got.BlueURL != testOnboardHealth || got.GreenURL != "http://127.0.0.1:4202/health" {
		t.Errorf("Suggest() urls = (%q, %q), want probed color urls", got.BlueURL, got.GreenURL)
	}

	if got.NginxConf != "/etc/nginx/sites-enabled/shop" {
		t.Errorf("Suggest() nginx_conf = %q, want scanned sites file", got.NginxConf)
	}

	if got.Marker != "# BLUEGREEN-ACTIVE" {
		t.Errorf("Suggest() marker = %q, want default marker", got.Marker)
	}

	if got.Confidence != model.ConfidenceMedium {
		t.Errorf("Suggest() confidence = %q, want medium (cutover stays manual)", got.Confidence)
	}

	requireContains(t, got.Warnings, []string{"fork the team cutover script"})
	requireContains(t, got.Reasons, []string{`pair "fe-blue" and "fe-green"`, "nginx marker found"})
}

func TestOnboardingSuggestBlueGreenNginxFallbacks(t *testing.T) {
	t.Parallel()

	containers := []docker.Container{
		suggestContainer("shop-fe-blue-1", composeLabels(testOnboardProject, "fe-blue", "/opt/shop"),
			docker.PublishedPort{HostIP: testOnboardWildcard, HostPort: 4201, ContainerPort: 80, Proto: testOnboardProto}),
		suggestContainer("shop-fe-green-1", composeLabels(testOnboardProject, "fe-green", "/opt/shop")),
	}

	tests := []struct {
		name         string
		nginxFiles   map[string]string
		nginxErr     error
		wantWarnings []string
	}{
		{
			name:         "unreadable sites dir",
			nginxErr:     errors.New("permission denied"),
			wantWarnings: []string{"nginx scan skipped"},
		},
		{
			name:         "no marker match",
			nginxFiles:   map[string]string{"shop": "upstream fe {\n    server 127.0.0.1:4201;\n}\n"},
			wantWarnings: []string{"no BLUEGREEN-ACTIVE marker"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			db := openInventoryDB(t)
			ctx := context.Background()
			services := store.NewServiceStore(db)
			readDir, readFile := stubNginx(tt.nginxFiles, tt.nginxErr)

			onboarding := NewOnboarding(OnboardingConfig{
				Services:   services,
				Docker:     stubLister{containers: containers},
				Prober:     stubProber{},
				Audit:      NewAudit(services, store.NewAuditStore(db)),
				ScriptsDir: testOnboardScripts,
				NginxDir:   "/etc/nginx/sites-enabled",
				ReadDir:    readDir,
				ReadFile:   readFile,
			})

			got, err := onboarding.Suggest(ctx, "shop-fe-blue-1")
			if err != nil {
				t.Fatalf("Suggest() error = %v, want nil", err)
			}

			if got.Strategy != model.StrategyBlueGreen {
				t.Fatalf("Suggest() strategy = %q, want bluegreen", got.Strategy)
			}

			if got.NginxConf != "" {
				t.Errorf("Suggest() nginx_conf = %q, want blank", got.NginxConf)
			}

			if got.GreenTarget != "" {
				t.Errorf("Suggest() green_target = %q, want blank without a published port", got.GreenTarget)
			}

			if got.Confidence != model.ConfidenceLow {
				t.Errorf("Suggest() confidence = %q, want low without nginx conf", got.Confidence)
			}

			wants := append([]string{"green publishes no host port"}, tt.wantWarnings...)
			requireContains(t, got.Warnings, wants)
		})
	}
}

// requireContains fails when any substring is missing from list.
func requireContains(t *testing.T, list, wants []string) {
	t.Helper()

	for _, want := range wants {
		found := false

		for _, item := range list {
			if strings.Contains(item, want) {
				found = true

				break
			}
		}

		if !found {
			t.Errorf("list %q is missing %q", list, want)
		}
	}
}

// Script placeholders expand to temp executable paths per subtest, since
// no fixed executable path exists across macOS, Linux, and CI.
const (
	placeholderDeploy   = "@@DEPLOY@@"
	placeholderRollback = "@@ROLLBACK@@"
	placeholderCutover  = "@@CUTOVER@@"
	placeholderFile     = "@@FILE@@"
)

// validRecreateInput builds a passing recreate creation input.
func validRecreateInput(dir string) CreateInput {
	return CreateInput{
		ID:             "shop-web",
		Strategy:       model.StrategyRecreate,
		ComposeProject: "shop",
		ComposeDir:     dir,
		Config: json.RawMessage(`{"service":"web","health_url":"http://127.0.0.1:4201/health",` +
			`"deploy_script":"` + placeholderDeploy + `","rollback_script":"` + placeholderRollback + `"}`),
	}
}

// createScripts holds temp script paths for creation tests.
type createScripts struct {
	deploy   string
	rollback string
	cutover  string
	plain    string
}

// writeCreateScripts writes executable stubs plus one plain file.
func writeCreateScripts(t *testing.T) createScripts {
	t.Helper()

	const script = "#!/bin/sh\nexit 0\n"

	return createScripts{
		deploy:   writeTempFile(t, script, 0o755),
		rollback: writeTempFile(t, script, 0o755),
		cutover:  writeTempFile(t, script, 0o755),
		plain:    writeTempFile(t, "plain", 0o644),
	}
}

// expandPlaceholders resolves script placeholders in input.
func expandPlaceholders(input *CreateInput, scripts createScripts) {
	expand := func(raw string) string {
		raw = strings.ReplaceAll(raw, placeholderDeploy, scripts.deploy)
		raw = strings.ReplaceAll(raw, placeholderRollback, scripts.rollback)
		raw = strings.ReplaceAll(raw, placeholderCutover, scripts.cutover)
		raw = strings.ReplaceAll(raw, placeholderFile, scripts.plain)

		return raw
	}

	input.ComposeDir = expand(input.ComposeDir)
	input.Config = json.RawMessage(expand(string(input.Config)))
}

// createCase is one Create scenario.
type createCase struct {
	name      string
	mutate    func(*CreateInput)
	composeOK bool
	wantErr   error
	wantMsg   string
}

func TestOnboardingCreateRecreate(t *testing.T) {
	t.Parallel()

	tests := []createCase{
		{
			name:      "recreate happy path",
			mutate:    func(*CreateInput) {},
			composeOK: true,
		},
		{
			name:      "bad id",
			mutate:    func(in *CreateInput) { in.ID = "Shop_Web!" },
			composeOK: true,
			wantErr:   ErrInvalidInput,
			wantMsg:   `id "Shop_Web!"`,
		},
		{
			name:      "empty id",
			mutate:    func(in *CreateInput) { in.ID = "" },
			composeOK: true,
			wantErr:   ErrInvalidInput,
			wantMsg:   "want ^[a-z0-9-]+$",
		},
		{
			name:      "duplicate id",
			mutate:    func(in *CreateInput) { in.ID = testServiceAPI },
			composeOK: true,
			wantErr:   ErrConflict,
			wantMsg:   "already exists",
		},
		{
			name:      "bad strategy",
			mutate:    func(in *CreateInput) { in.Strategy = "canary" },
			composeOK: true,
			wantErr:   ErrInvalidInput,
			wantMsg:   "want recreate or bluegreen",
		},
		{
			name:      "empty project",
			mutate:    func(in *CreateInput) { in.ComposeProject = "" },
			composeOK: true,
			wantErr:   ErrInvalidInput,
			wantMsg:   "compose_project is required",
		},
		{
			name:      "missing compose dir",
			mutate:    func(*CreateInput) {},
			composeOK: false,
			wantErr:   ErrInvalidInput,
			wantMsg:   "is not a directory",
		},
		{
			name: "compose dir is a file",
			mutate: func(in *CreateInput) {
				in.ComposeDir = placeholderFile
			},
			composeOK: true,
			wantErr:   ErrInvalidInput,
			wantMsg:   "is not a directory",
		},
		{
			name:      "malformed config",
			mutate:    func(in *CreateInput) { in.Config = json.RawMessage(`{oops`) },
			composeOK: true,
			wantErr:   ErrInvalidInput,
			wantMsg:   "invalid recreate config",
		},
		{
			name: "missing health url",
			mutate: func(in *CreateInput) {
				in.Config = json.RawMessage(`{"service":"web","deploy_script":"` + placeholderDeploy + `",` +
					`"rollback_script":"` + placeholderRollback + `"}`)
			},
			composeOK: true,
			wantErr:   ErrInvalidInput,
			wantMsg:   "missing health_url",
		},
		{
			name: "missing rollback script",
			mutate: func(in *CreateInput) {
				in.Config = json.RawMessage(`{"service":"web","health_url":"http://x/",` +
					`"deploy_script":"` + placeholderDeploy + `"}`)
			},
			composeOK: true,
			wantErr:   ErrInvalidInput,
			wantMsg:   "missing rollback_script",
		},
		{
			name: "nonexecutable script",
			mutate: func(in *CreateInput) {
				in.Config = json.RawMessage(`{"service":"web","health_url":"http://x/",` +
					`"deploy_script":"` + placeholderDeploy + `","rollback_script":"/nope/missing.sh"}`)
			},
			composeOK: true,
			wantErr:   ErrInvalidInput,
			wantMsg:   "rollback script",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			runCreateCase(t, tt)
		})
	}
}

func TestOnboardingCreateBlueGreen(t *testing.T) {
	t.Parallel()

	tests := []createCase{
		{
			name: "bluegreen happy path",
			mutate: func(in *CreateInput) {
				in.Strategy = model.StrategyBlueGreen
				in.Config = json.RawMessage(`{"blue_service":"fe-blue","green_service":"fe-green",` +
					`"blue_target":"127.0.0.1:4201","green_target":"127.0.0.1:4202",` +
					`"blue_url":"http://127.0.0.1:4201/health","green_url":"http://127.0.0.1:4202/health",` +
					`"nginx_conf":"/etc/nginx/sites-enabled/shop","marker":"# BLUEGREEN-ACTIVE",` +
					`"cutover_script":"` + placeholderCutover + `"}`)
			},
			composeOK: true,
		},
		{
			name: "bluegreen missing nginx conf",
			mutate: func(in *CreateInput) {
				in.Strategy = model.StrategyBlueGreen
				in.Config = json.RawMessage(`{"blue_service":"fe-blue","green_service":"fe-green",` +
					`"blue_target":"127.0.0.1:4201","green_target":"127.0.0.1:4202",` +
					`"blue_url":"http://127.0.0.1:4201/health","green_url":"http://127.0.0.1:4202/health",` +
					`"marker":"# BLUEGREEN-ACTIVE","cutover_script":"` + placeholderCutover + `"}`)
			},
			composeOK: true,
			wantErr:   ErrInvalidInput,
			wantMsg:   "missing nginx_conf",
		},
		{
			name: "bluegreen bad restore script",
			mutate: func(in *CreateInput) {
				in.Strategy = model.StrategyBlueGreen
				in.Config = json.RawMessage(`{"blue_service":"fe-blue","green_service":"fe-green",` +
					`"blue_target":"127.0.0.1:4201","green_target":"127.0.0.1:4202",` +
					`"blue_url":"http://127.0.0.1:4201/health","green_url":"http://127.0.0.1:4202/health",` +
					`"nginx_conf":"/etc/nginx/sites-enabled/shop","marker":"# BLUEGREEN-ACTIVE",` +
					`"cutover_script":"` + placeholderCutover + `","restore_script":"/nope/missing.sh"}`)
			},
			composeOK: true,
			wantErr:   ErrInvalidInput,
			wantMsg:   "restore script",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			runCreateCase(t, tt)
		})
	}
}

// runCreateCase runs one Create scenario.
func runCreateCase(t *testing.T, tt createCase) {
	t.Helper()

	db := openInventoryDB(t)
	ctx := context.Background()
	services := store.NewServiceStore(db)

	scripts := writeCreateScripts(t)

	dir := t.TempDir()
	if !tt.composeOK {
		dir = filepath.Join(dir, "missing")
	}

	input := validRecreateInput(dir)
	tt.mutate(&input)
	expandPlaceholders(&input, scripts)

	onboarding := NewOnboarding(OnboardingConfig{
		Services:   services,
		Docker:     stubLister{},
		Prober:     stubProber{},
		Audit:      NewAudit(services, store.NewAuditStore(db)),
		ScriptsDir: t.TempDir(),
	})

	got, err := onboarding.Create(ctx, input, testAdminActor)
	if tt.wantErr != nil {
		if !errors.Is(err, tt.wantErr) {
			t.Fatalf("Create() error = %v, want %v", err, tt.wantErr)
		}

		if !strings.Contains(err.Error(), tt.wantMsg) {
			t.Errorf("Create() error = %q, want substring %q", err.Error(), tt.wantMsg)
		}

		return
	}

	if err != nil {
		t.Fatalf("Create() error = %v, want nil", err)
	}

	if got.ID != input.ID || got.Strategy != input.Strategy {
		t.Errorf("Create() = (%q, %q), want (%q, %q)", got.ID, got.Strategy, input.ID, input.Strategy)
	}

	stored, err := services.Get(ctx, input.ID)
	if err != nil {
		t.Fatalf("Get() error = %v, want nil", err)
	}

	if stored.Name != input.ID {
		t.Errorf("stored name = %q, want id default %q", stored.Name, input.ID)
	}
}

func TestOnboardingCreateAudits(t *testing.T) {
	t.Parallel()

	db := openInventoryDB(t)
	ctx := context.Background()
	services := store.NewServiceStore(db)
	audit := NewAudit(services, store.NewAuditStore(db))

	onboarding := NewOnboarding(OnboardingConfig{
		Services:   services,
		Docker:     stubLister{},
		Prober:     stubProber{},
		Audit:      audit,
		ScriptsDir: t.TempDir(),
	})

	scripts := writeCreateScripts(t)

	input := validRecreateInput(t.TempDir())
	input.Name = "Shop Web"
	expandPlaceholders(&input, scripts)

	if _, err := onboarding.Create(ctx, input, testAdminActor); err != nil {
		t.Fatalf("Create() error = %v, want nil", err)
	}

	entries, err := audit.List(ctx, input.ID, 10)
	if err != nil {
		t.Fatalf("List() error = %v, want nil", err)
	}

	if len(entries) != 1 {
		t.Fatalf("List() = %d entries, want 1", len(entries))
	}

	entry := entries[0]

	if entry.Action != model.AuditServiceCreate || entry.Result != model.AuditSuccess {
		t.Errorf("audit = (%q, %q), want (service_create, success)", entry.Action, entry.Result)
	}

	if entry.Actor != testAdminActor {
		t.Errorf("audit actor = %q, want %q", entry.Actor, testAdminActor)
	}

	if _, err := onboarding.Create(ctx, input, ""); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("Create(empty actor) error = %v, want ErrInvalidInput", err)
	}
}

// TestOnboardingNonexecutableScript proves script checks reject plain files.
func TestOnboardingNonexecutableScript(t *testing.T) {
	t.Parallel()

	db := openInventoryDB(t)
	ctx := context.Background()
	services := store.NewServiceStore(db)

	onboarding := NewOnboarding(OnboardingConfig{
		Services:   services,
		Docker:     stubLister{},
		Prober:     stubProber{},
		Audit:      NewAudit(services, store.NewAuditStore(db)),
		ScriptsDir: t.TempDir(),
	})

	scripts := writeCreateScripts(t)

	input := validRecreateInput(t.TempDir())
	input.Config = json.RawMessage(`{"service":"web","health_url":"http://x/",` +
		`"deploy_script":"` + scripts.plain + `","rollback_script":"` + scripts.rollback + `"}`)

	if _, err := onboarding.Create(ctx, input, testAdminActor); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("Create() error = %v, want ErrInvalidInput", err)
	}
}

func TestOnboardingDelete(t *testing.T) {
	t.Parallel()

	newDeleteOnboarding := func(t *testing.T) (*Onboarding, *store.ServiceStore) {
		t.Helper()

		db := openInventoryDB(t)
		services := store.NewServiceStore(db)

		return NewOnboarding(OnboardingConfig{
			Services:   services,
			Docker:     stubLister{},
			Prober:     stubProber{},
			Audit:      NewAudit(services, store.NewAuditStore(db)),
			ScriptsDir: t.TempDir(),
		}), services
	}

	t.Run("removes service and audits globally", func(t *testing.T) {
		t.Parallel()

		onboarding, services := newDeleteOnboarding(t)
		ctx := context.Background()

		if err := onboarding.Delete(ctx, testServiceAPI, testAdminActor); err != nil {
			t.Fatalf("Delete() error = %v, want nil", err)
		}

		if _, err := services.Get(ctx, testServiceAPI); !errors.Is(err, store.ErrServiceNotFound) {
			t.Errorf("Get() after delete error = %v, want ErrServiceNotFound", err)
		}
	})

	t.Run("unknown id", func(t *testing.T) {
		t.Parallel()

		onboarding, _ := newDeleteOnboarding(t)

		if err := onboarding.Delete(context.Background(), "nope", testAdminActor); !errors.Is(
			err,
			store.ErrServiceNotFound,
		) {
			t.Errorf("Delete() error = %v, want ErrServiceNotFound", err)
		}
	})

	t.Run("bad id shape", func(t *testing.T) {
		t.Parallel()

		onboarding, _ := newDeleteOnboarding(t)

		if err := onboarding.Delete(context.Background(), "Shop_Web!", testAdminActor); !errors.Is(
			err,
			ErrInvalidInput,
		) {
			t.Errorf("Delete() error = %v, want ErrInvalidInput", err)
		}
	})

	t.Run("missing actor", func(t *testing.T) {
		t.Parallel()

		onboarding, _ := newDeleteOnboarding(t)

		if err := onboarding.Delete(context.Background(), testServiceAPI, ""); !errors.Is(
			err,
			ErrInvalidInput,
		) {
			t.Errorf("Delete() error = %v, want ErrInvalidInput", err)
		}
	})
}
