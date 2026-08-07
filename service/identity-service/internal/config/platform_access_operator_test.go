package config

import (
	"log/slog"
	"strings"
	"testing"
	"time"
)

func TestLoadPlatformAccessOperator(t *testing.T) {
	t.Parallel()

	const operatorURL = "postgres://operator:secret@identity-postgres:5432/bridgeworks?sslmode=disable"
	cfg, err := loadPlatformAccessOperator(mapLookup(map[string]string{
		"PLATFORM_ACCESS_DATABASE_URL": operatorURL,
	}))
	if err != nil {
		t.Fatalf("load platform access operator: %v", err)
	}
	if cfg.DatabaseURL != operatorURL || cfg.DatabaseConnectTimeout != 5*time.Second ||
		cfg.CommandTimeout != 5*time.Second || cfg.LogLevel != slog.LevelInfo {
		t.Fatalf("unexpected operator config: %+v", cfg)
	}

	cfg, err = loadPlatformAccessOperator(mapLookup(map[string]string{
		"PLATFORM_ACCESS_DATABASE_URL":             operatorURL,
		"PLATFORM_ACCESS_DATABASE_CONNECT_TIMEOUT": "3s",
		"PLATFORM_ACCESS_COMMAND_TIMEOUT":          "12s",
		"LOG_LEVEL":                                "debug",
	}))
	if err != nil {
		t.Fatalf("load operator overrides: %v", err)
	}
	if cfg.DatabaseConnectTimeout != 3*time.Second || cfg.CommandTimeout != 12*time.Second || cfg.LogLevel != slog.LevelDebug {
		t.Fatalf("unexpected operator overrides: %+v", cfg)
	}
}

func TestLoadPlatformAccessOperatorRejectsInvalidValuesWithoutLeakingURL(t *testing.T) {
	t.Parallel()

	const operatorURL = "postgres://operator:secret-sensitive@identity-postgres:5432/bridgeworks?sslmode=disable"
	for _, test := range []struct {
		name string
		env  map[string]string
		want string
	}{
		{name: "missing database", env: nil, want: "PLATFORM_ACCESS_DATABASE_URL is required"},
		{name: "zero connect timeout", env: map[string]string{"PLATFORM_ACCESS_DATABASE_URL": operatorURL, "PLATFORM_ACCESS_DATABASE_CONNECT_TIMEOUT": "0s"}, want: "must be greater than zero"},
		{name: "zero command timeout", env: map[string]string{"PLATFORM_ACCESS_DATABASE_URL": operatorURL, "PLATFORM_ACCESS_COMMAND_TIMEOUT": "0s"}, want: "must be greater than zero"},
		{name: "command timeout too large", env: map[string]string{"PLATFORM_ACCESS_DATABASE_URL": operatorURL, "PLATFORM_ACCESS_COMMAND_TIMEOUT": "31s"}, want: "less than or equal to 30s"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := loadPlatformAccessOperator(mapLookup(test.env))
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
			if strings.Contains(err.Error(), operatorURL) || strings.Contains(err.Error(), "secret-sensitive") {
				t.Fatalf("operator config error leaked database credential: %q", err)
			}
		})
	}
}
