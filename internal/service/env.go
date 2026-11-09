package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/oralecarlangelo/touchgrass/internal/model"
	"github.com/oralecarlangelo/touchgrass/internal/secret"
	"github.com/oralecarlangelo/touchgrass/internal/store"
)

// Env pillar errors. Handlers translate each to its contract code.
var (
	ErrEnvKeyMissing           = errors.New("service: env encryption key is not configured")
	ErrEnvNotConfigured        = errors.New("service: env is not configured for this service")
	ErrEnvKeyNameInvalid       = errors.New("service: env key name is invalid")
	ErrEnvValueInvalid         = errors.New("service: env value is invalid")
	ErrEnvHasEntries           = errors.New("service: env entries already exist")
	ErrEnvApplyInProgress      = errors.New("service: env apply already in progress")
	ErrEnvNothingToApply       = errors.New("service: no pending env changes")
	ErrEnvConfirmationMismatch = errors.New("service: confirmation does not match the service name")
	ErrEnvRevealRateLimited    = errors.New("service: env reveal rate limit exceeded")
)

// Env pillar limits and defaults.
const (
	envMaxValueBytes        = 64 * 1024
	envMaxKeyLen            = 256
	defaultRevealLimit      = 10
	defaultRevealWindow     = time.Minute
	defaultValidateTimeout  = 60 * time.Second
	defaultRestartTimeout   = 10 * time.Minute
	defaultEnvProbeTimeout  = 120 * time.Second
	defaultEnvProbeInterval = time.Second
	envRevisionListLimit    = 50
	envHeader               = "# managed by touchgrass — do not edit\n"
)

// envKeyPattern is the accepted env key shape.
var envKeyPattern = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`)

// envInvalidLine extracts the key from a validator "INVALID <service>:<KEY>:" line.
var envInvalidLine = regexp.MustCompile(`^INVALID\s+[^:\s]+:([A-Z_][A-Z0-9_]*):`)

// envMissingLine matches a validator "missing required keys: A, B" line.
var envMissingLine = regexp.MustCompile(`(?i)missing required keys?:\s*(.+)$`)

// envKeyToken matches one key name inside a missing-required list.
var envKeyToken = regexp.MustCompile(`[A-Z_][A-Z0-9_]*`)

// secretKeyHints mark keys whose values default to secret on import.
var secretKeyHints = []string{"KEY", "SECRET", "TOKEN", "PASSWORD", "PASS", "DSN", "PRIVATE"}

// EnvConfig wires an Env service.
type EnvConfig struct {
	Services        *store.ServiceStore
	Store           *store.EnvStore
	Audit           *Audit
	Key             *secret.Key
	Host            EnvHost
	Prober          Prober
	Emit            func(model.Event)
	ValidateTimeout time.Duration
	RestartTimeout  time.Duration
	ProbeTimeout    time.Duration
	ProbeInterval   time.Duration
	RevealLimit     int
	RevealWindow    time.Duration
	Logger          *slog.Logger
}

// Env manages each service's runtime env file: encrypted entries, staged
// changes, audited reveals, and apply runs with rollback.
type Env struct {
	services        *store.ServiceStore
	store           *store.EnvStore
	audit           *Audit
	key             *secret.Key
	host            EnvHost
	prober          Prober
	emit            func(model.Event)
	validateTimeout time.Duration
	restartTimeout  time.Duration
	probeTimeout    time.Duration
	probeInterval   time.Duration
	revealLimit     int
	revealWindow    time.Duration
	logger          *slog.Logger

	mutex   sync.Mutex
	running map[string]string        // service id → run id
	done    map[string]chan struct{} // run id → closed when finished
	reveals map[string][]time.Time   // actor → recent reveal times
}

// NewEnv builds an Env. A nil Key disables the pillar; a nil Host uses
// the local host; a nil Emit drops events.
func NewEnv(cfg EnvConfig) *Env {
	env := &Env{
		services:        cfg.Services,
		store:           cfg.Store,
		audit:           cfg.Audit,
		key:             cfg.Key,
		host:            cfg.Host,
		prober:          cfg.Prober,
		emit:            cfg.Emit,
		validateTimeout: cfg.ValidateTimeout,
		restartTimeout:  cfg.RestartTimeout,
		probeTimeout:    cfg.ProbeTimeout,
		probeInterval:   cfg.ProbeInterval,
		revealLimit:     cfg.RevealLimit,
		revealWindow:    cfg.RevealWindow,
		logger:          cfg.Logger,
		running:         map[string]string{},
		done:            map[string]chan struct{}{},
		reveals:         map[string][]time.Time{},
	}

	if env.host == nil {
		env.host = NewLocalEnvHost()
	}

	if env.emit == nil {
		env.emit = func(model.Event) {}
	}

	if env.logger == nil {
		env.logger = slog.New(slog.DiscardHandler)
	}

	applyDefault(&env.validateTimeout, defaultValidateTimeout)
	applyDefault(&env.restartTimeout, defaultRestartTimeout)
	applyDefault(&env.probeTimeout, defaultEnvProbeTimeout)
	applyDefault(&env.probeInterval, defaultEnvProbeInterval)
	applyDefault(&env.revealWindow, defaultRevealWindow)

	if env.revealLimit <= 0 {
		env.revealLimit = defaultRevealLimit
	}

	return env
}

// applyDefault fills a non-positive duration.
func applyDefault(value *time.Duration, fallback time.Duration) {
	if *value <= 0 {
		*value = fallback
	}
}

// Enabled reports whether an encryption key is configured.
func (e *Env) Enabled() bool {
	return e.key != nil
}

// envTarget is a resolved service plus its env configuration.
type envTarget struct {
	def model.Service
	cfg model.EnvConfig
}

// resolve loads the service and its env config, failing when the pillar
// is disabled or the service has no env block.
func (e *Env) resolve(ctx context.Context, serviceID string) (envTarget, error) {
	def, err := e.services.Get(ctx, serviceID)
	if err != nil {
		return envTarget{}, err
	}

	if e.key == nil {
		return envTarget{}, ErrEnvKeyMissing
	}

	cfg, ok, err := model.EnvConfigOf(def)
	if err != nil {
		e.logger.Error("env config invalid", "service", def.ID, "error", err)

		return envTarget{}, fmt.Errorf("%w: service %q env config is invalid", ErrInvalidInput, def.ID)
	}

	if !ok {
		return envTarget{}, fmt.Errorf("%w: %q", ErrEnvNotConfigured, def.ID)
	}

	if !filepath.IsAbs(cfg.Target.Path) {
		return envTarget{}, fmt.Errorf("%w: env target path must be absolute", ErrInvalidInput)
	}

	return envTarget{def: def, cfg: cfg}, nil
}

// View returns the env state of a service. Secret values are never
// included; an unconfigured pillar reports configured=false with a reason.
func (e *Env) View(ctx context.Context, serviceID string) (model.EnvView, error) {
	target, err := e.resolve(ctx, serviceID)
	if err != nil {
		switch {
		case errors.Is(err, ErrEnvKeyMissing):
			return unconfiguredView(model.EnvReasonKeyMissing), nil
		case errors.Is(err, ErrEnvNotConfigured):
			return unconfiguredView(model.EnvReasonNotConfigured), nil
		default:
			return model.EnvView{}, err
		}
	}

	validation, required, err := e.viewValidation(ctx, serviceID)
	if err != nil {
		return model.EnvView{}, err
	}

	views, pending, err := e.entryViews(ctx, serviceID, required)
	if err != nil {
		return model.EnvView{}, err
	}

	runID, active := e.activeRun(serviceID)
	view := model.EnvView{
		Configured:      true,
		Target:          &target.cfg.Target,
		Entries:         views,
		PendingCount:    pending,
		LastValidation:  validation,
		ApplyInProgress: active,
		ActiveRunID:     runID,
	}

	applied, found, err := e.store.AppliedRevision(ctx, serviceID)
	if err != nil {
		return model.EnvView{}, err
	}

	if found {
		view.AppliedRevision = &model.EnvRevisionSummary{
			ID: applied.ID, CreatedAt: applied.CreatedAt, CreatedBy: applied.CreatedBy, Summary: applied.Summary,
		}
	}

	return view, nil
}

// viewValidation loads the last validator outcome and derives the set of
// keys it reported invalid or missing. A nil validation means none ran.
func (e *Env) viewValidation(ctx context.Context, serviceID string) (*model.EnvValidation, map[string]bool, error) {
	validation, found, err := e.store.GetValidation(ctx, serviceID)
	if err != nil {
		return nil, nil, err
	}

	required := map[string]bool{}
	if !found {
		return nil, required, nil
	}

	validation.MissingRequired = missingRequired(validation.Lines)
	required = requiredKeys(validation.Lines)

	for _, key := range validation.MissingRequired {
		required[key] = true
	}

	return &validation, required, nil
}

// entryViews builds the entry list with masking, applied values for
// changed non-secret entries, and the pending count.
func (e *Env) entryViews(
	ctx context.Context,
	serviceID string,
	required map[string]bool,
) ([]model.EnvEntryView, int, error) {
	entries, err := e.store.ListEntries(ctx, serviceID)
	if err != nil {
		return nil, 0, err
	}

	snapshot, err := e.appliedSnapshot(ctx, serviceID)
	if err != nil {
		return nil, 0, err
	}

	views := make([]model.EnvEntryView, 0, len(entries))
	pending := 0

	for _, entry := range entries {
		view, err := e.entryView(entry, required[entry.Key])
		if err != nil {
			return nil, 0, err
		}

		if err := e.attachAppliedValue(&view, entry, snapshot); err != nil {
			return nil, 0, err
		}

		if entry.Pending() {
			pending++
		}

		views = append(views, view)
	}

	return views, pending, nil
}

// unconfiguredView builds the empty view for a disabled pillar.
func unconfiguredView(reason string) model.EnvView {
	return model.EnvView{Configured: false, Reason: reason, Entries: []model.EnvEntryView{}}
}

// entryView masks secrets and decrypts non-secret values for display.
func (e *Env) entryView(entry model.EnvEntry, required bool) (model.EnvEntryView, error) {
	view := model.EnvEntryView{
		Key: entry.Key, IsSecret: entry.IsSecret, Masked: entry.IsSecret, UpdatedBy: entry.UpdatedBy,
		UpdatedAt: entry.UpdatedAt, State: entry.State, Required: required,
	}

	if entry.IsSecret {
		return view, nil
	}

	plaintext, err := e.decrypt(entry.ServiceID, entry.Key, entry.Ciphertext, entry.Nonce)
	if err != nil {
		return model.EnvEntryView{}, err
	}

	value := string(plaintext)
	secret.Zero(plaintext)

	view.Value = &value

	return view, nil
}

// attachAppliedValue adds the applied plaintext to a changed, non-secret
// entry so a UI can offer undo. Secrets never carry it.
func (e *Env) attachAppliedValue(
	view *model.EnvEntryView,
	entry model.EnvEntry,
	snapshot map[string]model.EnvRevisionEntry,
) error {
	if entry.State != model.EnvStateChanged || entry.IsSecret {
		return nil
	}

	previous, ok := snapshot[entry.Key]
	if !ok || previous.IsSecret {
		return nil
	}

	plaintext, err := e.decrypt(entry.ServiceID, entry.Key, previous.Ciphertext, previous.Nonce)
	if err != nil {
		return err
	}

	value := string(plaintext)
	secret.Zero(plaintext)

	view.AppliedValue = &value

	return nil
}

// missingRequired parses "missing required keys: A, B" validator lines.
func missingRequired(lines []string) []string {
	keys := []string{}
	seen := map[string]bool{}

	for _, line := range lines {
		match := envMissingLine.FindStringSubmatch(line)
		if match == nil {
			continue
		}

		for _, key := range envKeyToken.FindAllString(match[1], -1) {
			if !seen[key] {
				seen[key] = true
				keys = append(keys, key)
			}
		}
	}

	return keys
}

// requiredKeys parses validator lines for keys it reported invalid.
func requiredKeys(lines []string) map[string]bool {
	required := map[string]bool{}

	for _, line := range lines {
		if match := envInvalidLine.FindStringSubmatch(strings.TrimSpace(line)); match != nil {
			required[match[1]] = true
		}
	}

	return required
}

// Stage records a pending add or change without touching the file.
func (e *Env) Stage(
	ctx context.Context,
	serviceID, key, value string,
	isSecret bool,
	actor string,
) (model.EnvEntryView, error) {
	if _, err := e.resolve(ctx, serviceID); err != nil {
		return model.EnvEntryView{}, err
	}

	if err := validateEnvKey(key); err != nil {
		return model.EnvEntryView{}, err
	}

	if err := validateEnvValue(value); err != nil {
		return model.EnvEntryView{}, err
	}

	current, err := e.store.GetEntry(ctx, serviceID, key)
	if err != nil && !errors.Is(err, store.ErrEnvEntryNotFound) {
		return model.EnvEntryView{}, err
	}

	state, unchanged, err := e.stageState(current, err == nil, value, isSecret)
	if err != nil {
		return model.EnvEntryView{}, err
	}

	if unchanged {
		return e.entryView(current, false)
	}

	entry, err := e.encryptEntry(serviceID, key, value, isSecret, state, actor)
	if err != nil {
		return model.EnvEntryView{}, err
	}

	if err := e.store.UpsertEntry(ctx, entry); err != nil {
		return model.EnvEntryView{}, err
	}

	e.record(ctx, serviceID, actor, model.AuditEnvStage, model.AuditSuccess, state+" "+key)

	return e.entryView(entry, false)
}

// stageState picks the pending state for a staged value. Unchanged is
// true when an applied entry already holds the same value and flag.
func (e *Env) stageState(current model.EnvEntry, exists bool, value string, isSecret bool) (string, bool, error) {
	if !exists {
		return model.EnvStateAdded, false, nil
	}

	switch current.State {
	case model.EnvStateAdded:
		return model.EnvStateAdded, false, nil
	case model.EnvStateApplied:
		same, err := e.sameValue(current, value)
		if err != nil {
			return "", false, err
		}

		if same && current.IsSecret == isSecret {
			return model.EnvStateApplied, true, nil
		}

		return model.EnvStateChanged, false, nil
	default:
		return model.EnvStateChanged, false, nil
	}
}

// sameValue reports whether an entry decrypts to value.
func (e *Env) sameValue(entry model.EnvEntry, value string) (bool, error) {
	plaintext, err := e.decrypt(entry.ServiceID, entry.Key, entry.Ciphertext, entry.Nonce)
	if err != nil {
		return false, err
	}

	defer secret.Zero(plaintext)

	return string(plaintext) == value, nil
}

// Remove stages the removal of a key. Added keys vanish outright.
func (e *Env) Remove(ctx context.Context, serviceID, key, actor string) error {
	if _, err := e.resolve(ctx, serviceID); err != nil {
		return err
	}

	current, err := e.store.GetEntry(ctx, serviceID, key)
	if err != nil {
		return err
	}

	switch current.State {
	case model.EnvStateAdded:
		if err := e.store.DeleteEntry(ctx, serviceID, key); err != nil {
			return err
		}
	case model.EnvStateRemoved:
		return nil
	default:
		current.State = model.EnvStateRemoved
		current.UpdatedBy = actor
		current.UpdatedAt = time.Now()

		if err := e.store.UpsertEntry(ctx, current); err != nil {
			return err
		}
	}

	e.record(ctx, serviceID, actor, model.AuditEnvStage, model.AuditSuccess, "remove "+key)

	return nil
}

// Reveal decrypts one value for an admin. Every call is audited by key
// name and rate-limited per actor.
func (e *Env) Reveal(ctx context.Context, serviceID, key, actor string) (string, error) {
	if _, err := e.resolve(ctx, serviceID); err != nil {
		return "", err
	}

	if !e.allowReveal(actor) {
		e.record(ctx, serviceID, actor, model.AuditEnvReveal, model.AuditFailure, "rate limited "+key)

		return "", fmt.Errorf("%w: %d per %s", ErrEnvRevealRateLimited, e.revealLimit, e.revealWindow)
	}

	entry, err := e.store.GetEntry(ctx, serviceID, key)
	if err != nil {
		return "", err
	}

	plaintext, err := e.decrypt(serviceID, key, entry.Ciphertext, entry.Nonce)
	if err != nil {
		return "", err
	}

	value := string(plaintext)
	secret.Zero(plaintext)

	e.record(ctx, serviceID, actor, model.AuditEnvReveal, model.AuditSuccess, "reveal "+key)

	return value, nil
}

// allowReveal applies the per-actor sliding window.
func (e *Env) allowReveal(actor string) bool {
	e.mutex.Lock()
	defer e.mutex.Unlock()

	now := time.Now()
	cutoff := now.Add(-e.revealWindow)
	recent := e.reveals[actor][:0]

	for _, at := range e.reveals[actor] {
		if at.After(cutoff) {
			recent = append(recent, at)
		}
	}

	if len(recent) >= e.revealLimit {
		e.reveals[actor] = recent

		return false
	}

	e.reveals[actor] = append(recent, now)

	return true
}

// Diff compares pending entries with the applied set by key name.
func (e *Env) Diff(ctx context.Context, serviceID string) (model.EnvDiff, error) {
	if _, err := e.resolve(ctx, serviceID); err != nil {
		return model.EnvDiff{}, err
	}

	entries, err := e.store.ListEntries(ctx, serviceID)
	if err != nil {
		return model.EnvDiff{}, err
	}

	diff := diffEntries(entries)

	rendered, err := e.render(entries)
	if err != nil {
		return model.EnvDiff{}, err
	}

	diff.RenderedSHA256 = rendered.sha
	secret.Zero(rendered.data)

	return diff, nil
}

// diffEntries groups pending keys by state.
func diffEntries(entries []model.EnvEntry) model.EnvDiff {
	diff := model.EnvDiff{Added: []string{}, Changed: []string{}, Removed: []string{}}

	for _, entry := range entries {
		switch entry.State {
		case model.EnvStateAdded:
			diff.Added = append(diff.Added, entry.Key)
		case model.EnvStateChanged:
			diff.Changed = append(diff.Changed, entry.Key)
		case model.EnvStateRemoved:
			diff.Removed = append(diff.Removed, entry.Key)
		}
	}

	return diff
}

// Discard drops every pending change, restoring changed values from the
// applied revision snapshot.
func (e *Env) Discard(ctx context.Context, serviceID, actor string) error {
	if _, err := e.resolve(ctx, serviceID); err != nil {
		return err
	}

	entries, err := e.store.ListEntries(ctx, serviceID)
	if err != nil {
		return err
	}

	snapshot, err := e.appliedSnapshot(ctx, serviceID)
	if err != nil {
		return err
	}

	dropped := []string{}
	kept := []model.EnvEntry{}

	for _, entry := range entries {
		restored, keep := restoreEntry(entry, snapshot)
		if entry.Pending() {
			dropped = append(dropped, entry.Key)
		}

		if keep {
			kept = append(kept, restored)
		}
	}

	if len(dropped) == 0 {
		return nil
	}

	if err := e.store.ReplaceEntries(ctx, serviceID, kept); err != nil {
		return err
	}

	e.record(ctx, serviceID, actor, model.AuditEnvDiscard, model.AuditSuccess, "discard "+strings.Join(dropped, ","))

	return nil
}

// restoreEntry maps one working entry back to its applied form.
func restoreEntry(entry model.EnvEntry, snapshot map[string]model.EnvRevisionEntry) (model.EnvEntry, bool) {
	switch entry.State {
	case model.EnvStateApplied:
		return entry, true
	case model.EnvStateAdded:
		return model.EnvEntry{}, false
	default:
		previous, ok := snapshot[entry.Key]
		if !ok {
			return model.EnvEntry{}, false
		}

		entry.Ciphertext = previous.Ciphertext
		entry.Nonce = previous.Nonce
		entry.KeyID = previous.KeyID
		entry.IsSecret = previous.IsSecret
		entry.State = model.EnvStateApplied

		return entry, true
	}
}

// appliedSnapshot loads the applied revision's entries by key.
func (e *Env) appliedSnapshot(ctx context.Context, serviceID string) (map[string]model.EnvRevisionEntry, error) {
	snapshot := map[string]model.EnvRevisionEntry{}

	applied, found, err := e.store.AppliedRevision(ctx, serviceID)
	if err != nil || !found {
		return snapshot, err
	}

	entries, err := e.store.RevisionEntries(ctx, applied.ID)
	if err != nil {
		return nil, err
	}

	for _, entry := range entries {
		snapshot[entry.Key] = entry
	}

	return snapshot, nil
}

// Import reads the configured target file into the store as applied
// entries and records it as the applied revision.
func (e *Env) Import(ctx context.Context, serviceID string, replace bool, actor string) (model.EnvImportResult, error) {
	target, err := e.resolve(ctx, serviceID)
	if err != nil {
		return model.EnvImportResult{}, err
	}

	existing, err := e.store.ListEntries(ctx, serviceID)
	if err != nil {
		return model.EnvImportResult{}, err
	}

	if len(existing) > 0 && !replace {
		return model.EnvImportResult{}, fmt.Errorf("%w: %d entries", ErrEnvHasEntries, len(existing))
	}

	data, err := e.readTarget(ctx, target)
	if err != nil {
		e.logger.Error("env import read failed", "service", serviceID, "error", err)

		return model.EnvImportResult{}, fmt.Errorf("%w: env file could not be read", ErrInvalidInput)
	}

	defer secret.Zero(data)

	parsed, result := parseEnvFile(string(data))
	entries := make([]model.EnvEntry, 0, len(parsed))

	for _, line := range parsed {
		entry, err := e.encryptEntry(serviceID, line.key, line.value, line.secret, model.EnvStateApplied, actor)
		if err != nil {
			return model.EnvImportResult{}, err
		}

		entries = append(entries, entry)
	}

	if err := e.store.ReplaceEntries(ctx, serviceID, entries); err != nil {
		return model.EnvImportResult{}, err
	}

	rendered, err := e.render(entries)
	if err != nil {
		return model.EnvImportResult{}, err
	}

	secret.Zero(rendered.data)

	if _, err := e.store.InsertRevision(ctx, model.EnvRevision{
		ServiceID: serviceID, CreatedBy: actor, Summary: "imported from target file",
		RenderedSHA256: rendered.sha, Applied: true,
	}, snapshotEntries(entries)); err != nil {
		return model.EnvImportResult{}, err
	}

	e.record(ctx, serviceID, actor, model.AuditEnvImport, model.AuditSuccess,
		fmt.Sprintf("import %d keys (%d skipped, %d guessed secret)", result.Imported, result.Skipped, result.SecretGuessed))

	return result, nil
}

// readTarget returns the current file: read_cmd's stdout when configured,
// otherwise the target path itself.
func (e *Env) readTarget(ctx context.Context, target envTarget) ([]byte, error) {
	cfg := target.cfg

	if cfg.ReadCmd == "" {
		return e.host.ReadFile(ctx, cfg.Target.Path, cfg.Sudo)
	}

	readCtx, cancel := context.WithTimeout(ctx, e.validateTimeout)
	defer cancel()

	env := []string{"TOUCHGRASS_ENV_TARGET=" + cfg.Target.Path, "TOUCHGRASS_ENV_SERVICE=" + target.def.ID}

	return e.host.Output(readCtx, cfg.ReadCmd, env, cfg.Sudo)
}

// envLine is one parsed KEY=VALUE import line.
type envLine struct {
	key    string
	value  string
	secret bool
}

// parseEnvFile splits an env file into entries, skipping comments, blank
// lines, and malformed keys. Later duplicates win.
func parseEnvFile(content string) ([]envLine, model.EnvImportResult) {
	var result model.EnvImportResult

	byKey := map[string]envLine{}
	order := []string{}

	for raw := range strings.SplitSeq(strings.ReplaceAll(content, "\r\n", "\n"), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		line = strings.TrimPrefix(line, "export ")

		key, value, found := strings.Cut(line, "=")
		key = strings.TrimSpace(key)

		if !found || validateEnvKey(key) != nil || validateEnvValue(value) != nil {
			result.Skipped++

			continue
		}

		if _, seen := byKey[key]; !seen {
			order = append(order, key)
		}

		byKey[key] = envLine{key: key, value: value, secret: guessSecret(key, value)}
	}

	lines := make([]envLine, 0, len(order))

	for _, key := range order {
		line := byKey[key]
		if line.secret {
			result.SecretGuessed++
		}

		lines = append(lines, line)
	}

	result.Imported = len(lines)

	return lines, result
}

// guessSecret flags keys whose names suggest credentials, plus URI/URL
// values that embed user info.
func guessSecret(key, value string) bool {
	for _, hint := range secretKeyHints {
		if strings.Contains(key, hint) {
			return true
		}
	}

	if strings.Contains(key, "URI") || strings.Contains(key, "URL") {
		scheme, rest, found := strings.Cut(value, "://")

		return found && scheme != "" && strings.Contains(strings.SplitN(rest, "/", 2)[0], "@")
	}

	return false
}

// validateEnvKey enforces the key shape.
func validateEnvKey(key string) error {
	if len(key) == 0 || len(key) > envMaxKeyLen || !envKeyPattern.MatchString(key) {
		return fmt.Errorf("%w: want ^[A-Z_][A-Z0-9_]*$", ErrEnvKeyNameInvalid)
	}

	return nil
}

// validateEnvValue enforces UTF-8, no newlines, and the size cap.
func validateEnvValue(value string) error {
	if len(value) > envMaxValueBytes {
		return fmt.Errorf("%w: longer than %d bytes", ErrEnvValueInvalid, envMaxValueBytes)
	}

	if !utf8.ValidString(value) {
		return fmt.Errorf("%w: not valid UTF-8", ErrEnvValueInvalid)
	}

	if strings.ContainsAny(value, "\r\n") {
		return fmt.Errorf("%w: newlines are not allowed", ErrEnvValueInvalid)
	}

	return nil
}

// encryptEntry seals one value into a store entry.
func (e *Env) encryptEntry(
	serviceID, key, value string,
	isSecret bool,
	state, actor string,
) (model.EnvEntry, error) {
	ciphertext, nonce, err := e.key.Encrypt([]byte(value), secret.AAD(serviceID, key))
	if err != nil {
		return model.EnvEntry{}, fmt.Errorf("encrypting env value: %w", err)
	}

	return model.EnvEntry{
		ServiceID: serviceID, Key: key, Ciphertext: ciphertext, Nonce: nonce, KeyID: e.key.ID(),
		IsSecret: isSecret, State: state, UpdatedBy: actor, UpdatedAt: time.Now(),
	}, nil
}

// decrypt opens one stored value. The caller zeroes the result.
func (e *Env) decrypt(serviceID, key string, ciphertext, nonce []byte) ([]byte, error) {
	if e.key == nil {
		return nil, ErrEnvKeyMissing
	}

	plaintext, err := e.key.Decrypt(ciphertext, nonce, secret.AAD(serviceID, key))
	if err != nil {
		e.logger.Error("env value could not be decrypted", "service", serviceID, "key", key)

		return nil, fmt.Errorf("decrypting env value %s: %w", key, err)
	}

	return plaintext, nil
}

// renderedFile is one rendered env file with its digest.
type renderedFile struct {
	data []byte
	sha  string
	keys []string
}

// render builds the file for every non-removed entry: header, then
// KEY=VALUE lines sorted by key, LF-terminated, values raw.
func (e *Env) render(entries []model.EnvEntry) (renderedFile, error) {
	sorted := make([]model.EnvEntry, 0, len(entries))

	for _, entry := range entries {
		if entry.State != model.EnvStateRemoved {
			sorted = append(sorted, entry)
		}
	}

	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Key < sorted[j].Key })

	var builder strings.Builder

	builder.WriteString(envHeader)

	keys := make([]string, 0, len(sorted))

	for _, entry := range sorted {
		plaintext, err := e.decrypt(entry.ServiceID, entry.Key, entry.Ciphertext, entry.Nonce)
		if err != nil {
			return renderedFile{}, err
		}

		builder.WriteString(entry.Key)
		builder.WriteByte('=')
		builder.Write(plaintext)
		builder.WriteByte('\n')
		secret.Zero(plaintext)

		keys = append(keys, entry.Key)
	}

	data := []byte(builder.String())
	sum := sha256.Sum256(data)

	return renderedFile{data: data, sha: hex.EncodeToString(sum[:]), keys: keys}, nil
}

// snapshotEntries copies non-removed entries into revision rows. The AAD
// binds service and key only, so ciphertexts carry over unchanged.
func snapshotEntries(entries []model.EnvEntry) []model.EnvRevisionEntry {
	snapshot := make([]model.EnvRevisionEntry, 0, len(entries))

	for _, entry := range entries {
		if entry.State == model.EnvStateRemoved {
			continue
		}

		snapshot = append(snapshot, model.EnvRevisionEntry{
			Key: entry.Key, Ciphertext: entry.Ciphertext, Nonce: entry.Nonce,
			KeyID: entry.KeyID, IsSecret: entry.IsSecret,
		})
	}

	return snapshot
}

// record writes one audit row, logging rather than failing on error.
func (e *Env) record(ctx context.Context, serviceID, actor, action, result, detail string) {
	if _, err := e.audit.Record(ctx, model.AuditRecord{
		ServiceID: serviceID, Actor: actor, Action: action, Result: result, Detail: detail,
	}); err != nil {
		e.logger.Error("env audit failed", "service", serviceID, "action", action, "error", err)
	}
}
