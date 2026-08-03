package config

import (
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	defaultServiceName                = "identity-service"
	defaultHTTPAddr                   = ":8080"
	defaultReadHeaderTimeout          = 5 * time.Second
	defaultReadTimeout                = 15 * time.Second
	defaultWriteTimeout               = 15 * time.Second
	defaultIdleTimeout                = 60 * time.Second
	defaultShutdownTimeout            = 10 * time.Second
	defaultLogLevel                   = "info"
	defaultDatabaseConnectTimeout     = 5 * time.Second
	defaultDatabaseReadinessTimeout   = 2 * time.Second
	defaultDatabaseMaxConns           = int32(5)
	defaultDatabaseMinConns           = int32(0)
	defaultDatabaseMaxConnLifetime    = 30 * time.Minute
	defaultDatabaseMaxConnIdleTime    = 5 * time.Minute
	defaultDatabaseHealthCheckPeriod  = time.Minute
	defaultClerkWebhookProcessTimeout = 5 * time.Second
	maximumClerkWebhookProcessTimeout = 8 * time.Second
	defaultClerkWebhookMaxBodyBytes   = int64(1_048_576)
	maximumClerkWebhookMaxBodyBytes   = int64(5 * 1024 * 1024)
	defaultMigrationTimeout           = time.Minute
)

type Config struct {
	ServiceName       string
	HTTPAddr          string
	ReadHeaderTimeout time.Duration
	ReadTimeout       time.Duration
	WriteTimeout      time.Duration
	IdleTimeout       time.Duration
	ShutdownTimeout   time.Duration
	LogLevel          slog.Level

	DatabaseURL               string
	DatabaseConnectTimeout    time.Duration
	DatabaseReadinessTimeout  time.Duration
	DatabaseMaxConns          int32
	DatabaseMinConns          int32
	DatabaseMaxConnLifetime   time.Duration
	DatabaseMaxConnIdleTime   time.Duration
	DatabaseHealthCheckPeriod time.Duration

	ClerkWebhookSigningSecret  string
	ClerkWebhookProcessTimeout time.Duration
	ClerkWebhookMaxBodyBytes   int64
}

type MigrationConfig struct {
	DatabaseURL string
	Timeout     time.Duration
	LogLevel    slog.Level
}

type lookupEnvFunc func(string) (string, bool)

func Load() (Config, error) {
	return load(os.LookupEnv)
}

func LoadMigration() (MigrationConfig, error) {
	return loadMigration(os.LookupEnv)
}

func load(lookup lookupEnvFunc) (Config, error) {
	serviceName, err := nonEmptyValue(lookup, "SERVICE_NAME", defaultServiceName)
	if err != nil {
		return Config{}, err
	}

	httpAddr, err := nonEmptyValue(lookup, "HTTP_ADDR", defaultHTTPAddr)
	if err != nil {
		return Config{}, err
	}

	readHeaderTimeout, err := durationValue(lookup, "HTTP_READ_HEADER_TIMEOUT", defaultReadHeaderTimeout)
	if err != nil {
		return Config{}, err
	}
	readTimeout, err := durationValue(lookup, "HTTP_READ_TIMEOUT", defaultReadTimeout)
	if err != nil {
		return Config{}, err
	}
	writeTimeout, err := durationValue(lookup, "HTTP_WRITE_TIMEOUT", defaultWriteTimeout)
	if err != nil {
		return Config{}, err
	}
	idleTimeout, err := durationValue(lookup, "HTTP_IDLE_TIMEOUT", defaultIdleTimeout)
	if err != nil {
		return Config{}, err
	}
	shutdownTimeout, err := durationValue(lookup, "SHUTDOWN_TIMEOUT", defaultShutdownTimeout)
	if err != nil {
		return Config{}, err
	}
	logLevel, err := logLevelValue(lookup, "LOG_LEVEL", defaultLogLevel)
	if err != nil {
		return Config{}, err
	}

	databaseURL, err := requiredValue(lookup, "DATABASE_URL")
	if err != nil {
		return Config{}, err
	}
	databaseConnectTimeout, err := durationValue(lookup, "DATABASE_CONNECT_TIMEOUT", defaultDatabaseConnectTimeout)
	if err != nil {
		return Config{}, err
	}
	databaseReadinessTimeout, err := durationValue(lookup, "DATABASE_READINESS_TIMEOUT", defaultDatabaseReadinessTimeout)
	if err != nil {
		return Config{}, err
	}
	databaseMaxConns, err := int32Value(lookup, "DATABASE_MAX_CONNS", defaultDatabaseMaxConns)
	if err != nil {
		return Config{}, err
	}
	if databaseMaxConns <= 0 {
		return Config{}, fmt.Errorf("DATABASE_MAX_CONNS must be greater than zero")
	}
	databaseMinConns, err := int32Value(lookup, "DATABASE_MIN_CONNS", defaultDatabaseMinConns)
	if err != nil {
		return Config{}, err
	}
	if databaseMinConns < 0 {
		return Config{}, fmt.Errorf("DATABASE_MIN_CONNS must be greater than or equal to zero")
	}
	if databaseMinConns > databaseMaxConns {
		return Config{}, fmt.Errorf("DATABASE_MIN_CONNS must be less than or equal to DATABASE_MAX_CONNS")
	}
	databaseMaxConnLifetime, err := durationValue(lookup, "DATABASE_MAX_CONN_LIFETIME", defaultDatabaseMaxConnLifetime)
	if err != nil {
		return Config{}, err
	}
	databaseMaxConnIdleTime, err := durationValue(lookup, "DATABASE_MAX_CONN_IDLE_TIME", defaultDatabaseMaxConnIdleTime)
	if err != nil {
		return Config{}, err
	}
	databaseHealthCheckPeriod, err := durationValue(lookup, "DATABASE_HEALTH_CHECK_PERIOD", defaultDatabaseHealthCheckPeriod)
	if err != nil {
		return Config{}, err
	}

	clerkWebhookSigningSecret, err := requiredValue(lookup, "CLERK_WEBHOOK_SIGNING_SECRET")
	if err != nil {
		return Config{}, err
	}
	clerkWebhookProcessTimeout, err := durationValue(lookup, "CLERK_WEBHOOK_PROCESS_TIMEOUT", defaultClerkWebhookProcessTimeout)
	if err != nil {
		return Config{}, err
	}
	if clerkWebhookProcessTimeout > maximumClerkWebhookProcessTimeout {
		return Config{}, fmt.Errorf(
			"CLERK_WEBHOOK_PROCESS_TIMEOUT must be less than or equal to %s",
			maximumClerkWebhookProcessTimeout,
		)
	}
	if clerkWebhookProcessTimeout >= writeTimeout {
		return Config{}, fmt.Errorf("CLERK_WEBHOOK_PROCESS_TIMEOUT must be less than HTTP_WRITE_TIMEOUT")
	}
	clerkWebhookMaxBodyBytes, err := int64Value(lookup, "CLERK_WEBHOOK_MAX_BODY_BYTES", defaultClerkWebhookMaxBodyBytes)
	if err != nil {
		return Config{}, err
	}
	if clerkWebhookMaxBodyBytes <= 0 {
		return Config{}, fmt.Errorf("CLERK_WEBHOOK_MAX_BODY_BYTES must be greater than zero")
	}
	if clerkWebhookMaxBodyBytes > maximumClerkWebhookMaxBodyBytes {
		return Config{}, fmt.Errorf("CLERK_WEBHOOK_MAX_BODY_BYTES must be less than or equal to %d", maximumClerkWebhookMaxBodyBytes)
	}

	return Config{
		ServiceName:                serviceName,
		HTTPAddr:                   httpAddr,
		ReadHeaderTimeout:          readHeaderTimeout,
		ReadTimeout:                readTimeout,
		WriteTimeout:               writeTimeout,
		IdleTimeout:                idleTimeout,
		ShutdownTimeout:            shutdownTimeout,
		LogLevel:                   logLevel,
		DatabaseURL:                databaseURL,
		DatabaseConnectTimeout:     databaseConnectTimeout,
		DatabaseReadinessTimeout:   databaseReadinessTimeout,
		DatabaseMaxConns:           databaseMaxConns,
		DatabaseMinConns:           databaseMinConns,
		DatabaseMaxConnLifetime:    databaseMaxConnLifetime,
		DatabaseMaxConnIdleTime:    databaseMaxConnIdleTime,
		DatabaseHealthCheckPeriod:  databaseHealthCheckPeriod,
		ClerkWebhookSigningSecret:  clerkWebhookSigningSecret,
		ClerkWebhookProcessTimeout: clerkWebhookProcessTimeout,
		ClerkWebhookMaxBodyBytes:   clerkWebhookMaxBodyBytes,
	}, nil
}

func loadMigration(lookup lookupEnvFunc) (MigrationConfig, error) {
	databaseURL, err := requiredValue(lookup, "MIGRATION_DATABASE_URL")
	if err != nil {
		return MigrationConfig{}, err
	}
	timeout, err := durationValue(lookup, "MIGRATION_TIMEOUT", defaultMigrationTimeout)
	if err != nil {
		return MigrationConfig{}, err
	}
	logLevel, err := logLevelValue(lookup, "LOG_LEVEL", defaultLogLevel)
	if err != nil {
		return MigrationConfig{}, err
	}
	return MigrationConfig{DatabaseURL: databaseURL, Timeout: timeout, LogLevel: logLevel}, nil
}

func requiredValue(lookup lookupEnvFunc, key string) (string, error) {
	value, ok := lookup(key)
	if !ok || strings.TrimSpace(value) == "" {
		return "", fmt.Errorf("%s is required", key)
	}
	return strings.TrimSpace(value), nil
}

func nonEmptyValue(lookup lookupEnvFunc, key, fallback string) (string, error) {
	value, ok := lookup(key)
	if !ok {
		return fallback, nil
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("%s must not be empty", key)
	}
	return value, nil
}

func durationValue(lookup lookupEnvFunc, key string, fallback time.Duration) (time.Duration, error) {
	raw, ok := lookup(key)
	if !ok {
		return fallback, nil
	}
	raw = strings.TrimSpace(raw)
	value, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("%s must be a valid duration", key)
	}
	if value <= 0 {
		return 0, fmt.Errorf("%s must be greater than zero", key)
	}
	return value, nil
}

func int32Value(lookup lookupEnvFunc, key string, fallback int32) (int32, error) {
	raw, ok := lookup(key)
	if !ok {
		return fallback, nil
	}
	raw = strings.TrimSpace(raw)
	value, err := strconv.ParseInt(raw, 10, 32)
	if err != nil {
		return 0, fmt.Errorf("%s must be a valid 32-bit integer", key)
	}
	return int32(value), nil
}

func int64Value(lookup lookupEnvFunc, key string, fallback int64) (int64, error) {
	raw, ok := lookup(key)
	if !ok {
		return fallback, nil
	}
	raw = strings.TrimSpace(raw)
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s must be a valid integer", key)
	}
	return value, nil
}

func logLevelValue(lookup lookupEnvFunc, key, fallback string) (slog.Level, error) {
	raw, ok := lookup(key)
	if !ok {
		raw = fallback
	}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, fmt.Errorf("%s must not be empty", key)
	}
	switch strings.ToLower(raw) {
	case "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warn":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return 0, fmt.Errorf("%s must be one of debug, info, warn, error", key)
	}
}
