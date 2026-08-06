package config

import (
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"
)

const (
	defaultServiceName                   = "organization-service"
	defaultHTTPAddr                      = ":8080"
	defaultReadHeaderTimeout             = 5 * time.Second
	defaultReadTimeout                   = 15 * time.Second
	defaultWriteTimeout                  = 15 * time.Second
	defaultIdleTimeout                   = 60 * time.Second
	defaultShutdownTimeout               = 10 * time.Second
	defaultLogLevel                      = "info"
	defaultDatabaseConnectTimeout        = 5 * time.Second
	defaultDatabaseReadinessTimeout      = 2 * time.Second
	defaultDatabaseMaxConns              = int32(5)
	defaultDatabaseMinConns              = int32(0)
	defaultDatabaseMaxConnLifetime       = 30 * time.Minute
	defaultDatabaseMaxConnIdleTime       = 5 * time.Minute
	defaultDatabaseHealthCheckPeriod     = time.Minute
	defaultWebhookProcessTimeout         = 5 * time.Second
	maximumWebhookProcessTimeout         = 8 * time.Second
	defaultWebhookMaxBodyBytes           = int64(1_048_576)
	maximumWebhookMaxBodyBytes           = int64(5 * 1024 * 1024)
	defaultClerkAuthLeeway               = 5 * time.Second
	maximumClerkAuthLeeway               = 30 * time.Second
	defaultIdentityServiceURL            = "http://identity-service:8080"
	defaultIdentityServiceAuthMode       = "none"
	IdentityServiceAuthModeNone          = "none"
	IdentityServiceAuthModeGoogleIDToken = "google-id-token"
	defaultIdentityRequestTimeout        = 2 * time.Second
	maximumIdentityRequestTimeout        = 5 * time.Second
	defaultMigrationTimeout              = time.Minute
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

	ClerkJWTKey            string
	ClerkIssuer            string
	ClerkAuthorizedParties []string
	ClerkAuthLeeway        time.Duration

	WebhookSigningSecret  string
	WebhookProcessTimeout time.Duration
	WebhookMaxBodyBytes   int64

	IdentityServiceURL      string
	IdentityServiceAuthMode string
	IdentityServiceAudience string
	IdentityRequestTimeout  time.Duration
}

type MigrationConfig struct {
	DatabaseURL string
	Timeout     time.Duration
	LogLevel    slog.Level
}

type lookupEnvFunc func(string) (string, bool)

func Load() (Config, error)                   { return load(os.LookupEnv) }
func LoadMigration() (MigrationConfig, error) { return loadMigration(os.LookupEnv) }

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
	shutdownTimeout, err := durationValue(lookup, "HTTP_SHUTDOWN_TIMEOUT", defaultShutdownTimeout)
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
	if databaseMinConns < 0 || databaseMinConns > databaseMaxConns {
		return Config{}, fmt.Errorf("DATABASE_MIN_CONNS must be between zero and DATABASE_MAX_CONNS")
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

	clerkJWTKey, err := requiredValue(lookup, "CLERK_JWT_KEY")
	if err != nil {
		return Config{}, err
	}
	clerkIssuer, err := originValue(lookup, "CLERK_ISSUER")
	if err != nil {
		return Config{}, err
	}
	clerkAuthorizedParties, err := authorizedPartiesValue(lookup)
	if err != nil {
		return Config{}, err
	}
	clerkAuthLeeway, err := durationValue(lookup, "CLERK_AUTH_LEEWAY", defaultClerkAuthLeeway)
	if err != nil {
		return Config{}, err
	}
	if clerkAuthLeeway > maximumClerkAuthLeeway {
		return Config{}, fmt.Errorf("CLERK_AUTH_LEEWAY must be less than or equal to %s", maximumClerkAuthLeeway)
	}

	webhookSigningSecret, err := requiredValue(lookup, "CLERK_ORGANIZATION_WEBHOOK_SIGNING_SECRET")
	if err != nil {
		return Config{}, err
	}
	webhookProcessTimeout, err := durationValue(lookup, "CLERK_ORGANIZATION_WEBHOOK_PROCESS_TIMEOUT", defaultWebhookProcessTimeout)
	if err != nil {
		return Config{}, err
	}
	if webhookProcessTimeout > maximumWebhookProcessTimeout {
		return Config{}, fmt.Errorf("CLERK_ORGANIZATION_WEBHOOK_PROCESS_TIMEOUT must be less than or equal to %s", maximumWebhookProcessTimeout)
	}
	if webhookProcessTimeout >= writeTimeout {
		return Config{}, fmt.Errorf("CLERK_ORGANIZATION_WEBHOOK_PROCESS_TIMEOUT must be less than HTTP_WRITE_TIMEOUT")
	}
	webhookMaxBodyBytes, err := int64Value(lookup, "CLERK_ORGANIZATION_WEBHOOK_MAX_BODY_BYTES", defaultWebhookMaxBodyBytes)
	if err != nil {
		return Config{}, err
	}
	if webhookMaxBodyBytes <= 0 || webhookMaxBodyBytes > maximumWebhookMaxBodyBytes {
		return Config{}, fmt.Errorf("CLERK_ORGANIZATION_WEBHOOK_MAX_BODY_BYTES must be between 1 and %d", maximumWebhookMaxBodyBytes)
	}

	identityServiceURL, err := serviceURLValue(lookup, "IDENTITY_SERVICE_URL", defaultIdentityServiceURL)
	if err != nil {
		return Config{}, err
	}
	identityRequestTimeout, err := durationValue(lookup, "IDENTITY_SERVICE_REQUEST_TIMEOUT", defaultIdentityRequestTimeout)
	if err != nil {
		return Config{}, err
	}
	if identityRequestTimeout > maximumIdentityRequestTimeout {
		return Config{}, fmt.Errorf("IDENTITY_SERVICE_REQUEST_TIMEOUT must be less than or equal to %s", maximumIdentityRequestTimeout)
	}

	identityServiceAuthMode, err := nonEmptyValue(
		lookup,
		"IDENTITY_SERVICE_AUTH_MODE",
		defaultIdentityServiceAuthMode,
	)
	if err != nil {
		return Config{}, err
	}
	identityServiceAuthMode = strings.ToLower(identityServiceAuthMode)

	identityServiceAudience := ""
	switch identityServiceAuthMode {
	case IdentityServiceAuthModeNone:
		if rawAudience, ok := lookup("IDENTITY_SERVICE_AUDIENCE"); ok &&
			strings.TrimSpace(rawAudience) != "" {
			return Config{}, fmt.Errorf(
				"IDENTITY_SERVICE_AUDIENCE requires IDENTITY_SERVICE_AUTH_MODE=google-id-token",
			)
		}
	case IdentityServiceAuthModeGoogleIDToken:
		identityServiceAudience, err = originValue(
			lookup,
			"IDENTITY_SERVICE_AUDIENCE",
		)
		if err != nil {
			return Config{}, err
		}
	default:
		return Config{}, fmt.Errorf(
			"IDENTITY_SERVICE_AUTH_MODE must be one of none, google-id-token",
		)
	}

	return Config{
		ServiceName: serviceName, HTTPAddr: httpAddr,
		ReadHeaderTimeout: readHeaderTimeout, ReadTimeout: readTimeout,
		WriteTimeout: writeTimeout, IdleTimeout: idleTimeout, ShutdownTimeout: shutdownTimeout,
		LogLevel:    logLevel,
		DatabaseURL: databaseURL, DatabaseConnectTimeout: databaseConnectTimeout,
		DatabaseReadinessTimeout: databaseReadinessTimeout, DatabaseMaxConns: databaseMaxConns,
		DatabaseMinConns: databaseMinConns, DatabaseMaxConnLifetime: databaseMaxConnLifetime,
		DatabaseMaxConnIdleTime: databaseMaxConnIdleTime, DatabaseHealthCheckPeriod: databaseHealthCheckPeriod,
		ClerkJWTKey: clerkJWTKey, ClerkIssuer: clerkIssuer,
		ClerkAuthorizedParties: clerkAuthorizedParties, ClerkAuthLeeway: clerkAuthLeeway,
		WebhookSigningSecret: webhookSigningSecret, WebhookProcessTimeout: webhookProcessTimeout,
		WebhookMaxBodyBytes:     webhookMaxBodyBytes,
		IdentityServiceURL:      identityServiceURL,
		IdentityServiceAuthMode: identityServiceAuthMode,
		IdentityServiceAudience: identityServiceAudience,
		IdentityRequestTimeout:  identityRequestTimeout,
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
