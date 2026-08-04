package httpapi

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/DoMinhHHung/bridgeworks/service/identity-service/internal/clerkwebhook"
	"github.com/DoMinhHHung/bridgeworks/service/identity-service/internal/observability"
	"github.com/DoMinhHHung/bridgeworks/service/identity-service/internal/usersync"
)

type ClerkWebhookVerifier interface {
	VerifyAndParse([]byte, http.Header) (clerkwebhook.Event, error)
}

type ClerkWebhookProcessor interface {
	Process(context.Context, clerkwebhook.Event) error
}

type ClerkWebhookOutcomeProcessor interface {
	ProcessWithResult(context.Context, clerkwebhook.Event) (usersync.Result, error)
}

type clerkWebhookProcessFunc func(context.Context, clerkwebhook.Event) (usersync.Result, error)

func clerkWebhookHandler(
	logger *slog.Logger,
	verifier ClerkWebhookVerifier,
	processor ClerkWebhookProcessor,
	maxBodyBytes int64,
	processTimeout time.Duration,
) http.HandlerFunc {
	return clerkWebhookHandlerCore(
		logger,
		verifier,
		func(ctx context.Context, event clerkwebhook.Event) (usersync.Result, error) {
			return "", processor.Process(ctx, event)
		},
		nil,
		maxBodyBytes,
		processTimeout,
	)
}

func clerkWebhookHandlerWithMetrics(
	logger *slog.Logger,
	verifier ClerkWebhookVerifier,
	processor ClerkWebhookOutcomeProcessor,
	metrics Metrics,
	maxBodyBytes int64,
	processTimeout time.Duration,
) http.HandlerFunc {
	return clerkWebhookHandlerCore(
		logger,
		verifier,
		func(ctx context.Context, event clerkwebhook.Event) (usersync.Result, error) {
			return processor.ProcessWithResult(ctx, event)
		},
		metrics,
		maxBodyBytes,
		processTimeout,
	)
}

func clerkWebhookHandlerCore(
	logger *slog.Logger,
	verifier ClerkWebhookVerifier,
	process clerkWebhookProcessFunc,
	metrics Metrics,
	maxBodyBytes int64,
	processTimeout time.Duration,
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
		payload, err := io.ReadAll(r.Body)
		if err != nil {
			metricsObserveWebhook(metrics, observability.AggregateUser, observability.OutcomeRejected)
			var maxBytesError *http.MaxBytesError
			if errors.As(err, &maxBytesError) {
				logger.WarnContext(r.Context(), "Clerk webhook body rejected",
					"request_id", RequestIDFromContext(r.Context()), "reason", "request_too_large")
				writeError(w, r, http.StatusRequestEntityTooLarge, "request_too_large", "request body too large", nil)
				return
			}
			logger.WarnContext(r.Context(), "Clerk webhook body rejected",
				"request_id", RequestIDFromContext(r.Context()), "reason", "invalid_body")
			writeError(w, r, http.StatusBadRequest, "invalid_webhook", "invalid webhook request", nil)
			return
		}

		event, err := verifier.VerifyAndParse(payload, r.Header)
		if err != nil {
			metricsObserveWebhook(metrics, observability.AggregateUser, observability.OutcomeRejected)
			logger.WarnContext(r.Context(), "Clerk webhook rejected",
				"request_id", RequestIDFromContext(r.Context()), "reason", "verification_or_payload_invalid")
			writeError(w, r, http.StatusBadRequest, "invalid_webhook", "invalid webhook request", nil)
			return
		}
		if !event.Supported {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		processContext, cancel := context.WithTimeout(r.Context(), processTimeout)
		defer cancel()
		result, err := process(processContext, event)
		if err != nil {
			metricsObserveWebhook(metrics, observability.AggregateUser, observability.OutcomeRetryableFailure)
			logger.ErrorContext(r.Context(), "Clerk webhook processing failed",
				"request_id", RequestIDFromContext(r.Context()), "event_type", event.Type)
			writeError(w, r, http.StatusServiceUnavailable, "service_unavailable", "service temporarily unavailable", nil)
			return
		}

		metricsObserveWebhook(metrics, observability.AggregateUser, string(result))
		w.WriteHeader(http.StatusNoContent)
	}
}

func metricsObserveWebhook(metrics Metrics, aggregate, outcome string) {
	if metrics != nil {
		metrics.ObserveWebhook(aggregate, outcome)
	}
}
