package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"os"

	"github.com/DoMinhHHung/bridgeworks/service/identity-service/internal/config"
	identitymigrations "github.com/DoMinhHHung/bridgeworks/service/identity-service/migrations"
	"github.com/pressly/goose/v3"

	_ "github.com/jackc/pgx/v5/stdlib"
)

const migrationTableName = "app.goose_db_version"

func main() {
	bootstrapLogger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(context.Background(), os.Args[1:], bootstrapLogger); err != nil {
		bootstrapLogger.Error("identity migration failed", "error", err)
		os.Exit(1)
	}
}

func run(parent context.Context, args []string, bootstrapLogger *slog.Logger) error {
	command, err := parseCommand(args)
	if err != nil {
		return err
	}

	cfg, err := config.LoadMigration()
	if err != nil {
		return err
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: cfg.LogLevel})).With("component", "identity-migrate")

	ctx, cancel := context.WithTimeout(parent, cfg.Timeout)
	defer cancel()

	db, err := sql.Open("pgx", cfg.DatabaseURL)
	if err != nil {
		return errors.New("open migration database")
	}
	defer db.Close()

	if err := db.PingContext(ctx); err != nil {
		return errors.New("ping migration database")
	}
	if _, err := db.ExecContext(ctx, "create schema if not exists app"); err != nil {
		return errors.New("ensure migration schema")
	}

	provider, err := goose.NewProvider(
		goose.DialectPostgres,
		db,
		identitymigrations.FS,
		goose.WithTableName(migrationTableName),
		goose.WithSlog(logger),
	)
	if err != nil {
		return errors.New("initialize migration provider")
	}

	switch command {
	case "up":
		results, err := provider.Up(ctx)
		if err != nil {
			return errors.New("apply identity migrations")
		}
		logger.Info("identity migrations applied", "count", len(results))
	case "status":
		statuses, err := provider.Status(ctx)
		if err != nil {
			return errors.New("read identity migration status")
		}
		for _, status := range statuses {
			logger.Info(
				"identity migration status",
				"version", status.Source.Version,
				"path", status.Source.Path,
				"state", status.State.String(),
				"applied_at", status.AppliedAt,
			)
		}
	case "version":
		version, err := provider.GetDBVersion(ctx)
		if err != nil {
			return errors.New("read identity migration version")
		}
		logger.Info("identity migration version", "version", version)
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
