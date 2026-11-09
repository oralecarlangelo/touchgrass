package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/oralecarlangelo/touchgrass/internal/model"
)

// Env store errors.
var (
	ErrEnvEntryNotFound    = errors.New("store: env entry not found")
	ErrEnvRevisionNotFound = errors.New("store: env revision not found")
	ErrEnvRunNotFound      = errors.New("store: env run not found")
)

// EnvStore persists encrypted env entries, revisions, runs, and the last
// validator outcome. It never sees plaintext: callers pass ciphertext.
type EnvStore struct {
	db *DB
}

// NewEnvStore builds an EnvStore on db.
func NewEnvStore(db *DB) *EnvStore {
	return &EnvStore{db: db}
}

const envEntryColumns = `service_id, key, ciphertext, nonce, key_id, is_secret, state, updated_by, updated_at`

// ListEntries returns every entry for a service ordered by key.
func (s *EnvStore) ListEntries(ctx context.Context, serviceID string) ([]model.EnvEntry, error) {
	rows, err := s.db.sql.QueryContext(ctx,
		`SELECT `+envEntryColumns+` FROM env_entries WHERE service_id = ? ORDER BY key`, serviceID)
	if err != nil {
		return nil, fmt.Errorf("querying env entries: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()

	entries := []model.EnvEntry{}

	for rows.Next() {
		entry, err := scanEnvEntry(rows)
		if err != nil {
			return nil, err
		}

		entries = append(entries, entry)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating env entries: %w", err)
	}

	return entries, nil
}

// GetEntry returns one entry or ErrEnvEntryNotFound.
func (s *EnvStore) GetEntry(ctx context.Context, serviceID, key string) (model.EnvEntry, error) {
	row := s.db.sql.QueryRowContext(ctx,
		`SELECT `+envEntryColumns+` FROM env_entries WHERE service_id = ? AND key = ?`, serviceID, key)

	entry, err := scanEnvEntry(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return model.EnvEntry{}, fmt.Errorf("%w: %q", ErrEnvEntryNotFound, key)
		}

		return model.EnvEntry{}, err
	}

	return entry, nil
}

// UpsertEntry inserts or replaces one entry.
func (s *EnvStore) UpsertEntry(ctx context.Context, entry model.EnvEntry) error {
	if err := upsertEnvEntry(ctx, s.db.sql, entry); err != nil {
		return err
	}

	return nil
}

// execer covers *sql.DB and *sql.Tx.
type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// upsertEnvEntry writes one entry through db or a transaction.
func upsertEnvEntry(ctx context.Context, db execer, entry model.EnvEntry) error {
	if entry.UpdatedAt.IsZero() {
		entry.UpdatedAt = time.Now()
	}

	if _, err := db.ExecContext(ctx,
		`INSERT INTO env_entries (`+envEntryColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(service_id, key) DO UPDATE SET
		   ciphertext = excluded.ciphertext, nonce = excluded.nonce, key_id = excluded.key_id,
		   is_secret = excluded.is_secret, state = excluded.state,
		   updated_by = excluded.updated_by, updated_at = excluded.updated_at`,
		entry.ServiceID, entry.Key, entry.Ciphertext, entry.Nonce, entry.KeyID,
		boolInt(entry.IsSecret), entry.State, entry.UpdatedBy, formatTime(entry.UpdatedAt),
	); err != nil {
		return fmt.Errorf("upserting env entry: %w", err)
	}

	return nil
}

// DeleteEntry removes one entry row. Unknown keys fail with ErrEnvEntryNotFound.
func (s *EnvStore) DeleteEntry(ctx context.Context, serviceID, key string) error {
	res, err := s.db.sql.ExecContext(ctx,
		`DELETE FROM env_entries WHERE service_id = ? AND key = ?`, serviceID, key)
	if err != nil {
		return fmt.Errorf("deleting env entry: %w", err)
	}

	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("counting deleted env entries: %w", err)
	}

	if affected == 0 {
		return fmt.Errorf("%w: %q", ErrEnvEntryNotFound, key)
	}

	return nil
}

// ReplaceEntries swaps the whole working set of a service for entries in
// one transaction.
func (s *EnvStore) ReplaceEntries(ctx context.Context, serviceID string, entries []model.EnvEntry) error {
	tx, err := s.db.sql.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("starting env replace: %w", err)
	}

	defer func() {
		_ = tx.Rollback()
	}()

	if _, err := tx.ExecContext(ctx, `DELETE FROM env_entries WHERE service_id = ?`, serviceID); err != nil {
		return fmt.Errorf("clearing env entries: %w", err)
	}

	for _, entry := range entries {
		entry.ServiceID = serviceID

		if err := upsertEnvEntry(ctx, tx, entry); err != nil {
			return err
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing env replace: %w", err)
	}

	return nil
}

// MarkApplied promotes every pending entry: removed rows disappear and the
// rest become applied.
func (s *EnvStore) MarkApplied(ctx context.Context, serviceID string, at time.Time) error {
	tx, err := s.db.sql.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("starting env mark applied: %w", err)
	}

	defer func() {
		_ = tx.Rollback()
	}()

	if _, err := tx.ExecContext(ctx,
		`DELETE FROM env_entries WHERE service_id = ? AND state = ?`, serviceID, model.EnvStateRemoved); err != nil {
		return fmt.Errorf("dropping removed env entries: %w", err)
	}

	if _, err := tx.ExecContext(ctx,
		`UPDATE env_entries SET state = ?, updated_at = ? WHERE service_id = ? AND state != ?`,
		model.EnvStateApplied, formatTime(at), serviceID, model.EnvStateApplied); err != nil {
		return fmt.Errorf("marking env entries applied: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing env mark applied: %w", err)
	}

	return nil
}

// InsertRevision stores a revision with its snapshot entries. When the
// revision is applied, every other revision of the service loses the flag.
func (s *EnvStore) InsertRevision(
	ctx context.Context,
	revision model.EnvRevision,
	entries []model.EnvRevisionEntry,
) (int64, error) {
	if revision.CreatedAt.IsZero() {
		revision.CreatedAt = time.Now()
	}

	tx, err := s.db.sql.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("starting env revision insert: %w", err)
	}

	defer func() {
		_ = tx.Rollback()
	}()

	if revision.Applied {
		if _, err := tx.ExecContext(ctx,
			`UPDATE env_revisions SET applied = 0 WHERE service_id = ?`, revision.ServiceID); err != nil {
			return 0, fmt.Errorf("clearing applied env revisions: %w", err)
		}
	}

	res, err := tx.ExecContext(ctx,
		`INSERT INTO env_revisions (service_id, created_by, created_at, summary, rendered_sha256, applied)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		revision.ServiceID, revision.CreatedBy, formatTime(revision.CreatedAt),
		revision.Summary, revision.RenderedSHA256, boolInt(revision.Applied))
	if err != nil {
		return 0, fmt.Errorf("inserting env revision: %w", err)
	}

	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("reading env revision id: %w", err)
	}

	for _, entry := range entries {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO env_revision_entries (revision_id, key, ciphertext, nonce, key_id, is_secret)
			 VALUES (?, ?, ?, ?, ?, ?)`,
			id, entry.Key, entry.Ciphertext, entry.Nonce, entry.KeyID, boolInt(entry.IsSecret)); err != nil {
			return 0, fmt.Errorf("inserting env revision entry: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("committing env revision: %w", err)
	}

	return id, nil
}

const envRevisionColumns = `id, service_id, created_by, created_at, summary, rendered_sha256, applied`

// ListRevisions returns revisions newest first, bounded by limit.
func (s *EnvStore) ListRevisions(ctx context.Context, serviceID string, limit int) ([]model.EnvRevision, error) {
	rows, err := s.db.sql.QueryContext(ctx,
		`SELECT `+envRevisionColumns+` FROM env_revisions WHERE service_id = ? ORDER BY id DESC LIMIT ?`,
		serviceID, limit)
	if err != nil {
		return nil, fmt.Errorf("querying env revisions: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()

	revisions := []model.EnvRevision{}

	for rows.Next() {
		revision, err := scanEnvRevision(rows)
		if err != nil {
			return nil, err
		}

		revisions = append(revisions, revision)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating env revisions: %w", err)
	}

	return revisions, nil
}

// GetRevision returns one revision of a service or ErrEnvRevisionNotFound.
func (s *EnvStore) GetRevision(ctx context.Context, serviceID string, id int64) (model.EnvRevision, error) {
	row := s.db.sql.QueryRowContext(ctx,
		`SELECT `+envRevisionColumns+` FROM env_revisions WHERE service_id = ? AND id = ?`, serviceID, id)

	revision, err := scanEnvRevision(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return model.EnvRevision{}, fmt.Errorf("%w: %d", ErrEnvRevisionNotFound, id)
		}

		return model.EnvRevision{}, err
	}

	return revision, nil
}

// AppliedRevision returns the currently applied revision; found is false
// when nothing has been applied yet.
func (s *EnvStore) AppliedRevision(ctx context.Context, serviceID string) (model.EnvRevision, bool, error) {
	row := s.db.sql.QueryRowContext(ctx,
		`SELECT `+envRevisionColumns+` FROM env_revisions WHERE service_id = ? AND applied = 1 ORDER BY id DESC LIMIT 1`,
		serviceID)

	revision, err := scanEnvRevision(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return model.EnvRevision{}, false, nil
		}

		return model.EnvRevision{}, false, err
	}

	return revision, true, nil
}

// RevisionEntries returns a revision's snapshot ordered by key.
func (s *EnvStore) RevisionEntries(ctx context.Context, revisionID int64) ([]model.EnvRevisionEntry, error) {
	rows, err := s.db.sql.QueryContext(ctx,
		`SELECT revision_id, key, ciphertext, nonce, key_id, is_secret
		 FROM env_revision_entries WHERE revision_id = ? ORDER BY key`, revisionID)
	if err != nil {
		return nil, fmt.Errorf("querying env revision entries: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()

	entries := []model.EnvRevisionEntry{}

	for rows.Next() {
		var (
			entry    model.EnvRevisionEntry
			isSecret int
		)

		if err := rows.Scan(
			&entry.RevisionID, &entry.Key, &entry.Ciphertext, &entry.Nonce, &entry.KeyID, &isSecret,
		); err != nil {
			return nil, fmt.Errorf("scanning env revision entry: %w", err)
		}

		entry.IsSecret = isSecret == 1
		entries = append(entries, entry)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating env revision entries: %w", err)
	}

	return entries, nil
}

// InsertRun records the start of an apply run.
func (s *EnvStore) InsertRun(ctx context.Context, run model.EnvRun) error {
	steps, err := marshalSteps(run.Steps)
	if err != nil {
		return err
	}

	if _, err := s.db.sql.ExecContext(ctx,
		`INSERT INTO env_runs (id, service_id, revision_id, state, started_at, finished_at, steps_json)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		run.ID, run.ServiceID, nullInt64(run.RevisionID), run.State,
		formatTime(run.StartedAt), formatNullTime(run.FinishedAt), steps); err != nil {
		return fmt.Errorf("inserting env run: %w", err)
	}

	return nil
}

// UpdateRun stores the current state, finish time, revision, and step log.
func (s *EnvStore) UpdateRun(ctx context.Context, run model.EnvRun) error {
	steps, err := marshalSteps(run.Steps)
	if err != nil {
		return err
	}

	res, err := s.db.sql.ExecContext(ctx,
		`UPDATE env_runs SET revision_id = ?, state = ?, finished_at = ?, steps_json = ? WHERE id = ?`,
		nullInt64(run.RevisionID), run.State, formatNullTime(run.FinishedAt), steps, run.ID)
	if err != nil {
		return fmt.Errorf("updating env run: %w", err)
	}

	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("counting updated env runs: %w", err)
	}

	if affected == 0 {
		return fmt.Errorf("%w: %q", ErrEnvRunNotFound, run.ID)
	}

	return nil
}

// GetRun returns one run of a service or ErrEnvRunNotFound.
func (s *EnvStore) GetRun(ctx context.Context, serviceID, id string) (model.EnvRun, error) {
	var (
		run        model.EnvRun
		revisionID sql.NullInt64
		startedAt  string
		finishedAt sql.NullString
		steps      string
	)

	if err := s.db.sql.QueryRowContext(ctx,
		`SELECT id, service_id, revision_id, state, started_at, finished_at, steps_json
		 FROM env_runs WHERE service_id = ? AND id = ?`, serviceID, id,
	).Scan(&run.ID, &run.ServiceID, &revisionID, &run.State, &startedAt, &finishedAt, &steps); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return model.EnvRun{}, fmt.Errorf("%w: %q", ErrEnvRunNotFound, id)
		}

		return model.EnvRun{}, fmt.Errorf("scanning env run: %w", err)
	}

	started, err := parseTime(startedAt, "started_at")
	if err != nil {
		return model.EnvRun{}, err
	}

	finished, err := parseNullTime(finishedAt, "finished_at")
	if err != nil {
		return model.EnvRun{}, err
	}

	run.StartedAt = started
	run.FinishedAt = finished
	run.Steps = []model.EnvRunStep{}

	if revisionID.Valid {
		value := revisionID.Int64
		run.RevisionID = &value
	}

	if err := json.Unmarshal([]byte(steps), &run.Steps); err != nil {
		return model.EnvRun{}, fmt.Errorf("decoding env run steps: %w", err)
	}

	return run, nil
}

// FailRunningRuns marks every run still recorded as running as failed with
// the given note. It reconciles runs orphaned by a process restart.
func (s *EnvStore) FailRunningRuns(ctx context.Context, at time.Time, note string) (int64, error) {
	step, err := marshalSteps([]model.EnvRunStep{{Step: model.EnvStepFailed, Message: note, At: at}})
	if err != nil {
		return 0, err
	}

	res, err := s.db.sql.ExecContext(ctx,
		`UPDATE env_runs SET state = ?, finished_at = ?,
		   steps_json = json_insert(steps_json, '$[#]', json_extract(?, '$[0]'))
		 WHERE state = ?`,
		model.EnvRunFailed, formatTime(at), step, model.EnvRunRunning)
	if err != nil {
		return 0, fmt.Errorf("failing orphaned env runs: %w", err)
	}

	affected, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("counting failed env runs: %w", err)
	}

	return affected, nil
}

// UpsertValidation stores the latest validator outcome for a service.
func (s *EnvStore) UpsertValidation(ctx context.Context, validation model.EnvValidation) error {
	lines := validation.Lines
	if lines == nil {
		lines = []string{}
	}

	encoded, err := json.Marshal(lines)
	if err != nil {
		return fmt.Errorf("encoding env validation lines: %w", err)
	}

	if validation.At.IsZero() {
		validation.At = time.Now()
	}

	if _, err := s.db.sql.ExecContext(ctx,
		`INSERT INTO env_validations (service_id, ok, lines_json, at) VALUES (?, ?, ?, ?)
		 ON CONFLICT(service_id) DO UPDATE SET ok = excluded.ok, lines_json = excluded.lines_json, at = excluded.at`,
		validation.ServiceID, boolInt(validation.OK), string(encoded), formatTime(validation.At)); err != nil {
		return fmt.Errorf("upserting env validation: %w", err)
	}

	return nil
}

// GetValidation returns the last validator outcome; found is false when
// the validator never ran.
func (s *EnvStore) GetValidation(ctx context.Context, serviceID string) (model.EnvValidation, bool, error) {
	var (
		validation model.EnvValidation
		ok         int
		lines      string
		at         string
	)

	if err := s.db.sql.QueryRowContext(ctx,
		`SELECT service_id, ok, lines_json, at FROM env_validations WHERE service_id = ?`, serviceID,
	).Scan(&validation.ServiceID, &ok, &lines, &at); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return model.EnvValidation{}, false, nil
		}

		return model.EnvValidation{}, false, fmt.Errorf("scanning env validation: %w", err)
	}

	moment, err := parseTime(at, "at")
	if err != nil {
		return model.EnvValidation{}, false, err
	}

	validation.OK = ok == 1
	validation.At = moment
	validation.Lines = []string{}

	if err := json.Unmarshal([]byte(lines), &validation.Lines); err != nil {
		return model.EnvValidation{}, false, fmt.Errorf("decoding env validation lines: %w", err)
	}

	return validation, true, nil
}

// scanEnvEntry maps one env_entries row.
func scanEnvEntry(row scanner) (model.EnvEntry, error) {
	var (
		entry     model.EnvEntry
		isSecret  int
		updatedAt string
	)

	if err := row.Scan(
		&entry.ServiceID, &entry.Key, &entry.Ciphertext, &entry.Nonce, &entry.KeyID,
		&isSecret, &entry.State, &entry.UpdatedBy, &updatedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return model.EnvEntry{}, err
		}

		return model.EnvEntry{}, fmt.Errorf("scanning env entry: %w", err)
	}

	updated, err := parseTime(updatedAt, "updated_at")
	if err != nil {
		return model.EnvEntry{}, err
	}

	entry.IsSecret = isSecret == 1
	entry.UpdatedAt = updated

	return entry, nil
}

// scanEnvRevision maps one env_revisions row.
func scanEnvRevision(row scanner) (model.EnvRevision, error) {
	var (
		revision  model.EnvRevision
		createdAt string
		applied   int
	)

	if err := row.Scan(
		&revision.ID, &revision.ServiceID, &revision.CreatedBy, &createdAt,
		&revision.Summary, &revision.RenderedSHA256, &applied,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return model.EnvRevision{}, err
		}

		return model.EnvRevision{}, fmt.Errorf("scanning env revision: %w", err)
	}

	created, err := parseTime(createdAt, "created_at")
	if err != nil {
		return model.EnvRevision{}, err
	}

	revision.CreatedAt = created
	revision.Applied = applied == 1

	return revision, nil
}

// marshalSteps encodes a step log, never as null.
func marshalSteps(steps []model.EnvRunStep) (string, error) {
	if steps == nil {
		steps = []model.EnvRunStep{}
	}

	encoded, err := json.Marshal(steps)
	if err != nil {
		return "", fmt.Errorf("encoding env run steps: %w", err)
	}

	return string(encoded), nil
}

// boolInt renders a bool for SQLite storage.
func boolInt(value bool) int {
	if value {
		return 1
	}

	return 0
}

// nullInt64 renders an optional integer for storage.
func nullInt64(value *int64) any {
	if value == nil {
		return nil
	}

	return *value
}
