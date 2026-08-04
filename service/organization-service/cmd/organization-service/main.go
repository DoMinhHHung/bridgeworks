package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/DoMinhHHung/bridgeworks/service/organization-service/internal/authn"
	"github.com/DoMinhHHung/bridgeworks/service/organization-service/internal/clerkwebhook"
	"github.com/DoMinhHHung/bridgeworks/service/organization-service/internal/config"
	"github.com/DoMinhHHung/bridgeworks/service/organization-service/internal/currentorganization"
	"github.com/DoMinhHHung/bridgeworks/service/organization-service/internal/httpapi"
	"github.com/DoMinhHHung/bridgeworks/service/organization-service/internal/identityclient"
	"github.com/DoMinhHHung/bridgeworks/service/organization-service/internal/observability"
	"github.com/DoMinhHHung/bridgeworks/service/organization-service/internal/organizationid"
	"github.com/DoMinhHHung/bridgeworks/service/organization-service/internal/organizationsync"
	"github.com/DoMinhHHung/bridgeworks/service/organization-service/internal/platform"
	"github.com/DoMinhHHung/bridgeworks/service/organization-service/internal/postgres"
	"github.com/DoMinhHHung/bridgeworks/service/organization-service/internal/store"
)

type serveResult struct {
	name string
	err  error
}

type shutdownServer interface {
	Shutdown(context.Context) error
	Close() error
}

var _ httpapi.EventOutcomeProcessor = (*organizationsync.Service)(nil)

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
	metricsAddr, err := config.LoadMetricsAddr(cfg.HTTPAddr)
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

	metrics, err := observability.New(cfg.ServiceName, func() observability.PoolStat {
		return database.Stat()
	})
	if err != nil {
		return err
	}

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
		ServiceName: cfg.ServiceName, Logger: logger, Metrics: metrics,
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
	metricsServer := &http.Server{
		Addr: metricsAddr, Handler: metrics.Handler(),
		ReadHeaderTimeout: cfg.ReadHeaderTimeout,
		ReadTimeout:       cfg.ReadTimeout, WriteTimeout: cfg.WriteTimeout,
		IdleTimeout: cfg.IdleTimeout,
		ErrorLog:    slog.NewLogLogger(logger.Handler(), slog.LevelError),
	}

	applicationListener, err := net.Listen("tcp", cfg.HTTPAddr)
	if err != nil {
		return fmt.Errorf("listen application HTTP: %w", err)
	}
	defer func() { _ = applicationListener.Close() }()
	metricsListener, err := net.Listen("tcp", metricsAddr)
	if err != nil {
		return fmt.Errorf("listen private metrics HTTP: %w", err)
	}
	defer func() { _ = metricsListener.Close() }()

	serveErrors := make(chan serveResult, 2)
	go serve("application", server, applicationListener, logger, serveErrors)
	go serve("metrics", metricsServer, metricsListener, logger, serveErrors)

	var serveErr error
	select {
	case result := <-serveErrors:
		if result.err != nil && !errors.Is(result.err, http.ErrServerClosed) {
			serveErr = fmt.Errorf("serve %s HTTP: %w", result.name, result.err)
		} else {
			serveErr = fmt.Errorf("%s HTTP server stopped unexpectedly", result.name)
		}
	case <-signalContext.Done():
		logger.Info("shutdown signal received")
	}

	shutdownContext, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()
	shutdownErr := shutdownRuntime(shutdownContext, metricsServer, server, database.Close)
	cleanupDatabase = false
	identity.CloseIdleConnections()
	if serveErr != nil {
		return serveErr
	}
	if shutdownErr != nil {
		return shutdownErr
	}
	logger.Info("http servers stopped")
	return nil
}

func shutdownRuntime(
	ctx context.Context,
	metricsServer shutdownServer,
	applicationServer shutdownServer,
	closeDatabase func(),
) error {
	var result error
	if err := shutdownHTTPServer(ctx, "metrics", metricsServer); err != nil {
		result = err
	}
	if err := shutdownHTTPServer(ctx, "application", applicationServer); err != nil && result == nil {
		result = err
	}
	if closeDatabase != nil {
		closeDatabase()
	}
	return result
}

func shutdownHTTPServer(ctx context.Context, name string, server shutdownServer) error {
	if server == nil {
		return nil
	}
	if err := server.Shutdown(ctx); err != nil {
		_ = server.Close()
		return fmt.Errorf("shutdown %s HTTP server: %w", name, err)
	}
	return nil
}

func serve(name string, server *http.Server, listener net.Listener, logger *slog.Logger, results chan<- serveResult) {
	logger.Info("http server starting", "listener", name, "address", listener.Addr().String())
	results <- serveResult{name: name, err: server.Serve(listener)}
}
