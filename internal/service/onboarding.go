package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/oralecarlangelo/touchgrass/internal/docker"
	"github.com/oralecarlangelo/touchgrass/internal/model"
	"github.com/oralecarlangelo/touchgrass/internal/store"
)

const (
	// defaultNginxSitesDir holds the live nginx server files scanned for
	// blue-green markers.
	defaultNginxSitesDir = "/etc/nginx/sites-enabled"
	// blueGreenMarker is the default active-target marker line.
	blueGreenMarker = "# BLUEGREEN-ACTIVE"
	// suggestProbeTimeout bounds one onboarding health probe.
	suggestProbeTimeout = 3 * time.Second
	// recreateDeployScript is the default recreate deploy script name.
	recreateDeployScript = "recreate-deploy.sh"
	// recreateRollbackScript is the default recreate rollback script name.
	recreateRollbackScript = "recreate-rollback.sh"
	// colorBlueSuffix pairs with colorGreenSuffix for detection.
	colorBlueSuffix = "-blue"
	// colorGreenSuffix pairs with colorBlueSuffix for detection.
	colorGreenSuffix = "-green"
	// probePathRoot is tried before the health path.
	probePathRoot = "/"
	// probePathHealth is tried after the root path.
	probePathHealth = "/health"
	// minHealthyStatus is the lowest 2xx probe win.
	minHealthyStatus = 200
	// maxHealthyStatus is the highest 2xx probe win.
	maxHealthyStatus = 299
)

// serviceIDPattern constrains created service ids.
var serviceIDPattern = regexp.MustCompile(`^[a-z0-9-]+$`)

// OnboardingConfig wires an Onboarding service. NginxDir defaults to the
// live sites directory; ReadDir and ReadFile default to the os pair and
// exist so tests can stub nginx without touching the filesystem.
type OnboardingConfig struct {
	Services   *store.ServiceStore
	Docker     docker.Lister
	Prober     Prober
	Audit      *Audit
	ScriptsDir string
	NginxDir   string
	ReadDir    func(string) ([]fs.DirEntry, error)
	ReadFile   func(string) ([]byte, error)
}

// Onboarding drafts and creates managed service rows.
type Onboarding struct {
	services   *store.ServiceStore
	docker     docker.Lister
	prober     Prober
	audit      *Audit
	scriptsDir string
	nginxDir   string
	readDir    func(string) ([]fs.DirEntry, error)
	readFile   func(string) ([]byte, error)
}

// NewOnboarding builds an Onboarding service.
func NewOnboarding(cfg OnboardingConfig) *Onboarding {
	nginxDir := cfg.NginxDir
	if nginxDir == "" {
		nginxDir = defaultNginxSitesDir
	}

	readDir := cfg.ReadDir
	if readDir == nil {
		readDir = os.ReadDir
	}

	readFile := cfg.ReadFile
	if readFile == nil {
		readFile = os.ReadFile
	}

	return &Onboarding{
		services:   cfg.Services,
		docker:     cfg.Docker,
		prober:     cfg.Prober,
		audit:      cfg.Audit,
		scriptsDir: cfg.ScriptsDir,
		nginxDir:   nginxDir,
		readDir:    readDir,
		readFile:   readFile,
	}
}

// Suggest drafts a service row for the named daemon container. Unknown
// names are invalid input; containers already owned by a service row
// conflict.
func (o *Onboarding) Suggest(ctx context.Context, name string) (model.ServiceSuggestion, error) {
	if name == "" {
		return model.ServiceSuggestion{}, fmt.Errorf("%w: container query is required", ErrInvalidInput)
	}

	containers, err := o.docker.List(ctx)
	if err != nil {
		return model.ServiceSuggestion{}, fmt.Errorf("listing containers: %w", err)
	}

	found, ok := findContainer(containers, name)
	if !ok {
		return model.ServiceSuggestion{}, fmt.Errorf("%w: unknown container %q", ErrInvalidInput, name)
	}

	defs, err := o.services.All(ctx)
	if err != nil {
		return model.ServiceSuggestion{}, fmt.Errorf("loading services: %w", err)
	}

	project := found.Labels[docker.LabelComposeProject]
	svc := found.Labels[docker.LabelComposeService]

	if project != "" && svc != "" {
		if owner, managed := managedBy(defs, project, svc); managed {
			return model.ServiceSuggestion{},
				fmt.Errorf("%w: container already managed by service %q", ErrConflict, owner)
		}
	}

	draft := newDraft(name, found)

	id, warning, err := o.draftServiceID(ctx, project, svc)
	if err != nil {
		return model.ServiceSuggestion{}, err
	}

	draft.ServiceID = id

	switch {
	case warning != "":
		draft.Warnings = append(draft.Warnings, warning)
	case id != "":
		draft.Reasons = append(draft.Reasons, "service id "+strconv.Quote(id)+" is available")
	}

	if base, paired := colorPair(containers, project, svc); paired {
		o.draftBlueGreen(ctx, &draft, containers, project, base)
	} else {
		o.draftRecreate(ctx, &draft, found)
	}

	draft.Confidence = confidence(draft, project, svc, found.Labels[docker.LabelComposeWorkingDir])

	return draft, nil
}

// draftRecreate fills the recreate fields: probed health URL plus default
// script paths.
func (o *Onboarding) draftRecreate(ctx context.Context, draft *model.ServiceSuggestion, found docker.Container) {
	draft.Strategy = model.StrategyRecreate
	draft.DeployScript = filepath.Join(o.scriptsDir, recreateDeployScript)
	draft.RollbackScript = filepath.Join(o.scriptsDir, recreateRollbackScript)

	healthURL, port, ok := o.probeContainer(ctx, found)
	if !ok {
		if len(found.Published) == 0 {
			draft.Warnings = append(draft.Warnings, "container publishes no host ports; set health_url manually")
		} else {
			draft.Warnings = append(
				draft.Warnings,
				fmt.Sprintf("no 2xx from port %d / or /health; set health_url manually", port),
			)
		}

		return
	}

	draft.HealthURL = healthURL
	draft.Reasons = append(draft.Reasons, fmt.Sprintf("probed %s (2xx)", healthURL))
}

// draftBlueGreen fills the color-pair fields: services, targets from
// published ports, probed urls, and the nginx conf holding the marker.
func (o *Onboarding) draftBlueGreen(
	ctx context.Context,
	draft *model.ServiceSuggestion,
	containers []docker.Container,
	project, base string,
) {
	draft.Strategy = model.StrategyBlueGreen
	draft.BlueService = base + colorBlueSuffix
	draft.GreenService = base + colorGreenSuffix
	draft.Marker = blueGreenMarker
	draft.Reasons = append(
		draft.Reasons,
		fmt.Sprintf("detected blue-green pair %q and %q", draft.BlueService, draft.GreenService),
	)

	blue := findServiceContainer(containers, project, draft.BlueService)
	green := findServiceContainer(containers, project, draft.GreenService)

	bluePort, hasBlue := o.draftColorTarget(ctx, draft, blue, true)
	greenPort, hasGreen := o.draftColorTarget(ctx, draft, green, false)

	if healthURL, _, ok := o.probeContainer(ctx, findContainerOr(containers, draft.Container)); ok {
		draft.HealthURL = healthURL
		draft.Reasons = append(draft.Reasons, fmt.Sprintf("probed %s (2xx)", healthURL))
	}

	var ports []uint16

	if hasBlue {
		ports = append(ports, bluePort)
	}

	if hasGreen {
		ports = append(ports, greenPort)
	}

	conf, err := o.findNginxConf(ports)

	switch {
	case err != nil:
		draft.Warnings = append(draft.Warnings, "nginx scan skipped: "+err.Error())
	case conf == "":
		draft.Warnings = append(
			draft.Warnings,
			"no BLUEGREEN-ACTIVE marker on the color ports; set nginx_conf manually",
		)
	default:
		draft.NginxConf = conf
		draft.Reasons = append(draft.Reasons, "nginx marker found in "+conf)
	}

	draft.Warnings = append(draft.Warnings, "fork the team cutover script (manual); set cutover_script")
}

// draftColorTarget fills one color's target and probed url, returning its
// published port. Missing containers or ports warn instead of failing.
func (o *Onboarding) draftColorTarget(
	ctx context.Context,
	draft *model.ServiceSuggestion,
	found docker.Container,
	isBlue bool,
) (uint16, bool) {
	label := colorGreen
	if isBlue {
		label = colorBlue
	}

	setTarget := func(target string) {
		if isBlue {
			draft.BlueTarget = target
		} else {
			draft.GreenTarget = target
		}
	}

	setURL := func(url string) {
		if isBlue {
			draft.BlueURL = url
		} else {
			draft.GreenURL = url
		}
	}

	if found.ID == "" {
		name := draft.GreenService
		if isBlue {
			name = draft.BlueService
		}

		draft.Warnings = append(
			draft.Warnings,
			fmt.Sprintf("color container %q is not running; set %s_target manually", name, label),
		)

		return 0, false
	}

	port, ok := lowestPort(found)
	if !ok {
		draft.Warnings = append(
			draft.Warnings,
			fmt.Sprintf("%s publishes no host port; set %s_target manually", label, label),
		)

		return 0, false
	}

	setTarget(net.JoinHostPort(dialHost(lowestHostIP(found, port)), strconv.Itoa(int(port))))

	healthURL, _, probed := o.probeContainer(ctx, found)
	if probed {
		setURL(healthURL)
		draft.Reasons = append(draft.Reasons, fmt.Sprintf("probed %s %s (2xx)", label, healthURL))
	} else {
		draft.Warnings = append(
			draft.Warnings,
			fmt.Sprintf("no 2xx from %s port %d / or /health; set %s_url manually", label, port, label),
		)
	}

	return port, true
}

// probeContainer probes the lowest published host port's / then /health.
// It returns the winning target, the port tried, and whether one won.
func (o *Onboarding) probeContainer(ctx context.Context, found docker.Container) (string, uint16, bool) {
	port, ok := lowestPort(found)
	if !ok {
		return "", 0, false
	}

	base := "http://" + net.JoinHostPort(dialHost(lowestHostIP(found, port)), strconv.Itoa(int(port)))

	for _, path := range []string{probePathRoot, probePathHealth} {
		target := base + path

		attempt, cancel := context.WithTimeout(ctx, suggestProbeTimeout)
		result, err := o.prober.Check(attempt, target)

		cancel()

		if err != nil {
			continue
		}

		if result.StatusCode >= minHealthyStatus && result.StatusCode <= maxHealthyStatus {
			return target, port, true
		}
	}

	return "", port, false
}

// findNginxConf returns the first sites file whose marker line sits on one
// of ports. An empty result with no error means no marker matched.
func (o *Onboarding) findNginxConf(ports []uint16) (string, error) {
	if len(ports) == 0 {
		return "", errors.New("no color ports to match")
	}

	entries, err := o.readDir(o.nginxDir)
	if err != nil {
		return "", fmt.Errorf("cannot read %s: %w", o.nginxDir, err)
	}

	names := make([]string, 0, len(entries))
	files := make(map[string]bool, len(entries))

	for _, entry := range entries {
		names = append(names, entry.Name())
		files[entry.Name()] = !entry.IsDir()
	}

	slices.Sort(names)

	for _, name := range names {
		if !files[name] {
			continue
		}

		path := filepath.Join(o.nginxDir, name)

		raw, err := o.readFile(path)
		if err != nil {
			continue
		}

		if confHasMarker(string(raw), ports) {
			return path, nil
		}
	}

	return "", nil
}

// confHasMarker reports whether any marker line sits on one of ports.
func confHasMarker(conf string, ports []uint16) bool {
	for line := range strings.Lines(conf) {
		if !strings.Contains(line, blueGreenMarker) {
			continue
		}

		for _, port := range ports {
			if strings.Contains(line, ":"+strconv.Itoa(int(port))) {
				return true
			}
		}
	}

	return false
}

// CreateInput carries a new service definition. Config holds raw strategy JSON.
type CreateInput struct {
	ID             string
	Name           string
	Strategy       model.Strategy
	ComposeProject string
	ComposeDir     string
	Config         json.RawMessage
}

// CreateOutput reports the created row.
type CreateOutput struct {
	ID       string         `json:"id"`
	Strategy model.Strategy `json:"strategy"`
}

// Create validates and stores a new service row, auditing the creation.
func (o *Onboarding) Create(
	ctx context.Context,
	input CreateInput,
	actor string,
) (CreateOutput, error) {
	if err := o.checkCreateInput(ctx, input, actor); err != nil {
		return CreateOutput{}, err
	}

	def := model.Service{ID: input.ID, Config: input.Config}

	if err := o.validateConfig(def, input.Strategy); err != nil {
		return CreateOutput{}, err
	}

	name := input.Name
	if name == "" {
		name = input.ID
	}

	service := model.Service{
		ID:             input.ID,
		Name:           name,
		Strategy:       input.Strategy,
		ComposeProject: input.ComposeProject,
		ComposeDir:     input.ComposeDir,
		Config:         input.Config,
	}

	if err := o.services.Create(ctx, service); err != nil {
		return CreateOutput{}, fmt.Errorf("creating service: %w", err)
	}

	if _, err := o.audit.Record(ctx, model.AuditRecord{
		ServiceID: input.ID,
		Actor:     actor,
		Action:    model.AuditServiceCreate,
		Result:    model.AuditSuccess,
		Detail:    "created with strategy " + string(input.Strategy),
	}); err != nil {
		return CreateOutput{}, fmt.Errorf("auditing service creation: %w", err)
	}

	return CreateOutput{ID: input.ID, Strategy: input.Strategy}, nil
}

// checkCreateInput validates the creation envelope: actor, id shape and
// uniqueness, strategy, and compose locations.
func (o *Onboarding) checkCreateInput(ctx context.Context, input CreateInput, actor string) error {
	if actor == "" {
		return fmt.Errorf("%w: actor is required", ErrInvalidInput)
	}

	if !serviceIDPattern.MatchString(input.ID) {
		return fmt.Errorf("%w: id %q (want ^[a-z0-9-]+$)", ErrInvalidInput, input.ID)
	}

	if _, err := o.services.Get(ctx, input.ID); err == nil {
		return fmt.Errorf("%w: service id %q already exists", ErrConflict, input.ID)
	} else if !errors.Is(err, store.ErrServiceNotFound) {
		return fmt.Errorf("checking service id: %w", err)
	}

	if input.Strategy != model.StrategyRecreate && input.Strategy != model.StrategyBlueGreen {
		return fmt.Errorf("%w: strategy %q (want recreate or bluegreen)", ErrInvalidInput, input.Strategy)
	}

	if input.ComposeProject == "" {
		return fmt.Errorf("%w: compose_project is required", ErrInvalidInput)
	}

	if input.ComposeDir == "" {
		return fmt.Errorf("%w: compose_dir is required", ErrInvalidInput)
	}

	if info, err := os.Stat(input.ComposeDir); err != nil || !info.IsDir() {
		return fmt.Errorf("%w: compose_dir %q is not a directory", ErrInvalidInput, input.ComposeDir)
	}

	return nil
}

// validateConfig decodes raw strategy config and checks required fields
// plus script executability.
func (o *Onboarding) validateConfig(def model.Service, strategy model.Strategy) error {
	switch strategy {
	case model.StrategyRecreate:
		cfg, err := decodeRecreate(def)
		if err != nil {
			return fmt.Errorf("%w: %w", ErrInvalidInput, err)
		}

		return validateRecreateConfig(def.ID, cfg)
	case model.StrategyBlueGreen:
		cfg, err := decodeBlueGreen(def)
		if err != nil {
			return fmt.Errorf("%w: %w", ErrInvalidInput, err)
		}

		return validateBlueGreenConfig(def.ID, cfg)
	case model.StrategyUnknown:
		return fmt.Errorf("%w: strategy %q (want recreate or bluegreen)", ErrInvalidInput, strategy)
	default:
		return fmt.Errorf("%w: strategy %q (want recreate or bluegreen)", ErrInvalidInput, strategy)
	}
}

// validateRecreateConfig checks recreate required fields and scripts.
func validateRecreateConfig(id string, cfg model.RecreateConfig) error {
	switch {
	case cfg.Service == "":
		return fmt.Errorf("%w: recreate config for %q is missing service", ErrInvalidInput, id)
	case cfg.HealthURL == "":
		return fmt.Errorf("%w: recreate config for %q is missing health_url", ErrInvalidInput, id)
	case cfg.DeployScript == "":
		return fmt.Errorf("%w: recreate config for %q is missing deploy_script", ErrInvalidInput, id)
	case cfg.RollbackScript == "":
		return fmt.Errorf("%w: recreate config for %q is missing rollback_script", ErrInvalidInput, id)
	}

	if err := checkExecutable(cfg.DeployScript); err != nil {
		return fmt.Errorf("%w: deploy script %q: %w", ErrInvalidInput, cfg.DeployScript, err)
	}

	if err := checkExecutable(cfg.RollbackScript); err != nil {
		return fmt.Errorf("%w: rollback script %q: %w", ErrInvalidInput, cfg.RollbackScript, err)
	}

	return nil
}

// validateBlueGreenConfig checks blue-green required fields and scripts.
func validateBlueGreenConfig(id string, cfg model.BlueGreenConfig) error {
	switch {
	case cfg.BlueService == "":
		return fmt.Errorf("%w: blue-green config for %q is missing blue_service", ErrInvalidInput, id)
	case cfg.GreenService == "":
		return fmt.Errorf("%w: blue-green config for %q is missing green_service", ErrInvalidInput, id)
	case cfg.BlueTarget == "":
		return fmt.Errorf("%w: blue-green config for %q is missing blue_target", ErrInvalidInput, id)
	case cfg.GreenTarget == "":
		return fmt.Errorf("%w: blue-green config for %q is missing green_target", ErrInvalidInput, id)
	case cfg.BlueURL == "":
		return fmt.Errorf("%w: blue-green config for %q is missing blue_url", ErrInvalidInput, id)
	case cfg.GreenURL == "":
		return fmt.Errorf("%w: blue-green config for %q is missing green_url", ErrInvalidInput, id)
	case cfg.NginxConf == "":
		return fmt.Errorf("%w: blue-green config for %q is missing nginx_conf", ErrInvalidInput, id)
	case cfg.Marker == "":
		return fmt.Errorf("%w: blue-green config for %q is missing marker", ErrInvalidInput, id)
	case cfg.CutoverScript == "":
		return fmt.Errorf("%w: blue-green config for %q is missing cutover_script", ErrInvalidInput, id)
	}

	if err := checkExecutable(cfg.CutoverScript); err != nil {
		return fmt.Errorf("%w: cutover script %q: %w", ErrInvalidInput, cfg.CutoverScript, err)
	}

	if cfg.RestoreScript != "" {
		if err := checkExecutable(cfg.RestoreScript); err != nil {
			return fmt.Errorf("%w: restore script %q: %w", ErrInvalidInput, cfg.RestoreScript, err)
		}
	}

	return nil
}

// draftServiceID tries the project then project-service as a service id,
// returning a warning when both are taken or no candidate exists.
func (o *Onboarding) draftServiceID(ctx context.Context, project, svc string) (string, string, error) {
	var candidates []string

	seen := map[string]bool{}

	for _, raw := range []string{project, joinServiceID(project, svc)} {
		candidate := sanitizeServiceID(raw)
		if candidate == "" || seen[candidate] {
			continue
		}

		seen[candidate] = true
		candidates = append(candidates, candidate)
	}

	if len(candidates) == 0 {
		return "", "cannot draft a service id without compose labels; choose an id", nil
	}

	for _, candidate := range candidates {
		_, err := o.services.Get(ctx, candidate)
		if errors.Is(err, store.ErrServiceNotFound) {
			return candidate, "", nil
		}

		if err != nil {
			return "", "", fmt.Errorf("checking service id: %w", err)
		}
	}

	taken := `"` + strings.Join(candidates, `" and "`) + `"`

	return "", fmt.Sprintf("service ids %s are taken; choose an id", taken), nil
}

// newDraft seeds a suggestion from container labels, warning about blanks.
func newDraft(name string, found docker.Container) model.ServiceSuggestion {
	draft := model.ServiceSuggestion{
		Container:      name,
		Service:        found.Labels[docker.LabelComposeService],
		ComposeProject: found.Labels[docker.LabelComposeProject],
		ComposeDir:     found.Labels[docker.LabelComposeWorkingDir],
		Reasons:        []string{},
		Warnings:       []string{},
	}

	if draft.ComposeProject == "" {
		draft.Warnings = append(draft.Warnings, "container has no compose project label; set compose_project")
	}

	if draft.Service == "" {
		draft.Warnings = append(draft.Warnings, "container has no compose service label; set service")
	}

	if draft.ComposeDir == "" {
		draft.Warnings = append(draft.Warnings, "container has no compose working-dir label; set compose_dir")
	}

	if draft.ComposeProject != "" && draft.Service != "" {
		draft.Reasons = append(
			draft.Reasons,
			fmt.Sprintf("compose project %q service %q from container labels", draft.ComposeProject, draft.Service),
		)
	}

	return draft
}

// confidence grades a draft: warnings drop high to medium, while missing
// labels, a missing id, or a blue-green draft without nginx conf drop to low.
func confidence(draft model.ServiceSuggestion, project, svc, dir string) string {
	graded := model.ConfidenceHigh

	if len(draft.Warnings) > 0 {
		graded = model.ConfidenceMedium
	}

	if project == "" || svc == "" || dir == "" || draft.ServiceID == "" {
		return model.ConfidenceLow
	}

	if draft.Strategy == model.StrategyBlueGreen && draft.NginxConf == "" {
		return model.ConfidenceLow
	}

	return graded
}

// findContainer returns the container named name.
func findContainer(containers []docker.Container, name string) (docker.Container, bool) {
	for _, found := range containers {
		if found.Name == name {
			return found, true
		}
	}

	return docker.Container{}, false
}

// findContainerOr returns the named container or a zero Container.
func findContainerOr(containers []docker.Container, name string) docker.Container {
	found, _ := findContainer(containers, name)

	return found
}

// findServiceContainer returns the first project container running service.
func findServiceContainer(containers []docker.Container, project, service string) docker.Container {
	for _, found := range containers {
		if found.Labels[docker.LabelComposeProject] == project &&
			found.Labels[docker.LabelComposeService] == service {
			return found
		}
	}

	return docker.Container{}
}

// managedBy reports the service row owning project+service, skipping rows
// whose config no longer decodes.
func managedBy(defs []model.Service, project, svc string) (string, bool) {
	for _, def := range defs {
		if def.ComposeProject != project {
			continue
		}

		names, err := serviceNames(def)
		if err != nil {
			continue
		}

		if slices.Contains(names, svc) {
			return def.ID, true
		}
	}

	return "", false
}

// colorPair finds an X-blue/X-green service pair in project, preferring the
// requested container's own color family. Detection needs a project label,
// so unlabeled containers never pair.
func colorPair(containers []docker.Container, project, svc string) (string, bool) {
	if project == "" {
		return "", false
	}

	services := map[string]bool{}

	for _, found := range containers {
		if found.Labels[docker.LabelComposeProject] == project {
			services[found.Labels[docker.LabelComposeService]] = true
		}
	}

	if base, ok := colorBase(svc); ok && services[base+colorBlueSuffix] && services[base+colorGreenSuffix] {
		return base, true
	}

	for _, service := range slices.Sorted(maps.Keys(services)) {
		base, ok := colorBase(service)
		if !ok {
			continue
		}

		if services[base+colorBlueSuffix] && services[base+colorGreenSuffix] {
			return base, true
		}
	}

	return "", false
}

// colorBase strips a blue/green suffix, reporting whether one was present.
func colorBase(service string) (string, bool) {
	if base, ok := strings.CutSuffix(service, colorBlueSuffix); ok {
		return base, true
	}

	if base, ok := strings.CutSuffix(service, colorGreenSuffix); ok {
		return base, true
	}

	return "", false
}

// lowestPort returns the smallest published host port.
func lowestPort(found docker.Container) (uint16, bool) {
	if len(found.Published) == 0 {
		return 0, false
	}

	lowest := found.Published[0].HostPort

	for _, published := range found.Published[1:] {
		lowest = min(lowest, published.HostPort)
	}

	return lowest, true
}

// lowestHostIP returns the host IP of the lowest published port.
func lowestHostIP(found docker.Container, port uint16) string {
	for _, published := range found.Published {
		if published.HostPort == port {
			return published.HostIP
		}
	}

	return ""
}

// dialHost maps wildcard publish IPs to loopback for direct probing.
func dialHost(hostIP string) string {
	if hostIP == "" || hostIP == "0.0.0.0" || hostIP == "::" {
		return "127.0.0.1"
	}

	return hostIP
}

// joinServiceID joins a project-service candidate id.
func joinServiceID(project, svc string) string {
	if project == "" || svc == "" {
		return ""
	}

	return project + "-" + svc
}

// sanitizeServiceID folds raw into service-id shape.
func sanitizeServiceID(raw string) string {
	lowered := strings.ToLower(raw)

	var folded strings.Builder

	folded.Grow(len(lowered))

	lastDash := true

	for _, r := range lowered {
		isID := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')

		if !isID {
			if !lastDash {
				folded.WriteByte('-')
			}

			lastDash = true

			continue
		}

		folded.WriteRune(r)

		lastDash = false
	}

	return strings.Trim(folded.String(), "-")
}
