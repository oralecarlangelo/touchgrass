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
		PublishedPorts                                                                  []string
	}{
		User: value.User, EntrypointHash: value.EntrypointHash, CommandHash: value.CommandHash,
		PIDMode: value.PIDMode, IPCMode: value.IPCMode, NetworkMode: value.NetworkMode,
		RestartPolicy: value.RestartPolicy, Privileged: value.Privileged,
		ReadonlyRootFS: value.ReadonlyRootFS, HasDockerSocket: value.HasDockerSocket,
		CapAdd: capabilities, MemoryBytes: value.MemoryBytes, NanoCPUs: value.NanoCPUs,
		PIDsLimit: value.PIDsLimit, Mounts: mounts,
		PublishedPorts: slices.Sorted(slices.Values(value.PublishedPorts)),
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

type SecurityEvaluationResult struct {
	Finding   model.SecurityFinding
	Opened    bool
	Recovered bool
}

// Evaluate stores one observation and updates its durable finding episode in
// the same transaction.
func (s *SecurityStore) Evaluate(ctx context.Context, evaluation model.SecurityEvaluation) (SecurityEvaluationResult, error) {
	observation := evaluation.Observation
	if err := validateObservation(observation); err != nil {
		return SecurityEvaluationResult{}, err
	}
	if observation.ObservedAt.IsZero() {
		observation.ObservedAt = time.Now().UTC()
	}
	fingerprint := findingFingerprint(observation.ContainerID, observation.RuleID, evaluation.BaselineFingerprint)
	evidence, err := json.Marshal(observation.Evidence)
	if err != nil {
		return SecurityEvaluationResult{}, fmt.Errorf("encoding security evidence: %w", err)
	}
	if len(evidence) > model.MaximumSecurityEvidenceBytes {
		return SecurityEvaluationResult{}, errors.New("evaluating security: evidence metadata exceeds 32 KiB")
	}
	tx, err := s.db.sql.BeginTx(ctx, nil)
	if err != nil {
		return SecurityEvaluationResult{}, fmt.Errorf("beginning security evaluation: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	_, err = tx.ExecContext(ctx, `INSERT INTO security_observations
		(container_id, rule_id, category, severity, confidence, detected,
		coverage_complete, evidence_json, observed_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		observation.ContainerID, observation.RuleID, observation.Category, observation.Severity,
		observation.Confidence, boolInteger(observation.Detected), boolInteger(observation.CoverageComplete),
		string(evidence), formatTime(observation.ObservedAt))
	if err != nil {
		return SecurityEvaluationResult{}, fmt.Errorf("inserting security observation: %w", err)
	}

	open, found, err := findOpenFinding(ctx, tx, fingerprint)
	if err != nil {
		return SecurityEvaluationResult{}, err
	}
	result := SecurityEvaluationResult{}
	if observation.Detected && observation.CoverageComplete {
		if found {
			_, err = tx.ExecContext(ctx, `UPDATE security_findings SET occurrence_count = occurrence_count + 1,
				last_seen_at = ?, severity = ?, confidence = ?, evidence_json = ? WHERE id = ?`,
				formatTime(observation.ObservedAt), observation.Severity, observation.Confidence, string(evidence), open.ID)
			if err != nil {
				return SecurityEvaluationResult{}, fmt.Errorf("updating security episode: %w", err)
			}
			open.OccurrenceCount++
			open.LastSeenAt = observation.ObservedAt
			open.Severity = observation.Severity
			open.Confidence = observation.Confidence
			open.Evidence = cloneEvidence(observation.Evidence)
			result.Finding = open
		} else {
			var episode int
			if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(episode), 0) + 1
				FROM security_findings WHERE fingerprint = ?`, fingerprint).Scan(&episode); err != nil {
				return SecurityEvaluationResult{}, fmt.Errorf("selecting finding episode: %w", err)
			}
			created, createErr := insertFinding(ctx, tx, fingerprint, episode, observation, string(evidence))
			if createErr != nil {
				return SecurityEvaluationResult{}, createErr
			}
			result.Finding, result.Opened = created, true
		}
	} else if found && observation.CoverageComplete {
		_, err = tx.ExecContext(ctx, `UPDATE security_findings SET status = 'resolved',
			resolved_at = ?, resolved_by = 'collector', last_seen_at = ? WHERE id = ?`,
			formatTime(observation.ObservedAt), formatTime(observation.ObservedAt), open.ID)
		if err != nil {
			return SecurityEvaluationResult{}, fmt.Errorf("resolving recovered finding: %w", err)
		}
		open.Status = model.SecurityFindingStatusResolved
		resolvedAt := observation.ObservedAt
		open.ResolvedAt = &resolvedAt
		open.ResolvedBy = "collector"
		open.LastSeenAt = observation.ObservedAt
		result.Finding, result.Recovered = open, true
	}
	if err := tx.Commit(); err != nil {
		return SecurityEvaluationResult{}, fmt.Errorf("committing security evaluation: %w", err)
	}
	return result, nil
}

// Policy returns the service-specific policy, then the global policy, or the
// conservative zero-value policy when no override exists.
func (s *SecurityStore) Policy(ctx context.Context, serviceID string) (model.SecurityPolicy, error) {
	queries := [][2]string{{"service", serviceID}, {"global", ""}}
	for _, query := range queries {
		if query[0] == "service" && query[1] == "" {
			continue
		}
		var encoded string
		err := s.db.sql.QueryRowContext(ctx, `SELECT policy_json FROM security_policies
			WHERE scope = ? AND scope_id = ?`, query[0], query[1]).Scan(&encoded)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return model.SecurityPolicy{}, fmt.Errorf("reading security policy: %w", err)
		}
		var policy model.SecurityPolicy
		if err := json.Unmarshal([]byte(encoded), &policy); err != nil {
			return model.SecurityPolicy{}, fmt.Errorf("decoding security policy: %w", err)
		}
		return policy, nil
	}
	return model.SecurityPolicy{}, nil
}

func (s *SecurityStore) Findings(ctx context.Context, query model.SecurityFindingQuery) ([]model.SecurityFinding, error) {
	statement := `SELECT id, fingerprint, episode, container_id, rule_id, category, severity,
		confidence, status, occurrence_count, evidence_json, opened_at, last_seen_at,
		acknowledged_at, acknowledged_by, resolved_at, resolved_by, suppressed_until,
		suppression_reason, suppression_actor, notification_status
		FROM security_findings WHERE 1 = 1`
	args := []any{}
	if query.Status != model.SecurityFindingStatusUnknown {
		statement += " AND status = ?"
		args = append(args, query.Status)
	}
	if query.Severity != model.SecuritySeverityUnknown {
		statement += " AND severity = ?"
		args = append(args, query.Severity)
	}
	if query.ContainerID != "" {
		statement += " AND container_id = ?"
		args = append(args, query.ContainerID)
	}
	switch query.Sort {
	case "opened":
		statement += " ORDER BY opened_at DESC, id DESC"
	case "severity":
		statement += ` ORDER BY CASE severity WHEN 'critical' THEN 4 WHEN 'high' THEN 3
			WHEN 'medium' THEN 2 WHEN 'low' THEN 1 ELSE 0 END DESC, last_seen_at DESC, id DESC`
	default:
		statement += " ORDER BY last_seen_at DESC, id DESC"
	}
	statement += " LIMIT ? OFFSET ?"
	args = append(args, securityLimit(query.Limit), max(query.Offset, 0))
	rows, err := s.db.sql.QueryContext(ctx, statement, args...)
	if err != nil {
		return nil, fmt.Errorf("listing security findings: %w", err)
	}
	defer func() { _ = rows.Close() }()
	findings := []model.SecurityFinding{}
	for rows.Next() {
		finding, err := scanSecurityFinding(rows)
		if err != nil {
			return nil, err
		}
		findings = append(findings, finding)
	}
	return findings, rows.Err()
}

func (s *SecurityStore) Summary(ctx context.Context) (model.SecuritySummary, error) {
	var summary model.SecuritySummary
	err := s.db.sql.QueryRowContext(ctx, `SELECT
		COALESCE(SUM(CASE WHEN status IN ('open','acknowledged') THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN status IN ('open','acknowledged') AND severity = 'critical' THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN status IN ('open','acknowledged') AND severity = 'high' THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN status = 'suppressed' THEN 1 ELSE 0 END), 0)
		FROM security_findings`).Scan(&summary.Open, &summary.Critical, &summary.High, &summary.Suppressed)
	if err != nil {
		return model.SecuritySummary{}, fmt.Errorf("summarizing security findings: %w", err)
	}
	var lastObserved sql.NullString
	var coverage sql.NullInt64
	if err := s.db.sql.QueryRowContext(ctx, `SELECT coverage_complete, observed_at
		FROM security_observations ORDER BY observed_at DESC, id DESC LIMIT 1`).Scan(&coverage, &lastObserved); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return model.SecuritySummary{}, fmt.Errorf("reading security coverage: %w", err)
	}
	summary.CoverageComplete = coverage.Valid && coverage.Int64 != 0
	summary.LastObservedAt, err = parseNullTime(lastObserved, "observed_at")
	if err != nil {
		return model.SecuritySummary{}, err
	}
	return summary, nil
}

// Finding returns one finding episode by id.
func (s *SecurityStore) Finding(ctx context.Context, id int64) (model.SecurityFinding, error) {
	return s.findingByID(ctx, id)
}

// Policies lists every policy in deterministic scope order.
func (s *SecurityStore) Policies(ctx context.Context) ([]model.SecurityPolicy, error) {
	rows, err := s.db.sql.QueryContext(ctx, `SELECT id, scope, scope_id, policy_json, updated_by, updated_at
		FROM security_policies ORDER BY scope, scope_id, id`)
	if err != nil {
		return nil, fmt.Errorf("listing security policies: %w", err)
	}
	defer func() { _ = rows.Close() }()
	policies := []model.SecurityPolicy{}
	for rows.Next() {
		policy, err := scanSecurityPolicy(rows)
		if err != nil {
			return nil, err
		}
		policies = append(policies, policy)
	}
	return policies, rows.Err()
}

// SecurityPolicy returns one persisted policy by id.
func (s *SecurityStore) SecurityPolicy(ctx context.Context, id int64) (model.SecurityPolicy, error) {
	row := s.db.sql.QueryRowContext(ctx, `SELECT id, scope, scope_id, policy_json, updated_by, updated_at
		FROM security_policies WHERE id = ?`, id)
	return scanSecurityPolicy(row)
}

// CreatePolicy persists one validated policy.
func (s *SecurityStore) CreatePolicy(ctx context.Context, policy model.SecurityPolicy, actor string) (model.SecurityPolicy, error) {
	if err := validatePolicy(policy, actor); err != nil {
		return model.SecurityPolicy{}, err
	}
	now := time.Now().UTC()
	encoded, err := encodePolicy(policy)
	if err != nil {
		return model.SecurityPolicy{}, err
	}
	result, err := s.db.sql.ExecContext(ctx, `INSERT INTO security_policies
		(scope, scope_id, policy_json, updated_by, updated_at) VALUES (?, ?, ?, ?, ?)`,
		policy.Scope, policy.ScopeID, encoded, actor, formatTime(now))
	if err != nil {
		return model.SecurityPolicy{}, fmt.Errorf("creating security policy: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return model.SecurityPolicy{}, err
	}
	return s.SecurityPolicy(ctx, id)
}

// UpdatePolicy replaces the policy body while preserving its scope.
func (s *SecurityStore) UpdatePolicy(ctx context.Context, id int64, policy model.SecurityPolicy, actor string) (model.SecurityPolicy, error) {
	current, err := s.SecurityPolicy(ctx, id)
	if err != nil {
		return model.SecurityPolicy{}, err
	}
	policy.Scope, policy.ScopeID = current.Scope, current.ScopeID
	if err := validatePolicy(policy, actor); err != nil {
		return model.SecurityPolicy{}, err
	}
	encoded, err := encodePolicy(policy)
	if err != nil {
		return model.SecurityPolicy{}, err
	}
	_, err = s.db.sql.ExecContext(ctx, `UPDATE security_policies SET policy_json = ?, updated_by = ?, updated_at = ? WHERE id = ?`,
		encoded, actor, formatTime(time.Now().UTC()), id)
	if err != nil {
		return model.SecurityPolicy{}, fmt.Errorf("updating security policy: %w", err)
	}
	return s.SecurityPolicy(ctx, id)
}

// DeletePolicy removes one policy by id.
func (s *SecurityStore) DeletePolicy(ctx context.Context, id int64) error {
	result, err := s.db.sql.ExecContext(ctx, `DELETE FROM security_policies WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("deleting security policy: %w", err)
	}
	if count, _ := result.RowsAffected(); count == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *SecurityStore) Acknowledge(ctx context.Context, id int64, actor string) (model.SecurityFinding, error) {
	return s.setFindingStatus(ctx, id, model.SecurityFindingStatusAcknowledged, actor, "")
}

func (s *SecurityStore) Resolve(ctx context.Context, id int64, actor string) (model.SecurityFinding, error) {
	return s.setFindingStatus(ctx, id, model.SecurityFindingStatusResolved, actor, "")
}

func (s *SecurityStore) Suppress(ctx context.Context, id int64, suppression model.FindingSuppression) (model.SecurityFinding, error) {
	if strings.TrimSpace(suppression.Actor) == "" || strings.TrimSpace(suppression.Reason) == "" {
		return model.SecurityFinding{}, errors.New("suppressing finding: actor and reason are required")
	}
	if suppression.Until != nil && !suppression.Until.After(time.Now()) {
		return model.SecurityFinding{}, errors.New("suppressing finding: until must be in the future")
	}
	_, err := s.db.sql.ExecContext(ctx, `UPDATE security_findings SET status = 'suppressed',
		suppressed_until = ?, suppression_reason = ?, suppression_actor = ?
		WHERE id = ? AND status IN ('open','acknowledged')`, formatNullTime(suppression.Until),
		suppression.Reason, suppression.Actor, id)
	if err != nil {
		return model.SecurityFinding{}, fmt.Errorf("suppressing finding: %w", err)
	}
	return s.findingByID(ctx, id)
}

func (s *SecurityStore) SetNotificationStatus(ctx context.Context, id int64, status string) error {
	_, err := s.db.sql.ExecContext(ctx, `UPDATE security_findings SET notification_status = ? WHERE id = ?`, status, id)
	if err != nil {
		return fmt.Errorf("updating finding notification status: %w", err)
	}
	return nil
}

func (s *SecurityStore) setFindingStatus(ctx context.Context, id int64, status model.SecurityFindingStatus, actor, _ string) (model.SecurityFinding, error) {
	if strings.TrimSpace(actor) == "" {
		return model.SecurityFinding{}, errors.New("updating finding: actor is required")
	}
	now := time.Now().UTC()
	var statement string
	switch status {
	case model.SecurityFindingStatusAcknowledged:
		statement = `UPDATE security_findings SET status = 'acknowledged', acknowledged_at = ?, acknowledged_by = ?
			WHERE id = ? AND status = 'open'`
	case model.SecurityFindingStatusResolved:
		statement = `UPDATE security_findings SET status = 'resolved', resolved_at = ?, resolved_by = ?
			WHERE id = ? AND status IN ('open','acknowledged','suppressed')`
	case model.SecurityFindingStatusUnknown, model.SecurityFindingStatusOpen, model.SecurityFindingStatusSuppressed:
		return model.SecurityFinding{}, errors.New("updating finding: invalid status")
	}
	if _, err := s.db.sql.ExecContext(ctx, statement, formatTime(now), actor, id); err != nil {
		return model.SecurityFinding{}, fmt.Errorf("updating finding status: %w", err)
	}
	return s.findingByID(ctx, id)
}

func (s *SecurityStore) findingByID(ctx context.Context, id int64) (model.SecurityFinding, error) {
	row := s.db.sql.QueryRowContext(ctx, `SELECT id, fingerprint, episode, container_id, rule_id, category, severity,
		confidence, status, occurrence_count, evidence_json, opened_at, last_seen_at,
		acknowledged_at, acknowledged_by, resolved_at, resolved_by, suppressed_until,
		suppression_reason, suppression_actor, notification_status FROM security_findings WHERE id = ?`, id)
	return scanSecurityFinding(row)
}

func validateObservation(value model.SecurityObservation) error {
	if value.ContainerID == "" || value.RuleID == "" || value.Category == model.SecurityCategoryUnknown ||
		value.Severity == model.SecuritySeverityUnknown || value.Confidence == model.SecurityConfidenceUnknown {
		return errors.New("evaluating security: required bounded fields are missing")
	}
	if len(value.ContainerID) > 128 || len(value.RuleID) > 128 || len(value.Evidence) > 32 {
		return errors.New("evaluating security: bounded fields exceed limits")
	}
	for key, item := range value.Evidence {
		if len(key) > 64 || len(item) > 256 {
			return errors.New("evaluating security: evidence exceeds limits")
		}
	}
	return nil
}

func findingFingerprint(containerID, ruleID, baseline string) string {
	sum := sha256.Sum256([]byte(containerID + "\x00" + ruleID + "\x00" + baseline))
	return hex.EncodeToString(sum[:])
}

func findOpenFinding(ctx context.Context, tx *sql.Tx, fingerprint string) (model.SecurityFinding, bool, error) {
	row := tx.QueryRowContext(ctx, `SELECT id, fingerprint, episode, container_id, rule_id, category, severity,
		confidence, status, occurrence_count, evidence_json, opened_at, last_seen_at,
		acknowledged_at, acknowledged_by, resolved_at, resolved_by, suppressed_until,
		suppression_reason, suppression_actor, notification_status FROM security_findings
		WHERE fingerprint = ? AND status IN ('open','acknowledged','suppressed') LIMIT 1`, fingerprint)
	finding, err := scanSecurityFinding(row)
	if errors.Is(err, sql.ErrNoRows) {
		return model.SecurityFinding{}, false, nil
	}
	return finding, err == nil, err
}

func insertFinding(ctx context.Context, tx *sql.Tx, fingerprint string, episode int,
	observation model.SecurityObservation, evidence string,
) (model.SecurityFinding, error) {
	result, err := tx.ExecContext(ctx, `INSERT INTO security_findings
		(fingerprint, episode, container_id, rule_id, category, severity, confidence, status,
		occurrence_count, evidence_json, opened_at, last_seen_at) VALUES (?, ?, ?, ?, ?, ?, ?, 'open', 1, ?, ?, ?)`,
		fingerprint, episode, observation.ContainerID, observation.RuleID, observation.Category,
		observation.Severity, observation.Confidence, evidence,
		formatTime(observation.ObservedAt), formatTime(observation.ObservedAt))
	if err != nil {
		return model.SecurityFinding{}, fmt.Errorf("opening security finding: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return model.SecurityFinding{}, err
	}
	return model.SecurityFinding{
		ID: id, Fingerprint: fingerprint, Episode: episode,
		ContainerID: observation.ContainerID, RuleID: observation.RuleID, Category: observation.Category,
		Severity: observation.Severity, Confidence: observation.Confidence,
		Status: model.SecurityFindingStatusOpen, OccurrenceCount: 1,
		Evidence: cloneEvidence(observation.Evidence), OpenedAt: observation.ObservedAt, LastSeenAt: observation.ObservedAt,
	}, nil
}

type securityFindingScanner interface{ Scan(...any) error }

func scanSecurityFinding(scanner securityFindingScanner) (model.SecurityFinding, error) {
	var finding model.SecurityFinding
	var category, severity, confidence, status, evidence, openedAt, lastSeenAt string
	var acknowledgedAt, resolvedAt, suppressedUntil sql.NullString
	err := scanner.Scan(&finding.ID, &finding.Fingerprint, &finding.Episode, &finding.ContainerID,
		&finding.RuleID, &category, &severity, &confidence, &status, &finding.OccurrenceCount,
		&evidence, &openedAt, &lastSeenAt, &acknowledgedAt, &finding.AcknowledgedBy,
		&resolvedAt, &finding.ResolvedBy, &suppressedUntil, &finding.SuppressionReason,
		&finding.SuppressionActor, &finding.NotificationStatus)
	if err != nil {
		return model.SecurityFinding{}, err
	}
	finding.Category, finding.Severity = model.SecurityCategory(category), model.SecuritySeverity(severity)
	finding.Confidence, finding.Status = model.SecurityConfidence(confidence), model.SecurityFindingStatus(status)
	if err := json.Unmarshal([]byte(evidence), &finding.Evidence); err != nil {
		return model.SecurityFinding{}, fmt.Errorf("decoding finding evidence: %w", err)
	}
	finding.OpenedAt, err = parseTime(openedAt, "opened_at")
	if err != nil {
		return model.SecurityFinding{}, err
	}
	finding.LastSeenAt, err = parseTime(lastSeenAt, "last_seen_at")
	if err != nil {
		return model.SecurityFinding{}, err
	}
	finding.AcknowledgedAt, err = parseNullTime(acknowledgedAt, "acknowledged_at")
	if err != nil {
		return model.SecurityFinding{}, err
	}
	finding.ResolvedAt, err = parseNullTime(resolvedAt, "resolved_at")
	if err != nil {
		return model.SecurityFinding{}, err
	}
	finding.SuppressedUntil, err = parseNullTime(suppressedUntil, "suppressed_until")
	return finding, err
}

func cloneEvidence(value map[string]string) map[string]string {
	if value == nil {
		return map[string]string{}
	}
	clone := make(map[string]string, len(value))
	for key, item := range value {
		clone[key] = item
	}
	return clone
}

func securityLimit(value int) int {
	if value <= 0 || value > 1000 {
		return 100
	}
	return value
}

func validatePolicy(policy model.SecurityPolicy, actor string) error {
	if strings.TrimSpace(actor) == "" {
		return errors.New("security policy actor is required")
	}
	switch policy.Scope {
	case "global":
		if policy.ScopeID != "" {
			return errors.New("global security policy must not have a scope id")
		}
	case "service", "container":
		if strings.TrimSpace(policy.ScopeID) == "" {
			return errors.New("scoped security policy requires a scope id")
		}
	default:
		return errors.New("security policy scope must be global, service, or container")
	}
	for _, values := range [][]string{
		policy.AllowedCapabilities, policy.AllowedPublishedPorts,
		policy.AllowedExecutablePaths, policy.AllowedWritablePaths,
	} {
		if len(values) > 128 {
			return errors.New("security policy allowlist is too large")
		}
		for _, value := range values {
			if strings.TrimSpace(value) == "" || len(value) > 256 || strings.ContainsAny(value, "\r\n\x00") {
				return errors.New("security policy contains an invalid allowlist value")
			}
		}
	}
	return nil
}

func encodePolicy(policy model.SecurityPolicy) (string, error) {
	policy.ID = 0
	policy.Scope = ""
	policy.ScopeID = ""
	policy.UpdatedBy = ""
	policy.UpdatedAt = time.Time{}
	encoded, err := json.Marshal(policy)
	if err != nil {
		return "", fmt.Errorf("encoding security policy: %w", err)
	}
	return string(encoded), nil
}

func scanSecurityPolicy(scanner securityFindingScanner) (model.SecurityPolicy, error) {
	var policy model.SecurityPolicy
	var encoded, updatedAt string
	if err := scanner.Scan(&policy.ID, &policy.Scope, &policy.ScopeID, &encoded, &policy.UpdatedBy, &updatedAt); err != nil {
		return model.SecurityPolicy{}, err
	}
	if err := json.Unmarshal([]byte(encoded), &policy); err != nil {
		return model.SecurityPolicy{}, fmt.Errorf("decoding security policy: %w", err)
	}
	parsed, err := parseTime(updatedAt, "updated_at")
	if err != nil {
		return model.SecurityPolicy{}, err
	}
	policy.UpdatedAt = parsed
	return policy, nil
}
