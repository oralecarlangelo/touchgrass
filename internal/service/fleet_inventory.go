package service

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"time"

	"github.com/docker/docker/errdefs"
	"github.com/oralecarlangelo/touchgrass/internal/docker"
	"github.com/oralecarlangelo/touchgrass/internal/model"
	"github.com/oralecarlangelo/touchgrass/internal/store"
)

const (
	defaultInventoryInterval = 30 * time.Second
	defaultInspectLimit      = 4
)

// FleetInventoryConfig wires the authoritative all-state reconciler.
type FleetInventoryConfig struct {
	Services     *store.ServiceStore
	Containers   *store.ContainerStore
	Docker       docker.InventoryLister
	Interval     time.Duration
	InspectLimit int
	Logger       *slog.Logger
}

// FleetInventory reconciles complete Docker inventory into durable identity.
type FleetInventory struct {
	services     *store.ServiceStore
	containers   *store.ContainerStore
	docker       docker.InventoryLister
	interval     time.Duration
	inspectLimit int
	logger       *slog.Logger
	requests     chan struct{}
	reconcileMu  sync.Mutex
}

// NewFleetInventory builds one authoritative inventory reconciler.
func NewFleetInventory(cfg FleetInventoryConfig) *FleetInventory {
	interval := cfg.Interval
	if interval <= 0 {
		interval = defaultInventoryInterval
	}
	inspectLimit := cfg.InspectLimit
	if inspectLimit <= 0 {
		inspectLimit = defaultInspectLimit
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &FleetInventory{
		services: cfg.Services, containers: cfg.Containers, docker: cfg.Docker,
		interval: interval, inspectLimit: inspectLimit, logger: logger,
		requests: make(chan struct{}, 1),
	}
}

// Run performs an immediate reconciliation and then responds to one ticker and
// coalesced lifecycle wakeups until cancellation.
func (f *FleetInventory) Run(ctx context.Context) {
	if f == nil || f.docker == nil || f.services == nil || f.containers == nil {
		return
	}
	f.reconcileAndLog(ctx)
	ticker := time.NewTicker(f.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			f.reconcileAndLog(ctx)
		case <-f.requests:
			f.reconcileAndLog(ctx)
		}
	}
}

// RequestReconcile queues at most one event-driven reconciliation.
func (f *FleetInventory) RequestReconcile() {
	if f == nil {
		return
	}
	select {
	case f.requests <- struct{}{}:
	default:
	}
}

func (f *FleetInventory) reconcileAndLog(ctx context.Context) {
	if _, err := f.Reconcile(ctx); err != nil && ctx.Err() == nil {
		f.logger.Warn("container inventory reconciliation failed", "error_category", "docker_inventory_unavailable")
	}
}

// Reconcile reads one complete daemon inventory and persists it atomically.
func (f *FleetInventory) Reconcile(ctx context.Context) (model.InventorySummary, error) {
	if f == nil || f.docker == nil || f.services == nil || f.containers == nil {
		return model.InventorySummary{}, fmt.Errorf("reconciling container inventory: dependencies are required")
	}
	f.reconcileMu.Lock()
	defer f.reconcileMu.Unlock()

	observedAt := time.Now().UTC()
	containers, err := f.docker.ListAll(ctx)
	if err != nil {
		writeErr := f.containers.Reconcile(ctx, model.InventoryBatch{
			ObservedAt: observedAt, Complete: false, LastErrorCategory: "docker_inventory_unavailable",
		})
		if writeErr != nil {
			return model.InventorySummary{}, fmt.Errorf("recording failed inventory: %w", writeErr)
		}
		return f.summaryWithError(ctx, fmt.Errorf("listing complete container inventory: %w", err))
	}

	definitions, err := f.services.All(ctx)
	if err != nil {
		return model.InventorySummary{}, fmt.Errorf("loading services for inventory: %w", err)
	}
	owners, err := containerOwners(definitions, containers)
	if err != nil {
		return model.InventorySummary{}, err
	}

	instances := f.inspectAll(ctx, containers, owners, observedAt)
	if err := f.containers.Reconcile(ctx, model.InventoryBatch{
		ObservedAt: observedAt, Instances: instances, Complete: true,
	}); err != nil {
		return model.InventorySummary{}, fmt.Errorf("persisting container inventory: %w", err)
	}
	return f.containers.Summary(ctx)
}

func (f *FleetInventory) summaryWithError(ctx context.Context, reconcileErr error) (model.InventorySummary, error) {
	summary, err := f.containers.Summary(ctx)
	if err != nil {
		return model.InventorySummary{}, fmt.Errorf("%v; reading inventory summary: %w", reconcileErr, err)
	}
	return summary, reconcileErr
}

type inspectionResult struct {
	index      int
	inspection docker.Inspection
	err        error
}

func (f *FleetInventory) inspectAll(
	ctx context.Context,
	containers []docker.Container,
	owners map[string]string,
	observedAt time.Time,
) []model.ContainerInstance {
	jobs := make(chan int)
	results := make(chan inspectionResult, len(containers))
	workers := min(f.inspectLimit, max(len(containers), 1))
	var group sync.WaitGroup
	for range workers {
		group.Go(func() {
			for index := range jobs {
				inspection, err := f.docker.Inspect(ctx, containers[index].ID)
				results <- inspectionResult{index: index, inspection: inspection, err: err}
			}
		})
	}
	go func() {
		defer close(jobs)
		for index := range containers {
			select {
			case <-ctx.Done():
				return
			case jobs <- index:
			}
		}
	}()
	go func() {
		group.Wait()
		close(results)
	}()

	byIndex := make(map[int]docker.Inspection, len(containers))
	vanished := make(map[int]bool)
	for result := range results {
		if result.err != nil {
			if errdefs.IsNotFound(result.err) {
				vanished[result.index] = true
				continue
			}
			f.logger.Warn("container inspection failed", "container", containers[result.index].Name,
				"error_category", "docker_inspect_unavailable")
			continue
		}
		byIndex[result.index] = result.inspection
	}

	instances := make([]model.ContainerInstance, 0, len(containers)-len(vanished))
	for index, item := range containers {
		if vanished[index] {
			continue
		}
		classification := model.ContainerClassificationUnknown
		if owners[item.ID] != "" {
			classification = model.ContainerClassificationManaged
		}
		instances = append(instances, model.ContainerInstance{
			ID: item.ID, Name: item.Name, Image: item.Image, ImageID: item.ImageID,
			State: item.State, Status: item.Status,
			ComposeProject: item.Labels[docker.LabelComposeProject],
			ComposeService: item.Labels[docker.LabelComposeService],
			ServiceID:      owners[item.ID], Classification: classification,
			Current: true, Present: true, FirstSeenAt: observedAt, LastSeenAt: observedAt,
			ImageFingerprint: item.ImageID, Inspection: safeInspection(byIndex[index]),
		})
	}
	return instances
}

// Containers returns bounded inventory rows.
func (f *FleetInventory) Containers(ctx context.Context, query model.ContainerQuery) ([]model.ContainerInstance, error) {
	return f.containers.List(ctx, query)
}

// Container returns one durable container identity.
func (f *FleetInventory) Container(ctx context.Context, id string) (model.ContainerInstance, error) {
	return f.containers.Get(ctx, id)
}

// Summary returns inventory freshness using two missed intervals as the stale
// threshold.
func (f *FleetInventory) Summary(ctx context.Context) (model.InventorySummary, error) {
	summary, err := f.containers.Summary(ctx)
	if err != nil {
		return model.InventorySummary{}, err
	}
	summary.Stale = summary.ReconciledAt == nil || time.Since(*summary.ReconciledAt) > 2*f.interval
	return summary, nil
}

func safeInspection(value docker.Inspection) model.SafeInspection {
	mounts := make([]model.SafeMount, 0, len(value.Mounts))
	for _, item := range value.Mounts {
		mounts = append(mounts, model.SafeMount{Type: item.Type, Destination: item.Destination, Writable: item.Writable})
	}
	labels := make(map[string]string, len(value.Labels))
	for key, label := range value.Labels {
		labels[key] = label
	}
	return model.SafeInspection{
		User: value.User, EntrypointHash: value.EntrypointHash, CommandHash: value.CommandHash,
		Privileged: value.Privileged, ReadonlyRootFS: value.ReadonlyRootFS,
		CapAdd: slices.Clone(value.CapAdd), PIDMode: value.PIDMode, IPCMode: value.IPCMode,
		NetworkMode: value.NetworkMode, RestartPolicy: value.RestartPolicy,
		MemoryBytes: value.MemoryBytes, NanoCPUs: value.NanoCPUs, PIDsLimit: value.PIDsLimit,
		Mounts: mounts, HasDockerSocket: value.HasDockerSocket, Labels: labels,
	}
}

func containerOwners(definitions []model.Service, containers []docker.Container) (map[string]string, error) {
	owners := make(map[string]string, len(containers))
	for _, definition := range definitions {
		names, err := serviceNames(definition)
		if err != nil {
			return nil, err
		}
		wanted := make(map[string]bool, len(names))
		for _, name := range names {
			wanted[name] = true
		}
		for _, item := range containers {
			if belongsTo(item, definition.ComposeProject, wanted) {
				owners[item.ID] = definition.ID
			}
		}
	}
	return owners, nil
}
