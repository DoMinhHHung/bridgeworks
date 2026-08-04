package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/DoMinhHHung/bridgeworks/service/organization-service/internal/authn"
	"github.com/DoMinhHHung/bridgeworks/service/organization-service/internal/clerkwebhook"
	"github.com/DoMinhHHung/bridgeworks/service/organization-service/internal/config"
	"github.com/DoMinhHHung/bridgeworks/service/organization-service/internal/currentorganization"
	"github.com/DoMinhHHung/bridgeworks/service/organization-service/internal/httpapi"
	"github.com/DoMinhHHung/bridgeworks/service/organization-service/internal/identityclient"
	"github.com/DoMinhHHung/bridgeworks/service/organization-service/internal/organizationid"
	"github.com/DoMinhHHung/bridgeworks/service/organization-service/internal/organizationsync"
	"github.com/DoMinhHHung/bridgeworks/service/organization-service/internal/platform"
	"github.com/DoMinhHHung/bridgeworks/service/organization-service/internal/postgres"
	"github.com/DoMinhHHung/bridgeworks/service/organization-service/internal/store"
)

func main() {
	bootstrapLogger := platform.NewLogger(os.Stdout, slog.LevelInfo)
	if err := run(); err != nil {
		bootstrapLogger.Error("organization service stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	logger := platform.NewLogger(os.Stdout, cfg.LogLevel).With("service", cfg.ServiceName)
	slog.SetDefault(logger)

	signalContext, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	database, err := postgres.Open(signalContext, postgres.Config{
		URL: cfg.DatabaseURL, ConnectTimeout: cfg.DatabaseConnectTimeout,
		MaxConns: cfg.DatabaseMaxConns, MinConns: cfg.DatabaseMinConns,
		MaxConnLifetime:   cfg.DatabaseMaxConnLifetime,
		MaxConnIdleTime:   cfg.DatabaseMaxConnIdleTime,
		HealthCheckPeriod: cfg.DatabaseHealthCheckPeriod,
	})
	if err != nil {
		return err
	}
	cleanupDatabase := true
	defer func() {
		if cleanupDatabase {
			database.Close()
		}
	}()

	identity, err := identityclient.New(cfg.IdentityServiceURL, cfg.IdentityRequestTimeout)
	if err != nil {
		return err
	}
	defer identity.CloseIdleConnections()

	verifier, err := clerkwebhook.NewVerifier(cfg.WebhookSigningSecret)
	if err != nil {
		return err
	}
	authenticate, err := authn.New(authn.Config{
		JWTKey: cfg.ClerkJWTKey, Issuer: cfg.ClerkIssuer,
		AuthorizedParties: cfg.ClerkAuthorizedParties, Leeway: cfg.ClerkAuthLeeway,
	}, logger, httpapi.RequestIDFromContext)
	if err != nil {
		return err
	}

	repository := store.New(database)
	synchronizer := organizationsync.New(repository, organizationid.UUIDV7Generator{})
	currentService := currentorganization.New(identity, repository)
	router := httpapi.NewRouter(httpapi.Dependencies{
		ServiceName: cfg.ServiceName, Logger: logger,
		Readiness: database, ReadinessTimeout: cfg.DatabaseReadinessTimeout,
		WebhookVerifier: verifier, WebhookProcessor: synchronizer,
		WebhookMaxBytes: cfg.WebhookMaxBodyBytes, WebhookTimeout: cfg.WebhookProcessTimeout,
		Authenticate: authenticate, CurrentResolver: currentService,
	})
	server := &http.Server{
		Addr: cfg.HTTPAddr, Handler: router,
		ReadHeaderTimeout: cfg.ReadHeaderTimeout,
		ReadTimeout:       cfg.ReadTimeout, WriteTimeout: cfg.WriteTimeout,
		IdleTimeout: cfg.IdleTimeout,
		ErrorLog:    slog.NewLogLogger(logger.Handler(), slog.LevelError),
	}

	serveErrors := make(chan error, 1)
	go func() {
		logger.Info("http server starting", "address", cfg.HTTPAddr)
		serveErrors <- server.ListenAndServe()
	}()

	select {
	case serveErr := <-serveErrors:
		if errors.Is(serveErr, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("serve HTTP: %w", serveErr)
	case <-signalContext.Done():
		logger.Info("shutdown signal received")
	}

	shutdownContext, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()
	if err := server.Shutdown(shutdownContext); err != nil {
		_ = server.Close()
		return fmt.Errorf("shutdown HTTP server: %w", err)
	}
	select {
	case serveErr := <-serveErrors:
		if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			return fmt.Errorf("serve HTTP during shutdown: %w", serveErr)
		}
	case <-time.After(cfg.ShutdownTimeout):
		return errors.New("http server did not stop before shutdown timeout")
	}

	database.Close()
	cleanupDatabase = false
	identity.CloseIdleConnections()
	logger.Info("http server stopped")
	return nil
}
