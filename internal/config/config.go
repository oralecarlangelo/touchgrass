// Package config parses touchgrass environment configuration.
package config

import (
	"fmt"
	"net"
	"os"
	"strconv"
	"time"
)

const (
	defaultAddr                   = "127.0.0.1:8080"
	defaultDB                     = "./touchgrass.db"
	defaultEnv                    = "prod"
	defaultMetricsInterval        = "30s"
	defaultRetentionMetrics       = "168h"
	defaultRetentionNotifications = "720h"
	defaultRetentionDeploys       = "8760h"
	defaultWatchInterval          = "5s"
	defaultCutoverTimeout         = "10m"
	defaultRetentionErrors        = "720h"
	defaultMaxOccurrences         = "10000"
	defaultRetentionLogs          = "168h"
	defaultMaxLogLines            = "50000"
	defaultLogPollInterval        = "5s"
	defaultRetentionSDKLogs       = "7"
	defaultPostgresUser           = "postgres"
	defaultPostgresDB             = "postgres"
	defaultBackupDir              = "./backups"
	defaultBackupKeep             = "14"
	addrEnvVar                    = "TOUCHGRASS_ADDR"
	dbEnvVar                      = "TOUCHGRASS_DB"
	envEnvVar                     = "APP_ENV"
	metricsIntervalEnvVar         = "TOUCHGRASS_METRICS_INTERVAL"
	retentionMetricsEnvVar        = "TOUCHGRASS_RETENTION_METRICS"
	retentionNotificationsEnvVar  = "TOUCHGRASS_RETENTION_NOTIFICATIONS"
	retentionDeploysEnvVar        = "TOUCHGRASS_RETENTION_DEPLOYS"
	adminPasswordEnvVar           = "TOUCHGRASS_ADMIN_PASSWORD"
	cookieSecureEnvVar            = "TOUCHGRASS_COOKIE_SECURE"
	watchIntervalEnvVar           = "TOUCHGRASS_WATCH_INTERVAL"
	cutoverTimeoutEnvVar          = "TOUCHGRASS_CUTOVER_TIMEOUT"
	retentionErrorsEnvVar         = "TOUCHGRASS_RETENTION_ERRORS"
	maxOccurrencesEnvVar          = "TOUCHGRASS_INGEST_MAX_OCCURRENCES"
	retentionLogsEnvVar           = "TOUCHGRASS_RETENTION_LOGS"
	maxLogLinesEnvVar             = "TOUCHGRASS_LOGS_MAX_LINES"
	logPollIntervalEnvVar         = "TOUCHGRASS_LOG_POLL_INTERVAL"
	retentionSDKLogsEnvVar        = "TOUCHGRASS_RETENTION_SDK_LOGS"
	postgresContainerEnvVar       = "TOUCHGRASS_POSTGRES_CONTAINER"
	postgresUserEnvVar            = "TOUCHGRASS_POSTGRES_USER"
	postgresDBEnvVar              = "TOUCHGRASS_POSTGRES_DB"
	backupDirEnvVar               = "TOUCHGRASS_DB_BACKUP_DIR"
	backupKeepEnvVar              = "TOUCHGRASS_DB_BACKUP_KEEP"
	redisContainerEnvVar          = "TOUCHGRASS_REDIS_CONTAINER"
	envDev                        = "dev"
	envProd                       = "prod"
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
	// RedisContainer is the redis container name; empty hides redis.
	RedisContainer string
}

// Load reads configuration from the environment.
func Load() (Config, error) {
	return load(os.Getenv)
}

// listenConfig groups validated startup locations.
type listenConfig struct {
	addr string
	db   string
	env  string
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
	redisContainer string
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

	return Config{
		Addr:                   listen.addr,
		DB:                     listen.db,
		Env:                    listen.env,
		MetricsInterval:        schedule.metrics,
		RetentionMetrics:       schedule.retentionMetrics,
		RetentionNotifications: schedule.retentionNotifications,
		RetentionDeploys:       schedule.retentionDeploys,
		AdminPassword:          admin.password,
		CookieSecure:           admin.secure,
		WatchInterval:          schedule.watch,
		CutoverTimeout:         schedule.cutoverTimeout,
		RetentionErrors:        schedule.retentionErrors,
		MaxOccurrences:         ingest.maxOccurrences,
		RetentionLogs:          schedule.retentionLogs,
		RetentionSDKLogs:       schedule.retentionSDKLogs,
		MaxLogLines:            ingest.maxLogLines,
		LogPollInterval:        schedule.logPoll,
		PostgresContainer:      database.container,
		PostgresUser:           database.user,
		PostgresDB:             database.dbName,
		BackupDir:              database.backupDir,
		BackupKeep:             database.backupKeep,
		RedisContainer:         database.redisContainer,
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

	return listenConfig{addr: addr, db: db, env: env}, nil
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

	return databaseConfig{
		container:      getenv(postgresContainerEnvVar),
		user:           user,
		dbName:         dbName,
		backupDir:      backupDir,
		backupKeep:     backupKeep,
		redisContainer: getenv(redisContainerEnvVar),
	}, nil
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
