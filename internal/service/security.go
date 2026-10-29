package service

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/oralecarlangelo/touchgrass/internal/model"
	"github.com/oralecarlangelo/touchgrass/internal/store"
)

// SecurityConfig wires the durable container-security workflow.
type SecurityConfig struct {
	Store         *store.SecurityStore
	Notifications *store.NotificationStore
	Audit         *Audit
	Emit          func(model.Event)
	NotifyHigh    bool
	Logger        *slog.Logger
}

// Security owns finding episodes, notification delivery, and operator actions.
type Security struct {
	store         *store.SecurityStore
	notifications *store.NotificationStore
	audit         *Audit
	emit          func(model.Event)
	notifyHigh    bool
	logger        *slog.Logger
}

// NewSecurity builds the security workflow service.
func NewSecurity(cfg SecurityConfig) *Security {
	emit := cfg.Emit
	if emit == nil {
		emit = func(model.Event) {}
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	return &Security{
		store: cfg.Store, notifications: cfg.Notifications, audit: cfg.Audit,
		emit: emit, notifyHigh: cfg.NotifyHigh, logger: logger,
	}
}

// Evaluate persists one observation and emits side effects only for episode
// transitions. Delivery failures never undo the durable finding state.
func (s *Security) Evaluate(ctx context.Context, evaluation model.SecurityEvaluation) (model.SecurityFinding, error) {
	result, err := s.store.Evaluate(ctx, evaluation)
	if err != nil {
		return model.SecurityFinding{}, err
	}
	if result.Opened {
		s.notifyOpened(ctx, evaluation.ServiceID, result.Finding)
		s.emit(model.Event{
			Type: model.EventSecurityFinding, ServiceID: evaluation.ServiceID,
			Message: securityEventMessage(result.Finding), At: time.Now().UTC(),
		})
	}
	if result.Recovered {
		s.emit(model.Event{
			Type: model.EventSecurityRecovery, ServiceID: evaluation.ServiceID,
			Message: securityEventMessage(result.Finding), At: time.Now().UTC(),
		})
	}
	return result.Finding, nil
}

func (s *Security) notifyOpened(ctx context.Context, serviceID string, finding model.SecurityFinding) {
	if s.notifications == nil || (finding.Severity != model.SecuritySeverityCritical &&
		!(s.notifyHigh && finding.Severity == model.SecuritySeverityHigh)) {
		return
	}
	_, err := s.notifications.Insert(ctx, serviceID, model.NotificationSecurity,
		"Container security finding", securityEventMessage(finding))
	if err != nil {
		s.logger.Error("delivering security notification", "finding_id", finding.ID, "error", err)
		if statusErr := s.store.SetNotificationStatus(ctx, finding.ID, "degraded"); statusErr != nil {
			s.logger.Error("recording degraded security notification", "finding_id", finding.ID, "error", statusErr)
		}
		return
	}
	if err := s.store.SetNotificationStatus(ctx, finding.ID, "delivered"); err != nil {
		s.logger.Error("recording security notification delivery", "finding_id", finding.ID, "error", err)
	}
}

// Summary returns aggregate active-finding posture.
func (s *Security) Summary(ctx context.Context) (model.SecuritySummary, error) {
	return s.store.Summary(ctx)
}

// Findings returns bounded findings matching query.
func (s *Security) Findings(ctx context.Context, query model.SecurityFindingQuery) ([]model.SecurityFinding, error) {
	return s.store.Findings(ctx, query)
}

// Acknowledge records an operator acknowledgement and audit entry.
func (s *Security) Acknowledge(ctx context.Context, id int64, actor string) (model.SecurityFinding, error) {
	finding, err := s.store.Acknowledge(ctx, id, actor)
	if err != nil {
		return model.SecurityFinding{}, err
	}
	return finding, s.recordAudit(ctx, actor, model.AuditSecurityAcknowledge, id)
}

// Resolve records an operator resolution and audit entry.
func (s *Security) Resolve(ctx context.Context, id int64, actor string) (model.SecurityFinding, error) {
	finding, err := s.store.Resolve(ctx, id, actor)
	if err != nil {
		return model.SecurityFinding{}, err
	}
	return finding, s.recordAudit(ctx, actor, model.AuditSecurityResolve, id)
}

// Suppress records a bounded operator suppression and audit entry.
func (s *Security) Suppress(ctx context.Context, id int64, suppression model.FindingSuppression) (model.SecurityFinding, error) {
	if strings.TrimSpace(suppression.Actor) == "" || strings.TrimSpace(suppression.Reason) == "" {
		return model.SecurityFinding{}, fmt.Errorf("%w: actor and reason are required", ErrInvalidInput)
	}
	finding, err := s.store.Suppress(ctx, id, suppression)
	if err != nil {
		return model.SecurityFinding{}, err
	}
	return finding, s.recordAudit(ctx, suppression.Actor, model.AuditSecuritySuppress, id)
}

func (s *Security) recordAudit(ctx context.Context, actor, action string, findingID int64) error {
	if s.audit == nil {
		return nil
	}
	_, err := s.audit.Record(ctx, model.AuditRecord{
		Actor: actor, Action: action, Result: model.AuditSuccess,
		Detail: fmt.Sprintf("security finding %d", findingID),
	})
	return err
}

func securityEventMessage(finding model.SecurityFinding) string {
	return fmt.Sprintf("%s finding %s for container %.12s", finding.Severity, finding.RuleID, finding.ContainerID)
}
