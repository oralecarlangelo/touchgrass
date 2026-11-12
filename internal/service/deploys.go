package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/oralecarlangelo/touchgrass/internal/model"
	"github.com/oralecarlangelo/touchgrass/internal/store"
)

// Deploys records and lists deploy history.
type Deploys struct {
	services *store.ServiceStore
	deploys  *store.DeployStore
	audit    *Audit
}

// NewDeploys builds a Deploys service.
func NewDeploys(services *store.ServiceStore, deploys *store.DeployStore) *Deploys {
	return &Deploys{services: services, deploys: deploys}
}

// WithAudit sets where RecordManual writes its audit row, and returns
// the service for chaining.
func (d *Deploys) WithAudit(audit *Audit) *Deploys {
	d.audit = audit

	return d
}

// RecordManual stores a history entry a user reported by hand, and
// audits it under recordedBy, the signed-in user. record.Actor is who
// the history entry is attributed to; when that is someone else, the
// audit detail says so. The engine's own runs go through Record: the
// cutover service audits those itself.
func (d *Deploys) RecordManual(ctx context.Context, record model.DeployRecord, recordedBy string) (int64, error) {
	if d.audit == nil {
		return 0, errors.New("recording a manual deploy: no audit service configured")
	}

	if recordedBy == "" {
		return 0, fmt.Errorf("%w: the recording user is required", ErrInvalidInput)
	}

	id, err := d.Record(ctx, record)
	if err != nil {
		return 0, err
	}

	detail := fmt.Sprintf("recorded %s %s of %s (deploy %d)", record.Type, record.Outcome, record.SHA, id)
	if record.Actor != recordedBy {
		detail += ", attributed to " + record.Actor
	}

	if _, err := d.audit.Record(ctx, model.AuditRecord{
		ServiceID: record.ServiceID, Actor: recordedBy, Action: model.AuditDeployRecord,
		Result: model.AuditSuccess, Detail: detail,
	}); err != nil {
		return 0, fmt.Errorf("auditing manual deploy record: %w", err)
	}

	return id, nil
}

// History returns entries for a service, newest first.
func (d *Deploys) History(ctx context.Context, serviceID string, limit int) ([]model.Deploy, error) {
	if _, err := d.services.Get(ctx, serviceID); err != nil {
		return nil, err
	}

	return d.deploys.ListByService(ctx, serviceID, clampLimit(limit))
}

// Record validates and stores one history entry.
func (d *Deploys) Record(ctx context.Context, record model.DeployRecord) (int64, error) {
	if _, err := d.services.Get(ctx, record.ServiceID); err != nil {
		return 0, err
	}

	switch record.Type {
	case model.DeployCutover, model.DeployRollback, model.DeployManual, model.DeployDeploy:
	default:
		return 0, fmt.Errorf("%w: type %q (want cutover, rollback, manual, or deploy)", ErrInvalidInput, record.Type)
	}

	switch record.Outcome {
	case model.DeploySuccess, model.DeployFailure:
	default:
		return 0, fmt.Errorf("%w: outcome %q (want success or failure)", ErrInvalidInput, record.Outcome)
	}

	if record.SHA == "" {
		return 0, fmt.Errorf("%w: sha is required", ErrInvalidInput)
	}

	if record.Actor == "" {
		return 0, fmt.Errorf("%w: actor is required", ErrInvalidInput)
	}

	deploy := model.Deploy{
		ServiceID:    record.ServiceID,
		SHA:          record.SHA,
		Actor:        record.Actor,
		Type:         record.Type,
		Outcome:      record.Outcome,
		Notes:        record.Notes,
		StartedAt:    record.StartedAt,
		FinishedAt:   record.FinishedAt,
		DowntimeSecs: record.DowntimeSecs,
		CreatedAt:    time.Now(),
	}

	if record.StartedAt != nil && record.FinishedAt != nil {
		secs := int64(record.FinishedAt.Sub(*record.StartedAt).Seconds())
		if secs < 0 {
			return 0, fmt.Errorf("%w: finished_at precedes started_at", ErrInvalidInput)
		}

		deploy.DurationSecs = &secs
	}

	return d.deploys.Record(ctx, deploy)
}
