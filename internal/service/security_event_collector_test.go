package service

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/oralecarlangelo/touchgrass/internal/model"
	"github.com/oralecarlangelo/touchgrass/internal/store"
)

type fakeSecurityEventSource struct {
	events       []model.SecurityEvent
	cursor       model.CollectorCursor
	acknowledged bool
}

func (s *fakeSecurityEventSource) Read(context.Context) ([]model.SecurityEvent, model.CollectorCursor, error) {
	return s.events, s.cursor, nil
}

func (s *fakeSecurityEventSource) Acknowledge(model.CollectorCursor) { s.acknowledged = true }

func TestSecurityEventCollectorTagsAndPersistsFinding(t *testing.T) {
	db := openInventoryDB(t)
	ctx := context.Background()
	securityStore := store.NewSecurityStore(db)
	if _, err := securityStore.Activate(ctx, "test"); err != nil {
		t.Fatal(err)
	}

	source := &fakeSecurityEventSource{
		events: []model.SecurityEvent{{
			Timestamp: time.Date(2026, 11, 2, 5, 4, 53, 0, time.UTC),
			EventType: "security_probe", RuleID: "edge.malformed_server_action",
			Category: model.SecurityCategoryTraffic, Severity: model.SecuritySeverityMedium,
			Confidence: model.SecurityConfidenceHigh, Source: "nginx", Action: "blocked",
			Method: "POST", Status: 404,
		}},
		cursor: model.CollectorCursor{Source: "security_events", Offset: 1},
	}
	collector := NewSecurityEventCollector(SecurityEventCollectorConfig{
		ServiceID: "web", Source: source, Security: NewSecurity(SecurityConfig{Store: securityStore}),
		Cursors: store.NewCursorStore(db), Logger: slog.New(slog.DiscardHandler),
	})

	if err := collector.collect(ctx); err != nil {
		t.Fatalf("collect() error = %v", err)
	}
	if !source.acknowledged {
		t.Fatal("source cursor was not acknowledged")
	}

	findings, err := securityStore.Findings(ctx, model.SecurityFindingQuery{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 1 {
		t.Fatalf("findings = %d, want 1", len(findings))
	}
	got := findings[0]
	if got.RuleID != "edge.malformed_server_action" || got.Category != model.SecurityCategoryTraffic ||
		got.Severity != model.SecuritySeverityMedium || got.Evidence["event_type"] != "security_probe" ||
		got.Evidence["action"] != "blocked" || got.Evidence["source"] != "nginx" {
		t.Fatalf("finding = %+v", got)
	}
}
