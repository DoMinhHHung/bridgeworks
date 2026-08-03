package config

import (
	"log/slog"
	"strings"
	"testing"
	"time"
)

func TestLoadDefaults(t *testing.T) {
	t.Parallel()

	cfg, err := load(mapLookup(nil))
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
}

func TestLoadOverrides(t *testing.T) {
	t.Parallel()

	cfg, err := load(mapLookup(map[string]string{
		"SERVICE_NAME":             "identity-test",
		"HTTP_ADDR":                "127.0.0.1:9090",
		"HTTP_READ_HEADER_TIMEOUT": "1s",
		"HTTP_READ_TIMEOUT":        "2s",
		"HTTP_WRITE_TIMEOUT":       "3s",
		"HTTP_IDLE_TIMEOUT":        "4s",
		"SHUTDOWN_TIMEOUT":         "5s",
		"LOG_LEVEL":                "DEBUG",
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
}

func TestLoadRejectsInvalidValues(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		env       map[string]string
		wantError string
	}{
		{name: "empty service name", env: map[string]string{"SERVICE_NAME": "  "}, wantError: "SERVICE_NAME must not be empty"},
		{name: "empty address", env: map[string]string{"HTTP_ADDR": ""}, wantError: "HTTP_ADDR must not be empty"},
		{name: "invalid duration", env: map[string]string{"HTTP_READ_TIMEOUT": "soon"}, wantError: "HTTP_READ_TIMEOUT must be a valid duration"},
		{name: "zero duration", env: map[string]string{"SHUTDOWN_TIMEOUT": "0s"}, wantError: "SHUTDOWN_TIMEOUT must be greater than zero"},
		{name: "offset info log level", env: map[string]string{"LOG_LEVEL": "INFO+2"}, wantError: "LOG_LEVEL must be one of debug, info, warn, error"},
		{name: "offset error log level", env: map[string]string{"LOG_LEVEL": "ERROR-8"}, wantError: "LOG_LEVEL must be one of debug, info, warn, error"},
		{name: "verbose log level", env: map[string]string{"LOG_LEVEL": "verbose"}, wantError: "LOG_LEVEL must be one of debug, info, warn, error"},
		{name: "empty log level", env: map[string]string{"LOG_LEVEL": ""}, wantError: "LOG_LEVEL must not be empty"},
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
		})
	}
}

func mapLookup(values map[string]string) lookupEnvFunc {
	return func(key string) (string, bool) {
		value, ok := values[key]
		return value, ok
	}
}
