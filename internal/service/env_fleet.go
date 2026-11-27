package service

import (
	"context"
	"path/filepath"

	"github.com/oralecarlangelo/touchgrass/internal/model"
)

// FleetSummary describes each authorized target without reading or decrypting
// key material. The detail page owns the per-entry read.
func (e *Env) FleetSummary(ctx context.Context, all bool, ids []string) ([]model.EnvFleetSummary, error) {
	var (
		definitions []model.Service
		err         error
	)

	if all {
		definitions, err = e.services.All(ctx)
	} else {
		definitions, err = e.services.ByIDs(ctx, ids)
	}

	if err != nil {
		return nil, err
	}

	views := make([]model.EnvFleetSummary, 0, len(definitions))
	configured := make([]string, 0, len(definitions))

	for _, def := range definitions {
		view := e.fleetTarget(def)
		if view.Configured {
			configured = append(configured, def.ID)
		}

		views = append(views, view)
	}

	rows, err := e.store.EnvFleetCounts(ctx, configured)
	if err != nil {
		return nil, err
	}

	counts := make(map[string]model.EnvFleetSummary, len(rows))
	for _, row := range rows {
		counts[row.ServiceID] = row
	}

	for index, view := range views {
		row := counts[view.ServiceID]
		views[index].KeyCount = row.KeyCount
		views[index].SecretCount = row.SecretCount
		views[index].PendingCount = row.PendingCount
		views[index].LastAppliedAt = row.LastAppliedAt
	}

	return views, nil
}

func (e *Env) fleetTarget(def model.Service) model.EnvFleetSummary {
	view := model.EnvFleetSummary{ServiceID: def.ID}
	if !e.Enabled() {
		view.Reason = model.EnvReasonKeyMissing

		return view
	}

	cfg, found, err := model.EnvConfigOf(def)

	switch {
	case err != nil, found && !filepath.IsAbs(cfg.Target.Path):
		view.Reason = "invalid_target"
	case !found:
		view.Reason = model.EnvReasonNotConfigured
	default:
		view.Configured = true
		view.TargetPath = cfg.Target.Path
		_, view.ApplyInProgress = e.activeRun(def.ID)
	}

	return view
}
