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

	"github.com/DoMinhHHung/bridgeworks/services/identity/internal/config"
	"github.com/DoMinhHHung/bridgeworks/services/identity/internal/httpapi"
	"github.com/DoMinhHHung/bridgeworks/services/identity/internal/platform"
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

	server := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           httpapi.NewRouter(cfg.ServiceName, logger),
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
		return err
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
