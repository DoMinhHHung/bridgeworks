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
	"time"

	"github.com/DoMinhHHung/bridgeworks/service/identity-service/internal/authn"
	"github.com/DoMinhHHung/bridgeworks/service/identity-service/internal/clerkwebhook"
	"github.com/DoMinhHHung/bridgeworks/service/identity-service/internal/config"
	"github.com/DoMinhHHung/bridgeworks/service/identity-service/internal/currentuser"
	"github.com/DoMinhHHung/bridgeworks/service/identity-service/internal/httpapi"
	"github.com/DoMinhHHung/bridgeworks/service/identity-service/internal/identityid"
	"github.com/DoMinhHHung/bridgeworks/service/identity-service/internal/observability"
	"github.com/DoMinhHHung/bridgeworks/service/identity-service/internal/platform"
	"github.com/DoMinhHHung/bridgeworks/service/identity-service/internal/postgres"
	"github.com/DoMinhHHung/bridgeworks/service/identity-service/internal/store"
	"github.com/DoMinhHHung/bridgeworks/service/identity-service/internal/usersync"
)

type serveResult struct {
	name string
	err  error
}

func main() {
	bootstrapLogger := platform.NewLogger(os.Stdout, slog.LevelInfo)
	if err := run(bootstrapLogger); err != nil {
		bootstrapLogger.Error("identity service stopped", "error", err)
		os.Exit(1)
	}
}

func run(bootstrapLogger *slog.Logger) error {
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
		URL:               cfg.DatabaseURL,
		ConnectTimeout:    cfg.DatabaseConnectTimeout,
		MaxConns:          cfg.DatabaseMaxConns,
		MinConns:          cfg.DatabaseMinConns,
		MaxConnLifetime:   cfg.DatabaseMaxConnLifetime,
		MaxConnIdleTime:   cfg.DatabaseMaxConnIdleTime,
		HealthCheckPeriod: cfg.DatabaseHealthCheckPeriod,
	})
	if err != nil {
		return err
	}
	defer database.Close()

	metrics, err := observability.New(cfg.ServiceName, func() observability.PoolStat {
		return database.Stat()
	})
	if err != nil {
		return err
	}

	verifier, err := clerkwebhook.NewVerifier(cfg.ClerkWebhookSigningSecret)
	if err != nil {
		return err
	}
	authenticate, err := authn.New(
		authn.Config{
			JWTKey:            cfg.ClerkJWTKey,
			Issuer:            cfg.ClerkIssuer,
			AuthorizedParties: cfg.ClerkAuthorizedParties,
			Leeway:            cfg.ClerkAuthLeeway,
		},
		logger,
		httpapi.RequestIDFromContext,
	)
	if err != nil {
		return err
	}

	idUserGenerator, err := identityid.New(time.Now, nil)
	if err != nil {
		return fmt.Errorf("initialize id_user generator: %w", err)
	}
	identityRepository := store.New(database)
	userSynchronizer := usersync.New(
		identityRepository,
		identityid.UUIDV7Generator{},
		idUserGenerator,
	)
	currentUserService := currentuser.New(identityRepository)

	server := &http.Server{
		Addr: cfg.HTTPAddr,
		Handler: httpapi.NewRouter(
			httpapi.RouterConfig{
				ServiceName:                cfg.ServiceName,
				ReadinessTimeout:           cfg.DatabaseReadinessTimeout,
				ClerkWebhookProcessTimeout: cfg.ClerkWebhookProcessTimeout,
				ClerkWebhookMaxBodyBytes:   cfg.ClerkWebhookMaxBodyBytes,
				Metrics:                    metrics,
			},
			logger,
			database,
			verifier,
			userSynchronizer,
			authenticate,
			currentUserService,
		),
		ReadHeaderTimeout: cfg.ReadHeaderTimeout,
		ReadTimeout:       cfg.ReadTimeout,
		WriteTimeout:      cfg.WriteTimeout,
		IdleTimeout:       cfg.IdleTimeout,
		ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelError),
	}
	metricsServer := &http.Server{
		Addr:              metricsAddr,
		Handler:           metrics.Handler(),
		ReadHeaderTimeout: cfg.ReadHeaderTimeout,
		ReadTimeout:       cfg.ReadTimeout,
		WriteTimeout:      cfg.WriteTimeout,
		IdleTimeout:       cfg.IdleTimeout,
		ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelError),
	}

	applicationListener, err := net.Listen("tcp", cfg.HTTPAddr)
	if err != nil {
		return fmt.Errorf("listen application HTTP: %w", err)
	}
	defer applicationListener.Close()
	metricsListener, err := net.Listen("tcp", metricsAddr)
	if err != nil {
		return fmt.Errorf("listen private metrics HTTP: %w", err)
	}
	defer metricsListener.Close()

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
	if err := metricsServer.Shutdown(shutdownContext); err != nil {
		_ = metricsServer.Close()
		if serveErr == nil {
			serveErr = fmt.Errorf("shutdown metrics HTTP server: %w", err)
		}
	}
	if err := server.Shutdown(shutdownContext); err != nil {
		_ = server.Close()
		if serveErr == nil {
			serveErr = fmt.Errorf("shutdown application HTTP server: %w", err)
		}
	}
	if serveErr != nil {
		return serveErr
	}
	logger.Info("http servers stopped")
	return nil
}

func serve(name string, server *http.Server, listener net.Listener, logger *slog.Logger, results chan<- serveResult) {
	logger.Info("http server starting", "listener", name, "address", listener.Addr().String())
	results <- serveResult{name: name, err: server.Serve(listener)}
}
