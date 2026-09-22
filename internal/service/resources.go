package service

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/oralecarlangelo/touchgrass/internal/docker"
	"github.com/oralecarlangelo/touchgrass/internal/model"
	"github.com/oralecarlangelo/touchgrass/internal/store"
)

const (
	defaultListLimit = 100
	maxListLimit     = 1000
)

// StatsLister is the docker surface the sampler needs.
type StatsLister interface {
	docker.Lister
	Stats(ctx context.Context, id string) (docker.Stats, error)
	SizedList(ctx context.Context) ([]docker.Container, []int64, error)
}

// Retention holds per-area max ages enforced on the sample schedule.
type Retention struct {
	Metrics       time.Duration
	Notifications time.Duration
	Deploys       time.Duration
	Errors        time.Duration
	Logs          time.Duration
	SDKLogs       time.Duration
}

// SamplerConfig wires a Sampler.
type SamplerConfig struct {
	Services      *store.ServiceStore
	Docker        StatsLister
	Metrics       *store.MetricStore
	Rules         *store.RuleStore
	Notifications *store.NotificationStore
	Deploys       *store.DeployStore
	Occurrences   *store.OccurrenceStore
	Issues        *store.IssueStore
	IssueRules    *store.IssueRuleStore
	Logs          *store.LogStore
	SDKLogs       *store.SDKLogStore
	Fleet         *store.FleetStore
	Interval      time.Duration
	Retention     Retention
	Logger        *slog.Logger
}

// Sampler collects metrics, evaluates alerts, and enforces retention.
type Sampler struct {
	services      *store.ServiceStore
	docker        StatsLister
	metrics       *store.MetricStore
	rules         *store.RuleStore
	notifications *store.NotificationStore
	deploys       *store.DeployStore
	occurrences   *store.OccurrenceStore
	issues        *store.IssueStore
	issueRules    *store.IssueRuleStore
	logs          *store.LogStore
	sdkLogs       *store.SDKLogStore
	fleet         *store.FleetStore
	interval      time.Duration
	retention     Retention
	logger        *slog.Logger
	mutex         sync.Mutex
	breaches      map[int64]breachState
	spikes        map[int64]map[int64]bool
	cpuMu         sync.Mutex
	cpuLast       *cpuSample
}

// breachState tracks one rule's ongoing breach episode.
type breachState struct {
	start time.Time
	fired bool
}

// NewSampler builds a Sampler.
func NewSampler(cfg SamplerConfig) *Sampler {
	return &Sampler{
		services:      cfg.Services,
		docker:        cfg.Docker,
		metrics:       cfg.Metrics,
		rules:         cfg.Rules,
		notifications: cfg.Notifications,
		deploys:       cfg.Deploys,
		occurrences:   cfg.Occurrences,
		issues:        cfg.Issues,
		issueRules:    cfg.IssueRules,
		logs:          cfg.Logs,
		sdkLogs:       cfg.SDKLogs,
		fleet:         cfg.Fleet,
		interval:      cfg.Interval,
		retention:     cfg.Retention,
		logger:        cfg.Logger,
		breaches:      map[int64]breachState{},
		spikes:        map[int64]map[int64]bool{},
	}
}

// Run samples until ctx is cancelled. Collection failures log and retry;
// only cancellation stops the loop.
func (s *Sampler) Run(ctx context.Context) {
	s.sample(ctx)

	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.sample(ctx)
		}
	}
}

// sample runs one collect-evaluate-trim cycle.
func (s *Sampler) sample(ctx context.Context) {
	if err := s.collect(ctx); err != nil {
		s.logger.Warn("metric collection failed", "error", err)
	}

	if err := s.evaluate(ctx); err != nil {
		s.logger.Warn("alert evaluation failed", "error", err)
	}

	if err := s.evaluateIssues(ctx); err != nil {
		s.logger.Warn("issue alert evaluation failed", "error", err)
	}

	s.trim(ctx)
}

// collect records one sample per managed container, then the fleet and
// host samples.
func (s *Sampler) collect(ctx context.Context) error {
	defs, err := s.services.All(ctx)
	if err != nil {
		return fmt.Errorf("loading services: %w", err)
	}

	containers, sizes, err := s.docker.SizedList(ctx)
	if err != nil {
		return fmt.Errorf("listing containers: %w", err)
	}

	diskByID := make(map[string]int64, len(containers))
	for i, c := range containers {
		diskByID[c.ID] = sizes[i]
	}

	var group sync.WaitGroup

	for _, def := range defs {
		names, err := serviceNames(def)
		if err != nil {
			return err
		}

		want := make(map[string]bool, len(names))
		for _, name := range names {
			want[name] = true
		}

		for _, c := range containers {
			if !belongsTo(c, def.ComposeProject, want) {
				continue
			}

			group.Go(func() {
				s.sampleOne(ctx, def.ID, c, diskByID[c.ID])
			})
		}
	}

	group.Wait()

	return s.sampleFleet(ctx, defs, containers, time.Now())
}

// sampleOne stats one container and records the sample. Failures log and
// skip the container without failing the cycle.
func (s *Sampler) sampleOne(ctx context.Context, serviceID string, c docker.Container, disk int64) {
	stats, err := s.docker.Stats(ctx, c.ID)
	if err != nil {
		s.logger.Warn("container stats failed", "container", c.Name, "error", err)

		return
	}

	var uptime int64

	if !stats.StartedAt.IsZero() {
		uptime = max(int64(time.Since(stats.StartedAt).Seconds()), 0)
	}

	metric := model.Metric{
		ServiceID:     serviceID,
		ContainerName: c.Name,
		SampledAt:     time.Now(),
		CPUPercent:    stats.CPUPercent,
		MemBytes:      stats.MemBytes,
		MemLimit:      stats.MemLimit,
		DiskBytes:     disk,
		Restarts:      stats.Restarts,
		UptimeSecs:    uptime,
	}

	if err := s.metrics.Insert(ctx, metric); err != nil {
		s.logger.Warn("metric insert failed", "container", c.Name, "error", err)
	}
}

// evaluate fires newly-breached rules.
func (s *Sampler) evaluate(ctx context.Context) error {
	rules, err := s.rules.AllEnabled(ctx)
	if err != nil {
		return fmt.Errorf("loading alert rules: %w", err)
	}

	for _, rule := range rules {
		if err := s.evaluateRule(ctx, rule); err != nil {
			s.logger.Warn("rule evaluation failed", "rule", rule.ID, "error", err)
		}
	}

	return nil
}

// evaluateRule edge-triggers one rule: sustained breach past the duration
// fires once per episode.
func (s *Sampler) evaluateRule(ctx context.Context, rule model.AlertRule) error {
	latest, err := s.metrics.LatestByService(ctx, rule.ServiceID)
	if err != nil {
		return fmt.Errorf("loading latest metrics: %w", err)
	}

	now := time.Now()
	breaching := false

	for _, sample := range latest {
		if metricValue(rule.Metric, sample) > rule.Threshold {
			breaching = true

			break
		}
	}

	s.mutex.Lock()
	state := s.breaches[rule.ID]
	s.mutex.Unlock()

	if !breaching {
		s.mutex.Lock()
		delete(s.breaches, rule.ID)
		s.mutex.Unlock()

		return nil
	}

	if state.start.IsZero() {
		state.start = now
	}

	if !state.fired && now.Sub(state.start) >= time.Duration(rule.DurationSecs)*time.Second {
		if err := s.fire(ctx, rule, now); err != nil {
			return err
		}

		state.fired = true
	}

	s.mutex.Lock()
	s.breaches[rule.ID] = state
	s.mutex.Unlock()

	return nil
}

// fire records a breach notification.
func (s *Sampler) fire(ctx context.Context, rule model.AlertRule, now time.Time) error {
	title := fmt.Sprintf("%s %s above %g", rule.ServiceID, rule.Metric, rule.Threshold)
	body := fmt.Sprintf("sustained for %ds as of %s", rule.DurationSecs, now.UTC().Format(time.RFC3339))

	_, err := s.notifications.Insert(ctx, rule.ServiceID, model.NotificationAlertBreach, title, body)
	if err != nil {
		return fmt.Errorf("recording breach notification: %w", err)
	}

	s.logger.Info("alert fired", "rule", rule.ID, "service", rule.ServiceID, "metric", rule.Metric)

	return nil
}

// metricValue extracts the rule metric from a sample.
func metricValue(metric string, sample model.Metric) float64 {
	switch metric {
	case model.MetricCPU:
		return sample.CPUPercent
	case model.MetricMem:
		if sample.MemLimit == 0 {
			return 0
		}

		return float64(sample.MemBytes) / float64(sample.MemLimit) * 100
	case model.MetricDisk:
		return float64(sample.DiskBytes)
	default:
		return 0
	}
}

// trimTarget pairs a retention area with its trim function.
type trimTarget struct {
	name string
	trim func(ctx context.Context, before time.Time) (int64, error)
}

// trim enforces per-area retention, logging only actual trims.
func (s *Sampler) trim(ctx context.Context) {
	now := time.Now()

	targets := []trimTarget{
		{name: "metrics", trim: s.metrics.TrimBefore},
		{name: "notifications", trim: s.notifications.TrimBefore},
		{name: "deploys", trim: s.deploys.TrimBefore},
		{name: "deploy probes", trim: s.deploys.TrimProbes},
		{name: "occurrences", trim: s.occurrences.TrimBefore},
		{name: "issues", trim: s.issues.TrimBefore},
		{name: "logs", trim: s.logs.TrimBefore},
		{name: "sdk_logs", trim: s.sdkLogs.TrimBefore},
		{name: "host samples", trim: s.fleet.TrimHostsBefore},
		{name: "container samples", trim: s.fleet.TrimContainersBefore},
	}

	for _, target := range targets {
		s.trimOne(ctx, target, now)
	}
}

// trimOne enforces one area's retention, logging only actual trims.
func (s *Sampler) trimOne(ctx context.Context, target trimTarget, now time.Time) {
	before := now.Add(-s.retentionFor(target.name))

	trimmed, err := target.trim(ctx, before)
	if err != nil {
		s.logger.Warn(target.name+" trim failed", "error", err)

		return
	}

	if trimmed > 0 {
		s.logger.Info("retention trimmed "+target.name, "rows", trimmed)
	}
}

// retentionFor resolves the max age for a trim target name.
func (s *Sampler) retentionFor(name string) time.Duration {
	switch name {
	case "metrics", "host samples", "container samples":
		return s.retention.Metrics
	case "notifications":
		return s.retention.Notifications
	case "deploys", "deploy probes":
		return s.retention.Deploys
	case "occurrences", "issues":
		return s.retention.Errors
	case "sdk_logs":
		return s.retention.SDKLogs
	default:
		return s.retention.Logs
	}
}

// Metrics returns samples for a service, newest bound by limit.
func (s *Sampler) Metrics(
	ctx context.Context,
	serviceID string,
	since time.Time,
	limit int,
) ([]model.Metric, error) {
	if _, err := s.services.Get(ctx, serviceID); err != nil {
		return nil, err
	}

	return s.metrics.ListByService(ctx, serviceID, since, clampLimit(limit))
}

// Rules returns alert rules for a service.
func (s *Sampler) Rules(ctx context.Context, serviceID string) ([]model.AlertRule, error) {
	if _, err := s.services.Get(ctx, serviceID); err != nil {
		return nil, err
	}

	return s.rules.ListByService(ctx, serviceID)
}

// CreateRule validates and stores an alert rule.
func (s *Sampler) CreateRule(ctx context.Context, create model.RuleCreate) (model.AlertRule, error) {
	if _, err := s.services.Get(ctx, create.ServiceID); err != nil {
		return model.AlertRule{}, err
	}

	switch create.Metric {
	case model.MetricCPU, model.MetricMem, model.MetricDisk:
	default:
		return model.AlertRule{}, fmt.Errorf("%w: metric %q (want cpu, mem, or disk)", ErrInvalidInput, create.Metric)
	}

	if create.Threshold <= 0 {
		return model.AlertRule{}, fmt.Errorf("%w: threshold must be positive", ErrInvalidInput)
	}

	if create.DurationSecs <= 0 {
		return model.AlertRule{}, fmt.Errorf("%w: duration must be positive", ErrInvalidInput)
	}

	return s.rules.Create(ctx, create)
}

// DeleteRule removes an alert rule.
func (s *Sampler) DeleteRule(ctx context.Context, id int64) error {
	return s.rules.Delete(ctx, id)
}

// Notifications returns the newest notifications first, optionally for one
// service, plus the unread count for the same scope.
func (s *Sampler) Notifications(
	ctx context.Context,
	serviceID string,
	limit int,
) ([]model.Notification, int64, error) {
	if serviceID != "" {
		if _, err := s.services.Get(ctx, serviceID); err != nil {
			return nil, 0, err
		}
	}

	notifications, err := s.notifications.List(ctx, serviceID, clampLimit(limit))
	if err != nil {
		return nil, 0, err
	}

	unread, err := s.notifications.CountUnread(ctx, serviceID)
	if err != nil {
		return nil, 0, err
	}

	return notifications, unread, nil
}

// MarkNotificationRead stamps one notification read.
func (s *Sampler) MarkNotificationRead(ctx context.Context, id int64) error {
	return s.notifications.MarkRead(ctx, id)
}

// MarkNotificationsRead stamps every unread notification read in scope and
// reports the count. An empty serviceID selects every service.
func (s *Sampler) MarkNotificationsRead(ctx context.Context, serviceID string) (int64, error) {
	if serviceID != "" {
		if _, err := s.services.Get(ctx, serviceID); err != nil {
			return 0, err
		}
	}

	return s.notifications.MarkAllRead(ctx, serviceID)
}

// clampLimit applies the default and max list bounds.
func clampLimit(limit int) int {
	if limit <= 0 {
		return defaultListLimit
	}

	if limit > maxListLimit {
		return maxListLimit
	}

	return limit
}
