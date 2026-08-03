package postgres

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestBuildPoolConfigAppliesAllSettings(t *testing.T) {
	t.Parallel()

	cfg := Config{
		URL:               "postgres://runtime:local-only@127.0.0.1:5432/identity?sslmode=disable",
		ConnectTimeout:    7 * time.Second,
		MaxConns:          11,
		MinConns:          3,
		MaxConnLifetime:   45 * time.Minute,
		MaxConnIdleTime:   8 * time.Minute,
		HealthCheckPeriod: 90 * time.Second,
	}

	poolConfig, err := buildPoolConfig(cfg)
	if err != nil {
		t.Fatalf("build pool config: %v", err)
	}

	if poolConfig.ConnConfig.ConnectTimeout != cfg.ConnectTimeout {
		t.Fatalf("connect timeout = %s, want %s", poolConfig.ConnConfig.ConnectTimeout, cfg.ConnectTimeout)
	}
	if poolConfig.MaxConns != cfg.MaxConns {
		t.Fatalf("max conns = %d, want %d", poolConfig.MaxConns, cfg.MaxConns)
	}
	if poolConfig.MinConns != cfg.MinConns {
		t.Fatalf("min conns = %d, want %d", poolConfig.MinConns, cfg.MinConns)
	}
	if poolConfig.MaxConnLifetime != cfg.MaxConnLifetime {
		t.Fatalf("max conn lifetime = %s, want %s", poolConfig.MaxConnLifetime, cfg.MaxConnLifetime)
	}
	if poolConfig.MaxConnIdleTime != cfg.MaxConnIdleTime {
		t.Fatalf("max conn idle time = %s, want %s", poolConfig.MaxConnIdleTime, cfg.MaxConnIdleTime)
	}
	if poolConfig.HealthCheckPeriod != cfg.HealthCheckPeriod {
		t.Fatalf("health check period = %s, want %s", poolConfig.HealthCheckPeriod, cfg.HealthCheckPeriod)
	}
}

func TestBuildPoolConfigRedactsURLAndPreservesParseCause(t *testing.T) {
	t.Parallel()

	const secretURL = "postgres://runtime:super-secret@identity-postgres:5432/%zz"

	_, err := buildPoolConfig(Config{URL: secretURL})
	if err == nil {
		t.Fatal("expected error")
	}
	if err.Error() != "parse postgres pool configuration" {
		t.Fatalf("unexpected error: %q", err)
	}

	cause := errors.Unwrap(err)
	if cause == nil {
		t.Fatal("parse cause was not preserved")
	}
	if !errors.Is(err, cause) {
		t.Fatal("errors.Is cannot reach parse cause")
	}

	for _, secret := range []string{
		secretURL,
		"super-secret",
		"identity-postgres",
		"runtime",
	} {
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("database connection details leaked: %q", err)
		}
	}
}

func TestOpenRedactsDatabaseURLFromParseError(t *testing.T) {
	t.Parallel()

	const secretURL = "postgres://runtime:super-secret@identity-postgres:5432/%zz"

	_, err := Open(context.Background(), Config{
		URL:               secretURL,
		ConnectTimeout:    time.Second,
		MaxConns:          1,
		MinConns:          0,
		MaxConnLifetime:   time.Minute,
		MaxConnIdleTime:   time.Minute,
		HealthCheckPeriod: time.Minute,
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if strings.Contains(err.Error(), secretURL) ||
		strings.Contains(err.Error(), "super-secret") ||
		strings.Contains(err.Error(), "identity-postgres") {
		t.Fatalf("database connection details leaked: %q", err)
	}
	if err.Error() != "parse postgres pool configuration" {
		t.Fatalf("unexpected error: %q", err)
	}
	if errors.Unwrap(err) == nil {
		t.Fatal("expected parse cause to be preserved")
	}
}

func TestNilDBOperationsAreSafe(t *testing.T) {
	t.Parallel()

	var db *DB
	if err := db.Ping(context.Background()); err == nil {
		t.Fatal("expected ping error")
	}
	db.Close()
}
