package config

import (
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"
)

const (
	defaultServiceName       = "identity-service"
	defaultHTTPAddr          = ":8080"
	defaultReadHeaderTimeout = 5 * time.Second
	defaultReadTimeout       = 15 * time.Second
	defaultWriteTimeout      = 15 * time.Second
	defaultIdleTimeout       = 60 * time.Second
	defaultShutdownTimeout   = 10 * time.Second
	defaultLogLevel          = "info"
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
}

type lookupEnvFunc func(string) (string, bool)

func Load() (Config, error) {
	return load(os.LookupEnv)
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

	return Config{
		ServiceName:       serviceName,
		HTTPAddr:          httpAddr,
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       readTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
		ShutdownTimeout:   shutdownTimeout,
		LogLevel:          logLevel,
	}, nil
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
		return 0, fmt.Errorf("%s must be a valid duration: %w", key, err)
	}
	if value <= 0 {
		return 0, fmt.Errorf("%s must be greater than zero", key)
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

	var level slog.Level
	if err := level.UnmarshalText([]byte(raw)); err != nil {
		return 0, fmt.Errorf("%s must be one of debug, info, warn, error: %w", key, err)
	}

	return level, nil
}
