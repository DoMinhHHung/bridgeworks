package postgres

import (
	"testing"
	"time"
)

func TestBuildPoolConfigAppliesRuntimeLimits(t *testing.T) {
	cfg := Config{URL: "postgres://user:pass@localhost:5432/db?sslmode=disable", ConnectTimeout: 3*time.Second, MaxConns: 7, MinConns: 2, MaxConnLifetime: 20*time.Minute, MaxConnIdleTime: 4*time.Minute, HealthCheckPeriod: 45*time.Second}
	pool, err := buildPoolConfig(cfg); if err != nil { t.Fatal(err) }
	if pool.ConnConfig.ConnectTimeout != cfg.ConnectTimeout || pool.MaxConns != cfg.MaxConns || pool.MinConns != cfg.MinConns || pool.MaxConnLifetime != cfg.MaxConnLifetime || pool.MaxConnIdleTime != cfg.MaxConnIdleTime || pool.HealthCheckPeriod != cfg.HealthCheckPeriod { t.Fatalf("pool config not applied: %+v", pool) }
}
