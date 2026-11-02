package service

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/oralecarlangelo/touchgrass/internal/model"
)

const defaultSecurityEventInterval = 5 * time.Second

// SecurityEventSource reads a bounded privacy-safe event stream. Acknowledge
// advances its in-memory cursor only after Touchgrass persists the batch.
type SecurityEventSource interface {
	Read(context.Context) ([]model.SecurityEvent, model.CollectorCursor, error)
	Acknowledge(model.CollectorCursor)
}

// SecurityEventCollectorConfig wires an optional host security-event source.
type SecurityEventCollectorConfig struct {
	ServiceID string
	Source    SecurityEventSource
	Security  *Security
	Cursors   collectorCursorStore
	Interval  time.Duration
	Logger    *slog.Logger
}

// SecurityEventCollector turns trusted, bounded edge events into durable
// security findings without retaining request paths or client identifiers.
type SecurityEventCollector struct {
	serviceID string
	source    SecurityEventSource
	security  *Security
	cursors   collectorCursorStore
	interval  time.Duration
	logger    *slog.Logger
}

// NewSecurityEventCollector builds an optional event collector.
func NewSecurityEventCollector(cfg SecurityEventCollectorConfig) *SecurityEventCollector {
	interval := cfg.Interval
	if interval <= 0 {
		interval = defaultSecurityEventInterval
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &SecurityEventCollector{
		serviceID: cfg.ServiceID, source: cfg.Source, security: cfg.Security,
		cursors: cfg.Cursors, interval: interval, logger: logger,
	}
}

// Run collects immediately and then on the configured interval.
func (c *SecurityEventCollector) Run(ctx context.Context) {
	if c == nil || c.source == nil || c.security == nil || c.cursors == nil || c.serviceID == "" {
		return
	}
	if err := c.collect(ctx); err != nil && ctx.Err() == nil {
		c.logger.Warn("security event collection failed", "error", err)
	}
	ticker := time.NewTicker(c.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := c.collect(ctx); err != nil {
				c.logger.Warn("security event collection failed", "error", err)
			}
		}
	}
}

func (c *SecurityEventCollector) collect(ctx context.Context) error {
	events, cursor, err := c.source.Read(ctx)
	if err != nil {
		return fmt.Errorf("reading security events: %w", err)
	}
	for _, event := range events {
		_, err := c.security.Evaluate(ctx, model.SecurityEvaluation{
			ServiceID: c.serviceID, BaselineFingerprint: event.EventType,
			Observation: model.SecurityObservation{
				ContainerID: "source:" + event.Source, RuleID: event.RuleID,
				Category: event.Category, Severity: event.Severity, Confidence: event.Confidence,
				Detected: true, CoverageComplete: true, ObservedAt: event.Timestamp,
				Evidence: map[string]string{
					"event_type": event.EventType, "source": event.Source, "action": event.Action,
					"method": event.Method, "status": strconv.Itoa(event.Status),
				},
			},
		})
		if err != nil {
			return fmt.Errorf("evaluating security event: %w", err)
		}
	}
	if err := c.cursors.Upsert(ctx, cursor); err != nil {
		return fmt.Errorf("persisting security event cursor: %w", err)
	}
	c.source.Acknowledge(cursor)
	return nil
}
