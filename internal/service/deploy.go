// Package service implements domain logic.
package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"github.com/oralecarlangelo/touchgrass/internal/docker"
	"github.com/oralecarlangelo/touchgrass/internal/model"
	"github.com/oralecarlangelo/touchgrass/internal/probe"
	"github.com/oralecarlangelo/touchgrass/internal/store"
)

// Live colors reported for blue-green services.
const (
	colorBlue    = "blue"
	colorGreen   = "green"
	colorLegacy  = "legacy"
	colorUnknown = "unknown"
)

const shortIDLen = 12

// Prober performs direct HTTP health checks.
type Prober interface {
	Check(ctx context.Context, target string) (probe.Result, error)
}

// Inventory reports live service state.
type Inventory struct {
	services *store.ServiceStore
	docker   docker.Lister
	prober   Prober
	logger   *slog.Logger

	notifications *store.NotificationStore
	audit         *Audit
	activeRun     func(serviceID string) bool
}

// NewInventory builds an Inventory.
func NewInventory(
	services *store.ServiceStore,
	lister docker.Lister,
	prober Prober,
	logger *slog.Logger,
) *Inventory {
	return &Inventory{
		services: services,
		docker:   lister,
		prober:   prober,
		logger:   logger,
	}
}

// Services returns every managed service with live containers, live color,
// and health.
func (in *Inventory) Services(ctx context.Context) ([]model.ServiceView, error) {
	defs, err := in.services.All(ctx)
	if err != nil {
		return nil, fmt.Errorf("loading services: %w", err)
	}

	containers, err := in.docker.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing containers: %w", err)
	}

	views := make([]model.ServiceView, 0, len(defs))

	for _, def := range defs {
		view, err := in.view(ctx, def, containers)
		if err != nil {
			return nil, err
		}

		views = append(views, view)
	}

	return views, nil
}

// view builds one service view by strategy.
func (in *Inventory) view(
	ctx context.Context,
	def model.Service,
	containers []docker.Container,
) (model.ServiceView, error) {
	switch def.Strategy {
	case model.StrategyBlueGreen:
		return in.blueGreenView(ctx, def, containers)
	case model.StrategyRecreate:
		return in.recreateView(ctx, def, containers)
	case model.StrategyUnknown:
		return model.ServiceView{}, fmt.Errorf("service %q has unknown strategy", def.ID)
	default:
		return model.ServiceView{}, fmt.Errorf("service %q has unknown strategy %q", def.ID, def.Strategy)
	}
}

// blueGreenView resolves the live color from nginx and probes the live
// color plus any color with a running container. An idle color with no
// running container reports unhealthy without a probe: dialing a stopped
// color every sample only spams the log with refused connections.
func (in *Inventory) blueGreenView(
	ctx context.Context,
	def model.Service,
	containers []docker.Container,
) (model.ServiceView, error) {
	cfg, err := decodeBlueGreen(def)
	if err != nil {
		return model.ServiceView{}, err
	}

	live := in.liveColor(def, cfg)
	running := runningServices(def.ComposeProject, containers)

	targets := []struct {
		name    string
		service string
		url     string
	}{
		{colorBlue, cfg.BlueService, cfg.BlueURL},
		{colorGreen, cfg.GreenService, cfg.GreenURL},
	}

	urls := make([]string, 0, len(targets))

	for _, color := range targets {
		if live == color.name || running[color.service] {
			urls = append(urls, color.url)

			continue
		}

		in.logger.Debug("skipping probe for stopped idle color", "service", def.ID, "color", color.name)
	}

	results := in.probeAll(ctx, urls)

	colors := []model.ColorView{
		{
			Name:      colorBlue,
			Live:      live == colorBlue,
			Health:    colorHealth(results[cfg.BlueURL]),
			Target:    cfg.BlueTarget,
			HealthURL: cfg.BlueURL,
		},
		{
			Name:      colorGreen,
			Live:      live == colorGreen,
			Health:    colorHealth(results[cfg.GreenURL]),
			Target:    cfg.GreenTarget,
			HealthURL: cfg.GreenURL,
		},
	}

	return model.ServiceView{
		ID:         def.ID,
		Name:       def.Name,
		Strategy:   def.Strategy,
		LiveColor:  live,
		Health:     serviceHealth(live, results[cfg.BlueURL], results[cfg.GreenURL]),
		Colors:     colors,
		Containers: matchContainers(def, []string{cfg.BlueService, cfg.GreenService}, containers),
	}, nil
}

// recreateView probes the single health endpoint.
func (in *Inventory) recreateView(
	ctx context.Context,
	def model.Service,
	containers []docker.Container,
) (model.ServiceView, error) {
	var cfg model.RecreateConfig

	if err := json.Unmarshal(def.Config, &cfg); err != nil {
		return model.ServiceView{}, fmt.Errorf("service %q has invalid recreate config: %w", def.ID, err)
	}

	health := model.HealthHealthy

	result, err := in.prober.Check(ctx, cfg.HealthURL)
	if err != nil {
		in.logger.Warn("health probe failed", "service", def.ID, "url", cfg.HealthURL, "error", err)

		health = model.HealthUnhealthy
	} else if !result.Healthy {
		health = model.HealthUnhealthy
	}

	return model.ServiceView{
		ID:         def.ID,
		Name:       def.Name,
		Strategy:   def.Strategy,
		LiveColor:  "",
		Health:     health,
		Colors:     []model.ColorView{},
		Containers: matchContainers(def, []string{cfg.Service}, containers),
	}, nil
}

// liveColor resolves the live color from the nginx active target. Detection
// failure degrades to unknown rather than failing the request.
func (in *Inventory) liveColor(def model.Service, cfg model.BlueGreenConfig) string {
	target, err := probe.LiveTarget(cfg.NginxConf, cfg.Marker)
	if err != nil {
		in.logger.Warn("live-color detection failed", "service", def.ID, "error", err)

		return colorUnknown
	}

	switch target {
	case cfg.BlueTarget:
		return colorBlue
	case cfg.GreenTarget:
		return colorGreen
	case cfg.LegacyTarget:
		return colorLegacy
	default:
		in.logger.Warn("nginx target matches no color", "service", def.ID, "target", target)

		return colorUnknown
	}
}

// probeAll probes urls concurrently. Failures log and read as unhealthy.
func (in *Inventory) probeAll(ctx context.Context, urls []string) map[string]probe.Result {
	results := make(map[string]probe.Result, len(urls))

	var (
		mutex sync.Mutex
		group sync.WaitGroup
	)

	for _, target := range urls {
		group.Go(func() {
			result, err := in.prober.Check(ctx, target)
			if err != nil {
				in.logger.Warn("health probe failed", "url", target, "error", err)
			}

			mutex.Lock()
			results[target] = result
			mutex.Unlock()
		})
	}

	group.Wait()

	return results
}

// colorHealth maps one color probe to health.
func colorHealth(result probe.Result) model.Health {
	if result.Healthy {
		return model.HealthHealthy
	}

	return model.HealthUnhealthy
}

// serviceHealth reports the live color's health, or unknown when no color
// is live (legacy topology or failed detection).
func serviceHealth(live string, blue, green probe.Result) model.Health {
	switch live {
	case colorBlue:
		return colorHealth(blue)
	case colorGreen:
		return colorHealth(green)
	default:
		return model.HealthUnknown
	}
}

// matchContainers selects containers belonging to def by compose labels.
func matchContainers(
	def model.Service,
	services []string,
	containers []docker.Container,
) []model.ContainerView {
	want := make(map[string]bool, len(services))

	for _, name := range services {
		want[name] = true
	}

	matched := []model.ContainerView{}

	for _, c := range containers {
		if !belongsTo(c, def.ComposeProject, want) {
			continue
		}

		ports := c.Ports
		if ports == nil {
			ports = []string{}
		}

		matched = append(matched, model.ContainerView{
			ID:     shortID(c.ID),
			Name:   c.Name,
			Image:  c.Image,
			SHA:    shortID(strings.TrimPrefix(c.ImageID, "sha256:")),
			State:  c.State,
			Status: c.Status,
			Ports:  ports,
		})
	}

	return matched
}

// shortID truncates identifiers for display.
func shortID(id string) string {
	if len(id) > shortIDLen {
		return id[:shortIDLen]
	}

	return id
}

// belongsTo reports whether c belongs to a compose project and service set.
func belongsTo(c docker.Container, project string, want map[string]bool) bool {
	return c.Labels[docker.LabelComposeProject] == project && want[c.Labels[docker.LabelComposeService]]
}

// runningServices returns the compose service names with a running
// container in project. The daemon list covers running containers only,
// so membership alone proves liveness.
func runningServices(project string, containers []docker.Container) map[string]bool {
	running := make(map[string]bool)

	for _, c := range containers {
		if c.Labels[docker.LabelComposeProject] != project {
			continue
		}

		if service := c.Labels[docker.LabelComposeService]; service != "" {
			running[service] = true
		}
	}

	return running
}

// decodeBlueGreen parses blue-green strategy config.
func decodeBlueGreen(def model.Service) (model.BlueGreenConfig, error) {
	var cfg model.BlueGreenConfig

	if err := json.Unmarshal(def.Config, &cfg); err != nil {
		return model.BlueGreenConfig{}, fmt.Errorf("service %q has invalid blue-green config: %w", def.ID, err)
	}

	return cfg, nil
}

// decodeRecreate parses recreate strategy config.
func decodeRecreate(def model.Service) (model.RecreateConfig, error) {
	var cfg model.RecreateConfig

	if err := json.Unmarshal(def.Config, &cfg); err != nil {
		return model.RecreateConfig{}, fmt.Errorf("service %q has invalid recreate config: %w", def.ID, err)
	}

	return cfg, nil
}

// serviceNames returns the compose service names for def.
func serviceNames(def model.Service) ([]string, error) {
	switch def.Strategy {
	case model.StrategyBlueGreen:
		var cfg model.BlueGreenConfig

		if err := json.Unmarshal(def.Config, &cfg); err != nil {
			return nil, fmt.Errorf("service %q has invalid blue-green config: %w", def.ID, err)
		}

		return []string{cfg.BlueService, cfg.GreenService}, nil
	case model.StrategyRecreate:
		var cfg model.RecreateConfig

		if err := json.Unmarshal(def.Config, &cfg); err != nil {
			return nil, fmt.Errorf("service %q has invalid recreate config: %w", def.ID, err)
		}

		return []string{cfg.Service}, nil
	case model.StrategyUnknown:
		return nil, fmt.Errorf("service %q has unknown strategy", def.ID)
	default:
		return nil, fmt.Errorf("service %q has unknown strategy %q", def.ID, def.Strategy)
	}
}
