package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/DoMinhHHung/bridgeworks/service/identity-service/internal/platform/safeerr"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Config struct {
	URL               string
	ConnectTimeout    time.Duration
	MaxConns          int32
	MinConns          int32
	MaxConnLifetime   time.Duration
	MaxConnIdleTime   time.Duration
	HealthCheckPeriod time.Duration
}

type DB struct {
	pool *pgxpool.Pool
}

func Open(ctx context.Context, cfg Config) (*DB, error) {
	poolConfig, err := buildPoolConfig(cfg)
	if err != nil {
		return nil, err
	}

	connectContext, cancel := context.WithTimeout(ctx, cfg.ConnectTimeout)
	defer cancel()

	pool, err := pgxpool.NewWithConfig(connectContext, poolConfig)
	if err != nil {
		return nil, safeerr.Wrap("create postgres connection pool", err)
	}

	if err := pool.Ping(connectContext); err != nil {
		pool.Close()
		return nil, safeerr.Wrap("ping postgres during startup", err)
	}

	return &DB{pool: pool}, nil
}

func buildPoolConfig(cfg Config) (*pgxpool.Config, error) {
	poolConfig, err := pgxpool.ParseConfig(cfg.URL)
	if err != nil {
		return nil, safeerr.Wrap("parse postgres pool configuration", err)
	}

	poolConfig.ConnConfig.ConnectTimeout = cfg.ConnectTimeout
	poolConfig.MaxConns = cfg.MaxConns
	poolConfig.MinConns = cfg.MinConns
	poolConfig.MaxConnLifetime = cfg.MaxConnLifetime
	poolConfig.MaxConnIdleTime = cfg.MaxConnIdleTime
	poolConfig.HealthCheckPeriod = cfg.HealthCheckPeriod

	return poolConfig, nil
}

func (db *DB) Ping(ctx context.Context) error {
	if db == nil || db.pool == nil {
		return errors.New("postgres pool is not initialized")
	}
	if err := db.pool.Ping(ctx); err != nil {
		return safeerr.Wrap("postgres ping failed", err)
	}

	return nil
}

func (db *DB) Check(ctx context.Context) error {
	return db.Ping(ctx)
}

func (db *DB) Close() {
	if db == nil || db.pool == nil {
		return
	}
	db.pool.Close()
}
