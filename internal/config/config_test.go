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
		MetricsInterval: 30 * time.Second, RetentionMetrics: 168 * time.Hour,
		RetentionNotifications: 720 * time.Hour, RetentionDeploys: 8760 * time.Hour,
		AdminPassword: testAdminPassword, CookieSecure: false,
		WatchInterval: 5 * time.Second, CutoverTimeout: 10 * time.Minute,
		RetentionErrors: 720 * time.Hour, MaxOccurrences: 10000,
		RetentionLogs: 168 * time.Hour, MaxLogLines: 50000, LogPollInterval: 5 * time.Second,
		RetentionSDKLogs:  7 * 24 * time.Hour,
		PostgresContainer: "", PostgresUser: defaultPostgresUser, PostgresDB: defaultPostgresDB,
		BackupDir: defaultBackupDir, BackupKeep: 14, RedisContainer: "",
	}
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
				"TOUCHGRASS_METRICS_INTERVAL": "10s", adminPasswordEnvVar: testAdminPassword,
				"TOUCHGRASS_COOKIE_SECURE": "true", "TOUCHGRASS_WATCH_INTERVAL": "2s",
				"TOUCHGRASS_RETENTION_ERRORS": "24h", "TOUCHGRASS_INGEST_MAX_OCCURRENCES": "500",
				"TOUCHGRASS_RETENTION_LOGS": "48h", "TOUCHGRASS_LOGS_MAX_LINES": "1000",
				"TOUCHGRASS_LOG_POLL_INTERVAL": "2s", "TOUCHGRASS_RETENTION_SDK_LOGS": "3",
				"TOUCHGRASS_POSTGRES_CONTAINER": "pg", "TOUCHGRASS_POSTGRES_USER": "app",
				"TOUCHGRASS_POSTGRES_DB": "ticketnation", "TOUCHGRASS_DB_BACKUP_DIR": "/opt/backups",
				"TOUCHGRASS_DB_BACKUP_KEEP": "30", "TOUCHGRASS_REDIS_CONTAINER": "redis",
			},
			mutate: func(cfg *Config) {
				cfg.Addr = ":8080"
				cfg.DB = "/data/t.db"
				cfg.Env = "dev"
				cfg.MetricsInterval = 10 * time.Second
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
				cfg.RedisContainer = "redis"
			},
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
			name:        "invalid interval",
			env:         map[string]string{"TOUCHGRASS_METRICS_INTERVAL": "soon", adminPasswordEnvVar: "s"},
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
