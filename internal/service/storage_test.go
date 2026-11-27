package service

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/oralecarlangelo/touchgrass/internal/docker"
	"github.com/oralecarlangelo/touchgrass/internal/model"
)

type storageSnapshotDaemon struct {
	stubDaemonQuerier
	storageCalls int
	imageCalls   int
	usage        model.StorageUsage
	err          error
}

func (d *storageSnapshotDaemon) Images(ctx context.Context) ([]docker.Image, error) {
	d.imageCalls++
	return d.stubDaemonQuerier.Images(ctx)
}

func (d *storageSnapshotDaemon) StorageUsage(context.Context) (model.StorageUsage, error) {
	d.storageCalls++
	return d.usage, d.err
}

func TestStorageSnapshotsAreImmediateAndPreserveLastSuccess(t *testing.T) {
	t.Parallel()

	size := int64(1234)
	daemon := &storageSnapshotDaemon{
		stubDaemonQuerier: stubDaemonQuerier{images: []docker.Image{{ID: "generic-image", Size: 1234}}},
		usage:             model.StorageUsage{SampledAt: time.Now(), ImageLayersBytes: &size, Entries: []model.StorageEntry{{ID: "generic-volume", Kind: "volume"}}},
	}
	system := NewSystem(SystemConfig{Docker: daemon, Logger: slog.New(slog.DiscardHandler)})

	checkColdStorageSnapshot(t, system, daemon)

	system.refreshStorage(t.Context())

	fresh := readStorageSnapshot(t, system)
	if fresh.Freshness.Stale || !fresh.Freshness.Complete || len(fresh.Entries) != 1 {
		t.Fatalf("bad fresh snapshot: %+v", fresh)
	}

	daemon.err = errors.New("daemon unavailable")
	daemon.imagesErr = daemon.err

	system.refreshStorage(t.Context())

	stale := readStorageSnapshot(t, system)
	if !stale.Freshness.Stale || stale.ImageLayersBytes == nil || *stale.ImageLayersBytes != size || len(stale.Entries) != 1 {
		t.Fatalf("failed refresh lost last good data: %+v", stale)
	}

	images := readImageSnapshot(t, system)
	if len(images) != 1 || !system.ImagesFreshness().Stale {
		t.Fatal("failed image refresh lost prior inventory or freshness")
	}

	checkStorageSnapshotIsolation(t, system, daemon)
}

func TestStorageSnapshotAgesWithoutSuccessfulRefresh(t *testing.T) {
	t.Parallel()

	system := NewSystem(SystemConfig{Docker: &storageSnapshotDaemon{}, Logger: slog.New(slog.DiscardHandler)})
	observed := time.Now().Add(-time.Hour)
	system.storageMaxAge = time.Minute
	system.storage.Freshness = model.CollectionFreshness{ObservedAt: &observed, Complete: true}
	system.imagesFreshness = system.storage.Freshness

	usage := readStorageSnapshot(t, system)
	if !usage.Freshness.Stale || !system.ImagesFreshness().Stale {
		t.Fatal("old snapshots must age to stale")
	}
}

func readStorageSnapshot(t *testing.T, system *System) model.StorageUsage {
	t.Helper()

	usage, err := system.StorageUsage(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	return usage
}

func readImageSnapshot(t *testing.T, system *System) []model.ImageView {
	t.Helper()

	images, err := system.Images(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	return images
}

func checkStorageSnapshotIsolation(t *testing.T, system *System, daemon *storageSnapshotDaemon) {
	t.Helper()

	calls := daemon.storageCalls

	for range 100 {
		snapshot := readStorageSnapshot(t, system)
		snapshot.Entries[0].ID = "caller-mutation"
	}

	preserved := readStorageSnapshot(t, system)
	if daemon.storageCalls != calls || preserved.Entries[0].ID != "generic-volume" {
		t.Fatal("reads triggered collection or mutated shared entries")
	}
}

func checkColdStorageSnapshot(t *testing.T, system *System, daemon *storageSnapshotDaemon) {
	t.Helper()

	cold := readStorageSnapshot(t, system)
	if !cold.Freshness.Stale || cold.Freshness.ObservedAt != nil || cold.ImageLayersBytes != nil {
		t.Fatalf("cold snapshot must be explicitly unknown: %+v", cold)
	}

	readImageSnapshot(t, system)

	if daemon.storageCalls != 0 || daemon.imageCalls != 0 {
		t.Fatal("cache reads called Docker")
	}
}
