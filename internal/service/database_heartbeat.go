package service

import (
	"context"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/oralecarlangelo/touchgrass/internal/docker"
	"github.com/oralecarlangelo/touchgrass/internal/model"
)

const (
	databaseHeartbeatInterval = 30 * time.Second
	sqliteHeartbeatTimeout    = 2 * time.Second
	databaseWaiting           = "waiting"
	databaseUnavailable       = "unavailable"
	databaseStale             = "stale"
	databaseHealthy           = "healthy"
)

// Only briefly lock to read or replace completed observations. Collection
// never holds this lock while doing I/O.
type databaseHeartbeat struct {
	mu              sync.RWMutex
	external        []model.Database
	sqlite          model.Database
	dockerObserved  time.Time
	dockerExpires   time.Time
	discoveryFailed bool
}

func (d *Database) databaseTargets() []model.Database {
	return []model.Database{
		{
			ID: model.DatabasePostgres, Label: model.DatabasePostgresLabel, Configured: d.container != "",
			Container: d.container, Status: databaseWaiting,
		},
		{
			ID: model.DatabaseRedis, Label: model.DatabaseRedisLabel, Configured: d.redisContainer != "",
			Container: d.redisContainer, Status: databaseWaiting,
		},
	}
}

// Databases returns a copy of the heartbeat, without touching the host or any database.
func (d *Database) Databases() model.DatabaseSnapshot {
	d.snapshot.mu.RLock()
	defer d.snapshot.mu.RUnlock()

	now := time.Now()

	external := slices.Clone(d.snapshot.external)
	if external == nil {
		external = d.databaseTargets()
	}

	result := model.DatabaseSnapshot{InventoryStatus: databaseWaiting}

	observedAt := d.snapshot.dockerObserved
	if !observedAt.IsZero() {
		result.DockerObservedAt = &observedAt

		result.InventoryStatus = "current"
		if d.snapshot.discoveryFailed || now.After(d.snapshot.dockerExpires) {
			result.InventoryStatus = databaseStale
			for i := range external {
				external[i].Status = databaseStale
				external[i].Reachable = false
			}
		}
	} else if d.snapshot.discoveryFailed {
		result.InventoryStatus = databaseUnavailable
	}

	sqlite := d.snapshot.sqlite
	switch {
	case sqlite.ObservedAt == nil:
		sqlite = newSQLiteObservation()
	case now.Sub(*sqlite.ObservedAt) > 2*databaseHeartbeatInterval:
		sqlite.Status = databaseStale
		sqlite.Reachable = false
	}

	result.Databases = append(append(external[:2:2], sqlite), external[2:]...)

	return result
}

// ObserveContainers reuses the fleet's existing Docker inventory heartbeat.
// A nil list indicates an unsuccessful read; the last inventory stays visible.
func (d *Database) ObserveContainers(containers []docker.Container, observedAt time.Time, validFor time.Duration) {
	if containers == nil {
		d.snapshot.mu.Lock()
		d.snapshot.discoveryFailed = true
		d.snapshot.mu.Unlock()

		return
	}

	targets := d.databaseTargets()
	for i := range targets {
		targets[i].Status = "missing"
		targets[i].ObservedAt = &observedAt
	}

	discovered := []model.Database{}
	knownImages := d.discoveredDatabaseImages()

	for _, container := range containers {
		container.Image = inventoryImage(container, knownImages[container.ID])

		matched := false

		for i := range targets {
			if targets[i].Configured && matchesDatabaseTarget(targets[i].Container, container) {
				applyContainerObservation(&targets[i], container, &observedAt)

				matched = true
			}
		}

		if matched {
			continue
		}

		if label := databaseImageLabel(container.Image); label != "" {
			entry := model.Database{ID: "docker:" + container.ID, Label: label}
			applyContainerObservation(&entry, container, &observedAt)
			discovered = append(discovered, entry)
		}
	}

	slices.SortFunc(discovered, func(a, b model.Database) int {
		return strings.Compare(a.Container, b.Container)
	})

	d.snapshot.mu.Lock()
	defer d.snapshot.mu.Unlock()

	targets = append(targets, discovered...)

	d.snapshot.external = targets
	d.snapshot.dockerObserved = observedAt
	d.snapshot.dockerExpires = observedAt.Add(validFor)
	d.snapshot.discoveryFailed = false
}

func (d *Database) discoveredDatabaseImages() map[string]string {
	d.snapshot.mu.RLock()
	defer d.snapshot.mu.RUnlock()

	known := map[string]string{}

	for _, database := range d.snapshot.external {
		if id, ok := strings.CutPrefix(database.ID, "docker:"); ok {
			known[id] = database.Image
		}
	}

	return known
}

func matchesDatabaseTarget(target string, container docker.Container) bool {
	return strings.TrimPrefix(target, "/") == container.Name ||
		(len(target) >= 12 && strings.HasPrefix(container.ID, target))
}

func applyContainerObservation(entry *model.Database, container docker.Container, observedAt *time.Time) {
	entry.Container, entry.Image, entry.ObservedAt = container.Name, container.Image, observedAt

	entry.Status = container.State
	if container.State == "running" {
		switch {
		case strings.HasSuffix(container.Status, "(unhealthy)"):
			entry.Status = "unhealthy"
		case strings.HasSuffix(container.Status, "(health: starting)"):
			entry.Status = "starting"
		case strings.HasSuffix(container.Status, "(healthy)"):
			entry.Status = databaseHealthy
		}
	}
	// A running process alone provides no proof of database readiness.
	entry.Reachable = entry.Status == databaseHealthy
}

func databaseImageLabel(image string) string {
	repository, _, _ := strings.Cut(image, "@")
	repository, _, _ = strings.Cut(repository, ":")
	repository = strings.TrimPrefix(repository, "docker.io/")
	repository = strings.TrimPrefix(repository, "index.docker.io/")

	repository = strings.TrimPrefix(repository, "library/")
	switch repository {
	case "postgres":
		return model.DatabasePostgresLabel
	case "redis":
		return model.DatabaseRedisLabel
	case "valkey/valkey":
		return "Valkey"
	case "mysql":
		return "MySQL"
	case "mariadb":
		return "MariaDB"
	case "mongo":
		return "MongoDB"
	default:
		return ""
	}
}

func newSQLiteObservation() model.Database {
	return model.Database{
		ID: model.DatabaseSQLite, Label: model.DatabaseSQLiteLabel,
		Configured: true, Status: databaseWaiting,
	}
}

// RunHeartbeat samples SQLite metadata independent of browser traffic.
func (d *Database) RunHeartbeat(ctx context.Context) {
	ticker := time.NewTicker(databaseHeartbeatInterval)
	defer ticker.Stop()

	for ctx.Err() == nil {
		d.sampleSQLite(ctx)

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (d *Database) sampleSQLite(ctx context.Context) {
	observation := newSQLiteObservation()
	now := time.Now().UTC()
	observation.ObservedAt = &now
	observation.Status = databaseUnavailable

	if d.sqlite != nil {
		probeCtx, cancel := context.WithTimeout(ctx, sqliteHeartbeatTimeout)
		defer cancel()

		size := databaseBytes(d.dbPath)

		observation.SizeBytes = &size
		if version, err := d.sqlite.SQLiteVersion(probeCtx); err == nil {
			observation.Version = &version
			observation.Status = databaseHealthy
			observation.Reachable = true
		}
	}

	d.snapshot.mu.Lock()
	d.snapshot.sqlite = observation
	d.snapshot.mu.Unlock()
}
