// Package config parses touchgrass environment configuration.
package config

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	defaultAddr                      = "127.0.0.1:8080"
	defaultDB                        = "./touchgrass.db"
	defaultEnv                       = "prod"
	defaultMetricsInterval           = "30s"
	defaultRetentionMetrics          = "168h"
	defaultRetentionNotifications    = "720h"
	defaultRetentionDeploys          = "8760h"
	defaultWatchInterval             = "5s"
	defaultCutoverTimeout            = "10m"
	defaultRetentionErrors           = "720h"
	defaultMaxOccurrences            = "10000"
	defaultRetentionLogs             = "168h"
	defaultMaxLogLines               = "50000"
	defaultLogPollInterval           = "5s"
	defaultRetentionSDKLogs          = "7"
	defaultPostgresUser              = "postgres"
	defaultPostgresDB                = "postgres"
	defaultBackupDir                 = "./backups"
	defaultBackupKeep                = "14"
	defaultBackupInterval            = "24h"
	defaultScriptsDir                = "scripts"
	defaultMaintenanceInterval       = "5m"
	defaultSQLiteMaxBytes            = "1073741824"
	defaultSQLiteBackupDir           = "./backups/sqlite"
	defaultRetentionPerformance      = "168h"
	defaultRetentionPerfHourly       = "720h"
	defaultPerformanceInterval       = "30s"
	addrEnvVar                       = "TOUCHGRASS_ADDR"
	dbEnvVar                         = "TOUCHGRASS_DB"
	envEnvVar                        = "APP_ENV"
	metricsIntervalEnvVar            = "TOUCHGRASS_METRICS_INTERVAL"
	retentionMetricsEnvVar           = "TOUCHGRASS_RETENTION_METRICS"
	retentionNotificationsEnvVar     = "TOUCHGRASS_RETENTION_NOTIFICATIONS"
	retentionDeploysEnvVar           = "TOUCHGRASS_RETENTION_DEPLOYS"
	adminPasswordEnvVar              = "TOUCHGRASS_ADMIN_PASSWORD"
	cookieSecureEnvVar               = "TOUCHGRASS_COOKIE_SECURE"
	watchIntervalEnvVar              = "TOUCHGRASS_WATCH_INTERVAL"
	cutoverTimeoutEnvVar             = "TOUCHGRASS_CUTOVER_TIMEOUT"
	retentionErrorsEnvVar            = "TOUCHGRASS_RETENTION_ERRORS"
	maxOccurrencesEnvVar             = "TOUCHGRASS_INGEST_MAX_OCCURRENCES"
	retentionLogsEnvVar              = "TOUCHGRASS_RETENTION_LOGS"
	maxLogLinesEnvVar                = "TOUCHGRASS_LOGS_MAX_LINES"
	logPollIntervalEnvVar            = "TOUCHGRASS_LOG_POLL_INTERVAL"
	retentionSDKLogsEnvVar           = "TOUCHGRASS_RETENTION_SDK_LOGS"
	postgresContainerEnvVar          = "TOUCHGRASS_POSTGRES_CONTAINER"
	postgresUserEnvVar               = "TOUCHGRASS_POSTGRES_USER"
	postgresDBEnvVar                 = "TOUCHGRASS_POSTGRES_DB"
	backupDirEnvVar                  = "TOUCHGRASS_DB_BACKUP_DIR"
	backupKeepEnvVar                 = "TOUCHGRASS_DB_BACKUP_KEEP"
	backupIntervalEnvVar             = "TOUCHGRASS_DB_BACKUP_INTERVAL"
	redisContainerEnvVar             = "TOUCHGRASS_REDIS_CONTAINER"
	scriptsDirEnvVar                 = "TOUCHGRASS_SCRIPTS_DIR"
	maintenanceIntervalEnvVar        = "TOUCHGRASS_MAINTENANCE_INTERVAL"
	sqliteMaxBytesEnvVar             = "TOUCHGRASS_SQLITE_MAX_BYTES"
	sqliteBackupDirEnvVar            = "TOUCHGRASS_SQLITE_BACKUP_DIR"
	retentionPerformanceEnvVar       = "TOUCHGRASS_RETENTION_PERFORMANCE"
	retentionPerformanceHourlyEnvVar = "TOUCHGRASS_RETENTION_PERFORMANCE_HOURLY"
	nginxTimingLogEnvVar             = "TOUCHGRASS_NGINX_TIMING_LOG"
	performanceServiceIDEnvVar       = "TOUCHGRASS_PERFORMANCE_SERVICE_ID"
	performanceIntervalEnvVar        = "TOUCHGRASS_PERFORMANCE_INTERVAL"
	envDev                           = "dev"
	envProd                          = "prod"
)

// Config is the validated touchgrass runtime configuration.
type Config struct {
	// Addr is the TCP address the HTTP server listens on.
	Addr string
	// DB is the SQLite file path, or :memory: for tests.
	DB string
	// Env is dev (Vite proxy) or prod (embedded UI).
	Env string
	// MetricsInterval paces metric sampling.
	MetricsInterval time.Duration
	// Retention bounds per-area max age.
	RetentionMetrics       time.Duration
	RetentionNotifications time.Duration
	RetentionDeploys       time.Duration
	// AdminPassword guards every API route (required).
	AdminPassword string
	// CookieSecure sets the Secure cookie flag (behind TLS).
	CookieSecure bool
	// WatchInterval paces nginx target polling.
	WatchInterval time.Duration
	// CutoverTimeout bounds one cutover run.
	CutoverTimeout time.Duration
	// RetentionErrors bounds occurrence max age.
	RetentionErrors time.Duration
	// MaxOccurrences caps stored occurrences per service at write time.
	MaxOccurrences int
	// RetentionLogs bounds log line max age.
	RetentionLogs time.Duration
	// RetentionSDKLogs bounds SDK log row max age.
	RetentionSDKLogs time.Duration
	// MaxLogLines caps stored log lines per service, newest kept.
	MaxLogLines int
	// LogPollInterval paces container log polling.
	LogPollInterval time.Duration
	// PostgresContainer is the postgres container name; empty disables postgres.
	PostgresContainer string
	// PostgresUser is the local-trust postgres role for health and jobs.
	PostgresUser string
	// PostgresDB is the database health checks and backups target.
	PostgresDB string
	// BackupDir holds pg_dump artifacts plus sha256 sidecars.
	BackupDir string
	// BackupKeep bounds newest backups retained after each backup.
	BackupKeep int
	// BackupInterval paces scheduled backups; zero disables them.
	BackupInterval time.Duration
	// RedisContainer is the redis container name; empty hides redis.
	RedisContainer string
	// ScriptsDir holds the deploy helper scripts; relative paths resolve
	// against the app working directory.
	ScriptsDir string
	// MaintenanceInterval paces bounded SQLite retention and checkpoint work.
	MaintenanceInterval time.Duration
	// SQLiteMaxBytes is the total SQLite storage budget.
	SQLiteMaxBytes int64
	// SQLiteBackupDir contains online SQLite snapshots.
	SQLiteBackupDir string
	// RetentionPerformance bounds minute and hourly aggregate history.
	RetentionPerformance       time.Duration
	RetentionPerformanceHourly time.Duration
	// NginxTimingLog is an absolute path to the dedicated privacy-safe JSONL
	// source. Empty disables only the Nginx collector.
	NginxTimingLog string
	// PerformanceServiceID attaches native source aggregates to one known
	// service. Runtime wiring verifies that the service exists.
	PerformanceServiceID string
	// PerformanceInterval paces native source sampling.
	PerformanceInterval time.Duration
}

// Load reads configuration from the environment.
func Load() (Config, error) {
	return load(os.Getenv)
}

// listenConfig groups validated startup locations.
type listenConfig struct {
	addr       string
	db         string
	env        string
	scriptsDir string
}

// scheduleConfig groups validated durations.
type scheduleConfig struct {
	metrics                time.Duration
	retentionMetrics       time.Duration
	retentionNotifications time.Duration
	retentionDeploys       time.Duration
	retentionErrors        time.Duration
	watch                  time.Duration
	cutoverTimeout         time.Duration
	retentionLogs          time.Duration
	retentionSDKLogs       time.Duration
	logPoll                time.Duration
}

// ingestConfig groups validated ingestion caps.
type ingestConfig struct {
	maxOccurrences int
	maxLogLines    int
}

// adminConfig groups validated admin authentication settings.
type adminConfig struct {
	password string
	secure   bool
}

// databaseConfig groups validated database health and backup settings.
type databaseConfig struct {
	container      string
	user           string
	dbName         string
	backupDir      string
	backupKeep     int
	backupInterval time.Duration
	redisContainer string
}

// storageConfig groups SQLite maintenance and performance retention settings.
type storageConfig struct {
	maintenanceInterval        time.Duration
	maxBytes                   int64
	backupDir                  string
	retentionPerformance       time.Duration
	retentionPerformanceHourly time.Duration
}

type performanceConfig struct {
	nginxTimingLog string
	serviceID      string
	interval       time.Duration
}

// load resolves configuration using getenv so tests can stub the environment.
func load(getenv func(string) string) (Config, error) {
	listen, err := loadListen(getenv)
	if err != nil {
		return Config{}, err
	}

	schedule, err := loadSchedule(getenv)
	if err != nil {
		return Config{}, err
	}

	admin, err := loadAdmin(getenv)
	if err != nil {
		return Config{}, err
	}

	ingest, err := loadIngest(getenv)
	if err != nil {
		return Config{}, err
	}

	database, err := loadDatabase(getenv)
	if err != nil {
		return Config{}, err
	}

	storage, err := loadStorage(getenv)
	if err != nil {
		return Config{}, err
	}

	performance, err := loadPerformance(getenv, database)
	if err != nil {
		return Config{}, err
	}

	return Config{
		Addr:                       listen.addr,
		DB:                         listen.db,
		Env:                        listen.env,
		MetricsInterval:            schedule.metrics,
		RetentionMetrics:           schedule.retentionMetrics,
		RetentionNotifications:     schedule.retentionNotifications,
		RetentionDeploys:           schedule.retentionDeploys,
		AdminPassword:              admin.password,
		CookieSecure:               admin.secure,
		WatchInterval:              schedule.watch,
		CutoverTimeout:             schedule.cutoverTimeout,
		RetentionErrors:            schedule.retentionErrors,
		MaxOccurrences:             ingest.maxOccurrences,
		RetentionLogs:              schedule.retentionLogs,
		RetentionSDKLogs:           schedule.retentionSDKLogs,
		MaxLogLines:                ingest.maxLogLines,
		LogPollInterval:            schedule.logPoll,
		PostgresContainer:          database.container,
		PostgresUser:               database.user,
		PostgresDB:                 database.dbName,
		BackupDir:                  database.backupDir,
		BackupKeep:                 database.backupKeep,
		BackupInterval:             database.backupInterval,
		RedisContainer:             database.redisContainer,
		ScriptsDir:                 listen.scriptsDir,
		MaintenanceInterval:        storage.maintenanceInterval,
		SQLiteMaxBytes:             storage.maxBytes,
		SQLiteBackupDir:            storage.backupDir,
		RetentionPerformance:       storage.retentionPerformance,
		RetentionPerformanceHourly: storage.retentionPerformanceHourly,
		NginxTimingLog:             performance.nginxTimingLog,
		PerformanceServiceID:       performance.serviceID,
		PerformanceInterval:        performance.interval,
	}, nil
}

func loadPerformance(getenv func(string) string, database databaseConfig) (performanceConfig, error) {
	interval, err := parseDuration(getenv, performanceIntervalEnvVar, defaultPerformanceInterval)
	if err != nil {
		return performanceConfig{}, err
	}

	path := getenv(nginxTimingLogEnvVar)
	if path != "" && !filepath.IsAbs(path) {
		return performanceConfig{}, fmt.Errorf("invalid %s %q: want absolute path", nginxTimingLogEnvVar, path)
	}
	serviceID := strings.TrimSpace(getenv(performanceServiceIDEnvVar))
	if (path != "" || database.container != "" || database.redisContainer != "") && serviceID == "" {
		return performanceConfig{}, fmt.Errorf("%s is required when a performance source is enabled", performanceServiceIDEnvVar)
	}

	return performanceConfig{nginxTimingLog: path, serviceID: serviceID, interval: interval}, nil
}

// loadStorage validates bounded SQLite maintenance settings.
func loadStorage(getenv func(string) string) (storageConfig, error) {
	interval, err := parseDuration(getenv, maintenanceIntervalEnvVar, defaultMaintenanceInterval)
	if err != nil {
		return storageConfig{}, err
	}

	maxBytes, err := parsePositiveInt64(getenv, sqliteMaxBytesEnvVar, defaultSQLiteMaxBytes)
	if err != nil {
		return storageConfig{}, err
	}

	retentionPerformance, err := parseDuration(getenv, retentionPerformanceEnvVar, defaultRetentionPerformance)
	if err != nil {
		return storageConfig{}, err
	}

	retentionHourly, err := parseDuration(getenv, retentionPerformanceHourlyEnvVar, defaultRetentionPerfHourly)
	if err != nil {
		return storageConfig{}, err
	}

	backupDir := getenv(sqliteBackupDirEnvVar)
	if backupDir == "" {
		backupDir = defaultSQLiteBackupDir
	}

	return storageConfig{
		maintenanceInterval: interval, maxBytes: maxBytes, backupDir: backupDir,
		retentionPerformance: retentionPerformance, retentionPerformanceHourly: retentionHourly,
	}, nil
}

// loadListen validates the listen address, database path, and environment.
func loadListen(getenv func(string) string) (listenConfig, error) {
	addr := getenv(addrEnvVar)
	if addr == "" {
		addr = defaultAddr
	}

	if _, _, err := net.SplitHostPort(addr); err != nil {
		return listenConfig{}, fmt.Errorf("invalid %s %q: %w", addrEnvVar, addr, err)
	}

	db := getenv(dbEnvVar)
	if db == "" {
		db = defaultDB
	}

	env := getenv(envEnvVar)
	if env == "" {
		env = defaultEnv
	}

	if env != envDev && env != envProd {
		return listenConfig{}, fmt.Errorf("invalid %s %q: want dev or prod", envEnvVar, env)
	}

	scriptsDir := getenv(scriptsDirEnvVar)
	if scriptsDir == "" {
		scriptsDir = defaultScriptsDir
	}

	return listenConfig{addr: addr, db: db, env: env, scriptsDir: scriptsDir}, nil
}

// loadSchedule validates sampling, retention, watch, and cutover durations.
func loadSchedule(getenv func(string) string) (scheduleConfig, error) {
	metrics, err := parseDuration(getenv, metricsIntervalEnvVar, defaultMetricsInterval)
	if err != nil {
		return scheduleConfig{}, err
	}

	retentionMetrics, err := parseDuration(getenv, retentionMetricsEnvVar, defaultRetentionMetrics)
	if err != nil {
		return scheduleConfig{}, err
	}

	retentionNotifications, err := parseDuration(getenv, retentionNotificationsEnvVar, defaultRetentionNotifications)
	if err != nil {
		return scheduleConfig{}, err
	}

	retentionDeploys, err := parseDuration(getenv, retentionDeploysEnvVar, defaultRetentionDeploys)
	if err != nil {
		return scheduleConfig{}, err
	}

	watch, err := parseDuration(getenv, watchIntervalEnvVar, defaultWatchInterval)
	if err != nil {
		return scheduleConfig{}, err
	}

	cutoverTimeout, err := parseDuration(getenv, cutoverTimeoutEnvVar, defaultCutoverTimeout)
	if err != nil {
		return scheduleConfig{}, err
	}

	retentionErrors, err := parseDuration(getenv, retentionErrorsEnvVar, defaultRetentionErrors)
	if err != nil {
		return scheduleConfig{}, err
	}

	retentionLogs, err := parseDuration(getenv, retentionLogsEnvVar, defaultRetentionLogs)
	if err != nil {
		return scheduleConfig{}, err
	}

	retentionSDKLogs, err := parseRetentionDays(getenv, retentionSDKLogsEnvVar, defaultRetentionSDKLogs)
	if err != nil {
		return scheduleConfig{}, err
	}

	logPoll, err := parseDuration(getenv, logPollIntervalEnvVar, defaultLogPollInterval)
	if err != nil {
		return scheduleConfig{}, err
	}

	return scheduleConfig{
		metrics:                metrics,
		retentionMetrics:       retentionMetrics,
		retentionNotifications: retentionNotifications,
		retentionDeploys:       retentionDeploys,
		retentionErrors:        retentionErrors,
		watch:                  watch,
		cutoverTimeout:         cutoverTimeout,
		retentionLogs:          retentionLogs,
		retentionSDKLogs:       retentionSDKLogs,
		logPoll:                logPoll,
	}, nil
}

// loadIngest validates the per-service occurrence and log line caps.
func loadIngest(getenv func(string) string) (ingestConfig, error) {
	maxOccurrences, err := parsePositiveInt(getenv, maxOccurrencesEnvVar, defaultMaxOccurrences)
	if err != nil {
		return ingestConfig{}, err
	}

	maxLogLines, err := parsePositiveInt(getenv, maxLogLinesEnvVar, defaultMaxLogLines)
	if err != nil {
		return ingestConfig{}, err
	}

	return ingestConfig{maxOccurrences: maxOccurrences, maxLogLines: maxLogLines}, nil
}

// loadDatabase validates postgres health, backup, and redis settings.
// Empty container names are valid: they disable that probe.
func loadDatabase(getenv func(string) string) (databaseConfig, error) {
	user := getenv(postgresUserEnvVar)
	if user == "" {
		user = defaultPostgresUser
	}

	dbName := getenv(postgresDBEnvVar)
	if dbName == "" {
		dbName = defaultPostgresDB
	}

	backupDir := getenv(backupDirEnvVar)
	if backupDir == "" {
		backupDir = defaultBackupDir
	}

	backupKeep, err := parsePositiveInt(getenv, backupKeepEnvVar, defaultBackupKeep)
	if err != nil {
		return databaseConfig{}, err
	}

	backupInterval, err := parseBackupInterval(getenv)
	if err != nil {
		return databaseConfig{}, err
	}

	return databaseConfig{
		container:      getenv(postgresContainerEnvVar),
		user:           user,
		dbName:         dbName,
		backupDir:      backupDir,
		backupKeep:     backupKeep,
		backupInterval: backupInterval,
		redisContainer: getenv(redisContainerEnvVar),
	}, nil
}

// parseBackupInterval resolves the scheduled-backup cadence: empty means
// the default, zero ("0", "0s") disables scheduled backups, anything
// else must be a positive Go duration.
func parseBackupInterval(getenv func(string) string) (time.Duration, error) {
	raw := getenv(backupIntervalEnvVar)
	if raw == "" {
		raw = defaultBackupInterval
	}

	duration, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("invalid %s %q: want Go duration", backupIntervalEnvVar, raw)
	}

	if duration < 0 {
		return 0, fmt.Errorf("invalid %s %q: must not be negative", backupIntervalEnvVar, raw)
	}

	return duration, nil
}

// parsePositiveInt resolves a required positive integer setting.
func parsePositiveInt(getenv func(string) string, name, fallback string) (int, error) {
	raw := getenv(name)
	if raw == "" {
		raw = fallback
	}

	value, err := strconv.Atoi(raw)
	if err != nil || value < 1 {
		return 0, fmt.Errorf("invalid %s %q: want positive integer", name, raw)
	}

	return value, nil
}

// parsePositiveInt64 resolves a required positive 64-bit integer setting.
func parsePositiveInt64(getenv func(string) string, name, fallback string) (int64, error) {
	raw := getenv(name)
	if raw == "" {
		raw = fallback
	}

	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value < 1 {
		return 0, fmt.Errorf("invalid %s %q: want positive integer", name, raw)
	}

	return value, nil
}

// loadAdmin validates the admin password and cookie-security flag.
func loadAdmin(getenv func(string) string) (adminConfig, error) {
	password := getenv(adminPasswordEnvVar)
	if password == "" {
		return adminConfig{}, fmt.Errorf("invalid %s: must be set", adminPasswordEnvVar)
	}

	secure := false

	if raw := getenv(cookieSecureEnvVar); raw != "" {
		parsed, err := strconv.ParseBool(raw)
		if err != nil {
			return adminConfig{}, fmt.Errorf("invalid %s %q: want boolean", cookieSecureEnvVar, raw)
		}

		secure = parsed
	}

	return adminConfig{password: password, secure: secure}, nil
}

// parseRetentionDays reads a day-count retention variable with a
// default, accepting a plain day count ("7") or — for consistency
// with the other retention variables — a Go duration ("168h").
func parseRetentionDays(getenv func(string) string, name, def string) (time.Duration, error) {
	raw := getenv(name)
	if raw == "" {
		raw = def
	}

	if days, err := strconv.Atoi(raw); err == nil {
		if days < 1 {
			return 0, fmt.Errorf("invalid %s %q: must be positive", name, raw)
		}

		return time.Duration(days) * 24 * time.Hour, nil
	}

	duration, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("invalid %s %q: want day count or Go duration", name, raw)
	}

	if duration <= 0 {
		return 0, fmt.Errorf("invalid %s %q: must be positive", name, raw)
	}

	return duration, nil
}

// parseDuration reads a Go duration variable with a default.
func parseDuration(getenv func(string) string, name, def string) (time.Duration, error) {
	raw := getenv(name)
	if raw == "" {
		raw = def
	}

	duration, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("invalid %s %q: want Go duration", name, raw)
	}

	if duration <= 0 {
		return 0, fmt.Errorf("invalid %s %q: must be positive", name, raw)
	}

	return duration, nil
}
