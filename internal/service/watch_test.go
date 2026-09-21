package service

import (
	"context"
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
	inv.checkTarget(def, last, failed, emit)

	if last[def.ID] != "127.0.0.1:4101" || len(events) != 0 {
		t.Fatalf("baseline = (%v, %d events), want target set, no events", last, len(events))
	}

	// Unchanged target stays silent.
	inv.checkTarget(def, last, failed, emit)

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

	inv.checkTarget(def, last, failed, emit)

	if len(events) != 1 || events[0].Type != model.EventNginxChanged {
		t.Fatalf("events = %+v, want one nginx_changed", events)
	}

	// Read errors mark failed without emitting.
	broken, _ := testBlueGreenDef(t, "", "/nonexistent.conf")
	broken.ID = "broken"

	inv.checkTarget(broken, last, failed, emit)

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
