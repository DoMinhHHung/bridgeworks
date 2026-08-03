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

func load(lookupEnv lookupEnvFunc) (Config, error) {
	cfg := Config{
		ServiceName:       envOrDefault(lookupEnv, "SERVICE_NAME", defaultServiceName),
		HTTPAddr:          envOrDefault(lookupEnv, "HTTP_ADDR", defaultHTTPAddr),
		ReadHeaderTimeout: defaultReadHeaderTimeout,
		ReadTimeout:       defaultReadTimeout,
		WriteTimeout:      defaultWriteTimeout,
		IdleTimeout:       defaultIdleTimeout,
		ShutdownTimeout:   defaultShutdownTimeout,
		LogLevel:          slog.LevelInfo,
	}

	if strings.TrimSpace(cfg.ServiceName) == "" {
		return Config{}, fmt.Errorf("SERVICE_NAME must not be empty")
	}
	if strings.TrimSpace(cfg.HTTPAddr) == "" {
		return Config{}, fmt.Errorf("HTTP_ADDR must not be empty")
	}

	var err error
	if cfg.ReadHeaderTimeout, err = durationFromEnv(lookupEnv, "HTTP_READ_HEADER_TIMEOUT", cfg.ReadHeaderTimeout); err != nil {
		return Config{}, err
	}
	if cfg.ReadTimeout, err = durationFromEnv(lookupEnv, "HTTP_READ_TIMEOUT", cfg.ReadTimeout); err != nil {
		return Config{}, err
	}
	if cfg.WriteTimeout, err = durationFromEnv(lookupEnv, "HTTP_WRITE_TIMEOUT", cfg.WriteTimeout); err != nil {
		return Config{}, err
	}
	if cfg.IdleTimeout, err = durationFromEnv(lookupEnv, "HTTP_IDLE_TIMEOUT", cfg.IdleTimeout); err != nil {
		return Config{}, err
	}
	if cfg.ShutdownTimeout, err = durationFromEnv(lookupEnv, "SHUTDOWN_TIMEOUT", cfg.ShutdownTimeout); err != nil {
		return Config{}, err
	}
	if cfg.LogLevel, err = logLevelFromEnv(lookupEnv, "LOG_LEVEL", cfg.LogLevel); err != nil {
		return Config{}, err
	}

	return cfg, nil
}

func envOrDefault(lookupEnv lookupEnvFunc, key, fallback string) string {
	value, ok := lookupEnv(key)
	if !ok {
		return fallback
	}
	return value
}

func durationFromEnv(lookupEnv lookupEnvFunc, key string, fallback time.Duration) (time.Duration, error) {
	value, ok := lookupEnv(key)
	if !ok {
		return fallback, nil
	}

	duration, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("%s must be a valid duration, got %q: %w", key, value, err)
	}
	if duration <= 0 {
		return 0, fmt.Errorf("%s must be greater than zero, got %q", key, value)
	}
	return duration, nil
}

func logLevelFromEnv(lookupEnv lookupEnvFunc, key string, fallback slog.Level) (slog.Level, error) {
	value, ok := lookupEnv(key)
	if !ok {
		return fallback, nil
	}

	var level slog.Level
	if err := level.UnmarshalText([]byte(strings.ToUpper(strings.TrimSpace(value)))); err != nil {
		return 0, fmt.Errorf("%s must be one of debug, info, warn, error, got %q: %w", key, value, err)
	}
	return level, nil
}
