package config

import (
	"testing"
	"time"
)

const testAdminPassword = "secret"

// defaultTestConfig returns the expected config for a password-only environment.
func defaultTestConfig() Config {
	return Config{
		Addr: "127.0.0.1:8080", DB: "./touchgrass.db", Env: "prod",
		HostRoot:        "/",
		MetricsInterval: 30 * time.Second, RetentionMetrics: 168 * time.Hour,
		InventoryInterval: 30 * time.Second, InventoryWorkers: 4,
		SecurityProcessInterval: 30 * time.Second, SecurityWorkers: 4,
		SecurityDiffInterval:   10 * time.Minute,
		RetentionNotifications: 720 * time.Hour, RetentionDeploys: 8760 * time.Hour,
		AdminPassword: testAdminPassword, CookieSecure: false,
		WatchInterval: 5 * time.Second, CutoverTimeout: 10 * time.Minute,
		RetentionErrors: 720 * time.Hour, MaxOccurrences: 10000,
		RetentionLogs: 168 * time.Hour, MaxLogLines: 50000, LogPollInterval: 5 * time.Second,
		RetentionSDKLogs:  7 * 24 * time.Hour,
		PostgresContainer: "", PostgresUser: defaultPostgresUser, PostgresDB: defaultPostgresDB,
		BackupDir: defaultBackupDir, BackupKeep: 14, BackupInterval: 24 * time.Hour,
		RedisContainer: "", ScriptsDir: defaultScriptsDir,
		MaintenanceInterval: 5 * time.Minute, SQLiteMaxBytes: 1 << 30,
		SQLiteBackupDir: defaultSQLiteBackupDir, SQLiteBackupKeep: 14,
		RetentionPerformance: 168 * time.Hour, RetentionPerformanceHourly: 720 * time.Hour,
		NginxTimingLog: "", PerformanceServiceID: "", PerformanceInterval: 30 * time.Second,
	}
}

// mutateCustomTestConfig applies the custom-values case expectations.
func mutateCustomTestConfig(cfg *Config) {
	cfg.Addr = ":8080"
	cfg.DB = "/data/t.db"
	cfg.Env = "dev"
	cfg.HostRoot = "/hostfs"
	cfg.MetricsInterval = 10 * time.Second
	cfg.InventoryInterval = 12 * time.Second
	cfg.InventoryWorkers = 8
	cfg.SecurityProcessInterval = 45 * time.Second
	cfg.SecurityWorkers = 6
	cfg.SecurityDiffInterval = 12 * time.Minute
	cfg.CookieSecure = true
	cfg.WatchInterval = 2 * time.Second
	cfg.RetentionErrors = 24 * time.Hour
	cfg.MaxOccurrences = 500
	cfg.RetentionLogs = 48 * time.Hour
	cfg.MaxLogLines = 1000
	cfg.LogPollInterval = 2 * time.Second
	cfg.RetentionSDKLogs = 3 * 24 * time.Hour
	cfg.PostgresContainer = "pg"
	cfg.PostgresUser = "app"
	cfg.PostgresDB = "ticketnation"
	cfg.BackupDir = "/opt/backups"
	cfg.BackupKeep = 30
	cfg.BackupInterval = 6 * time.Hour
	cfg.RedisContainer = "redis"
	cfg.ScriptsDir = "/opt/touchgrass/scripts"
	cfg.MaintenanceInterval = 2 * time.Minute
	cfg.SQLiteMaxBytes = 2 << 20
	cfg.SQLiteBackupDir = "/var/backups/touchgrass"
	cfg.SQLiteBackupKeep = 30
	cfg.RetentionPerformance = 24 * time.Hour
	cfg.RetentionPerformanceHourly = 240 * time.Hour
	cfg.NginxTimingLog = "/var/log/nginx/ticketnation-performance/requests.jsonl"
	cfg.PerformanceServiceID = "tn-api"
	cfg.PerformanceInterval = 15 * time.Second
}

func TestLoad(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		env         map[string]string
		mutate      func(*Config)
		expectedErr bool
	}{
		{
			name: "defaults with password",
			env:  map[string]string{adminPasswordEnvVar: testAdminPassword},
		},
		{
			name: "custom values from env",
			env: map[string]string{
				"TOUCHGRASS_ADDR": ":8080", "TOUCHGRASS_DB": "/data/t.db", "APP_ENV": "dev",
				hostRootEnvVar:                "/hostfs",
				"TOUCHGRASS_METRICS_INTERVAL": "10s", adminPasswordEnvVar: testAdminPassword,
				inventoryIntervalEnvVar: "12s", inventoryWorkersEnvVar: "8",
				securityProcessIntervalEnvVar: "45s", securityWorkersEnvVar: "6",
				securityDiffIntervalEnvVar: "12m",
				"TOUCHGRASS_COOKIE_SECURE": "true", "TOUCHGRASS_WATCH_INTERVAL": "2s",
				"TOUCHGRASS_RETENTION_ERRORS": "24h", "TOUCHGRASS_INGEST_MAX_OCCURRENCES": "500",
				"TOUCHGRASS_RETENTION_LOGS": "48h", "TOUCHGRASS_LOGS_MAX_LINES": "1000",
				"TOUCHGRASS_LOG_POLL_INTERVAL": "2s", "TOUCHGRASS_RETENTION_SDK_LOGS": "3",
				"TOUCHGRASS_POSTGRES_CONTAINER": "pg", "TOUCHGRASS_POSTGRES_USER": "app",
				"TOUCHGRASS_POSTGRES_DB": "ticketnation", "TOUCHGRASS_DB_BACKUP_DIR": "/opt/backups",
				"TOUCHGRASS_DB_BACKUP_KEEP": "30", backupIntervalEnvVar: "6h",
				"TOUCHGRASS_REDIS_CONTAINER": "redis",
				"TOUCHGRASS_SCRIPTS_DIR":     "/opt/touchgrass/scripts",
				maintenanceIntervalEnvVar:    "2m", sqliteMaxBytesEnvVar: "2097152",
				sqliteBackupDirEnvVar:      "/var/backups/touchgrass",
				sqliteBackupKeepEnvVar:     "30",
				retentionPerformanceEnvVar: "24h", retentionPerformanceHourlyEnvVar: "240h",
				nginxTimingLogEnvVar:       "/var/log/nginx/ticketnation-performance/requests.jsonl",
				performanceServiceIDEnvVar: "tn-api", performanceIntervalEnvVar: "15s",
			},
			mutate: mutateCustomTestConfig,
		},
		{
			name:        "missing password",
			env:         map[string]string{},
			expectedErr: true,
		},
		{
			name:        "invalid addr",
			env:         map[string]string{"TOUCHGRASS_ADDR": "not-an-addr", adminPasswordEnvVar: "s"},
			expectedErr: true,
		},
		{
			name:        "invalid env",
			env:         map[string]string{"APP_ENV": "staging", adminPasswordEnvVar: "s"},
			expectedErr: true,
		},
		{
			name:        "relative host root",
			env:         map[string]string{hostRootEnvVar: "hostfs", adminPasswordEnvVar: "s"},
			expectedErr: true,
		},
		{
			name:        "invalid interval",
			env:         map[string]string{"TOUCHGRASS_METRICS_INTERVAL": "soon", adminPasswordEnvVar: "s"},
			expectedErr: true,
		},
		{
			name:        "invalid inventory interval",
			env:         map[string]string{inventoryIntervalEnvVar: "soon", adminPasswordEnvVar: "s"},
			expectedErr: true,
		},
		{
			name:        "inventory workers out of range",
			env:         map[string]string{inventoryWorkersEnvVar: "33", adminPasswordEnvVar: "s"},
			expectedErr: true,
		},
		{
			name:        "invalid security process interval",
			env:         map[string]string{securityProcessIntervalEnvVar: "soon", adminPasswordEnvVar: "s"},
			expectedErr: true,
		},
		{
			name:        "security workers out of range",
			env:         map[string]string{securityWorkersEnvVar: "33", adminPasswordEnvVar: "s"},
			expectedErr: true,
		},
		{
			name:        "security diff interval too short",
			env:         map[string]string{securityDiffIntervalEnvVar: "30s", adminPasswordEnvVar: "s"},
			expectedErr: true,
		},
		{
			name:        "security diff interval too long",
			env:         map[string]string{securityDiffIntervalEnvVar: "25h", adminPasswordEnvVar: "s"},
			expectedErr: true,
		},
		{
			name:        "non-positive retention",
			env:         map[string]string{"TOUCHGRASS_RETENTION_METRICS": "0s", adminPasswordEnvVar: "s"},
			expectedErr: true,
		},
		{
			name:        "non-boolean cookie flag",
			env:         map[string]string{"TOUCHGRASS_COOKIE_SECURE": "maybe", adminPasswordEnvVar: "s"},
			expectedErr: true,
		},
		{
			name: "zero backup interval disables",
			env:  map[string]string{backupIntervalEnvVar: "0", adminPasswordEnvVar: testAdminPassword},
			mutate: func(cfg *Config) {
				cfg.BackupInterval = 0
			},
		},
		{
			name:        "invalid backup interval",
			env:         map[string]string{backupIntervalEnvVar: "nightly", adminPasswordEnvVar: "s"},
			expectedErr: true,
		},
		{
			name:        "negative backup interval",
			env:         map[string]string{backupIntervalEnvVar: "-1h", adminPasswordEnvVar: "s"},
			expectedErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cfg, err := load(func(key string) string {
				return tt.env[key]
			})

			if tt.expectedErr {
				if err == nil {
					t.Fatal("load() error = nil, want error")
				}

				return
			}

			if err != nil {
				t.Fatalf("load() error = %v, want nil", err)
			}

			expected := defaultTestConfig()
			if tt.mutate != nil {
				tt.mutate(&expected)
			}

			if cfg != expected {
				t.Errorf("load() = %+v, want %+v", cfg, expected)
			}
		})
	}
}

func TestLoadIngest(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		env  map[string]string
	}{
		{
			name: "invalid errors retention",
			env:  map[string]string{"TOUCHGRASS_RETENTION_ERRORS": "soon", adminPasswordEnvVar: "s"},
		},
		{
			name: "non-positive occurrence cap",
			env:  map[string]string{"TOUCHGRASS_INGEST_MAX_OCCURRENCES": "0", adminPasswordEnvVar: "s"},
		},
		{
			name: "non-numeric occurrence cap",
			env:  map[string]string{"TOUCHGRASS_INGEST_MAX_OCCURRENCES": "many", adminPasswordEnvVar: "s"},
		},
		{
			name: "invalid logs retention",
			env:  map[string]string{"TOUCHGRASS_RETENTION_LOGS": "soon", adminPasswordEnvVar: "s"},
		},
		{
			name: "non-positive log line cap",
			env:  map[string]string{"TOUCHGRASS_LOGS_MAX_LINES": "0", adminPasswordEnvVar: "s"},
		},
		{
			name: "invalid log poll interval",
			env:  map[string]string{"TOUCHGRASS_LOG_POLL_INTERVAL": "often", adminPasswordEnvVar: "s"},
		},
		{
			name: "invalid sdk logs retention",
			env:  map[string]string{"TOUCHGRASS_RETENTION_SDK_LOGS": "seven", adminPasswordEnvVar: "s"},
		},
		{
			name: "non-positive sdk logs retention",
			env:  map[string]string{"TOUCHGRASS_RETENTION_SDK_LOGS": "0", adminPasswordEnvVar: "s"},
		},
		{
			name: "non-positive backup keep",
			env:  map[string]string{"TOUCHGRASS_DB_BACKUP_KEEP": "0", adminPasswordEnvVar: "s"},
		},
		{
			name: "non-numeric backup keep",
			env:  map[string]string{"TOUCHGRASS_DB_BACKUP_KEEP": "many", adminPasswordEnvVar: "s"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if _, err := load(func(key string) string { return tt.env[key] }); err == nil {
				t.Error("load() error = nil, want error")
			}
		})
	}
}

func TestLoadRetentionSDKLogsForms(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		value    string
		expected time.Duration
	}{
		{name: "day count", value: "7", expected: 7 * 24 * time.Hour},
		{name: "go duration", value: "48h", expected: 48 * time.Hour},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cfg, err := load(func(key string) string {
				if key == retentionSDKLogsEnvVar {
					return tt.value
				}

				if key == adminPasswordEnvVar {
					return testAdminPassword
				}

				return ""
			})
			if err != nil {
				t.Fatalf("load() error = %v, want nil", err)
			}

			if cfg.RetentionSDKLogs != tt.expected {
				t.Errorf("RetentionSDKLogs = %v, want %v", cfg.RetentionSDKLogs, tt.expected)
			}
		})
	}
}

func TestLoadMaintenance(t *testing.T) {
	t.Parallel()

	t.Run("defaults", func(t *testing.T) {
		t.Parallel()

		cfg, err := load(func(key string) string {
			if key == adminPasswordEnvVar {
				return testAdminPassword
			}

			return ""
		})
		if err != nil {
			t.Fatalf("load() error = %v", err)
		}
		if cfg.MaintenanceInterval != 5*time.Minute || cfg.SQLiteMaxBytes != 1<<30 {
			t.Fatalf("maintenance defaults = (%v, %d)", cfg.MaintenanceInterval, cfg.SQLiteMaxBytes)
		}

		if cfg.SQLiteBackupDir != "./backups/sqlite" || cfg.SQLiteBackupKeep != 14 {
			t.Fatalf("SQLite backup config = (%q, %d)", cfg.SQLiteBackupDir, cfg.SQLiteBackupKeep)
		}
		if cfg.RetentionPerformance != 168*time.Hour || cfg.RetentionPerformanceHourly != 720*time.Hour {
			t.Fatalf("performance retention = (%v, %v)", cfg.RetentionPerformance, cfg.RetentionPerformanceHourly)
		}
	})

	t.Run("custom", func(t *testing.T) {
		t.Parallel()
		env := map[string]string{
			adminPasswordEnvVar:              testAdminPassword,
			maintenanceIntervalEnvVar:        "2m",
			sqliteMaxBytesEnvVar:             "2097152",
			sqliteBackupDirEnvVar:            "/var/backups/touchgrass",
			sqliteBackupKeepEnvVar:           "30",
			retentionPerformanceEnvVar:       "24h",
			retentionPerformanceHourlyEnvVar: "240h",
		}
		cfg, err := load(func(key string) string { return env[key] })
		if err != nil {
			t.Fatalf("load() error = %v", err)
		}
		if cfg.MaintenanceInterval != 2*time.Minute || cfg.SQLiteMaxBytes != 2<<20 ||
			cfg.SQLiteBackupDir != "/var/backups/touchgrass" || cfg.SQLiteBackupKeep != 30 ||
			cfg.RetentionPerformance != 24*time.Hour || cfg.RetentionPerformanceHourly != 240*time.Hour {
			t.Fatalf("custom maintenance config = %+v", cfg)
		}
	})

	for _, tt := range []struct {
		name  string
		key   string
		value string
	}{
		{name: "invalid interval", key: maintenanceIntervalEnvVar, value: "often"},
		{name: "non-positive bytes", key: sqliteMaxBytesEnvVar, value: "0"},
		{name: "non-positive backup keep", key: sqliteBackupKeepEnvVar, value: "0"},
		{name: "invalid performance retention", key: retentionPerformanceEnvVar, value: "forever"},
		{name: "non-positive hourly retention", key: retentionPerformanceHourlyEnvVar, value: "0s"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			env := map[string]string{adminPasswordEnvVar: testAdminPassword, tt.key: tt.value}
			if _, err := load(func(key string) string { return env[key] }); err == nil {
				t.Fatal("load() error = nil, want error")
			}
		})
	}
}

func TestLoadPerformanceCollector(t *testing.T) {
	t.Parallel()

	t.Run("disabled nginx defaults", func(t *testing.T) {
		t.Parallel()
		cfg, err := load(func(key string) string {
			if key == adminPasswordEnvVar {
				return testAdminPassword
			}
			return ""
		})
		if err != nil {
			t.Fatal(err)
		}
		if cfg.NginxTimingLog != "" || cfg.PerformanceServiceID != "" || cfg.PerformanceInterval != 30*time.Second {
			t.Fatalf("performance defaults = %+v", cfg)
		}
	})

	t.Run("enabled nginx", func(t *testing.T) {
		t.Parallel()
		env := map[string]string{
			adminPasswordEnvVar:        testAdminPassword,
			nginxTimingLogEnvVar:       "/var/log/nginx/timing.jsonl",
			performanceServiceIDEnvVar: "tn-api",
			performanceIntervalEnvVar:  "15s",
		}
		cfg, err := load(func(key string) string { return env[key] })
		if err != nil {
			t.Fatal(err)
		}
		if cfg.NginxTimingLog != env[nginxTimingLogEnvVar] || cfg.PerformanceServiceID != "tn-api" || cfg.PerformanceInterval != 15*time.Second {
			t.Fatalf("performance config = %+v", cfg)
		}
	})

	for _, tt := range []struct {
		name string
		env  map[string]string
	}{
		{name: "relative timing path", env: map[string]string{nginxTimingLogEnvVar: "logs/timing.jsonl", performanceServiceIDEnvVar: "tn-api"}},
		{name: "missing service for nginx", env: map[string]string{nginxTimingLogEnvVar: "/var/log/nginx/timing.jsonl"}},
		{name: "missing service for postgres", env: map[string]string{postgresContainerEnvVar: "postgres"}},
		{name: "missing service for redis", env: map[string]string{redisContainerEnvVar: "redis"}},
		{name: "invalid interval", env: map[string]string{performanceIntervalEnvVar: "often"}},
		{name: "non-positive interval", env: map[string]string{performanceIntervalEnvVar: "0s"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			tt.env[adminPasswordEnvVar] = testAdminPassword
			if _, err := load(func(key string) string { return tt.env[key] }); err == nil {
				t.Fatal("load() error = nil, want error")
			}
		})
	}
}

func TestLoadReadsProcessEnv(t *testing.T) {
	t.Setenv("TOUCHGRASS_ADDR", "127.0.0.1:9090")
	t.Setenv("TOUCHGRASS_DB", ":memory:")
	t.Setenv("APP_ENV", "dev")
	t.Setenv(adminPasswordEnvVar, testAdminPassword)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v, want nil", err)
	}

	expected := defaultTestConfig()
	expected.Addr = "127.0.0.1:9090"
	expected.DB = ":memory:"
	expected.Env = "dev"

	if cfg != expected {
		t.Errorf("Load() = %+v, want %+v", cfg, expected)
	}
}
