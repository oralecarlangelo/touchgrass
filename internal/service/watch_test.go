package service

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/oralecarlangelo/touchgrass/internal/model"
	"github.com/oralecarlangelo/touchgrass/internal/store"
)

func TestCheckTarget(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	db := openInventoryDB(t)
	inv := NewInventory(store.NewServiceStore(db), stubLister{}, stubProber{}, slog.New(slog.DiscardHandler))

	conf := filepath.Join(t.TempDir(), "nginx.conf")
	if err := os.WriteFile(conf, []byte(nginxBlueConf), 0o600); err != nil {
		t.Fatalf("writing conf: %v", err)
	}

	def, _ := testBlueGreenDef(t, "", conf)

	last := map[string]string{}
	failed := map[string]bool{}

	var events []model.Event

	emit := func(event model.Event) {
		events = append(events, event)
	}

	// Baseline sets state without emitting.
	inv.checkTarget(ctx, def, last, failed, emit)

	if last[def.ID] != "127.0.0.1:4101" || len(events) != 0 {
		t.Fatalf("baseline = (%v, %d events), want target set, no events", last, len(events))
	}

	// Unchanged target stays silent.
	inv.checkTarget(ctx, def, last, failed, emit)

	if len(events) != 0 {
		t.Fatalf("events = %d, want 0 (unchanged)", len(events))
	}

	// Changed target emits once.
	green := `upstream tn_api_active {
    server 127.0.0.1:4102; # BLUEGREEN-ACTIVE
}
`
	if err := os.WriteFile(conf, []byte(green), 0o600); err != nil {
		t.Fatalf("writing conf: %v", err)
	}

	inv.checkTarget(ctx, def, last, failed, emit)

	if len(events) != 1 || events[0].Type != model.EventNginxChanged {
		t.Fatalf("events = %+v, want one nginx_changed", events)
	}

	// Read errors mark failed without emitting.
	broken, _ := testBlueGreenDef(t, "", "/nonexistent.conf")
	broken.ID = "broken"

	inv.checkTarget(ctx, broken, last, failed, emit)

	if !failed["broken"] || len(events) != 1 {
		t.Errorf("failed = %v, events = %d; want failed set, no new events", failed, len(events))
	}
}

func TestWatchStopsOnCancel(t *testing.T) {
	t.Parallel()

	db := openInventoryDB(t)
	inv := NewInventory(store.NewServiceStore(db), stubLister{}, stubProber{}, slog.New(slog.DiscardHandler))

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})

	go func() {
		defer close(done)

		inv.Watch(ctx, 5*time.Millisecond, func(model.Event) {})
	}()

	time.Sleep(20 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Watch() did not stop after cancel")
	}
}

func TestCheckTargetFlipReporting(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		reporter    bool
		active      bool
		wantRecords bool
	}{
		{name: "external flip records", reporter: true, active: false, wantRecords: true},
		{name: "own run stays event-only", reporter: true, active: true, wantRecords: false},
		{name: "no reporter stays event-only", reporter: false, active: false, wantRecords: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			checkFlipReporting(t, tt.reporter, tt.active, tt.wantRecords)
		})
	}
}

// checkFlipReporting flips one watched target and asserts durable records.
func checkFlipReporting(t *testing.T, reporter, active, wantRecords bool) {
	t.Helper()

	ctx := context.Background()
	db := openInventoryDB(t)
	services := store.NewServiceStore(db)
	inv := NewInventory(services, stubLister{}, stubProber{}, slog.New(slog.DiscardHandler))

	conf := filepath.Join(t.TempDir(), "nginx.conf")
	if err := os.WriteFile(conf, []byte(nginxBlueConf), 0o600); err != nil {
		t.Fatalf("writing conf: %v", err)
	}

	_, cfg := testBlueGreenDef(t, "", conf)

	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshaling config: %v", err)
	}

	if err := services.UpdateConfig(ctx, testServiceAPI, raw); err != nil {
		t.Fatalf("UpdateConfig() error = %v", err)
	}

	def, err := services.Get(ctx, testServiceAPI)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}

	if reporter {
		inv.SetFlipReporter(
			store.NewNotificationStore(db),
			NewAudit(services, store.NewAuditStore(db)),
			func(string) bool { return active },
		)
	}

	last := map[string]string{}
	failed := map[string]bool{}

	var events []model.Event

	emit := func(event model.Event) {
		events = append(events, event)
	}

	inv.checkTarget(ctx, def, last, failed, nil)
	writeGreenConf(t, conf)
	inv.checkTarget(ctx, def, last, failed, emit)

	assertFlipRecords(t, db, services, def, events, wantRecords)
}

// writeGreenConf points the test marker at the green target.
func writeGreenConf(t *testing.T, conf string) {
	t.Helper()

	green := `upstream tn_api_active {
    server 127.0.0.1:4102; # BLUEGREEN-ACTIVE
}
`
	if err := os.WriteFile(conf, []byte(green), 0o600); err != nil {
		t.Fatalf("writing conf: %v", err)
	}
}

// assertFlipRecords checks the event plus durable notification/audit rows.
func assertFlipRecords(
	t *testing.T,
	db *store.DB,
	services *store.ServiceStore,
	def model.Service,
	events []model.Event,
	wantRecords bool,
) {
	t.Helper()

	ctx := context.Background()

	if len(events) != 1 {
		t.Fatalf("events = %d, want 1", len(events))
	}

	notifications, err := store.NewNotificationStore(db).List(ctx, def.ID, 10)
	if err != nil {
		t.Fatalf("notifications List() error = %v", err)
	}

	audits, err := NewAudit(services, store.NewAuditStore(db)).List(ctx, def.ID, 10)
	if err != nil {
		t.Fatalf("audit List() error = %v", err)
	}

	want := 0
	if wantRecords {
		want = 1
	}

	if len(notifications) != want {
		t.Errorf("notifications = %d, want %d", len(notifications), want)
	}

	if len(audits) != want {
		t.Errorf("audits = %d, want %d", len(audits), want)
	}

	if !wantRecords {
		return
	}

	if notifications[0].Kind != model.NotificationDeploy {
		t.Errorf("kind = %q, want deploy", notifications[0].Kind)
	}

	if audits[0].Actor != actorSystem || audits[0].Action != model.AuditCutover {
		t.Errorf("audit = %s/%s, want system/cutover", audits[0].Actor, audits[0].Action)
	}
}
