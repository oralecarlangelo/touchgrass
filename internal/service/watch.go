package service

import (
	"context"
	"time"

	"github.com/oralecarlangelo/touchgrass/internal/model"
	"github.com/oralecarlangelo/touchgrass/internal/probe"
	"github.com/oralecarlangelo/touchgrass/internal/store"
)

// Watch polls blue-green nginx targets and emits change events until ctx ends.
func (in *Inventory) Watch(ctx context.Context, interval time.Duration, emit func(model.Event)) {
	last := map[string]string{}
	failed := map[string]bool{}

	in.pollTargets(ctx, last, failed, nil)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			in.pollTargets(ctx, last, failed, emit)
		}
	}
}

// pollTargets reads live targets and emits changes (nil emit sets baseline).
func (in *Inventory) pollTargets(
	ctx context.Context,
	last map[string]string,
	failed map[string]bool,
	emit func(model.Event),
) {
	defs, err := in.services.All(ctx)
	if err != nil {
		in.logger.Warn("watch: loading services failed", "error", err)

		return
	}

	for _, def := range defs {
		if def.Strategy != model.StrategyBlueGreen {
			continue
		}

		in.checkTarget(ctx, def, last, failed, emit)
	}
}

// actorSystem attributes watch-detected entries to the tool, not a human.
const actorSystem = "system"

// SetFlipReporter enables durable out-of-band flip alerts: when the live
// target changes with no active touchgrass run, the watch loop stores a
// notification and an audit entry alongside the SSE event. A nil activeRun
// treats every flip as external; flips during an active run stay event-only.
func (in *Inventory) SetFlipReporter(
	notifications *store.NotificationStore,
	audit *Audit,
	activeRun func(serviceID string) bool,
) {
	in.notifications = notifications
	in.audit = audit
	in.activeRun = activeRun
}

// checkTarget reads one target and emits on change.
func (in *Inventory) checkTarget(
	ctx context.Context,
	def model.Service,
	last map[string]string,
	failed map[string]bool,
	emit func(model.Event),
) {
	target, err := in.liveTarget(def)
	if err != nil {
		if !failed[def.ID] {
			in.logger.Warn("watch: live target read failed", "service", def.ID, "error", err)

			failed[def.ID] = true
		}

		return
	}

	failed[def.ID] = false

	if prev, ok := last[def.ID]; ok && prev != target {
		if emit != nil {
			emit(model.Event{
				Type:      model.EventNginxChanged,
				ServiceID: def.ID,
				Message:   "live target changed " + prev + " → " + target,
				At:        time.Now(),
			})
		}

		in.reportFlip(ctx, def, prev, target)
	}

	last[def.ID] = target
}

// reportFlip records an out-of-band traffic move: the live target changed
// with no active touchgrass run. Best-effort; failures only log.
func (in *Inventory) reportFlip(ctx context.Context, def model.Service, prev, target string) {
	if in.notifications == nil || in.audit == nil {
		return
	}

	if in.activeRun != nil && in.activeRun(def.ID) {
		return
	}

	from, to := flipColors(def, prev, target)
	title := "external flip detected: " + def.ID + " → " + to
	body := "live target changed " + from + " (" + prev + ") → " + to +
		" (" + target + ") outside touchgrass"

	if _, err := in.notifications.Insert(ctx, def.ID, model.NotificationDeploy, title, body); err != nil {
		in.logger.Warn("watch: flip notification failed", "service", def.ID, "error", err)
	}

	if _, err := in.audit.Record(ctx, model.AuditRecord{
		ServiceID: def.ID, Actor: actorSystem,
		Action: model.AuditCutover, Result: model.AuditSuccess,
		Detail: body,
	}); err != nil {
		in.logger.Warn("watch: flip audit failed", "service", def.ID, "error", err)
	}
}

// flipColors maps raw nginx targets to color names, falling back to raw.
func flipColors(def model.Service, prev, target string) (string, string) {
	cfg, err := decodeBlueGreen(def)
	if err != nil {
		return prev, target
	}

	return colorName(cfg, prev), colorName(cfg, target)
}

// colorName maps one nginx target to its color name or the raw target.
func colorName(cfg model.BlueGreenConfig, target string) string {
	switch target {
	case cfg.BlueTarget:
		return colorBlue
	case cfg.GreenTarget:
		return colorGreen
	case cfg.LegacyTarget:
		return colorLegacy
	default:
		return target
	}
}

// liveTarget resolves the raw nginx active target for a blue-green service.
func (in *Inventory) liveTarget(def model.Service) (string, error) {
	cfg, err := decodeBlueGreen(def)
	if err != nil {
		return "", err
	}

	return probe.LiveTarget(cfg.NginxConf, cfg.Marker)
}
