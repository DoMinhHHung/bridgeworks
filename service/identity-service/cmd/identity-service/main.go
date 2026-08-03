package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/DoMinhHHung/bridgeworks/service/identity-service/internal/config"
	"github.com/DoMinhHHung/bridgeworks/service/identity-service/internal/httpapi"
	"github.com/DoMinhHHung/bridgeworks/service/identity-service/internal/platform"
	"github.com/DoMinhHHung/bridgeworks/service/identity-service/internal/postgres"
)

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

	logger := platform.NewLogger(os.Stdout, cfg.LogLevel).With("service", cfg.ServiceName)
	slog.SetDefault(logger)

	database, err := postgres.Open(context.Background(), postgres.Config{
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

	server := &http.Server{
		Addr: cfg.HTTPAddr,
		Handler: httpapi.NewRouter(
			cfg.ServiceName,
			logger,
			database,
			cfg.DatabaseReadinessTimeout,
		),
		ReadHeaderTimeout: cfg.ReadHeaderTimeout,
		ReadTimeout:       cfg.ReadTimeout,
		WriteTimeout:      cfg.WriteTimeout,
		IdleTimeout:       cfg.IdleTimeout,
		ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelError),
	}

	signalContext, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

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
		return serveErr
	case <-signalContext.Done():
		logger.Info("shutdown signal received")
	}

	shutdownContext, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()

	if err := server.Shutdown(shutdownContext); err != nil {
		_ = server.Close()
		return errors.New("shutdown HTTP server")
	}

	select {
	case serveErr := <-serveErrors:
		if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			return serveErr
		}
	case <-time.After(cfg.ShutdownTimeout):
		return errors.New("http server did not stop before shutdown timeout")
	}

	logger.Info("http server stopped")
	return nil
}
