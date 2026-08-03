package config

import (
	"log/slog"
	"strings"
	"testing"
	"time"
)

const testDatabaseURL = "postgres://runtime:runtime@identity-postgres:5432/bridgeworks?sslmode=disable"

func TestLoadDefaults(t *testing.T) {
	t.Parallel()

	cfg, err := load(mapLookup(map[string]string{
		"DATABASE_URL": testDatabaseURL,
	}))
	if err != nil {
		t.Fatalf("load defaults: %v", err)
	}

	if cfg.ServiceName != "identity-service" {
		t.Fatalf("ServiceName = %q", cfg.ServiceName)
	}
	if cfg.HTTPAddr != ":8080" {
		t.Fatalf("HTTPAddr = %q", cfg.HTTPAddr)
	}
	if cfg.ReadHeaderTimeout != 5*time.Second {
		t.Fatalf("ReadHeaderTimeout = %s", cfg.ReadHeaderTimeout)
	}
	if cfg.ReadTimeout != 15*time.Second {
		t.Fatalf("ReadTimeout = %s", cfg.ReadTimeout)
	}
	if cfg.WriteTimeout != 15*time.Second {
		t.Fatalf("WriteTimeout = %s", cfg.WriteTimeout)
	}
	if cfg.IdleTimeout != 60*time.Second {
		t.Fatalf("IdleTimeout = %s", cfg.IdleTimeout)
	}
	if cfg.ShutdownTimeout != 10*time.Second {
		t.Fatalf("ShutdownTimeout = %s", cfg.ShutdownTimeout)
	}
	if cfg.LogLevel != slog.LevelInfo {
		t.Fatalf("LogLevel = %s", cfg.LogLevel)
	}
	if cfg.DatabaseURL != testDatabaseURL {
		t.Fatal("DatabaseURL was not loaded")
	}
	if cfg.DatabaseConnectTimeout != 5*time.Second {
		t.Fatalf("DatabaseConnectTimeout = %s", cfg.DatabaseConnectTimeout)
	}
	if cfg.DatabaseReadinessTimeout != 2*time.Second {
		t.Fatalf("DatabaseReadinessTimeout = %s", cfg.DatabaseReadinessTimeout)
	}
	if cfg.DatabaseMaxConns != 5 || cfg.DatabaseMinConns != 0 {
		t.Fatalf("unexpected pool sizes: max=%d min=%d", cfg.DatabaseMaxConns, cfg.DatabaseMinConns)
	}
	if cfg.DatabaseMaxConnLifetime != 30*time.Minute {
		t.Fatalf("DatabaseMaxConnLifetime = %s", cfg.DatabaseMaxConnLifetime)
	}
	if cfg.DatabaseMaxConnIdleTime != 5*time.Minute {
		t.Fatalf("DatabaseMaxConnIdleTime = %s", cfg.DatabaseMaxConnIdleTime)
	}
	if cfg.DatabaseHealthCheckPeriod != time.Minute {
		t.Fatalf("DatabaseHealthCheckPeriod = %s", cfg.DatabaseHealthCheckPeriod)
	}
}

func TestLoadOverrides(t *testing.T) {
	t.Parallel()

	cfg, err := load(mapLookup(map[string]string{
		"SERVICE_NAME":                 "identity-test",
		"HTTP_ADDR":                    "127.0.0.1:9090",
		"HTTP_READ_HEADER_TIMEOUT":     "1s",
		"HTTP_READ_TIMEOUT":            "2s",
		"HTTP_WRITE_TIMEOUT":           "3s",
		"HTTP_IDLE_TIMEOUT":            "4s",
		"SHUTDOWN_TIMEOUT":             "5s",
		"LOG_LEVEL":                    "DEBUG",
		"DATABASE_URL":                 testDatabaseURL,
		"DATABASE_CONNECT_TIMEOUT":     "6s",
		"DATABASE_READINESS_TIMEOUT":   "7s",
		"DATABASE_MAX_CONNS":           "9",
		"DATABASE_MIN_CONNS":           "2",
		"DATABASE_MAX_CONN_LIFETIME":   "10m",
		"DATABASE_MAX_CONN_IDLE_TIME":  "3m",
		"DATABASE_HEALTH_CHECK_PERIOD": "30s",
	}))
	if err != nil {
		t.Fatalf("load overrides: %v", err)
	}

	if cfg.ServiceName != "identity-test" || cfg.HTTPAddr != "127.0.0.1:9090" {
		t.Fatalf("unexpected identity config: %+v", cfg)
	}
	if cfg.ReadHeaderTimeout != time.Second || cfg.ReadTimeout != 2*time.Second ||
		cfg.WriteTimeout != 3*time.Second || cfg.IdleTimeout != 4*time.Second ||
		cfg.ShutdownTimeout != 5*time.Second {
		t.Fatalf("unexpected durations: %+v", cfg)
	}
	if cfg.LogLevel != slog.LevelDebug {
		t.Fatalf("LogLevel = %s", cfg.LogLevel)
	}
	if cfg.DatabaseConnectTimeout != 6*time.Second ||
		cfg.DatabaseReadinessTimeout != 7*time.Second ||
		cfg.DatabaseMaxConns != 9 ||
		cfg.DatabaseMinConns != 2 ||
		cfg.DatabaseMaxConnLifetime != 10*time.Minute ||
		cfg.DatabaseMaxConnIdleTime != 3*time.Minute ||
		cfg.DatabaseHealthCheckPeriod != 30*time.Second {
		t.Fatalf("unexpected database config: %+v", cfg)
	}
}

func TestLoadRejectsInvalidValues(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		env       map[string]string
		wantError string
	}{
		{name: "missing database URL", env: map[string]string{}, wantError: "DATABASE_URL is required"},
		{name: "empty database URL", env: map[string]string{"DATABASE_URL": "  "}, wantError: "DATABASE_URL is required"},
		{name: "empty service name", env: runtimeEnv(map[string]string{"SERVICE_NAME": "  "}), wantError: "SERVICE_NAME must not be empty"},
		{name: "empty address", env: runtimeEnv(map[string]string{"HTTP_ADDR": ""}), wantError: "HTTP_ADDR must not be empty"},
		{name: "invalid duration", env: runtimeEnv(map[string]string{"HTTP_READ_TIMEOUT": "soon"}), wantError: "HTTP_READ_TIMEOUT must be a valid duration"},
		{name: "zero duration", env: runtimeEnv(map[string]string{"SHUTDOWN_TIMEOUT": "0s"}), wantError: "SHUTDOWN_TIMEOUT must be greater than zero"},
		{name: "zero database connect timeout", env: runtimeEnv(map[string]string{"DATABASE_CONNECT_TIMEOUT": "0s"}), wantError: "DATABASE_CONNECT_TIMEOUT must be greater than zero"},
		{name: "zero readiness timeout", env: runtimeEnv(map[string]string{"DATABASE_READINESS_TIMEOUT": "0s"}), wantError: "DATABASE_READINESS_TIMEOUT must be greater than zero"},
		{name: "zero max conns", env: runtimeEnv(map[string]string{"DATABASE_MAX_CONNS": "0"}), wantError: "DATABASE_MAX_CONNS must be greater than zero"},
		{name: "negative max conns", env: runtimeEnv(map[string]string{"DATABASE_MAX_CONNS": "-1"}), wantError: "DATABASE_MAX_CONNS must be greater than zero"},
		{name: "invalid max conns", env: runtimeEnv(map[string]string{"DATABASE_MAX_CONNS": "many"}), wantError: "DATABASE_MAX_CONNS must be a valid 32-bit integer"},
		{name: "negative min conns", env: runtimeEnv(map[string]string{"DATABASE_MIN_CONNS": "-1"}), wantError: "DATABASE_MIN_CONNS must be greater than or equal to zero"},
		{name: "min exceeds max", env: runtimeEnv(map[string]string{"DATABASE_MIN_CONNS": "6", "DATABASE_MAX_CONNS": "5"}), wantError: "DATABASE_MIN_CONNS must be less than or equal to DATABASE_MAX_CONNS"},
		{name: "zero max lifetime", env: runtimeEnv(map[string]string{"DATABASE_MAX_CONN_LIFETIME": "0s"}), wantError: "DATABASE_MAX_CONN_LIFETIME must be greater than zero"},
		{name: "zero max idle time", env: runtimeEnv(map[string]string{"DATABASE_MAX_CONN_IDLE_TIME": "0s"}), wantError: "DATABASE_MAX_CONN_IDLE_TIME must be greater than zero"},
		{name: "zero health period", env: runtimeEnv(map[string]string{"DATABASE_HEALTH_CHECK_PERIOD": "0s"}), wantError: "DATABASE_HEALTH_CHECK_PERIOD must be greater than zero"},
		{name: "offset info log level", env: runtimeEnv(map[string]string{"LOG_LEVEL": "INFO+2"}), wantError: "LOG_LEVEL must be one of debug, info, warn, error"},
		{name: "offset error log level", env: runtimeEnv(map[string]string{"LOG_LEVEL": "ERROR-8"}), wantError: "LOG_LEVEL must be one of debug, info, warn, error"},
		{name: "verbose log level", env: runtimeEnv(map[string]string{"LOG_LEVEL": "verbose"}), wantError: "LOG_LEVEL must be one of debug, info, warn, error"},
		{name: "empty log level", env: runtimeEnv(map[string]string{"LOG_LEVEL": ""}), wantError: "LOG_LEVEL must not be empty"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := load(mapLookup(tt.env))
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tt.wantError) {
				t.Fatalf("error = %q, want substring %q", err, tt.wantError)
			}
			for _, secret := range []string{"runtime", testDatabaseURL} {
				if strings.Contains(err.Error(), secret) {
					t.Fatalf("error leaked database configuration: %q", err)
				}
			}
		})
	}
}

func TestLoadMigration(t *testing.T) {
	t.Parallel()

	cfg, err := loadMigration(mapLookup(map[string]string{
		"MIGRATION_DATABASE_URL": testDatabaseURL,
	}))
	if err != nil {
		t.Fatalf("load migration config: %v", err)
	}
	if cfg.DatabaseURL != testDatabaseURL {
		t.Fatal("migration DatabaseURL was not loaded")
	}
	if cfg.Timeout != time.Minute {
		t.Fatalf("Timeout = %s", cfg.Timeout)
	}
	if cfg.LogLevel != slog.LevelInfo {
		t.Fatalf("LogLevel = %s", cfg.LogLevel)
	}
}

func TestLoadMigrationRejectsInvalidValues(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		env       map[string]string
		wantError string
	}{
		{name: "missing URL", env: nil, wantError: "MIGRATION_DATABASE_URL is required"},
		{name: "empty URL", env: map[string]string{"MIGRATION_DATABASE_URL": ""}, wantError: "MIGRATION_DATABASE_URL is required"},
		{name: "zero timeout", env: map[string]string{"MIGRATION_DATABASE_URL": testDatabaseURL, "MIGRATION_TIMEOUT": "0s"}, wantError: "MIGRATION_TIMEOUT must be greater than zero"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := loadMigration(mapLookup(tt.env))
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tt.wantError) {
				t.Fatalf("error = %q, want substring %q", err, tt.wantError)
			}
			if strings.Contains(err.Error(), testDatabaseURL) {
				t.Fatalf("error leaked migration URL: %q", err)
			}
		})
	}
}

func runtimeEnv(overrides map[string]string) map[string]string {
	values := map[string]string{"DATABASE_URL": testDatabaseURL}
	for key, value := range overrides {
		values[key] = value
	}
	return values
}

func mapLookup(values map[string]string) lookupEnvFunc {
	return func(key string) (string, bool) {
		value, ok := values[key]
		return value, ok
	}
}
