package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"path"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/oralecarlangelo/touchgrass/internal/dbmigrate"
	"github.com/oralecarlangelo/touchgrass/internal/docker"
	"github.com/oralecarlangelo/touchgrass/internal/model"
	"github.com/oralecarlangelo/touchgrass/internal/probe"
	"github.com/oralecarlangelo/touchgrass/internal/secret"
	"github.com/oralecarlangelo/touchgrass/internal/store"
)

// Migration errors reported to handlers.
var (
	// ErrMigrationsDisabled reports a config write with no env key: the
	// database URL cannot be stored without the envelope.
	ErrMigrationsDisabled = errors.New("service: migrations need the env key to store a database url")
	// ErrMigrationsNotConfigured reports an action on a service with no
	// migration setup.
	ErrMigrationsNotConfigured = errors.New("service: migrations are not configured for this service")
	// ErrMigrationBusy reports a second command while one is running.
	ErrMigrationBusy = errors.New("service: a migration command is already running for this service")
	// ErrMigrationConfirmation reports a command whose confirm text is
	// not the service name.
	ErrMigrationConfirmation = errors.New("service: confirmation must equal the service name")
	// ErrMigrationContainer reports that no running container was found
	// to read from or run in.
	ErrMigrationContainer = errors.New("service: no running container for this service")
)

const (
	// migrationURLField is the envelope field name of the database URL.
	// It is not a valid env key, so it can never collide with one.
	migrationURLField = "migrations.database_url"
	// migrationHistoryTimeout bounds one history read.
	migrationHistoryTimeout = 8 * time.Second
	// migrationRunTimeout bounds one command.
	migrationRunTimeout = 20 * time.Minute
	// migrationRunsLimit bounds the run list.
	migrationRunsLimit = 50
	// migrationProbeErrorMax bounds a probe error shown to operators.
	migrationProbeErrorMax = 300
	// migrationCommandMaxArgs bounds a stored command prefix.
	migrationCommandMaxArgs = 12
	// migrationArgMaxLen bounds one stored argument or path.
	migrationArgMaxLen = 256
)

// composeServiceRe is the compose service name shape.
var composeServiceRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)

// MigrationDocker is the engine access the migrations pillar needs.
type MigrationDocker interface {
	List(ctx context.Context) ([]docker.Container, error)
	Workdir(ctx context.Context, id string) (string, error)
	ReadFiles(ctx context.Context, id, containerPath string) (map[string][]byte, error)
	Exec(ctx context.Context, id string, spec docker.ExecSpec, output io.Writer) (int, error)
}

// MigrationsConfig wires a Migrations.
type MigrationsConfig struct {
	Services       *store.ServiceStore
	Configs        *store.MigrationConfigStore
	Runs           *store.MigrationRunStore
	Audit          *Audit
	Key            *secret.Key
	Tools          *dbmigrate.Registry
	Docker         MigrationDocker
	HistoryTimeout time.Duration
	RunTimeout     time.Duration
	Logger         *slog.Logger
}

// Migrations reports the schema migration state of monitored services
// and runs their migration tool's commands. It reads the application's
// database with a stored URL, reads the shipped migrations from the
// running container, and executes commands inside that container, so
// the application's own environment and tool version are what run.
type Migrations struct {
	services       *store.ServiceStore
	configs        *store.MigrationConfigStore
	runs           *store.MigrationRunStore
	audit          *Audit
	key            *secret.Key
	tools          *dbmigrate.Registry
	docker         MigrationDocker
	historyTimeout time.Duration
	runTimeout     time.Duration
	logger         *slog.Logger

	mutex   sync.Mutex
	running map[string]context.CancelFunc // service id → cancel of its run
	group   sync.WaitGroup
}

// NewMigrations builds a Migrations. A nil Key leaves reads working for
// nothing (no config can exist) and refuses config writes.
func NewMigrations(cfg MigrationsConfig) *Migrations {
	migrations := &Migrations{
		services:       cfg.Services,
		configs:        cfg.Configs,
		runs:           cfg.Runs,
		audit:          cfg.Audit,
		key:            cfg.Key,
		tools:          cfg.Tools,
		docker:         cfg.Docker,
		historyTimeout: cfg.HistoryTimeout,
		runTimeout:     cfg.RunTimeout,
		logger:         cfg.Logger,
		running:        map[string]context.CancelFunc{},
	}

	if migrations.historyTimeout <= 0 {
		migrations.historyTimeout = migrationHistoryTimeout
	}

	if migrations.runTimeout <= 0 {
		migrations.runTimeout = migrationRunTimeout
	}

	if migrations.logger == nil {
		migrations.logger = slog.New(slog.DiscardHandler)
	}

	return migrations
}

// Tools lists the migration tools this build offers with their
// defaults, in name order.
func (m *Migrations) Tools() []model.MigrationTool {
	names := m.tools.Names()
	tools := make([]model.MigrationTool, 0, len(names))

	for _, name := range names {
		tool, err := m.tools.Get(name)
		if err != nil {
			continue
		}

		tools = append(tools, model.MigrationTool{
			Name:          tool.Name(),
			MigrationsDir: tool.Layout().MigrationsDir,
			Command:       tool.DefaultCommand(),
		})
	}

	return tools
}

// Config returns a service's migration setup. The second result is
// false when the service has none.
func (m *Migrations) Config(ctx context.Context, serviceID string) (model.MigrationConfig, bool, error) {
	if _, err := m.services.Get(ctx, serviceID); err != nil {
		return model.MigrationConfig{}, false, fmt.Errorf("loading service: %w", err)
	}

	row, err := m.configs.Get(ctx, serviceID)
	if err != nil {
		if errors.Is(err, store.ErrMigrationConfigNotFound) {
			return model.MigrationConfig{}, false, nil
		}

		return model.MigrationConfig{}, false, fmt.Errorf("loading migration config: %w", err)
	}

	return row.MigrationConfig, true, nil
}

// SaveConfig creates or replaces a service's migration setup. An empty
// DatabaseURL keeps the stored URL, so the other fields can change
// without the secret being sent again.
func (m *Migrations) SaveConfig(
	ctx context.Context,
	serviceID string,
	input model.MigrationConfigInput,
	actor string,
) (model.MigrationConfig, error) {
	if m.key == nil {
		return model.MigrationConfig{}, ErrMigrationsDisabled
	}

	if _, err := m.services.Get(ctx, serviceID); err != nil {
		return model.MigrationConfig{}, fmt.Errorf("loading service: %w", err)
	}

	tool, err := m.tools.Get(input.Tool)
	if err != nil {
		return model.MigrationConfig{}, fmt.Errorf("%w: tool must be one of %s",
			ErrInvalidInput, strings.Join(m.tools.Names(), ", "))
	}

	if err := validateMigrationLayout(input); err != nil {
		return model.MigrationConfig{}, err
	}

	row := store.MigrationConfigRow{
		MigrationConfig: model.MigrationConfig{
			ServiceID:     serviceID,
			Tool:          tool.Name(),
			Container:     input.Container,
			Workdir:       input.Workdir,
			MigrationsDir: input.MigrationsDir,
			Command:       append([]string{}, input.Command...),
			UpdatedBy:     actor,
			UpdatedAt:     time.Now().UTC(),
		},
	}

	if err := m.sealURL(ctx, tool, input.DatabaseURL, &row); err != nil {
		return model.MigrationConfig{}, err
	}

	if err := m.configs.Put(ctx, row); err != nil {
		return model.MigrationConfig{}, fmt.Errorf("saving migration config: %w", err)
	}

	m.record(ctx, serviceID, actor, model.AuditMigrationConfig, model.AuditSuccess,
		fmt.Sprintf("configured %s against %s", row.Tool, row.Database))

	return row.MigrationConfig, nil
}

// sealURL fills the sealed database URL of row: a new URL is validated
// and encrypted; an empty one carries the stored envelope over.
func (m *Migrations) sealURL(
	ctx context.Context,
	tool dbmigrate.Tool,
	databaseURL string,
	row *store.MigrationConfigRow,
) error {
	if strings.TrimSpace(databaseURL) == "" {
		existing, err := m.configs.Get(ctx, row.ServiceID)
		if err != nil {
			if errors.Is(err, store.ErrMigrationConfigNotFound) {
				return fmt.Errorf("%w: database_url is required", ErrInvalidInput)
			}

			return fmt.Errorf("loading migration config: %w", err)
		}

		if existing.Tool != row.Tool {
			return fmt.Errorf("%w: database_url is required when the tool changes", ErrInvalidInput)
		}

		row.URLCiphertext = existing.URLCiphertext
		row.URLNonce = existing.URLNonce
		row.URLKeyID = existing.URLKeyID
		row.Database = existing.Database

		return nil
	}

	trimmed := strings.TrimSpace(databaseURL)

	target, err := tool.Describe(trimmed)
	if err != nil {
		// Describe errors never echo the URL.
		return fmt.Errorf("%w: database_url: %w", ErrInvalidInput, err)
	}

	plaintext := []byte(trimmed)
	defer secret.Zero(plaintext)

	ciphertext, nonce, err := m.key.Encrypt(plaintext, secret.AAD(row.ServiceID, migrationURLField))
	if err != nil {
		return fmt.Errorf("sealing database url: %w", err)
	}

	row.URLCiphertext = ciphertext
	row.URLNonce = nonce
	row.URLKeyID = m.key.ID()
	row.Database = target.Label

	return nil
}

// validateMigrationLayout checks the non-secret config fields. Each is
// later used as one argument or one path element, never through a shell.
func validateMigrationLayout(input model.MigrationConfigInput) error {
	if input.Container != "" && !composeServiceRe.MatchString(input.Container) {
		return fmt.Errorf("%w: container must be a compose service name", ErrInvalidInput)
	}

	if input.Workdir != "" && (!path.IsAbs(input.Workdir) || !cleanArg(input.Workdir)) {
		return fmt.Errorf("%w: workdir must be an absolute path", ErrInvalidInput)
	}

	if dir := input.MigrationsDir; dir != "" {
		escapes := path.IsAbs(dir) || path.Clean(dir) != dir || dir == ".." || strings.HasPrefix(dir, "../")
		if escapes || !cleanArg(dir) {
			return fmt.Errorf("%w: migrations_dir must be a clean path relative to workdir", ErrInvalidInput)
		}
	}

	return validateMigrationCommand(input.Command)
}

// validateMigrationCommand checks a stored command prefix.
func validateMigrationCommand(command []string) error {
	if len(command) > migrationCommandMaxArgs {
		return fmt.Errorf("%w: command has more than %d arguments", ErrInvalidInput, migrationCommandMaxArgs)
	}

	for _, arg := range command {
		if arg == "" || !cleanArg(arg) {
			return fmt.Errorf("%w: command arguments must be non-empty printable text", ErrInvalidInput)
		}
	}

	return nil
}

// cleanArg reports whether value is bounded and free of control bytes.
func cleanArg(value string) bool {
	if len(value) > migrationArgMaxLen {
		return false
	}

	return !strings.ContainsFunc(value, func(r rune) bool {
		return r < 0x20 || r == 0x7f
	})
}

// DeleteConfig removes a service's migration setup. Run history stays.
func (m *Migrations) DeleteConfig(ctx context.Context, serviceID, actor string) error {
	if _, err := m.services.Get(ctx, serviceID); err != nil {
		return fmt.Errorf("loading service: %w", err)
	}

	removed, err := m.configs.Delete(ctx, serviceID)
	if err != nil {
		return fmt.Errorf("deleting migration config: %w", err)
	}

	if removed {
		m.record(ctx, serviceID, actor, model.AuditMigrationConfig, model.AuditSuccess, "removed migration setup")
	}

	return nil
}

// Status reads the migration state of a service. A database or a
// container that cannot be read is part of the answer, not an error:
// the view shows which side failed and why.
func (m *Migrations) Status(ctx context.Context, serviceID string) (model.MigrationStatus, error) {
	def, err := m.services.Get(ctx, serviceID)
	if err != nil {
		return model.MigrationStatus{}, fmt.Errorf("loading service: %w", err)
	}

	status := model.MigrationStatus{
		Verdict:    model.MigrationVerdictUnknown,
		Migrations: []model.Migration{},
		CheckedAt:  time.Now().UTC(),
	}

	setup, err := m.load(ctx, serviceID)
	if err != nil {
		if errors.Is(err, ErrMigrationsNotConfigured) {
			return status, nil
		}

		return model.MigrationStatus{}, err
	}

	defer secret.Zero(setup.url)

	status.Configured = true
	status.Tool = setup.tool.Name()

	records, historyProbe := m.readHistory(ctx, setup)
	source, shippedProbe := m.readShipped(ctx, def, setup)

	status.History = historyProbe
	status.Shipped = shippedProbe
	status.Migrations = dbmigrate.Compare(records, source.Migrations, shippedProbe.OK)
	status.Counts = dbmigrate.Count(status.Migrations)
	status.Verdict = dbmigrate.Verdict(status.Counts, historyProbe.OK)
	status.Seed = model.MigrationSeed{Configured: source.SeedCommand != "", Command: source.SeedCommand}

	return status, nil
}

// migrationSetup is a loaded config with its tool and opened URL.
type migrationSetup struct {
	row  store.MigrationConfigRow
	tool dbmigrate.Tool
	url  []byte
}

// load opens a service's migration setup. The caller zeroes url.
func (m *Migrations) load(ctx context.Context, serviceID string) (migrationSetup, error) {
	row, err := m.configs.Get(ctx, serviceID)
	if err != nil {
		if errors.Is(err, store.ErrMigrationConfigNotFound) {
			return migrationSetup{}, ErrMigrationsNotConfigured
		}

		return migrationSetup{}, fmt.Errorf("loading migration config: %w", err)
	}

	tool, err := m.tools.Get(row.Tool)
	if err != nil {
		return migrationSetup{}, fmt.Errorf("loading migration tool: %w", err)
	}

	if m.key == nil {
		return migrationSetup{}, ErrMigrationsDisabled
	}

	plaintext, err := m.key.Decrypt(row.URLCiphertext, row.URLNonce, secret.AAD(serviceID, migrationURLField))
	if err != nil {
		return migrationSetup{}, fmt.Errorf("opening database url: %w", err)
	}

	return migrationSetup{row: row, tool: tool, url: plaintext}, nil
}

// readHistory reads the applied record from the database.
func (m *Migrations) readHistory(ctx context.Context, setup migrationSetup) ([]dbmigrate.Record, model.MigrationProbe) {
	result := model.MigrationProbe{Target: setup.row.Database}

	readCtx, cancel := context.WithTimeout(ctx, m.historyTimeout)
	defer cancel()

	records, err := setup.tool.History(readCtx, string(setup.url))
	if err != nil {
		result.Error = scrubSecret(err.Error(), setup.url)

		return []dbmigrate.Record{}, result
	}

	result.OK = true

	return records, result
}

// readShipped reads the migrations the running container ships.
func (m *Migrations) readShipped(
	ctx context.Context,
	def model.Service,
	setup migrationSetup,
) (dbmigrate.Source, model.MigrationProbe) {
	result := model.MigrationProbe{}

	found, err := m.container(ctx, def, setup.row.Container)
	if err != nil {
		result.Error = bounded(err.Error())

		return dbmigrate.Source{}, result
	}

	workdir, err := m.workdir(ctx, found, setup.row.Workdir)
	if err != nil {
		result.Target = found.Name
		result.Error = bounded(err.Error())

		return dbmigrate.Source{}, result
	}

	layout := setup.tool.Layout()
	if setup.row.MigrationsDir != "" {
		layout.MigrationsDir = setup.row.MigrationsDir
	}

	migrationsPath := path.Join(workdir, layout.MigrationsDir)
	result.Target = found.Name + ":" + migrationsPath

	migrations, err := m.docker.ReadFiles(ctx, found.ID, migrationsPath)
	if err != nil {
		result.Error = bounded(err.Error())

		// The seed is still worth reporting without the migrations.
		source, _ := setup.tool.Source(dbmigrate.Files{}, m.manifests(ctx, found.ID, workdir, layout))

		return dbmigrate.Source{SeedCommand: source.SeedCommand}, result
	}

	source, err := setup.tool.Source(migrations, m.manifests(ctx, found.ID, workdir, layout))
	if err != nil {
		result.Error = bounded(err.Error())

		return dbmigrate.Source{}, result
	}

	result.OK = true

	return source, result
}

// manifests reads the tool's single files. A missing or unreadable one
// is skipped: it only ever adds detail (the declared seed).
func (m *Migrations) manifests(ctx context.Context, containerID, workdir string, layout dbmigrate.Layout) dbmigrate.Files {
	manifests := dbmigrate.Files{}

	for _, name := range layout.Manifests {
		files, err := m.docker.ReadFiles(ctx, containerID, path.Join(workdir, name))
		if err != nil {
			if !errors.Is(err, docker.ErrPathNotFound) {
				m.logger.Warn("reading migration manifest failed", "file", name, "error", err)
			}

			continue
		}

		if content, ok := files[path.Base(name)]; ok {
			manifests[name] = content
		}
	}

	return manifests
}

// workdir returns the directory commands run in and paths resolve from.
func (m *Migrations) workdir(ctx context.Context, found docker.Container, configured string) (string, error) {
	if configured != "" {
		return configured, nil
	}

	workdir, err := m.docker.Workdir(ctx, found.ID)
	if err != nil {
		return "", fmt.Errorf("reading container working directory: %w", err)
	}

	return workdir, nil
}

// container picks the running container that ships the migrations: the
// configured compose service, or the one the deploy strategy names (for
// blue-green, the live color first).
func (m *Migrations) container(ctx context.Context, def model.Service, configured string) (docker.Container, error) {
	candidates, err := migrationCandidates(def, configured)
	if err != nil {
		return docker.Container{}, err
	}

	containers, err := m.docker.List(ctx)
	if err != nil {
		return docker.Container{}, fmt.Errorf("listing containers: %w", err)
	}

	for _, candidate := range candidates {
		for _, found := range containers {
			sameProject := found.Labels[docker.LabelComposeProject] == def.ComposeProject
			if sameProject && found.Labels[docker.LabelComposeService] == candidate {
				return found, nil
			}
		}
	}

	return docker.Container{}, fmt.Errorf("%w: looked for %s in compose project %q",
		ErrMigrationContainer, strings.Join(candidates, ", "), def.ComposeProject)
}

// migrationCandidates lists compose service names to try, best first.
func migrationCandidates(def model.Service, configured string) ([]string, error) {
	if configured != "" {
		return []string{configured}, nil
	}

	switch def.Strategy {
	case model.StrategyRecreate:
		var cfg model.RecreateConfig

		if err := json.Unmarshal(def.Config, &cfg); err != nil {
			return nil, fmt.Errorf("service %q has invalid recreate config: %w", def.ID, err)
		}

		return []string{cfg.Service}, nil
	case model.StrategyBlueGreen:
		cfg, err := decodeBlueGreen(def)
		if err != nil {
			return nil, err
		}

		// The live color runs the build users are on; fall back to
		// whichever color is up when the proxy cannot be read.
		if target, err := probe.LiveTarget(cfg.NginxConf, cfg.Marker); err == nil && target == cfg.GreenTarget {
			return []string{cfg.GreenService, cfg.BlueService}, nil
		}

		return []string{cfg.BlueService, cfg.GreenService}, nil
	case model.StrategyUnknown:
		return nil, fmt.Errorf("service %q has unknown strategy", def.ID)
	default:
		return nil, fmt.Errorf("service %q has unknown strategy %q", def.ID, def.Strategy)
	}
}

// scrubSecret removes a database URL and its password from a message
// that is about to be shown, then bounds it.
func scrubSecret(text string, databaseURL []byte) string {
	return bounded(redact(text, databaseURL))
}

// redact replaces a database URL and its password wherever they occur.
func redact(text string, databaseURL []byte) string {
	raw := string(databaseURL)
	if raw == "" {
		return text
	}

	text = strings.ReplaceAll(text, raw, "[database url]")

	password := urlPassword(raw)
	if len(password) < minScrubLen {
		return text
	}

	for _, form := range []string{password, url.QueryEscape(password), url.PathEscape(password)} {
		text = strings.ReplaceAll(text, form, "[redacted]")
	}

	return text
}

// urlPassword returns the decoded password of a database URL, or "".
// It reads the authority by hand because a URL may list several hosts
// with ports, which net/url refuses to parse.
func urlPassword(raw string) string {
	_, rest, found := strings.Cut(raw, "://")
	if !found {
		return ""
	}

	authority, _, _ := strings.Cut(rest, "/")
	authority, _, _ = strings.Cut(authority, "?")

	at := strings.LastIndex(authority, "@")
	if at < 0 {
		return ""
	}

	_, encoded, found := strings.Cut(authority[:at], ":")
	if !found {
		return ""
	}

	password, err := url.PathUnescape(encoded)
	if err != nil {
		return encoded
	}

	return password
}

// minScrubLen is the shortest password worth replacing in output: a
// shorter one would blank ordinary words.
const minScrubLen = 4

// bounded trims an operator-facing message.
func bounded(text string) string {
	if len(text) > migrationProbeErrorMax {
		return text[:migrationProbeErrorMax] + "…"
	}

	return text
}

// record writes one audit row; a failed write is logged, never fatal.
func (m *Migrations) record(ctx context.Context, serviceID, actor, action, result, detail string) {
	if _, err := m.audit.Record(ctx, model.AuditRecord{
		ServiceID: serviceID, Actor: actor, Action: action, Result: result, Detail: detail,
	}); err != nil {
		m.logger.Error("migration audit failed", "service", serviceID, "action", action, "error", err)
	}
}
