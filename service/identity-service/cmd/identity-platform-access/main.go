package main

import (
	"context"
	"errors"
	"flag"
	"io"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/DoMinhHHung/bridgeworks/service/identity-service/internal/config"
	"github.com/DoMinhHHung/bridgeworks/service/identity-service/internal/platform"
	"github.com/DoMinhHHung/bridgeworks/service/identity-service/internal/platformaccess"
	"github.com/DoMinhHHung/bridgeworks/service/identity-service/internal/platformaccessoperator"
	"github.com/DoMinhHHung/bridgeworks/service/identity-service/internal/postgres"
	"github.com/DoMinhHHung/bridgeworks/service/identity-service/internal/store"
)

const usageMessage = "usage: identity-platform-access <grant|revoke|status> --id-user <bridgeworks-id-user>"

type command struct {
	name   string
	idUser string
}

func main() {
	bootstrapLogger := platform.NewLogger(os.Stdout, slog.LevelInfo).With("component", "identity-platform-access")
	if err := run(context.Background(), os.Args[1:]); err != nil {
		bootstrapLogger.Error("platform access command failed", "error", err)
		os.Exit(1)
	}
}

func run(parent context.Context, args []string) error {
	parsed, err := parseCommand(args)
	if err != nil {
		return err
	}
	cfg, err := config.LoadPlatformAccessOperator()
	if err != nil {
		return err
	}
	logger := platform.NewLogger(os.Stdout, cfg.LogLevel).With("component", "identity-platform-access")

	database, err := postgres.Open(parent, postgres.Config{
		URL:               cfg.DatabaseURL,
		ConnectTimeout:    cfg.DatabaseConnectTimeout,
		MaxConns:          1,
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

	service := platformaccessoperator.New(store.New(database))
	var status platformaccessoperator.Status
	switch parsed.name {
	case "grant":
		status, err = service.Grant(ctx, parsed.idUser)
	case "revoke":
		status, err = service.Revoke(ctx, parsed.idUser)
	case "status":
		status, err = service.Status(ctx, parsed.idUser)
	default:
		return errors.New(usageMessage)
	}
	if err != nil {
		return err
	}

	logger.Info(
		"platform access command completed",
		"command", parsed.name,
		"id_user", parsed.idUser,
		"role", platformaccess.RolePlatformAdmin,
		"assigned", status.Assigned,
		"active", status.Active,
	)
	return nil
}

func parseCommand(args []string) (command, error) {
	if len(args) == 0 {
		return command{}, errors.New(usageMessage)
	}
	name := strings.TrimSpace(args[0])
	if name != "grant" && name != "revoke" && name != "status" {
		return command{}, errors.New(usageMessage)
	}

	flags := flag.NewFlagSet(name, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	idUser := flags.String("id-user", "", "public BridgeWorks user ID")
	if err := flags.Parse(args[1:]); err != nil || flags.NArg() != 0 {
		return command{}, errors.New(usageMessage)
	}
	value := strings.TrimSpace(*idUser)
	if !validIDUser(value) {
		return command{}, errors.New("id_user must match the BridgeWorks public user ID format")
	}
	return command{name: name, idUser: value}, nil
}

func validIDUser(value string) bool {
	if len(value) != 14 || !strings.HasPrefix(value, "bw") {
		return false
	}
	for _, char := range value[2:] {
		if char < '0' || char > '9' {
			return false
		}
	}
	return true
}
