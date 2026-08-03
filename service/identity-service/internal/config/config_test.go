package config

import (
	"log/slog"
	"reflect"
	"strings"
	"testing"
	"time"
)

const (
	testDatabaseURL      = "postgres://runtime:runtime@identity-postgres:5432/bridgeworks?sslmode=disable"
	testWebhookSecret    = "whsec_local_test_secret"
	testJWTKey           = "test-only-public-key"
	testIssuer           = "https://clerk.bridgeworks.test"
	testAuthorizedParty  = "http://localhost:3000"
	testAuthorizedParty2 = "https://app.bridgeworks.test"
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
	if cfg.ClerkJWTKey != testJWTKey || cfg.ClerkIssuer != testIssuer ||
		!reflect.DeepEqual(cfg.ClerkAuthorizedParties, []string{testAuthorizedParty}) ||
		cfg.ClerkAuthLeeway != 5*time.Second {
		t.Fatalf("unexpected Clerk authentication config: %+v", cfg)
	}
}

func TestLoadOverrides(t *testing.T) {
	t.Parallel()

	cfg, err := load(mapLookup(runtimeEnv(map[string]string{
		"SERVICE_NAME":                  "identity-test",
		"HTTP_ADDR":                     "127.0.0.1:9090",
		"HTTP_READ_HEADER_TIMEOUT":      "1s",
		"HTTP_READ_TIMEOUT":             "2s",
		"HTTP_WRITE_TIMEOUT":            "9s",
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
		"CLERK_JWT_KEY":                 "  test-jwk-override  ",
		"CLERK_ISSUER":                  "https://clerk.override.test",
		"CLERK_AUTHORIZED_PARTIES":      " http://127.0.0.1:5173 , https://app.override.test ",
		"CLERK_AUTH_LEEWAY":             "30s",
	})))
	if err != nil {
		t.Fatalf("load overrides: %v", err)
	}

	if cfg.ServiceName != "identity-test" || cfg.HTTPAddr != "127.0.0.1:9090" ||
		cfg.ReadHeaderTimeout != time.Second || cfg.ReadTimeout != 2*time.Second ||
		cfg.WriteTimeout != 9*time.Second || cfg.IdleTimeout != 4*time.Second ||
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
	if cfg.ClerkJWTKey != "test-jwk-override" || cfg.ClerkIssuer != "https://clerk.override.test" ||
		!reflect.DeepEqual(cfg.ClerkAuthorizedParties, []string{"http://127.0.0.1:5173", "https://app.override.test"}) ||
		cfg.ClerkAuthLeeway != 30*time.Second {
		t.Fatalf("unexpected authentication config: %+v", cfg)
	}
}

func TestLoadClerkWebhookProcessTimeoutInvariants(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		overrides map[string]string
		want      time.Duration
		wantError string
	}{
		{
			name: "default five seconds is valid",
			want: 5 * time.Second,
		},
		{
			name:      "maximum eight seconds is valid",
			overrides: map[string]string{"CLERK_WEBHOOK_PROCESS_TIMEOUT": "8s"},
			want:      8 * time.Second,
		},
		{
			name:      "nine seconds exceeds maximum",
			overrides: map[string]string{"CLERK_WEBHOOK_PROCESS_TIMEOUT": "9s"},
			wantError: "CLERK_WEBHOOK_PROCESS_TIMEOUT must be less than or equal to 8s",
		},
		{
			name: "process timeout equals HTTP write timeout",
			overrides: map[string]string{
				"CLERK_WEBHOOK_PROCESS_TIMEOUT": "8s",
				"HTTP_WRITE_TIMEOUT":            "8s",
			},
			wantError: "CLERK_WEBHOOK_PROCESS_TIMEOUT must be less than HTTP_WRITE_TIMEOUT",
		},
		{
			name: "process timeout exceeds HTTP write timeout",
			overrides: map[string]string{
				"CLERK_WEBHOOK_PROCESS_TIMEOUT": "8s",
				"HTTP_WRITE_TIMEOUT":            "7s",
			},
			wantError: "CLERK_WEBHOOK_PROCESS_TIMEOUT must be less than HTTP_WRITE_TIMEOUT",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cfg, err := load(mapLookup(runtimeEnv(tt.overrides)))
			if tt.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantError) {
					t.Fatalf("error = %q, want substring %q", err, tt.wantError)
				}
				return
			}
			if err != nil {
				t.Fatalf("load config: %v", err)
			}
			if cfg.ClerkWebhookProcessTimeout != tt.want {
				t.Fatalf("ClerkWebhookProcessTimeout = %s, want %s", cfg.ClerkWebhookProcessTimeout, tt.want)
			}
		})
	}
}

func TestLoadAllowsLocalHTTPOrigins(t *testing.T) {
	t.Parallel()

	cfg, err := load(mapLookup(runtimeEnv(map[string]string{
		"CLERK_ISSUER":             "http://localhost:8081",
		"CLERK_AUTHORIZED_PARTIES": "http://127.0.0.1:5173,http://localhost:3000",
	})))
	if err != nil {
		t.Fatalf("load local HTTP origins: %v", err)
	}
	if cfg.ClerkIssuer != "http://localhost:8081" ||
		!reflect.DeepEqual(cfg.ClerkAuthorizedParties, []string{"http://127.0.0.1:5173", "http://localhost:3000"}) {
		t.Fatalf("unexpected local origins: %+v", cfg)
	}
}

func TestLoadRejectsInvalidClerkAuthenticationConfig(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		env       map[string]string
		wantError string
	}{
		{
			name: "missing JWK",
			env: map[string]string{
				"DATABASE_URL":                    testDatabaseURL,
				"CLERK_WEBHOOK_SIGNING_SECRET":    testWebhookSecret,
				"CLERK_ISSUER":                    testIssuer,
				"CLERK_AUTHORIZED_PARTIES":        testAuthorizedParty,
			},
			wantError: "CLERK_JWT_KEY is required",
		},
		{name: "blank JWK", env: runtimeEnv(map[string]string{"CLERK_JWT_KEY": "  "}), wantError: "CLERK_JWT_KEY is required"},
		{name: "missing issuer", env: runtimeEnv(map[string]string{"CLERK_ISSUER": "  "}), wantError: "CLERK_ISSUER is required"},
		{name: "issuer has trailing slash", env: runtimeEnv(map[string]string{"CLERK_ISSUER": "https://clerk.bridgeworks.test/"}), wantError: "CLERK_ISSUER must be an origin"},
		{name: "issuer HTTP outside local", env: runtimeEnv(map[string]string{"CLERK_ISSUER": "http://clerk.example.com"}), wantError: "CLERK_ISSUER must use HTTPS"},
		{name: "missing authorized parties", env: runtimeEnv(map[string]string{"CLERK_AUTHORIZED_PARTIES": "  "}), wantError: "CLERK_AUTHORIZED_PARTIES is required"},
		{name: "blank authorized party", env: runtimeEnv(map[string]string{"CLERK_AUTHORIZED_PARTIES": testAuthorizedParty + ", ," + testAuthorizedParty2}), wantError: "must not contain empty items"},
		{name: "duplicate authorized party", env: runtimeEnv(map[string]string{"CLERK_AUTHORIZED_PARTIES": testAuthorizedParty + ", " + testAuthorizedParty}), wantError: "must not contain duplicates"},
		{name: "authorized party HTTP outside local", env: runtimeEnv(map[string]string{"CLERK_AUTHORIZED_PARTIES": "http://app.example.com"}), wantError: "CLERK_AUTHORIZED_PARTIES must use HTTPS"},
		{name: "authorized party has path", env: runtimeEnv(map[string]string{"CLERK_AUTHORIZED_PARTIES": "https://app.example.com/path"}), wantError: "CLERK_AUTHORIZED_PARTIES must be an origin"},
		{name: "zero leeway", env: runtimeEnv(map[string]string{"CLERK_AUTH_LEEWAY": "0s"}), wantError: "CLERK_AUTH_LEEWAY must be greater than zero"},
		{name: "leeway exceeds maximum", env: runtimeEnv(map[string]string{"CLERK_AUTH_LEEWAY": "31s"}), wantError: "CLERK_AUTH_LEEWAY must be less than or equal to 30s"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := load(mapLookup(tt.env))
			if err == nil || !strings.Contains(err.Error(), tt.wantError) {
				t.Fatalf("error = %q, want substring %q", err, tt.wantError)
			}
			for _, forbidden := range []string{testJWTKey, "test-only-public-key-sensitive", testDatabaseURL, testWebhookSecret} {
				if strings.Contains(err.Error(), forbidden) {
					t.Fatalf("error leaked configuration: %q", err)
				}
			}
		})
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
			for _, secret := range []string{testDatabaseURL, "runtime:runtime", testWebhookSecret, "whsec_override", testJWTKey} {
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
		"CLERK_JWT_KEY":                testJWTKey,
		"CLERK_ISSUER":                 testIssuer,
		"CLERK_AUTHORIZED_PARTIES":     testAuthorizedParty,
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
