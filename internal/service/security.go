package service

import (
	"context"
	"errors"
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
	Cursors       collectorCursorStore
	Audit         *Audit
	Emit          func(model.Event)
	NotifyHigh    bool
	Logger        *slog.Logger
}

// Security owns finding episodes, notification delivery, and operator actions.
type Security struct {
	store         *store.SecurityStore
	notifications *store.NotificationStore
	cursors       collectorCursorStore
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
		store: cfg.Store, notifications: cfg.Notifications, cursors: cfg.Cursors, audit: cfg.Audit,
		emit: emit, notifyHigh: cfg.NotifyHigh, logger: logger,
	}
}

type collectorCursorStore interface {
	Get(context.Context, string) (model.CollectorCursor, error)
	Upsert(context.Context, model.CollectorCursor) error
}

// RecordCollector persists explicit coverage health and translates failures
// into one deduplicated finding episode with one recovery transition.
func (s *Security) RecordCollector(
	ctx context.Context,
	source string,
	complete bool,
	errorCategory string,
	effectiveInterval time.Duration,
) error {
	switch source {
	case "security_posture", "security_process", "security_diff":
	default:
		return fmt.Errorf("%w: unknown security collector", ErrInvalidInput)
	}
	if len(errorCategory) > 128 || strings.ContainsAny(errorCategory, "\r\n\x00") {
		return fmt.Errorf("%w: unsafe collector error category", ErrInvalidInput)
	}
	now := time.Now().UTC()
	if s.cursors != nil {
		cursor, err := s.cursors.Get(ctx, source)
		if err != nil && !errors.Is(err, store.ErrCollectorCursorNotFound) {
			return fmt.Errorf("reading security collector health: %w", err)
		}
		cursor.Source = source
		cursor.LastRecordedAt = now
		cursor.CoverageComplete = complete
		cursor.EffectiveIntervalSecs = int64(effectiveInterval / time.Second)
		cursor.LastError = errorCategory
		if complete {
			cursor.LastSuccessAt = now
		}
		if err := s.cursors.Upsert(ctx, cursor); err != nil {
			return fmt.Errorf("recording security collector health: %w", err)
		}
	}
	activation, err := s.store.Activation(ctx)
	if err != nil {
		return fmt.Errorf("reading security activation for collector: %w", err)
	}
	if activation.ActivatedAt == nil {
		return nil
	}
	_, err = s.Evaluate(ctx, model.SecurityEvaluation{
		BaselineFingerprint: source,
		Observation: model.SecurityObservation{
			ContainerID: "collector:" + source, RuleID: "collector." + source + ".failure",
			Category: model.SecurityCategoryDrift, Severity: model.SecuritySeverityHigh,
			Confidence: model.SecurityConfidenceHigh, Detected: !complete, CoverageComplete: complete,
			Evidence: map[string]string{
				"source": source, "error_category": errorCategory,
				"effective_interval_secs": fmt.Sprintf("%d", int64(effectiveInterval/time.Second)),
			}, ObservedAt: now,
		},
	})
	return err
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
	shouldNotify := finding.Severity == model.SecuritySeverityCritical ||
		(s.notifyHigh && finding.Severity == model.SecuritySeverityHigh)
	if s.notifications == nil || !shouldNotify {
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
	if err := s.expireSuppressions(ctx); err != nil {
		return model.SecuritySummary{}, err
	}
	return s.store.Summary(ctx)
}

// ActiveRiskCounts returns active finding counts for one bounded inventory page.
func (s *Security) ActiveRiskCounts(ctx context.Context, containerIDs []string) (map[string]map[model.SecuritySeverity]int, error) {
	return s.store.ActiveRiskCounts(ctx, containerIDs)
}

// Findings returns bounded findings matching query.
func (s *Security) Findings(ctx context.Context, query model.SecurityFindingQuery) ([]model.SecurityFinding, error) {
	if err := s.expireSuppressions(ctx); err != nil {
		return nil, err
	}
	return s.store.Findings(ctx, query)
}

// Finding returns one finding episode by id.
func (s *Security) Finding(ctx context.Context, id int64) (model.SecurityFinding, error) {
	if err := s.expireSuppressions(ctx); err != nil {
		return model.SecurityFinding{}, err
	}
	return s.store.Finding(ctx, id)
}

// RunSuppressionExpiry reopens elapsed temporary suppressions even when a
// container is stopped and produces no further scanner observations.
func (s *Security) RunSuppressionExpiry(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = time.Minute
	}
	if err := s.expireSuppressions(ctx); err != nil && ctx.Err() == nil {
		s.logger.Error("expiring container security suppressions", "error", err)
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := s.expireSuppressions(ctx); err != nil {
				s.logger.Error("expiring container security suppressions", "error", err)
			}
		}
	}
}

func (s *Security) expireSuppressions(ctx context.Context) error {
	findings, err := s.store.ExpireSuppressions(ctx, time.Now().UTC())
	if err != nil {
		return err
	}
	for _, finding := range findings {
		s.notifyOpened(ctx, "", finding)
		s.emit(model.Event{
			Type: model.EventSecurityFinding, Message: securityEventMessage(finding), At: time.Now().UTC(),
		})
	}
	return nil
}

// Policies returns every persisted security policy.
func (s *Security) Policies(ctx context.Context) ([]model.SecurityPolicy, error) {
	return s.store.Policies(ctx)
}

// CreatePolicy persists and audits a policy.
func (s *Security) CreatePolicy(ctx context.Context, policy model.SecurityPolicy, actor string) (model.SecurityPolicy, error) {
	created, err := s.store.CreatePolicy(ctx, policy, actor)
	if err != nil {
		return model.SecurityPolicy{}, err
	}
	return created, nil
}

// UpdatePolicy replaces and audits a policy body.
func (s *Security) UpdatePolicy(ctx context.Context, id int64, policy model.SecurityPolicy, actor string) (model.SecurityPolicy, error) {
	updated, err := s.store.UpdatePolicy(ctx, id, policy, actor)
	if err != nil {
		return model.SecurityPolicy{}, err
	}
	return updated, nil
}

// DeletePolicy deletes and audits a policy.
func (s *Security) DeletePolicy(ctx context.Context, id int64, actor string) error {
	if strings.TrimSpace(actor) == "" {
		return fmt.Errorf("%w: actor is required", ErrInvalidInput)
	}
	if err := s.store.DeletePolicy(ctx, id, actor); err != nil {
		return err
	}
	return nil
}

// Acknowledge records an operator acknowledgement and audit entry.
func (s *Security) Acknowledge(ctx context.Context, id int64, actor string) (model.SecurityFinding, error) {
	finding, err := s.store.Acknowledge(ctx, id, actor)
	if err != nil {
		return model.SecurityFinding{}, err
	}
	return finding, nil
}

// Resolve records an operator resolution and audit entry.
func (s *Security) Resolve(ctx context.Context, id int64, actor string) (model.SecurityFinding, error) {
	finding, err := s.store.Resolve(ctx, id, actor)
	if err != nil {
		return model.SecurityFinding{}, err
	}
	return finding, nil
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
	return finding, nil
}

func securityEventMessage(finding model.SecurityFinding) string {
	return fmt.Sprintf("Finding #%d · %s · %s · container %.12s", finding.ID, finding.Severity, finding.RuleID, finding.ContainerID)
}
