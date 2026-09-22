package service

import (
	"context"
	"fmt"
	"time"

	"github.com/oralecarlangelo/touchgrass/internal/model"
	"github.com/oralecarlangelo/touchgrass/internal/store"
)

// Audit records and lists append-only action entries.
type Audit struct {
	services *store.ServiceStore
	audits   *store.AuditStore
}

// NewAudit builds an Audit service.
func NewAudit(services *store.ServiceStore, audits *store.AuditStore) *Audit {
	return &Audit{services: services, audits: audits}
}

// Record validates and stores one audit entry.
func (a *Audit) Record(ctx context.Context, record model.AuditRecord) (int64, error) {
	if record.ServiceID != "" {
		if _, err := a.services.Get(ctx, record.ServiceID); err != nil {
			return 0, err
		}
	}

	switch record.Action {
	case model.AuditCutover, model.AuditRollback, model.AuditLogin, model.AuditDeploy, model.AuditServiceCreate:
	default:
		return 0, fmt.Errorf(
			"%w: action %q (want cutover, rollback, login, deploy, or service_create)",
			ErrInvalidInput,
			record.Action,
		)
	}

	switch record.Result {
	case model.AuditSuccess, model.AuditFailure:
	default:
		return 0, fmt.Errorf("%w: result %q (want success or failure)", ErrInvalidInput, record.Result)
	}

	if record.Actor == "" {
		return 0, fmt.Errorf("%w: actor is required", ErrInvalidInput)
	}

	entry := model.Audit{
		Actor:     record.Actor,
		Action:    record.Action,
		Result:    record.Result,
		Detail:    record.Detail,
		CreatedAt: time.Now(),
	}

	if record.ServiceID != "" {
		serviceID := record.ServiceID
		entry.ServiceID = &serviceID
	}

	return a.audits.Insert(ctx, entry)
}

// List returns entries newest first, optionally for one service.
func (a *Audit) List(
	ctx context.Context,
	serviceID string,
	limit int,
) ([]model.Audit, error) {
	if serviceID != "" {
		if _, err := a.services.Get(ctx, serviceID); err != nil {
			return nil, err
		}
	}

	return a.audits.List(ctx, serviceID, clampLimit(limit))
}
