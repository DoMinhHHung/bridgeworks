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
	testRedisAddr        = "identity-redis:6379"
	testRedisPassword    = "redis-local-placeholder"
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
	if cfg.ServiceName != "identity-service" || cfg.HTTPAddr != ":8080" || cfg.LogLevel != slog.LevelInfo {
		t.Fatalf("unexpected service config: %+v", cfg)
	}
	if cfg.ReadHeaderTimeout != 5*time.Second || cfg.ReadTimeout != 15*time.Second ||
		cfg.WriteTimeout != 15*time.Second || cfg.IdleTimeout != 60*time.Second ||
		cfg.ShutdownTimeout != 10*time.Second {
		t.Fatalf("unexpected HTTP timeouts: %+v", cfg)
	}
	if cfg.DatabaseURL != testDatabaseURL || cfg.DatabaseConnectTimeout != 5*time.Second ||
		cfg.DatabaseReadinessTimeout != 2*time.Second || cfg.DatabaseMaxConns != 5 ||
		cfg.DatabaseMinConns != 0 || cfg.DatabaseMaxConnLifetime != 30*time.Minute ||
		cfg.DatabaseMaxConnIdleTime != 5*time.Minute || cfg.DatabaseHealthCheckPeriod != time.Minute {
		t.Fatalf("unexpected database config: %+v", cfg)
	}
	if cfg.RedisAddr != testRedisAddr || cfg.RedisUsername != "default" ||
		cfg.RedisPassword != testRedisPassword || !cfg.RedisTLSEnabled ||
		cfg.RedisDialTimeout != 500*time.Millisecond || cfg.RedisOperationTimeout != 150*time.Millisecond ||
		cfg.CurrentUserCacheTTL != 30*time.Second {
		t.Fatalf("unexpected Redis config: %+v", cfg)
	}
	if cfg.ClerkWebhookSigningSecret != testWebhookSecret ||
		cfg.ClerkWebhookProcessTimeout != 5*time.Second || cfg.ClerkWebhookMaxBodyBytes != 1_048_576 {
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
		"REDIS_ADDR":                    "cache.example.test:6380",
		"REDIS_USERNAME":                "cache-user",
		"REDIS_PASSWORD":                "cache-password",
		"REDIS_TLS_ENABLED":             "false",
		"REDIS_DIAL_TIMEOUT":            "2s",
		"REDIS_OPERATION_TIMEOUT":       "750ms",
		"CURRENT_USER_CACHE_TTL":        "4m",
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
	if cfg.RedisAddr != "cache.example.test:6380" || cfg.RedisUsername != "cache-user" ||
		cfg.RedisPassword != "cache-password" || cfg.RedisTLSEnabled ||
		cfg.RedisDialTimeout != 2*time.Second || cfg.RedisOperationTimeout != 750*time.Millisecond ||
		cfg.CurrentUserCacheTTL != 4*time.Minute {
		t.Fatalf("unexpected Redis overrides: %+v", cfg)
	}
	if cfg.ClerkJWTKey != "test-jwk-override" || cfg.ClerkIssuer != "https://clerk.override.test" ||
		!reflect.DeepEqual(cfg.ClerkAuthorizedParties, []string{"http://127.0.0.1:5173", "https://app.override.test"}) ||
		cfg.ClerkAuthLeeway != 30*time.Second {
		t.Fatalf("unexpected authentication config: %+v", cfg)
	}
}

func TestLoadRedisBoundsAndSecretRedaction(t *testing.T) {
	t.Parallel()

	secretAddress := "rediss://default:redis-password-sensitive@cache.example:6379"
	tests := []struct {
		name      string
		overrides map[string]string
		wantError string
	}{
		{name: "missing address", overrides: map[string]string{"REDIS_ADDR": " "}, wantError: "REDIS_ADDR is required"},
		{name: "address with credentials", overrides: map[string]string{"REDIS_ADDR": secretAddress}, wantError: "REDIS_ADDR must be a host and port"},
		{name: "missing port", overrides: map[string]string{"REDIS_ADDR": "cache.example"}, wantError: "REDIS_ADDR must be a valid host and port"},
		{name: "invalid port", overrides: map[string]string{"REDIS_ADDR": "cache.example:70000"}, wantError: "REDIS_ADDR must contain a valid port"},
		{name: "blank username", overrides: map[string]string{"REDIS_USERNAME": " "}, wantError: "REDIS_USERNAME must not be empty"},
		{name: "blank password", overrides: map[string]string{"REDIS_PASSWORD": " "}, wantError: "REDIS_PASSWORD is required"},
		{name: "invalid TLS boolean", overrides: map[string]string{"REDIS_TLS_ENABLED": "sometimes"}, wantError: "REDIS_TLS_ENABLED must be true or false"},
		{name: "zero dial timeout", overrides: map[string]string{"REDIS_DIAL_TIMEOUT": "0s"}, wantError: "REDIS_DIAL_TIMEOUT must be greater than zero"},
		{name: "dial timeout too large", overrides: map[string]string{"REDIS_DIAL_TIMEOUT": "6s"}, wantError: "REDIS_DIAL_TIMEOUT must be less than or equal to 5s"},
		{name: "zero operation timeout", overrides: map[string]string{"REDIS_OPERATION_TIMEOUT": "0s"}, wantError: "REDIS_OPERATION_TIMEOUT must be greater than zero"},
		{name: "operation timeout too large", overrides: map[string]string{"REDIS_OPERATION_TIMEOUT": "1100ms"}, wantError: "REDIS_OPERATION_TIMEOUT must be less than or equal to 1s"},
		{name: "zero TTL", overrides: map[string]string{"CURRENT_USER_CACHE_TTL": "0s"}, wantError: "CURRENT_USER_CACHE_TTL must be greater than zero"},
		{name: "TTL too large", overrides: map[string]string{"CURRENT_USER_CACHE_TTL": "6m"}, wantError: "CURRENT_USER_CACHE_TTL must be less than or equal to 5m0s"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := load(mapLookup(runtimeEnv(test.overrides)))
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("error = %q, want substring %q", err, test.wantError)
			}
			for _, forbidden := range []string{secretAddress, "redis-password-sensitive", testRedisPassword, testDatabaseURL, testWebhookSecret, testJWTKey} {
				if strings.Contains(err.Error(), forbidden) {
					t.Fatalf("error leaked configuration %q: %q", forbidden, err)
				}
			}
		})
	}
}

func TestLoadClerkWebhookProcessTimeoutInvariants(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		overrides map[string]string
		wantError string
	}{
		{overrides: map[string]string{"CLERK_WEBHOOK_PROCESS_TIMEOUT": "9s"}, wantError: "less than or equal to 8s"},
		{overrides: map[string]string{"CLERK_WEBHOOK_PROCESS_TIMEOUT": "8s", "HTTP_WRITE_TIMEOUT": "8s"}, wantError: "less than HTTP_WRITE_TIMEOUT"},
		{overrides: map[string]string{"CLERK_WEBHOOK_PROCESS_TIMEOUT": "0s"}, wantError: "must be greater than zero"},
	} {
		_, err := load(mapLookup(runtimeEnv(test.overrides)))
		if err == nil || !strings.Contains(err.Error(), test.wantError) {
			t.Fatalf("error = %q, want %q", err, test.wantError)
		}
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

func TestLoadRejectsInvalidAuthenticationAndRuntimeValues(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		env       map[string]string
		wantError string
	}{
		{name: "missing JWK", env: runtimeEnv(map[string]string{"CLERK_JWT_KEY": " "}), wantError: "CLERK_JWT_KEY is required"},
		{name: "missing issuer", env: runtimeEnv(map[string]string{"CLERK_ISSUER": " "}), wantError: "CLERK_ISSUER is required"},
		{name: "issuer trailing slash", env: runtimeEnv(map[string]string{"CLERK_ISSUER": "https://clerk.bridgeworks.test/"}), wantError: "must be an origin"},
		{name: "issuer HTTP outside local", env: runtimeEnv(map[string]string{"CLERK_ISSUER": "http://clerk.example.com"}), wantError: "must use HTTPS"},
		{name: "blank party", env: runtimeEnv(map[string]string{"CLERK_AUTHORIZED_PARTIES": testAuthorizedParty + ", ," + testAuthorizedParty2}), wantError: "empty items"},
		{name: "duplicate party", env: runtimeEnv(map[string]string{"CLERK_AUTHORIZED_PARTIES": testAuthorizedParty + "," + testAuthorizedParty}), wantError: "duplicates"},
		{name: "leeway too large", env: runtimeEnv(map[string]string{"CLERK_AUTH_LEEWAY": "31s"}), wantError: "less than or equal to 30s"},
		{name: "missing database", env: runtimeEnv(map[string]string{"DATABASE_URL": " "}), wantError: "DATABASE_URL is required"},
		{name: "missing webhook secret", env: runtimeEnv(map[string]string{"CLERK_WEBHOOK_SIGNING_SECRET": " "}), wantError: "CLERK_WEBHOOK_SIGNING_SECRET is required"},
		{name: "zero max conns", env: runtimeEnv(map[string]string{"DATABASE_MAX_CONNS": "0"}), wantError: "DATABASE_MAX_CONNS must be greater than zero"},
		{name: "min exceeds max", env: runtimeEnv(map[string]string{"DATABASE_MIN_CONNS": "6", "DATABASE_MAX_CONNS": "5"}), wantError: "DATABASE_MIN_CONNS must be less than or equal"},
		{name: "invalid log level", env: runtimeEnv(map[string]string{"LOG_LEVEL": "INFO+2"}), wantError: "LOG_LEVEL must be one of"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := load(mapLookup(test.env))
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("error = %q, want %q", err, test.wantError)
			}
			for _, forbidden := range []string{testDatabaseURL, testWebhookSecret, testRedisPassword, testJWTKey} {
				if strings.Contains(err.Error(), forbidden) {
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
		"REDIS_ADDR":                   testRedisAddr,
		"REDIS_PASSWORD":               testRedisPassword,
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
