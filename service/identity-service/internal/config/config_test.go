package config

import (
	"log/slog"
	"strings"
	"testing"
	"time"
)

const (
	testDatabaseURL   = "postgres://runtime:runtime@identity-postgres:5432/bridgeworks?sslmode=disable"
	testWebhookSecret = "whsec_local_test_secret"
)

func TestLoadDefaults(t *testing.T) {
	t.Parallel()

	cfg, err := load(mapLookup(runtimeEnv(nil)))
	if err != nil {
		t.Fatalf("load defaults: %v", err)
	}

	if cfg.ServiceName != "identity-service" || cfg.HTTPAddr != ":8080" {
		t.Fatalf("unexpected service config: %+v", cfg)
	}
	if cfg.ReadHeaderTimeout != 5*time.Second || cfg.ReadTimeout != 15*time.Second ||
		cfg.WriteTimeout != 15*time.Second || cfg.IdleTimeout != 60*time.Second ||
		cfg.ShutdownTimeout != 10*time.Second {
		t.Fatalf("unexpected HTTP timeouts: %+v", cfg)
	}
	if cfg.LogLevel != slog.LevelInfo {
		t.Fatalf("LogLevel = %s", cfg.LogLevel)
	}
	if cfg.DatabaseURL != testDatabaseURL || cfg.DatabaseConnectTimeout != 5*time.Second ||
		cfg.DatabaseReadinessTimeout != 2*time.Second || cfg.DatabaseMaxConns != 5 ||
		cfg.DatabaseMinConns != 0 || cfg.DatabaseMaxConnLifetime != 30*time.Minute ||
		cfg.DatabaseMaxConnIdleTime != 5*time.Minute || cfg.DatabaseHealthCheckPeriod != time.Minute {
		t.Fatalf("unexpected database config: %+v", cfg)
	}
	if cfg.ClerkWebhookSigningSecret != testWebhookSecret ||
		cfg.ClerkWebhookProcessTimeout != 5*time.Second ||
		cfg.ClerkWebhookMaxBodyBytes != 1_048_576 {
		t.Fatalf("unexpected Clerk webhook config: %+v", cfg)
	}
}

func TestLoadOverrides(t *testing.T) {
	t.Parallel()

	cfg, err := load(mapLookup(runtimeEnv(map[string]string{
		"SERVICE_NAME":                  "identity-test",
		"HTTP_ADDR":                     "127.0.0.1:9090",
		"HTTP_READ_HEADER_TIMEOUT":      "1s",
		"HTTP_READ_TIMEOUT":             "2s",
		"HTTP_WRITE_TIMEOUT":            "3s",
		"HTTP_IDLE_TIMEOUT":             "4s",
		"SHUTDOWN_TIMEOUT":              "5s",
		"LOG_LEVEL":                     "DEBUG",
		"DATABASE_CONNECT_TIMEOUT":      "6s",
		"DATABASE_READINESS_TIMEOUT":    "7s",
		"DATABASE_MAX_CONNS":            "9",
		"DATABASE_MIN_CONNS":            "2",
		"DATABASE_MAX_CONN_LIFETIME":    "10m",
		"DATABASE_MAX_CONN_IDLE_TIME":   "3m",
		"DATABASE_HEALTH_CHECK_PERIOD":  "30s",
		"CLERK_WEBHOOK_SIGNING_SECRET":  "whsec_override",
		"CLERK_WEBHOOK_PROCESS_TIMEOUT": "8s",
		"CLERK_WEBHOOK_MAX_BODY_BYTES":  "2048",
	})))
	if err != nil {
		t.Fatalf("load overrides: %v", err)
	}

	if cfg.ServiceName != "identity-test" || cfg.HTTPAddr != "127.0.0.1:9090" ||
		cfg.ReadHeaderTimeout != time.Second || cfg.ReadTimeout != 2*time.Second ||
		cfg.WriteTimeout != 3*time.Second || cfg.IdleTimeout != 4*time.Second ||
		cfg.ShutdownTimeout != 5*time.Second || cfg.LogLevel != slog.LevelDebug {
		t.Fatalf("unexpected runtime config: %+v", cfg)
	}
	if cfg.DatabaseConnectTimeout != 6*time.Second || cfg.DatabaseReadinessTimeout != 7*time.Second ||
		cfg.DatabaseMaxConns != 9 || cfg.DatabaseMinConns != 2 ||
		cfg.DatabaseMaxConnLifetime != 10*time.Minute || cfg.DatabaseMaxConnIdleTime != 3*time.Minute ||
		cfg.DatabaseHealthCheckPeriod != 30*time.Second {
		t.Fatalf("unexpected database config: %+v", cfg)
	}
	if cfg.ClerkWebhookSigningSecret != "whsec_override" ||
		cfg.ClerkWebhookProcessTimeout != 8*time.Second || cfg.ClerkWebhookMaxBodyBytes != 2048 {
		t.Fatalf("unexpected webhook config: %+v", cfg)
	}
}

func TestLoadRejectsInvalidValues(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		env       map[string]string
		wantError string
	}{
		{name: "missing database URL", env: map[string]string{"CLERK_WEBHOOK_SIGNING_SECRET": testWebhookSecret}, wantError: "DATABASE_URL is required"},
		{name: "missing webhook secret", env: map[string]string{"DATABASE_URL": testDatabaseURL}, wantError: "CLERK_WEBHOOK_SIGNING_SECRET is required"},
		{name: "blank webhook secret", env: runtimeEnv(map[string]string{"CLERK_WEBHOOK_SIGNING_SECRET": "  "}), wantError: "CLERK_WEBHOOK_SIGNING_SECRET is required"},
		{name: "zero webhook timeout", env: runtimeEnv(map[string]string{"CLERK_WEBHOOK_PROCESS_TIMEOUT": "0s"}), wantError: "CLERK_WEBHOOK_PROCESS_TIMEOUT must be greater than zero"},
		{name: "invalid body limit", env: runtimeEnv(map[string]string{"CLERK_WEBHOOK_MAX_BODY_BYTES": "large"}), wantError: "CLERK_WEBHOOK_MAX_BODY_BYTES must be a valid integer"},
		{name: "zero body limit", env: runtimeEnv(map[string]string{"CLERK_WEBHOOK_MAX_BODY_BYTES": "0"}), wantError: "CLERK_WEBHOOK_MAX_BODY_BYTES must be greater than zero"},
		{name: "negative body limit", env: runtimeEnv(map[string]string{"CLERK_WEBHOOK_MAX_BODY_BYTES": "-1"}), wantError: "CLERK_WEBHOOK_MAX_BODY_BYTES must be greater than zero"},
		{name: "body limit exceeds maximum", env: runtimeEnv(map[string]string{"CLERK_WEBHOOK_MAX_BODY_BYTES": "5242881"}), wantError: "CLERK_WEBHOOK_MAX_BODY_BYTES must be less than or equal to 5242880"},
		{name: "empty service name", env: runtimeEnv(map[string]string{"SERVICE_NAME": "  "}), wantError: "SERVICE_NAME must not be empty"},
		{name: "invalid duration", env: runtimeEnv(map[string]string{"HTTP_READ_TIMEOUT": "soon"}), wantError: "HTTP_READ_TIMEOUT must be a valid duration"},
		{name: "zero max conns", env: runtimeEnv(map[string]string{"DATABASE_MAX_CONNS": "0"}), wantError: "DATABASE_MAX_CONNS must be greater than zero"},
		{name: "negative min conns", env: runtimeEnv(map[string]string{"DATABASE_MIN_CONNS": "-1"}), wantError: "DATABASE_MIN_CONNS must be greater than or equal to zero"},
		{name: "min exceeds max", env: runtimeEnv(map[string]string{"DATABASE_MIN_CONNS": "6", "DATABASE_MAX_CONNS": "5"}), wantError: "DATABASE_MIN_CONNS must be less than or equal to DATABASE_MAX_CONNS"},
		{name: "invalid log level", env: runtimeEnv(map[string]string{"LOG_LEVEL": "INFO+2"}), wantError: "LOG_LEVEL must be one of debug, info, warn, error"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := load(mapLookup(tt.env))
			if err == nil || !strings.Contains(err.Error(), tt.wantError) {
				t.Fatalf("error = %q, want substring %q", err, tt.wantError)
			}
			for _, secret := range []string{testDatabaseURL, "runtime:runtime", testWebhookSecret, "whsec_override"} {
				if strings.Contains(err.Error(), secret) {
					t.Fatalf("error leaked configuration: %q", err)
				}
			}
		})
	}
}

func TestLoadMigration(t *testing.T) {
	t.Parallel()

	cfg, err := loadMigration(mapLookup(map[string]string{"MIGRATION_DATABASE_URL": testDatabaseURL}))
	if err != nil {
		t.Fatalf("load migration config: %v", err)
	}
	if cfg.DatabaseURL != testDatabaseURL || cfg.Timeout != time.Minute || cfg.LogLevel != slog.LevelInfo {
		t.Fatalf("unexpected migration config: %+v", cfg)
	}
}

func TestLoadMigrationRejectsInvalidValues(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		env       map[string]string
		wantError string
	}{
		{env: nil, wantError: "MIGRATION_DATABASE_URL is required"},
		{env: map[string]string{"MIGRATION_DATABASE_URL": testDatabaseURL, "MIGRATION_TIMEOUT": "0s"}, wantError: "MIGRATION_TIMEOUT must be greater than zero"},
	} {
		_, err := loadMigration(mapLookup(test.env))
		if err == nil || !strings.Contains(err.Error(), test.wantError) {
			t.Fatalf("error = %q, want %q", err, test.wantError)
		}
		if strings.Contains(err.Error(), testDatabaseURL) {
			t.Fatalf("migration error leaked URL: %q", err)
		}
	}
}

func runtimeEnv(overrides map[string]string) map[string]string {
	values := map[string]string{
		"DATABASE_URL":                 testDatabaseURL,
		"CLERK_WEBHOOK_SIGNING_SECRET": testWebhookSecret,
	}
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
