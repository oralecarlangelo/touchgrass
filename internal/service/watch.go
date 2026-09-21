package service

import (
	"context"
	"time"

	"github.com/oralecarlangelo/touchgrass/internal/model"
	"github.com/oralecarlangelo/touchgrass/internal/probe"
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

		in.checkTarget(def, last, failed, emit)
	}
}

// checkTarget reads one target and emits on change.
func (in *Inventory) checkTarget(
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

	if prev, ok := last[def.ID]; ok && prev != target && emit != nil {
		emit(model.Event{
			Type:      model.EventNginxChanged,
			ServiceID: def.ID,
			Message:   "live target changed " + prev + " → " + target,
			At:        time.Now(),
		})
	}

	last[def.ID] = target
}

// liveTarget resolves the raw nginx active target for a blue-green service.
func (in *Inventory) liveTarget(def model.Service) (string, error) {
	cfg, err := decodeBlueGreen(def)
	if err != nil {
		return "", err
	}

	return probe.LiveTarget(cfg.NginxConf, cfg.Marker)
}
