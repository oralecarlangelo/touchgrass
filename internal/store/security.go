package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/oralecarlangelo/touchgrass/internal/model"
)

// SecurityStore persists activation, approval baselines, ignore decisions, and
// later security observations/findings.
type SecurityStore struct {
	db *DB
}

func NewSecurityStore(db *DB) *SecurityStore {
	return &SecurityStore{db: db}
}

// FingerprintInspection deterministically hashes only normalized safe fields.
func FingerprintInspection(value model.SafeInspection) string {
	capabilities := slices.Clone(value.CapAdd)
	slices.Sort(capabilities)
	mounts := slices.Clone(value.Mounts)
	slices.SortFunc(mounts, func(a, b model.SafeMount) int {
		if compared := strings.Compare(a.Type, b.Type); compared != 0 {
			return compared
		}
		if compared := strings.Compare(a.Destination, b.Destination); compared != 0 {
			return compared
		}
		return 0
	})
	canonical := struct {
		User, EntrypointHash, CommandHash, PIDMode, IPCMode, NetworkMode, RestartPolicy string
		Privileged, ReadonlyRootFS, HasDockerSocket                                     bool
		CapAdd                                                                          []string
		MemoryBytes, NanoCPUs                                                           int64
		PIDsLimit                                                                       *int64
		Mounts                                                                          []model.SafeMount
	}{
		User: value.User, EntrypointHash: value.EntrypointHash, CommandHash: value.CommandHash,
		PIDMode: value.PIDMode, IPCMode: value.IPCMode, NetworkMode: value.NetworkMode,
		RestartPolicy: value.RestartPolicy, Privileged: value.Privileged,
		ReadonlyRootFS: value.ReadonlyRootFS, HasDockerSocket: value.HasDockerSocket,
		CapAdd: capabilities, MemoryBytes: value.MemoryBytes, NanoCPUs: value.NanoCPUs,
		PIDsLimit: value.PIDsLimit, Mounts: mounts,
	}
	encoded, _ := json.Marshal(canonical)
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

// Activate enables security monitoring without changing any classification.
func (s *SecurityStore) Activate(ctx context.Context, actor string) (model.SecurityActivation, error) {
	if strings.TrimSpace(actor) == "" {
		return model.SecurityActivation{}, errors.New("activating security: actor is required")
	}
	now := time.Now().UTC()
	if _, err := s.db.sql.ExecContext(ctx, `UPDATE container_security_activation SET
		activated_at = COALESCE(activated_at, ?),
		activated_by = CASE WHEN activated_at IS NULL THEN ? ELSE activated_by END
		WHERE singleton = 1`, formatTime(now), actor); err != nil {
		return model.SecurityActivation{}, fmt.Errorf("activating security: %w", err)
	}
	return s.Activation(ctx)
}

func (s *SecurityStore) Activation(ctx context.Context) (model.SecurityActivation, error) {
	var activation model.SecurityActivation
	var at sql.NullString
	if err := s.db.sql.QueryRowContext(ctx, `SELECT activated_at, activated_by
		FROM container_security_activation WHERE singleton = 1`).Scan(&at, &activation.ActivatedBy); err != nil {
		return model.SecurityActivation{}, fmt.Errorf("reading security activation: %w", err)
	}
	var err error
	activation.ActivatedAt, err = parseNullTime(at, "activated_at")
	return activation, err
}

func (s *SecurityStore) Approve(ctx context.Context, approval model.ContainerApproval) (model.ContainerBaseline, error) {
	if approval.ContainerID == "" || approval.ImageFingerprint == "" || approval.ConfigurationFingerprint == "" ||
		strings.TrimSpace(approval.Actor) == "" {
		return model.ContainerBaseline{}, errors.New("approving container: id, fingerprints, and actor are required")
	}
	if approval.ApprovedAt.IsZero() {
		approval.ApprovedAt = time.Now().UTC()
	}
	tx, err := s.db.sql.BeginTx(ctx, nil)
	if err != nil {
		return model.ContainerBaseline{}, fmt.Errorf("beginning container approval: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if result, err := tx.ExecContext(ctx, `UPDATE container_instances SET
		classification = 'approved_observed', image_fingerprint = ?, configuration_fingerprint = ?
		WHERE container_id = ?`, approval.ImageFingerprint, approval.ConfigurationFingerprint, approval.ContainerID); err != nil {
		return model.ContainerBaseline{}, fmt.Errorf("classifying approved container: %w", err)
	} else if affected, _ := result.RowsAffected(); affected == 0 {
		return model.ContainerBaseline{}, fmt.Errorf("%w: %q", ErrContainerNotFound, approval.ContainerID)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE container_security_baselines SET active = 0 WHERE container_id = ?`, approval.ContainerID); err != nil {
		return model.ContainerBaseline{}, fmt.Errorf("retiring container baseline: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO container_security_baselines
		(container_id, image_fingerprint, configuration_fingerprint, approved_by, approved_at, active)
		VALUES (?, ?, ?, ?, ?, 1)
		ON CONFLICT(container_id, image_fingerprint, configuration_fingerprint) DO UPDATE SET
		approved_by = excluded.approved_by, approved_at = excluded.approved_at, active = 1`,
		approval.ContainerID, approval.ImageFingerprint, approval.ConfigurationFingerprint,
		approval.Actor, formatTime(approval.ApprovedAt)); err != nil {
		return model.ContainerBaseline{}, fmt.Errorf("saving container baseline: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM container_security_ignores WHERE container_id = ?`, approval.ContainerID); err != nil {
		return model.ContainerBaseline{}, fmt.Errorf("clearing container ignore: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return model.ContainerBaseline{}, fmt.Errorf("committing container approval: %w", err)
	}
	return s.Baseline(ctx, approval.ContainerID)
}

func (s *SecurityStore) Baseline(ctx context.Context, containerID string) (model.ContainerBaseline, error) {
	var baseline model.ContainerBaseline
	var approvedAt string
	var active int
	err := s.db.sql.QueryRowContext(ctx, `SELECT id, container_id, image_fingerprint,
		configuration_fingerprint, approved_by, approved_at, active
		FROM container_security_baselines WHERE container_id = ? AND active = 1
		ORDER BY approved_at DESC, id DESC LIMIT 1`, containerID).Scan(
		&baseline.ID, &baseline.ContainerID, &baseline.ImageFingerprint,
		&baseline.ConfigurationFingerprint, &baseline.ApprovedBy, &approvedAt, &active)
	if err != nil {
		return model.ContainerBaseline{}, err
	}
	baseline.ApprovedAt, err = parseTime(approvedAt, "approved_at")
	baseline.Active = active != 0
	return baseline, err
}

func (s *SecurityStore) Ignore(ctx context.Context, ignore model.ContainerIgnore) (model.ContainerIgnore, error) {
	ignore.Reason = strings.TrimSpace(ignore.Reason)
	if ignore.ContainerID == "" || ignore.Reason == "" || strings.TrimSpace(ignore.Actor) == "" {
		return model.ContainerIgnore{}, errors.New("ignoring container: id, reason, and actor are required")
	}
	if ignore.NonExpiring == (ignore.ExpiresAt != nil) {
		return model.ContainerIgnore{}, errors.New("ignoring container: choose expiry or explicit non-expiring")
	}
	if ignore.CreatedAt.IsZero() {
		ignore.CreatedAt = time.Now().UTC()
	}
	if ignore.ExpiresAt != nil && !ignore.ExpiresAt.After(ignore.CreatedAt) {
		return model.ContainerIgnore{}, errors.New("ignoring container: expiry must be in the future")
	}
	tx, err := s.db.sql.BeginTx(ctx, nil)
	if err != nil {
		return model.ContainerIgnore{}, err
	}
	defer func() { _ = tx.Rollback() }()
	result, err := tx.ExecContext(ctx, `UPDATE container_instances SET classification = 'ignored' WHERE container_id = ?`, ignore.ContainerID)
	if err != nil {
		return model.ContainerIgnore{}, fmt.Errorf("classifying ignored container: %w", err)
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return model.ContainerIgnore{}, fmt.Errorf("%w: %q", ErrContainerNotFound, ignore.ContainerID)
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO container_security_ignores
		(container_id, reason, expires_at, non_expiring, actor, created_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(container_id) DO UPDATE SET reason = excluded.reason,
		expires_at = excluded.expires_at, non_expiring = excluded.non_expiring,
		actor = excluded.actor, created_at = excluded.created_at`,
		ignore.ContainerID, ignore.Reason, formatNullTime(ignore.ExpiresAt), boolInteger(ignore.NonExpiring),
		ignore.Actor, formatTime(ignore.CreatedAt))
	if err != nil {
		return model.ContainerIgnore{}, fmt.Errorf("saving container ignore: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return model.ContainerIgnore{}, err
	}
	return ignore, nil
}

// Classify applies deterministic precedence: managed, active ignore, matching
// approved baseline, then unknown.
func (s *SecurityStore) Classify(ctx context.Context, instance model.ContainerInstance, now time.Time) (model.ContainerClassification, error) {
	if instance.ServiceID != "" {
		return model.ContainerClassificationManaged, nil
	}
	var expires sql.NullString
	var nonExpiring int
	err := s.db.sql.QueryRowContext(ctx, `SELECT expires_at, non_expiring
		FROM container_security_ignores WHERE container_id = ?`, instance.ID).Scan(&expires, &nonExpiring)
	if err == nil {
		expiresAt, parseErr := parseNullTime(expires, "expires_at")
		if parseErr != nil {
			return model.ContainerClassificationUnspecified, parseErr
		}
		if nonExpiring != 0 || (expiresAt != nil && expiresAt.After(now)) {
			return model.ContainerClassificationIgnored, nil
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return model.ContainerClassificationUnspecified, fmt.Errorf("reading container ignore: %w", err)
	}

	var count int
	err = s.db.sql.QueryRowContext(ctx, `SELECT COUNT(*) FROM container_security_baselines
		WHERE container_id = ? AND active = 1 AND image_fingerprint = ?
		AND configuration_fingerprint = ?`, instance.ID, instance.ImageFingerprint,
		instance.ConfigurationFingerprint).Scan(&count)
	if err != nil {
		return model.ContainerClassificationUnspecified, fmt.Errorf("matching container baseline: %w", err)
	}
	if count > 0 {
		return model.ContainerClassificationApprovedObserved, nil
	}
	return model.ContainerClassificationUnknown, nil
}
