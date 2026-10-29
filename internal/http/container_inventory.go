package http

import (
	"errors"
	"strconv"
	"time"

	nethttp "net/http"

	"github.com/oralecarlangelo/touchgrass/internal/model"
	"github.com/oralecarlangelo/touchgrass/internal/store"
)

type containerRiskCounts struct {
	Critical int `json:"critical"`
	High     int `json:"high"`
	Medium   int `json:"medium"`
	Low      int `json:"low"`
}

// containerListItem preserves the original fleet fields while adding durable
// identity, lifecycle, classification, and nullable live metrics.
type containerListItem struct {
	ID             string                        `json:"id,omitempty"`
	Name           string                        `json:"name"`
	Project        string                        `json:"project"`
	Managed        bool                          `json:"managed"`
	ServiceID      string                        `json:"service_id,omitempty"`
	State          string                        `json:"state"`
	Status         string                        `json:"status,omitempty"`
	Classification model.ContainerClassification `json:"classification"`
	Current        bool                          `json:"current"`
	Present        bool                          `json:"present"`
	Health         string                        `json:"health"`
	CPUPercent     *float64                      `json:"cpu_percent"`
	MemBytes       *uint64                       `json:"mem_bytes"`
	MemLimit       *uint64                       `json:"mem_limit"`
	Restarts       *int                          `json:"restarts"`
	SampledAt      *time.Time                    `json:"sampled_at,omitempty"`
	FirstSeenAt    *time.Time                    `json:"first_seen_at,omitempty"`
	LastSeenAt     *time.Time                    `json:"last_seen_at,omitempty"`
	AbsentAt       *time.Time                    `json:"absent_at,omitempty"`
	RiskCounts     containerRiskCounts           `json:"risk_counts"`
}

type collectorFreshness struct {
	ReconciledAt      *time.Time `json:"reconciled_at"`
	Complete          bool       `json:"complete"`
	Stale             bool       `json:"stale"`
	LastErrorCategory string     `json:"last_error_category,omitempty"`
	ObservedCount     int        `json:"observed_count"`
}

type containerListResponse struct {
	Containers []containerListItem `json:"containers"`
	Freshness  collectorFreshness  `json:"freshness"`
}

type safeInspectionResponse struct {
	User            string            `json:"user"`
	EntrypointHash  string            `json:"entrypoint_hash,omitempty"`
	CommandHash     string            `json:"command_hash,omitempty"`
	Privileged      bool              `json:"privileged"`
	ReadonlyRootFS  bool              `json:"readonly_root_fs"`
	CapAdd          []string          `json:"cap_add"`
	PIDMode         string            `json:"pid_mode,omitempty"`
	IPCMode         string            `json:"ipc_mode,omitempty"`
	NetworkMode     string            `json:"network_mode,omitempty"`
	RestartPolicy   string            `json:"restart_policy,omitempty"`
	MemoryBytes     int64             `json:"memory_bytes,omitempty"`
	NanoCPUs        int64             `json:"nano_cpus,omitempty"`
	PIDsLimit       *int64            `json:"pids_limit,omitempty"`
	Mounts          []model.SafeMount `json:"mounts"`
	HasDockerSocket bool              `json:"has_docker_socket"`
}

type containerDetailResponse struct {
	Container  containerListItem      `json:"container"`
	Image      string                 `json:"image"`
	ImageID    string                 `json:"image_id"`
	Inspection safeInspectionResponse `json:"inspection"`
}

func (s *Server) handleContainers(w nethttp.ResponseWriter, r *nethttp.Request) {
	query, ok := parseContainerQuery(w, s, r)
	if !ok {
		return
	}

	instances, err := s.fleetInventory.Containers(r.Context(), query)
	if err != nil {
		writeServiceError(w, s.logger, err, "failed to load container inventory")
		return
	}
	summary, err := s.fleetInventory.Summary(r.Context())
	if err != nil {
		writeServiceError(w, s.logger, err, "failed to load container freshness")
		return
	}

	items := make([]containerListItem, 0, len(instances))
	for _, instance := range instances {
		items = append(items, listItem(instance))
	}
	// Databases upgraded before the first reconciliation can still expose their
	// legacy samples. Once a reconciliation timestamp exists, inventory is the
	// sole authority, including an intentionally empty daemon.
	if len(items) == 0 && summary.ReconciledAt == nil && s.sampler != nil {
		legacy, legacyErr := s.sampler.FleetContainers(r.Context())
		if legacyErr != nil {
			writeServiceError(w, s.logger, legacyErr, "failed to load legacy fleet containers")
			return
		}
		for _, item := range legacy {
			items = append(items, legacyListItem(item))
		}
	}

	writeJSON(w, s.logger, nethttp.StatusOK, containerListResponse{
		Containers: items,
		Freshness: collectorFreshness{
			ReconciledAt: summary.ReconciledAt, Complete: summary.Complete, Stale: summary.Stale,
			LastErrorCategory: summary.LastErrorCategory, ObservedCount: summary.ObservedCount,
		},
	})
}

func (s *Server) handleContainer(w nethttp.ResponseWriter, r *nethttp.Request) {
	instance, err := s.fleetInventory.Container(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrContainerNotFound) {
		writeError(w, s.logger, err, "container not found", "not_found", nethttp.StatusNotFound)
		return
	}
	if err != nil {
		writeServiceError(w, s.logger, err, "failed to load container")
		return
	}
	inspection := instance.Inspection
	writeJSON(w, s.logger, nethttp.StatusOK, containerDetailResponse{
		Container: listItem(instance), Image: instance.Image, ImageID: instance.ImageID,
		Inspection: safeInspectionResponse{
			User: inspection.User, EntrypointHash: inspection.EntrypointHash,
			CommandHash: inspection.CommandHash, Privileged: inspection.Privileged,
			ReadonlyRootFS: inspection.ReadonlyRootFS, CapAdd: nonNilStrings(inspection.CapAdd),
			PIDMode: inspection.PIDMode, IPCMode: inspection.IPCMode, NetworkMode: inspection.NetworkMode,
			RestartPolicy: inspection.RestartPolicy, MemoryBytes: inspection.MemoryBytes,
			NanoCPUs: inspection.NanoCPUs, PIDsLimit: inspection.PIDsLimit,
			Mounts: nonNilMounts(inspection.Mounts), HasDockerSocket: inspection.HasDockerSocket,
		},
	})
}

func parseContainerQuery(w nethttp.ResponseWriter, s *Server, r *nethttp.Request) (model.ContainerQuery, bool) {
	query := model.ContainerQuery{
		State: r.URL.Query().Get("state"), ServiceID: r.URL.Query().Get("service_id"),
		Sort: r.URL.Query().Get("sort"),
	}
	if classification := r.URL.Query().Get("classification"); classification != "" {
		query.Classification = model.ContainerClassification(classification)
		switch query.Classification {
		case model.ContainerClassificationManaged, model.ContainerClassificationApprovedObserved,
			model.ContainerClassificationUnknown, model.ContainerClassificationIgnored,
			model.ContainerClassificationMissingExpected:
		default:
			writeError(w, s.logger, errors.New("invalid classification"), "invalid classification", "invalid_request", nethttp.StatusBadRequest)
			return model.ContainerQuery{}, false
		}
	}
	var err error
	if query.Limit, err = optionalNonNegativeInt(r.URL.Query().Get("limit")); err != nil {
		writeError(w, s.logger, err, "invalid limit", "invalid_request", nethttp.StatusBadRequest)
		return model.ContainerQuery{}, false
	}
	if query.Offset, err = optionalNonNegativeInt(r.URL.Query().Get("offset")); err != nil {
		writeError(w, s.logger, err, "invalid offset", "invalid_request", nethttp.StatusBadRequest)
		return model.ContainerQuery{}, false
	}
	if raw := r.URL.Query().Get("include_historical"); raw != "" {
		query.IncludeHistorical, err = strconv.ParseBool(raw)
		if err != nil {
			writeError(w, s.logger, err, "invalid include_historical", "invalid_request", nethttp.StatusBadRequest)
			return model.ContainerQuery{}, false
		}
	}
	if _, err := containerSort(query.Sort); err != nil {
		writeError(w, s.logger, err, "invalid sort", "invalid_request", nethttp.StatusBadRequest)
		return model.ContainerQuery{}, false
	}
	return query, true
}

func containerSort(value string) (string, error) {
	switch value {
	case "", "name", "last_seen", "state":
		return value, nil
	default:
		return "", errors.New("unknown container sort")
	}
}

func optionalNonNegativeInt(raw string) (int, error) {
	if raw == "" {
		return 0, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 0 {
		return 0, errors.New("want non-negative integer")
	}
	return value, nil
}

func listItem(instance model.ContainerInstance) containerListItem {
	classification := instance.Classification
	return containerListItem{
		ID: instance.ID, Name: instance.Name, Project: instance.ComposeProject,
		Managed: classification == model.ContainerClassificationManaged, ServiceID: instance.ServiceID,
		State: instance.State, Status: instance.Status, Classification: classification,
		Current: instance.Current, Present: instance.Present, Health: containerHealth(instance),
		CPUPercent: instance.CPUPercent, MemBytes: instance.MemBytes, MemLimit: instance.MemLimit,
		Restarts: instance.Restarts, FirstSeenAt: &instance.FirstSeenAt, LastSeenAt: &instance.LastSeenAt,
		AbsentAt: instance.AbsentAt,
	}
}

func legacyListItem(item model.FleetContainer) containerListItem {
	classification := model.ContainerClassificationUnknown
	if item.Managed {
		classification = model.ContainerClassificationManaged
	}
	cpu, memory, limit, restarts, sampled := item.CPUPercent, item.MemBytes, item.MemLimit, item.Restarts, item.SampledAt
	return containerListItem{ID: item.ID, Name: item.Name, Project: item.Project, Managed: item.Managed,
		ServiceID: item.ServiceID, State: item.State, Classification: classification,
		Current: true, Present: true, Health: "unknown", CPUPercent: &cpu, MemBytes: &memory,
		MemLimit: &limit, Restarts: &restarts, SampledAt: &sampled}
}

func containerHealth(instance model.ContainerInstance) string {
	if !instance.Present {
		return "absent"
	}
	if instance.State == "running" {
		return "running"
	}
	return instance.State
}

func nonNilStrings(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

func nonNilMounts(values []model.SafeMount) []model.SafeMount {
	if values == nil {
		return []model.SafeMount{}
	}
	return values
}
