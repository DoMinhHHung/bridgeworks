package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"os"

	"github.com/DoMinhHHung/bridgeworks/service/organization-service/internal/config"
	"github.com/DoMinhHHung/bridgeworks/service/organization-service/internal/platform/safeerr"
	organizationmigrations "github.com/DoMinhHHung/bridgeworks/service/organization-service/migrations"
	"github.com/pressly/goose/v3"

	_ "github.com/jackc/pgx/v5/stdlib"
)

const migrationTableName = "organization.goose_db_version"

func main() {
	bootstrapLogger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(context.Background(), os.Args[1:]); err != nil {
		bootstrapLogger.Error("organization migration failed", "error", err)
		os.Exit(1)
	}
}

func run(parent context.Context, args []string) error {
	command, err := parseCommand(args)
	if err != nil {
		return err
	}
	cfg, err := config.LoadMigration()
	if err != nil {
		return err
	}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: cfg.LogLevel})).With("component", "organization-migrate")
	ctx, cancel := context.WithTimeout(parent, cfg.Timeout)
	defer cancel()

	db, err := sql.Open("pgx", cfg.DatabaseURL)
	if err != nil {
		return safeerr.Wrap("open migration database", err)
	}
	defer func() {
		if closeErr := db.Close(); closeErr != nil {
			logger.Warn("migration database close failed")
		}
	}()
	if err := db.PingContext(ctx); err != nil {
		return safeerr.Wrap("ping migration database", err)
	}
	if _, err := db.ExecContext(ctx, "create schema if not exists organization"); err != nil {
		return safeerr.Wrap("ensure migration schema", err)
	}
	provider, err := goose.NewProvider(
		goose.DialectPostgres,
		db,
		organizationmigrations.FS,
		goose.WithTableName(migrationTableName),
		goose.WithSlog(logger),
	)
	if err != nil {
		return safeerr.Wrap("initialize migration provider", err)
	}

	switch command {
	case "up":
		results, applyErr := provider.Up(ctx)
		if applyErr != nil {
			return safeerr.Wrap("apply organization migrations", applyErr)
		}
		logger.Info("organization migrations applied", "count", len(results))
	case "status":
		statuses, statusErr := provider.Status(ctx)
		if statusErr != nil {
			return safeerr.Wrap("read organization migration status", statusErr)
		}
		for _, status := range statuses {
			logger.Info("organization migration status",
				"version", status.Source.Version,
				"path", status.Source.Path,
				"state", string(status.State),
				"applied_at", status.AppliedAt,
			)
		}
	case "version":
		version, versionErr := provider.GetDBVersion(ctx)
		if versionErr != nil {
			return safeerr.Wrap("read organization migration version", versionErr)
		}
		logger.Info("organization migration version", "version", version)
	default:
		return fmt.Errorf("unsupported migration command %q", command)
	}
	return nil
}

func parseCommand(args []string) (string, error) {
	if len(args) != 1 {
		return "", errors.New("migration command must be exactly one of: up, status, version")
	}
	switch args[0] {
	case "up", "status", "version":
		return args[0], nil
	default:
		return "", errors.New("migration command must be exactly one of: up, status, version")
	}
}
