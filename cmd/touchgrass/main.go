// Command touchgrass is the touchgrass control-plane binary.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	docssite "github.com/oralecarlangelo/touchgrass/docs-site"
	"github.com/oralecarlangelo/touchgrass/internal/config"
	"github.com/oralecarlangelo/touchgrass/internal/dbstats"
	"github.com/oralecarlangelo/touchgrass/internal/docker"
	"github.com/oralecarlangelo/touchgrass/internal/http"
	"github.com/oralecarlangelo/touchgrass/internal/model"
	"github.com/oralecarlangelo/touchgrass/internal/nginxlog"
	"github.com/oralecarlangelo/touchgrass/internal/probe"
	"github.com/oralecarlangelo/touchgrass/internal/service"
	"github.com/oralecarlangelo/touchgrass/internal/store"
	"github.com/oralecarlangelo/touchgrass/web"
)

var (
	version = "dev"
	commit  = "none"
)

const usageText = `Usage: touchgrass <command> [args]

Commands:
  serve               run the HTTP server (default when no command is given)
  migrate [up|status] manage the database schema (default: status)
  version             print the version and build SHA
`

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "touchgrass: %v\n", err)
		os.Exit(1)
	}
}

// run dispatches the subcommand and reports usage errors.
func run(args []string) error {
	if len(args) == 0 {
		return runServe(nil)
	}

	switch args[0] {
	case "serve":
		return runServe(args[1:])
	case "migrate":
		return runMigrate(args[1:])
	case "version", "-v", "--version":
		return runVersion(args[1:])
	case "help", "-h", "--help":
		return usage(os.Stdout)
	default:
		return fmt.Errorf("unknown command %q: want serve, migrate, or version", args[0])
	}
}

// backgroundLoops wires sampler, inventory watch, log collection, and
// scheduled backup and SQLite maintenance goroutines.
type backgroundLoops struct {
	sampler             *service.Sampler
	inventory           *service.Inventory
	collector           *service.LogCollector
	databases           *service.Database
	interval            time.Duration
	hub                 *http.Hub
	metrics             time.Duration
	logPoll             time.Duration
	backupInterval      time.Duration
	maintenance         *service.Maintenance
	maintenanceInterval time.Duration
	performance         *service.PerformanceCollector
	runtimeEvents       *service.ContainerEventCollector
	fleetInventory      *service.FleetInventory
	processSecurity     *service.ProcessScanner
	diffSecurity        *service.DiffScanner
}

// startBackground runs sampler, watch, log, and backup loops until ctx
// is cancelled.
func startBackground(
	ctx context.Context,
	loops backgroundLoops,
	group *sync.WaitGroup,
	logger *slog.Logger,
) {
	group.Add(10)

	go func() {
		defer group.Done()

		loops.sampler.Run(ctx)
	}()

	go func() {
		defer group.Done()

		loops.inventory.Watch(ctx, loops.interval, loops.hub.Broadcast)
	}()

	go func() {
		defer group.Done()

		loops.collector.Run(ctx)
	}()

	go func() {
		defer group.Done()

		loops.databases.RunScheduler(ctx, loops.backupInterval)
	}()

	go func() {
		defer group.Done()

		loops.maintenance.Run(ctx)
	}()

	go func() {
		defer group.Done()

		loops.performance.Run(ctx)
	}()

	go func() {
		defer group.Done()

		loops.runtimeEvents.Run(ctx)
	}()

	go func() {
		defer group.Done()

		loops.fleetInventory.Run(ctx)
	}()

	go func() {
		defer group.Done()

		loops.processSecurity.Run(ctx)
	}()

	go func() {
		defer group.Done()

		loops.diffSecurity.Run(ctx)
	}()

	logger.Info("starting background loops",
		"metrics_interval", loops.metrics.String(),
		"watch_interval", loops.interval.String(),
		"log_poll_interval", loops.logPoll.String(),
		"backup_interval", loops.backupInterval.String(),
		"maintenance_interval", loops.maintenanceInterval.String(),
	)
}

// serveServices wires HTTP handlers to domain services.
type serveServices struct {
	inventory   *service.Inventory
	sampler     *service.Sampler
	deploys     *service.Deploys
	cutover     *service.Cutover
	audit       *service.Audit
	auth        *http.Authenticator
	users       *service.Users
	events      *http.Hub
	ingestor    *service.Ingestor
	logs        *service.LogCollector
	sdkLogs     *service.LogIngestor
	system      *service.System
	database    *service.Database
	onboarding  *service.Onboarding
	performance *service.Performance
	fleet       *service.FleetInventory
	security    *service.Security
}

// newServeServer builds the HTTP server for the embedded UI or dev proxy.
func newServeServer(
	cfg config.Config,
	logger *slog.Logger,
	services serveServices,
	version string,
) (*http.Server, error) {
	dist, err := fs.Sub(web.Dist, "dist")
	if err != nil {
		return nil, fmt.Errorf("resolving embedded UI: %w", err)
	}

	docs, err := fs.Sub(docssite.Dist, "dist")
	if err != nil {
		return nil, fmt.Errorf("resolving embedded docs: %w", err)
	}

	devProxy := ""
	if cfg.Env == "dev" {
		devProxy = http.DefaultDevProxy
	}

	return http.New(http.Config{
		Addr:           cfg.Addr,
		Version:        version,
		Logger:         logger,
		Inventory:      services.inventory,
		Sampler:        services.sampler,
		Deploys:        services.deploys,
		Cutover:        services.cutover,
		Audit:          services.audit,
		Auth:           services.auth,
		Users:          services.users,
		Events:         services.events,
		Ingestor:       services.ingestor,
		Logs:           services.logs,
		SDKLogs:        services.sdkLogs,
		System:         services.system,
		Database:       services.database,
		Onboarding:     services.onboarding,
		Performance:    services.performance,
		FleetInventory: services.fleet,
		Security:       services.security,
		Dist:           dist,
		Docs:           docs,
		DevProxy:       devProxy,
	}), nil
}

// runServe starts the HTTP server until it is signalled to stop.
func runServe(args []string) error {
	if err := parseNoArgs("serve", args); err != nil {
		return err
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	db, err := store.Open(cfg.DB)
	if err != nil {
		return err
	}

	dockerClient, err := docker.New()
	if err != nil {
		_ = db.Close()

		return err
	}

	defer func() {
		if err := dockerClient.Close(); err != nil {
			logger.Warn("closing docker client", "error", err)
		}

		if err := db.Close(); err != nil {
			logger.Warn("closing database", "error", err)
		}
	}()

	if err := applyMigrations(ctx, db, logger); err != nil {
		return err
	}

	prober := probe.New()
	serviceStore := store.NewServiceStore(db)
	notificationStore := store.NewNotificationStore(db)
	deployStore := store.NewDeployStore(db)
	inv := service.NewInventory(serviceStore, dockerClient, prober, logger)

	wiring := serveWiring{
		services: serviceStore, docker: dockerClient, db: db, cfg: cfg, logger: logger,
	}
	performance := service.NewPerformance(service.PerformanceConfig{
		Services: serviceStore,
		Store:    store.NewPerformanceStore(db),
		Metrics:  store.NewMetricStore(db),
		Deploys:  deployStore,
	})
	sampler := newSampler(wiring, performance)
	deploys := service.NewDeploys(serviceStore, deployStore)
	audit := service.NewAudit(serviceStore, store.NewAuditStore(db))
	ingestor := newIngestor(serviceStore, db, cfg.MaxOccurrences, logger)
	collector := newLogCollector(wiring)
	sdkLogs := newLogIngestor(serviceStore, db)
	sys := newServeSystem(dockerClient, cfg.DB, cfg.HostRoot, logger)
	databases := newDatabase(db, cfg, logger)
	maintenance := newMaintenance(db, cfg, logger)
	performanceCollector, err := newPerformanceCollector(ctx, wiring)
	if err != nil {
		return err
	}
	reconcileDatabases(databases, logger)

	onboarding := newOnboarding(wiring, prober, audit)

	managedUsers, err := ensureUsers(ctx, db, audit, cfg.AdminPassword, logger)
	if err != nil {
		return err
	}

	auth := http.NewAuthenticator(managedUsers, cfg.CookieSecure)
	hub := http.NewHub(logger)
	securityStore := store.NewSecurityStore(db)
	securityService := service.NewSecurity(service.SecurityConfig{
		Store: securityStore, Notifications: notificationStore, Audit: audit,
		Cursors: store.NewCursorStore(db), Emit: hub.Broadcast, Logger: logger,
	})
	cutover := service.NewCutover(service.CutoverConfig{
		Services:      serviceStore,
		Docker:        dockerClient,
		Deploys:       deploys,
		Probes:        store.NewDeployStore(db),
		Notifications: notificationStore,
		Audit:         audit,
		Prober:        prober,
		Emit:          hub.Broadcast,
		Timeout:       cfg.CutoverTimeout,
		Logger:        logger,
	})
	inv.SetFlipReporter(notificationStore, audit, cutover.Active)
	fleetInventory := service.NewFleetInventory(service.FleetInventoryConfig{
		Services: serviceStore, Containers: store.NewContainerStore(db), Docker: dockerClient,
		Interval: cfg.InventoryInterval, InspectLimit: cfg.InventoryWorkers, Logger: logger,
		Security: securityStore, Evaluator: securityService, Audit: audit,
	})
	processSecurity := service.NewProcessScanner(service.ProcessScannerConfig{
		Inventory: fleetInventory, Docker: dockerClient, Security: securityService,
		Policies: securityStore, Interval: cfg.SecurityProcessInterval,
		Workers: cfg.SecurityWorkers, Logger: logger,
	})
	diffSecurity := service.NewDiffScanner(service.DiffScannerConfig{
		Inventory: fleetInventory, Docker: dockerClient, Security: securityService,
		Policies: securityStore, Interval: cfg.SecurityDiffInterval,
		Workers: cfg.SecurityWorkers, Logger: logger,
	})
	runtimeEvents := service.NewContainerEventCollector(service.ContainerEventCollectorConfig{
		Services: serviceStore, Docker: dockerClient, Writer: store.NewPerformanceStore(db),
		Environment: cfg.Env, Expected: cutover.Active, Logger: logger,
		RequestReconcile: fleetInventory.RequestReconcile,
	})

	var group sync.WaitGroup

	startBackground(ctx, backgroundLoops{
		sampler:             sampler,
		inventory:           inv,
		collector:           collector,
		databases:           databases,
		interval:            cfg.WatchInterval,
		hub:                 hub,
		metrics:             cfg.MetricsInterval,
		logPoll:             cfg.LogPollInterval,
		backupInterval:      cfg.BackupInterval,
		maintenance:         maintenance,
		maintenanceInterval: cfg.MaintenanceInterval,
		performance:         performanceCollector,
		runtimeEvents:       runtimeEvents,
		fleetInventory:      fleetInventory,
		processSecurity:     processSecurity,
		diffSecurity:        diffSecurity,
	}, &group, logger)

	server, err := newServeServer(cfg, logger, serveServices{
		inventory: inv, sampler: sampler, deploys: deploys,
		cutover: cutover, audit: audit, auth: auth, users: managedUsers, events: hub,
		ingestor: ingestor, logs: collector, sdkLogs: sdkLogs, system: sys,
		database: databases, onboarding: onboarding, performance: performance,
		fleet:    fleetInventory,
		security: securityService,
	}, version)
	if err != nil {
		return err
	}

	runErr := server.Run(ctx)

	stop()
	group.Wait()

	if runErr != nil {
		return fmt.Errorf("serving: %w", runErr)
	}

	return nil
}

// ensureUsers wires managed accounts and guarantees a usable admin.
func ensureUsers(
	ctx context.Context,
	db *store.DB,
	audit *service.Audit,
	adminPassword string,
	logger *slog.Logger,
) (*service.Users, error) {
	users := service.NewUsers(store.NewUserStore(db), audit)

	seeded, err := users.EnsureAdmin(ctx, adminPassword)
	if err != nil {
		return nil, fmt.Errorf("ensuring admin account: %w", err)
	}

	if seeded {
		logger.Warn("no enabled admin found; seeded admin from TOUCHGRASS_ADMIN_PASSWORD")
	}

	return users, nil
}

// applyMigrations brings the serve database current, logging what applied.
func applyMigrations(ctx context.Context, db *store.DB, logger *slog.Logger) error {
	applied, err := db.MigrateUp(ctx)
	if err != nil {
		return err
	}

	if len(applied) > 0 {
		logger.Info("applied migrations", "versions", strings.Join(applied, ","))
	}

	return nil
}

// serveWiring bundles the shared deps sampler and collector construction need.
type serveWiring struct {
	services *store.ServiceStore
	docker   *docker.Client
	db       *store.DB
	cfg      config.Config
	logger   *slog.Logger
}

// newOnboarding wires the suggest + create service.
func newOnboarding(wiring serveWiring, prober *probe.Prober, audit *service.Audit) *service.Onboarding {
	return service.NewOnboarding(service.OnboardingConfig{
		Services:   wiring.services,
		Docker:     wiring.docker,
		Prober:     prober,
		Audit:      audit,
		ScriptsDir: wiring.cfg.ScriptsDir,
	})
}

// newSampler wires metrics sampling, alerting, and retention.
func newSampler(wiring serveWiring, performance *service.Performance) *service.Sampler {
	return service.NewSampler(service.SamplerConfig{
		Services:      wiring.services,
		Docker:        wiring.docker,
		Metrics:       store.NewMetricStore(wiring.db),
		Rules:         store.NewRuleStore(wiring.db),
		Notifications: store.NewNotificationStore(wiring.db),
		Deploys:       store.NewDeployStore(wiring.db),
		Occurrences:   store.NewOccurrenceStore(wiring.db),
		Issues:        store.NewIssueStore(wiring.db),
		IssueRules:    store.NewIssueRuleStore(wiring.db),
		Logs:          store.NewLogStore(wiring.db),
		SDKLogs:       store.NewSDKLogStore(wiring.db),
		Fleet:         store.NewFleetStore(wiring.db),
		Performance:   performance,
		Interval:      wiring.cfg.MetricsInterval,
		HostRoot:      wiring.cfg.HostRoot,
		Retention: service.Retention{
			Metrics:       wiring.cfg.RetentionMetrics,
			Notifications: wiring.cfg.RetentionNotifications,
			Deploys:       wiring.cfg.RetentionDeploys,
			Errors:        wiring.cfg.RetentionErrors,
			Logs:          wiring.cfg.RetentionLogs,
			SDKLogs:       wiring.cfg.RetentionSDKLogs,
		},
		Logger: wiring.logger,
	})
}

// newLogCollector wires container log tailing.
func newLogCollector(wiring serveWiring) *service.LogCollector {
	return service.NewLogCollector(service.LogCollectorConfig{
		Services:           wiring.services,
		Docker:             wiring.docker,
		Logs:               store.NewLogStore(wiring.db),
		Cursors:            store.NewCursorStore(wiring.db),
		Performance:        store.NewPerformanceStore(wiring.db),
		Environment:        wiring.cfg.Env,
		Interval:           wiring.cfg.LogPollInterval,
		MaxLinesPerService: wiring.cfg.MaxLogLines,
		Logger:             wiring.logger,
	})
}

// newDatabase wires postgres health, backup, and restore jobs.
func newDatabase(db *store.DB, cfg config.Config, logger *slog.Logger) *service.Database {
	return service.NewDatabase(service.DatabaseConfig{
		Container:      cfg.PostgresContainer,
		User:           cfg.PostgresUser,
		DBName:         cfg.PostgresDB,
		BackupDir:      cfg.BackupDir,
		BackupKeep:     cfg.BackupKeep,
		RedisContainer: cfg.RedisContainer,
		DBPath:         cfg.DB,
		Jobs:           store.NewDBJobStore(db),
		Audit:          store.NewAuditStore(db),
		SQLite:         db,
		Logger:         logger,
	})
}

// newMaintenance wires bounded SQLite retention, checkpoints, and snapshots.
func newMaintenance(db *store.DB, cfg config.Config, logger *slog.Logger) *service.Maintenance {
	return service.NewMaintenance(service.MaintenanceConfig{
		Store: store.NewMaintenanceStore(db), Backupper: db,
		Interval: cfg.MaintenanceInterval, BackupDir: cfg.SQLiteBackupDir, BackupKeep: cfg.SQLiteBackupKeep,
		Policy: model.MaintenancePolicy{
			BatchSize: model.MaximumMaintenanceBatch, MaxBytes: cfg.SQLiteMaxBytes,
			RetentionMetrics:       cfg.RetentionMetrics,
			RetentionNotifications: cfg.RetentionNotifications,
			RetentionDeploys:       cfg.RetentionDeploys, RetentionErrors: cfg.RetentionErrors,
			RetentionLogs: cfg.RetentionLogs, RetentionSDKLogs: cfg.RetentionSDKLogs,
			RetentionPerformance:          cfg.RetentionPerformance,
			RetentionPerformanceHourly:    cfg.RetentionPerformanceHourly,
			RetentionSecurityObservations: cfg.RetentionSecurityObservations,
			RetentionContainerEvents:      cfg.RetentionContainerEvents,
			RetentionSecurityFindings:     cfg.RetentionSecurityFindings,
		},
		Logger: logger,
	})
}

// newPerformanceCollector validates service ownership and wires enabled
// privacy-safe native sources. Empty source configuration yields a no-op loop.
func newPerformanceCollector(ctx context.Context, wiring serveWiring) (*service.PerformanceCollector, error) {
	performance := store.NewPerformanceStore(wiring.db)
	config := service.PerformanceCollectorConfig{
		ServiceID: wiring.cfg.PerformanceServiceID, Environment: wiring.cfg.Env,
		Interval: wiring.cfg.PerformanceInterval, Writer: performance, Logger: wiring.logger,
	}
	if wiring.cfg.PerformanceServiceID == "" {
		return service.NewPerformanceCollector(config), nil
	}
	if _, err := wiring.services.Get(ctx, wiring.cfg.PerformanceServiceID); err != nil {
		return nil, fmt.Errorf("validating performance service %q: %w", wiring.cfg.PerformanceServiceID, err)
	}
	runner := dbstats.NewCommandRunner()
	if wiring.cfg.PostgresContainer != "" {
		config.Postgres = dbstats.NewPostgres(dbstats.PostgresConfig{
			Runner: runner, Container: wiring.cfg.PostgresContainer,
			User: wiring.cfg.PostgresUser, Database: wiring.cfg.PostgresDB,
		})
	}
	if wiring.cfg.RedisContainer != "" {
		config.Redis = dbstats.NewRedis(dbstats.RedisConfig{Runner: runner, Container: wiring.cfg.RedisContainer})
	}
	if wiring.cfg.NginxTimingLog != "" {
		cursor, err := performance.Get(ctx, "nginx")
		if err != nil && !errors.Is(err, store.ErrCollectorCursorNotFound) {
			return nil, fmt.Errorf("loading nginx collector cursor: %w", err)
		}
		config.HTTP = nginxlog.NewTailer(wiring.cfg.NginxTimingLog, nginxlog.Parser{}, "nginx", cursor)
	}
	return service.NewPerformanceCollector(config), nil
}

// reconcileDatabases fails jobs orphaned by a restart. Best effort: a
// reconcile error must never fail boot.
func reconcileDatabases(databases *service.Database, logger *slog.Logger) {
	if err := databases.Reconcile(context.Background()); err != nil {
		logger.Warn("database job reconcile failed", "error", err)
	}
}

// newServeSystem wires host telemetry.
func newServeSystem(
	daemon service.DaemonQuerier,
	dbPath string,
	hostRoot string,
	logger *slog.Logger,
) *service.System {
	return service.NewSystem(service.SystemConfig{
		Docker:    daemon,
		Version:   version,
		DBPath:    dbPath,
		HostRoot:  hostRoot,
		StartedAt: time.Now(),
		Logger:    logger,
	})
}

// newLogIngestor wires structured-log ingestion on the shared key store.
func newLogIngestor(serviceStore *store.ServiceStore, db *store.DB) *service.LogIngestor {
	return service.NewLogIngestor(service.LogIngestorConfig{
		Services: serviceStore,
		Keys:     store.NewKeyStore(db),
		SDKLogs:  store.NewSDKLogStore(db),
	})
}

// newIngestor wires error-ingestion services.
func newIngestor(
	serviceStore *store.ServiceStore,
	db *store.DB,
	maxOccurrences int,
	logger *slog.Logger,
) *service.Ingestor {
	return service.NewIngestor(service.IngestorConfig{
		Services:       serviceStore,
		Keys:           store.NewKeyStore(db),
		Occurrences:    store.NewOccurrenceStore(db),
		Issues:         store.NewIssueStore(db),
		IssueRules:     store.NewIssueRuleStore(db),
		Logs:           store.NewLogStore(db),
		MaxOccurrences: maxOccurrences,
		Logger:         logger,
	})
}

// runMigrate applies pending schema migrations or reports status.
func runMigrate(args []string) error {
	fs := flag.NewFlagSet("migrate", flag.ContinueOnError)

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}

		return fmt.Errorf("parsing migrate flags: %w", err)
	}

	if fs.NArg() > 1 {
		return errors.New("migrate takes at most one argument: up or status")
	}

	action := "status"
	if fs.NArg() == 1 {
		action = fs.Arg(0)
	}

	switch action {
	case "up":
		return migrateUp()
	case "status":
		return migrateStatus()
	default:
		return fmt.Errorf("unknown migrate action %q: want up or status", action)
	}
}

// migrateUp applies pending migrations.
func migrateUp() error {
	db, err := openMigrateDB()
	if err != nil {
		return err
	}

	defer func() {
		if err := db.Close(); err != nil {
			fmt.Fprintf(os.Stderr, "touchgrass: closing database: %v\n", err)
		}
	}()

	applied, err := db.MigrateUp(context.Background())
	if err != nil {
		return err
	}

	if len(applied) == 0 {
		return outputLine("database is current")
	}

	return outputLine("applied migrations: " + strings.Join(applied, ", "))
}

// migrateStatus reports applied and pending migrations.
func migrateStatus() error {
	db, err := openMigrateDB()
	if err != nil {
		return err
	}

	defer func() {
		if err := db.Close(); err != nil {
			fmt.Fprintf(os.Stderr, "touchgrass: closing database: %v\n", err)
		}
	}()

	applied, pending, err := db.MigrationStatus(context.Background())
	if err != nil {
		return err
	}

	if err := outputLine("applied: " + joinOrNone(applied)); err != nil {
		return err
	}

	return outputLine("pending: " + joinOrNone(pending))
}

// openMigrateDB opens the configured database for migration commands.
func openMigrateDB() (*store.DB, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}

	db, err := store.Open(cfg.DB)
	if err != nil {
		return nil, err
	}

	return db, nil
}

// joinOrNone renders a version list for humans.
func joinOrNone(versions []string) string {
	if len(versions) == 0 {
		return "(none)"
	}

	return strings.Join(versions, ", ")
}

// outputLine prints one stdout line.
func outputLine(line string) error {
	if _, err := fmt.Fprintln(os.Stdout, line); err != nil {
		return fmt.Errorf("writing output: %w", err)
	}

	return nil
}

// runVersion prints the binary version and build SHA.
func runVersion(args []string) error {
	if err := parseNoArgs("version", args); err != nil {
		return err
	}

	if _, err := fmt.Fprintf(os.Stdout, "touchgrass %s (commit %s)\n", version, commit); err != nil {
		return fmt.Errorf("writing version: %w", err)
	}

	return nil
}

// parseNoArgs parses a flag set that accepts no flags or arguments.
func parseNoArgs(name string, args []string) error {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}

		return fmt.Errorf("parsing %s flags: %w", name, err)
	}

	if fs.NArg() > 0 {
		return fmt.Errorf("%s takes no arguments", name)
	}

	return nil
}

// usage prints command help to w.
func usage(w io.Writer) error {
	if _, err := io.WriteString(w, usageText); err != nil {
		return fmt.Errorf("writing usage: %w", err)
	}

	return nil
}
