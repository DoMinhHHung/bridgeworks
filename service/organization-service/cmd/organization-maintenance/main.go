package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/DoMinhHHung/bridgeworks/service/organization-service/internal/clerkorganizationadmin"
	"github.com/DoMinhHHung/bridgeworks/service/organization-service/internal/config"
	"github.com/DoMinhHHung/bridgeworks/service/organization-service/internal/organizationmaintenance"
	"github.com/DoMinhHHung/bridgeworks/service/organization-service/internal/platform"
	"github.com/DoMinhHHung/bridgeworks/service/organization-service/internal/postgres"
	"github.com/DoMinhHHung/bridgeworks/service/organization-service/internal/store"
)

const usageMessage = "usage: organization-maintenance <prune-consumed-invitations|reconcile-removals|status>"

func main() {
	logger := platform.NewLogger(os.Stdout, slog.LevelInfo).With("component", "organization-maintenance")
	if err := run(context.Background(), os.Args[1:]); err != nil {
		logger.Error("organization maintenance command failed", "error", err)
		os.Exit(1)
	}
}

func run(parent context.Context, args []string) error {
	if len(args) != 1 {
		return errors.New(usageMessage)
	}
	command := strings.TrimSpace(args[0])
	if command != "prune-consumed-invitations" && command != "reconcile-removals" && command != "status" {
		return errors.New(usageMessage)
	}
	cfg, err := config.LoadMaintenance()
	if err != nil {
		return err
	}
	logger := platform.NewLogger(os.Stdout, cfg.LogLevel).With("component", "organization-maintenance")
	database, err := postgres.Open(parent, postgres.Config{
		URL:               cfg.DatabaseURL,
		ConnectTimeout:    cfg.DatabaseConnectTimeout,
		MaxConns:          2,
		MinConns:          0,
		MaxConnLifetime:   10 * time.Minute,
		MaxConnIdleTime:   time.Minute,
		HealthCheckPeriod: time.Minute,
	})
	if err != nil {
		return err
	}
	defer database.Close()

	ctx, cancel := context.WithTimeout(parent, cfg.CommandTimeout)
	defer cancel()
	repository := store.New(database)

	switch command {
	case "prune-consumed-invitations":
		service := organizationmaintenance.New(repository, nil)
		count, err := service.PruneConsumedInvitations(ctx, cfg.InvitationRetention, cfg.BatchSize, time.Now())
		if err != nil {
			return err
		}
		logger.Info("organization maintenance completed", "command", command, "pruned", count)
		return nil
	case "reconcile-removals":
		providerCfg, err := config.LoadMaintenanceProvider()
		if err != nil {
			return err
		}
		provider, err := clerkorganizationadmin.New(providerCfg.SecretKey, providerCfg.APIURL, providerCfg.Timeout)
		if err != nil {
			return err
		}
		defer provider.CloseIdleConnections()
		service := organizationmaintenance.New(repository, provider)
		result, err := service.ReconcileRemovals(ctx, cfg.RemovalReconcileAfter, cfg.BatchSize, time.Now())
		if err != nil {
			return err
		}
		logger.Info(
			"organization maintenance completed",
			"command", command,
			"examined", result.Examined,
			"finalized", result.Finalized,
			"unresolved", result.Unresolved,
		)
		return nil
	case "status":
		if err := database.Ping(ctx); err != nil {
			return err
		}
		logger.Info("organization maintenance status ok", "command", command)
		return nil
	default:
		return errors.New(usageMessage)
	}
}
