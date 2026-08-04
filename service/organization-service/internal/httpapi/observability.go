package httpapi

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/felixge/httpsnoop"
	"github.com/go-chi/chi/v5"
)

type Metrics interface {
	HTTPRequestStarted()
	HTTPRequestCompleted()
	ObserveHTTPRequest(route, method string, status int, duration time.Duration)
	ObserveWebhook(aggregate, outcome string)
}

func HTTPObservability(logger *slog.Logger, metrics Metrics) func(http.Handler) http.Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if metrics != nil {
				metrics.HTTPRequestStarted()
				defer metrics.HTTPRequestCompleted()
			}

			captured := httpsnoop.CaptureMetrics(next, w, r)
			route := routePattern(r)
			if metrics != nil {
				metrics.ObserveHTTPRequest(route, r.Method, captured.Code, captured.Duration)
			}

			level := slog.LevelInfo
			if route == "/health/live" || route == "/health/ready" {
				level = slog.LevelDebug
			}
			logger.LogAttrs(r.Context(), level, "http request completed",
				slog.String("request_id", RequestIDFromContext(r.Context())),
				slog.String("method", r.Method),
				slog.String("route", route),
				slog.Int("status", captured.Code),
				slog.Int64("duration_ms", captured.Duration.Milliseconds()),
				slog.Int64("response_bytes", captured.Written),
			)
		})
	}
}

func routePattern(r *http.Request) string {
	if r == nil {
		return "unknown"
	}
	pattern := chi.RouteContext(r.Context()).RoutePattern()
	if pattern == "" {
		return "unknown"
	}
	return pattern
}
