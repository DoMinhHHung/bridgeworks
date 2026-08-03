package config

import (
	"log/slog"
	"strings"
	"testing"
	"time"
)

func TestLoadDefaults(t *testing.T) {
	cfg, err := load(mapLookup(nil))
	if err != nil {
		t.Fatalf("load() error = %v", err)
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
	cfg, err := load(mapLookup(map[string]string{
		"SERVICE_NAME":             "custom-identity",
		"HTTP_ADDR":                ":9090",
		"HTTP_READ_HEADER_TIMEOUT": "1s",
		"HTTP_READ_TIMEOUT":        "2s",
		"HTTP_WRITE_TIMEOUT":       "3s",
		"HTTP_IDLE_TIMEOUT":        "4s",
		"SHUTDOWN_TIMEOUT":         "5s",
		"LOG_LEVEL":                "debug",
	}))
	if err != nil {
		t.Fatalf("load() error = %v", err)
	}

	if cfg.ServiceName != "custom-identity" || cfg.HTTPAddr != ":9090" {
		t.Fatalf("config = %+v", cfg)
	}
	if cfg.ReadHeaderTimeout != time.Second || cfg.ShutdownTimeout != 5*time.Second {
		t.Fatalf("config = %+v", cfg)
	}
	if cfg.LogLevel != slog.LevelDebug {
		t.Fatalf("LogLevel = %s", cfg.LogLevel)
	}
}

func TestLoadRejectsInvalidDuration(t *testing.T) {
	_, err := load(mapLookup(map[string]string{"HTTP_READ_TIMEOUT": "not-a-duration"}))
	if err == nil || !strings.Contains(err.Error(), "HTTP_READ_TIMEOUT") {
		t.Fatalf("load() error = %v", err)
	}
}

func TestLoadRejectsNonPositiveDuration(t *testing.T) {
	_, err := load(mapLookup(map[string]string{"SHUTDOWN_TIMEOUT": "0s"}))
	if err == nil || !strings.Contains(err.Error(), "greater than zero") {
		t.Fatalf("load() error = %v", err)
	}
}

func TestLoadRejectsInvalidLogLevel(t *testing.T) {
	_, err := load(mapLookup(map[string]string{"LOG_LEVEL": "verbose"}))
	if err == nil || !strings.Contains(err.Error(), "LOG_LEVEL") {
		t.Fatalf("load() error = %v", err)
	}
}

func TestLoadRejectsEmptyRequiredStrings(t *testing.T) {
	for _, key := range []string{"SERVICE_NAME", "HTTP_ADDR"} {
		t.Run(key, func(t *testing.T) {
			_, err := load(mapLookup(map[string]string{key: "   "}))
			if err == nil || !strings.Contains(err.Error(), key) {
				t.Fatalf("load() error = %v", err)
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
