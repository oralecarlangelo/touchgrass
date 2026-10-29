package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/oralecarlangelo/touchgrass/internal/model"
)

// ErrContainerNotFound reports an unknown durable container identity.
var ErrContainerNotFound = errors.New("store: container not found")

const containerColumns = `container_id, name, image, image_id, state, status,
	compose_project, compose_service, service_id, classification, current, present,
	first_seen_at, last_seen_at, absent_at, image_fingerprint,
	configuration_fingerprint, inspection_json, cpu_percent, mem_bytes, mem_limit, restarts`

// ContainerStore persists lifecycle identity and authoritative inventory state.
type ContainerStore struct {
	db *DB
}

// NewContainerStore builds a container inventory store.
func NewContainerStore(db *DB) *ContainerStore {
	return &ContainerStore{db: db}
}

// Reconcile persists one inventory batch atomically. Missing instances are
// marked absent only when the batch is complete.
func (s *ContainerStore) Reconcile(ctx context.Context, batch model.InventoryBatch) error {
	if batch.ObservedAt.IsZero() {
		batch.ObservedAt = time.Now().UTC()
	}
	if err := validateInventoryBatch(batch); err != nil {
		return err
	}

	tx, err := s.db.sql.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("beginning container reconciliation: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	observedAt := formatTime(batch.ObservedAt)
	if batch.Complete {
		if _, err := tx.ExecContext(ctx, `UPDATE container_instances
			SET current = 0, present = 0, absent_at = ?, updated_at = ?
			WHERE current = 1`, observedAt, observedAt); err != nil {
			return fmt.Errorf("marking unseen containers absent: %w", err)
		}
	}

	for _, instance := range batch.Instances {
		if _, err := tx.ExecContext(ctx, `UPDATE container_instances
			SET current = 0, present = 0, absent_at = ?, updated_at = ?
			WHERE name = ? AND container_id <> ? AND current = 1`,
			observedAt, observedAt, instance.Name, instance.ID); err != nil {
			return fmt.Errorf("retiring reused container name %q: %w", instance.Name, err)
		}

		if err := upsertContainer(ctx, tx, instance, batch.ObservedAt); err != nil {
			return err
		}
	}

	if _, err := tx.ExecContext(ctx, `UPDATE container_inventory_state SET
		reconciled_at = ?, complete = ?, last_error_category = ?, observed_count = ?
		WHERE singleton = 1`, observedAt, boolInteger(batch.Complete),
		batch.LastErrorCategory, len(batch.Instances)); err != nil {
		return fmt.Errorf("updating inventory state: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing container reconciliation: %w", err)
	}
	return nil
}

func upsertContainer(ctx context.Context, tx *sql.Tx, instance model.ContainerInstance, observedAt time.Time) error {
	classification := instance.Classification
	if classification == model.ContainerClassificationUnspecified {
		classification = model.ContainerClassificationUnknown
	}
	inspection := normalizeSafeInspection(instance.Inspection)
	encoded, err := json.Marshal(inspection)
	if err != nil {
		return fmt.Errorf("encoding safe inspection for %q: %w", instance.ID, err)
	}

	_, err = tx.ExecContext(ctx, `INSERT INTO container_instances (
		container_id, name, image, image_id, state, status, compose_project,
		compose_service, service_id, classification, current, present,
		first_seen_at, last_seen_at, absent_at, image_fingerprint,
		configuration_fingerprint, inspection_json, cpu_percent, mem_bytes,
		mem_limit, restarts, updated_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1, 1, ?, ?, NULL, ?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(container_id) DO UPDATE SET
		name = excluded.name, image = excluded.image, image_id = excluded.image_id,
		state = excluded.state, status = excluded.status,
		compose_project = excluded.compose_project, compose_service = excluded.compose_service,
		service_id = excluded.service_id,
		classification = CASE
			WHEN container_instances.classification IN ('approved_observed', 'ignored')
				AND excluded.classification = 'unknown' THEN container_instances.classification
			ELSE excluded.classification END,
		current = 1, present = 1, last_seen_at = excluded.last_seen_at, absent_at = NULL,
		image_fingerprint = excluded.image_fingerprint,
		configuration_fingerprint = excluded.configuration_fingerprint,
		inspection_json = excluded.inspection_json, cpu_percent = excluded.cpu_percent,
		mem_bytes = excluded.mem_bytes, mem_limit = excluded.mem_limit,
		restarts = excluded.restarts, updated_at = excluded.updated_at`,
		instance.ID, instance.Name, instance.Image, instance.ImageID, instance.State,
		instance.Status, instance.ComposeProject, instance.ComposeService,
		instance.ServiceID, classification, formatTime(observedAt), formatTime(observedAt),
		instance.ImageFingerprint, instance.ConfigurationFingerprint, string(encoded),
		nullFloat(instance.CPUPercent), nullUint(instance.MemBytes), nullUint(instance.MemLimit),
		nullInteger(instance.Restarts), formatTime(observedAt))
	if err != nil {
		return fmt.Errorf("upserting container %q: %w", instance.ID, err)
	}
	return nil
}

// Get returns one durable container instance by Docker ID.
func (s *ContainerStore) Get(ctx context.Context, id string) (model.ContainerInstance, error) {
	row := s.db.sql.QueryRowContext(ctx, "SELECT "+containerColumns+" FROM container_instances WHERE container_id = ?", id)
	instance, err := scanContainer(row)
	if errors.Is(err, sql.ErrNoRows) {
		return model.ContainerInstance{}, fmt.Errorf("%w: %q", ErrContainerNotFound, id)
	}
	if err != nil {
		return model.ContainerInstance{}, err
	}
	return instance, nil
}

// List returns a bounded deterministic inventory slice.
func (s *ContainerStore) List(ctx context.Context, query model.ContainerQuery) ([]model.ContainerInstance, error) {
	statement := "SELECT " + containerColumns + " FROM container_instances WHERE 1 = 1"
	args := []any{}
	if !query.IncludeHistorical {
		statement += " AND current = 1"
	}
	if query.Classification != model.ContainerClassificationUnspecified {
		statement += " AND classification = ?"
		args = append(args, query.Classification)
	}
	if query.State != "" {
		statement += " AND state = ?"
		args = append(args, query.State)
	}
	if query.ServiceID != "" {
		statement += " AND service_id = ?"
		args = append(args, query.ServiceID)
	}

	order, err := containerOrder(query.Sort)
	if err != nil {
		return nil, err
	}
	statement += " ORDER BY " + order + " LIMIT ? OFFSET ?"
	args = append(args, containerLimit(query.Limit), max(query.Offset, 0))

	rows, err := s.db.sql.QueryContext(ctx, statement, args...)
	if err != nil {
		return nil, fmt.Errorf("listing containers: %w", err)
	}
	defer func() { _ = rows.Close() }()

	instances := []model.ContainerInstance{}
	for rows.Next() {
		instance, err := scanContainer(rows)
		if err != nil {
			return nil, err
		}
		instances = append(instances, instance)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating containers: %w", err)
	}
	return instances, nil
}

// Summary returns collector freshness and current/present totals.
func (s *ContainerStore) Summary(ctx context.Context) (model.InventorySummary, error) {
	var (
		summary      model.InventorySummary
		reconciledAt sql.NullString
		complete     int
	)
	err := s.db.sql.QueryRowContext(ctx, `SELECT reconciled_at, complete,
		last_error_category, observed_count,
		(SELECT COUNT(*) FROM container_instances WHERE current = 1),
		(SELECT COUNT(*) FROM container_instances WHERE current = 1 AND present = 1)
		FROM container_inventory_state WHERE singleton = 1`).Scan(
		&reconciledAt, &complete, &summary.LastErrorCategory, &summary.ObservedCount,
		&summary.CurrentCount, &summary.PresentCount)
	if err != nil {
		return model.InventorySummary{}, fmt.Errorf("querying inventory summary: %w", err)
	}
	summary.ReconciledAt, err = parseNullTime(reconciledAt, "reconciled_at")
	if err != nil {
		return model.InventorySummary{}, err
	}
	summary.Complete = complete != 0
	return summary, nil
}

type containerScanner interface {
	Scan(dest ...any) error
}

func scanContainer(scanner containerScanner) (model.ContainerInstance, error) {
	var (
		instance                     model.ContainerInstance
		classification               string
		current, present             int
		firstSeen, lastSeen          string
		absentAt                     sql.NullString
		inspectionJSON               string
		cpuPercent                   sql.NullFloat64
		memBytes, memLimit, restarts sql.NullInt64
	)
	if err := scanner.Scan(&instance.ID, &instance.Name, &instance.Image, &instance.ImageID,
		&instance.State, &instance.Status, &instance.ComposeProject, &instance.ComposeService,
		&instance.ServiceID, &classification, &current, &present, &firstSeen, &lastSeen,
		&absentAt, &instance.ImageFingerprint, &instance.ConfigurationFingerprint,
		&inspectionJSON, &cpuPercent, &memBytes, &memLimit, &restarts); err != nil {
		return model.ContainerInstance{}, err
	}
	var err error
	instance.FirstSeenAt, err = parseTime(firstSeen, "first_seen_at")
	if err != nil {
		return model.ContainerInstance{}, err
	}
	instance.LastSeenAt, err = parseTime(lastSeen, "last_seen_at")
	if err != nil {
		return model.ContainerInstance{}, err
	}
	instance.AbsentAt, err = parseNullTime(absentAt, "absent_at")
	if err != nil {
		return model.ContainerInstance{}, err
	}
	if err := json.Unmarshal([]byte(inspectionJSON), &instance.Inspection); err != nil {
		return model.ContainerInstance{}, fmt.Errorf("decoding safe inspection for %q: %w", instance.ID, err)
	}
	instance.Inspection = normalizeSafeInspection(instance.Inspection)
	instance.Classification = model.ContainerClassification(classification)
	instance.Current = current != 0
	instance.Present = present != 0
	if cpuPercent.Valid {
		instance.CPUPercent = &cpuPercent.Float64
	}
	if memBytes.Valid {
		value := uint64(memBytes.Int64)
		instance.MemBytes = &value
	}
	if memLimit.Valid {
		value := uint64(memLimit.Int64)
		instance.MemLimit = &value
	}
	if restarts.Valid {
		value := int(restarts.Int64)
		instance.Restarts = &value
	}
	return instance, nil
}

func validateInventoryBatch(batch model.InventoryBatch) error {
	seen := make(map[string]struct{}, len(batch.Instances))
	for _, instance := range batch.Instances {
		if strings.TrimSpace(instance.ID) == "" || strings.TrimSpace(instance.Name) == "" {
			return errors.New("reconciling containers: id and name are required")
		}
		if len(instance.ID) > 128 || len(instance.Name) > 256 {
			return errors.New("reconciling containers: bounded identity is too long")
		}
		if _, ok := seen[instance.ID]; ok {
			return fmt.Errorf("reconciling containers: duplicate id %q", instance.ID)
		}
		seen[instance.ID] = struct{}{}
	}
	return nil
}

func normalizeSafeInspection(value model.SafeInspection) model.SafeInspection {
	if value.CapAdd == nil {
		value.CapAdd = []string{}
	}
	if value.Mounts == nil {
		value.Mounts = []model.SafeMount{}
	}
	if value.Labels == nil {
		value.Labels = map[string]string{}
	}
	return value
}

func containerOrder(sort string) (string, error) {
	switch sort {
	case "", "name":
		return "name ASC, container_id ASC", nil
	case "last_seen":
		return "last_seen_at DESC, container_id ASC", nil
	case "state":
		return "state ASC, name ASC, container_id ASC", nil
	default:
		return "", fmt.Errorf("unknown container sort %q", sort)
	}
}

func containerLimit(limit int) int {
	if limit <= 0 || limit > 500 {
		return 100
	}
	return limit
}

func nullUint(value *uint64) any {
	if value == nil {
		return nil
	}
	return int64(*value)
}

func nullInteger(value *int) any {
	if value == nil {
		return nil
	}
	return *value
}
