package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/DoMinhHHung/bridgeworks/service/organization-service/internal/clerkwebhook"
	"github.com/DoMinhHHung/bridgeworks/service/organization-service/internal/observability"
	"github.com/DoMinhHHung/bridgeworks/service/organization-service/internal/organizationsync"
)

type WebhookVerifier interface {
	VerifyAndParse([]byte, http.Header) (organizationsync.Event, bool, error)
}

type EventProcessor interface {
	Process(context.Context, organizationsync.Event) error
}

type eventOutcomeProcessor interface {
	ProcessWithResult(context.Context, organizationsync.Event) (organizationsync.Result, error)
}

func ClerkWebhook(
	verifier WebhookVerifier,
	processor EventProcessor,
	logger *slog.Logger,
	maxBodyBytes int64,
	processTimeout time.Duration,
) http.HandlerFunc {
	return ClerkWebhookWithMetrics(verifier, processor, nil, logger, maxBodyBytes, processTimeout)
}

func ClerkWebhookWithMetrics(
	verifier WebhookVerifier,
	processor EventProcessor,
	metrics Metrics,
	logger *slog.Logger,
	maxBodyBytes int64,
	processTimeout time.Duration,
) http.HandlerFunc {
	if logger == nil {
		logger = slog.Default()
	}
	return func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
		body, err := io.ReadAll(r.Body)
		if err != nil {
			metricsObserveWebhook(metrics, observability.AggregateOrganization, observability.OutcomeRejected)
			var maxBytesError *http.MaxBytesError
			if errors.As(err, &maxBytesError) {
				writeError(w, r, http.StatusRequestEntityTooLarge, "request_too_large", "request body too large")
				return
			}
			writeError(w, r, http.StatusBadRequest, "invalid_webhook", "invalid webhook request")
			return
		}
		event, supported, err := verifier.VerifyAndParse(body, r.Header)
		if err != nil {
			metricsObserveWebhook(metrics, rejectedAggregate(body), observability.OutcomeRejected)
			logger.WarnContext(r.Context(), "organization webhook rejected",
				"request_id", RequestIDFromContext(r.Context()), "reason", "verification_failed")
			writeError(w, r, http.StatusBadRequest, "invalid_webhook", "invalid webhook request")
			return
		}
		if !supported {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), processTimeout)
		defer cancel()

		result := organizationsync.ResultProcessed
		if outcomeProcessor, ok := processor.(eventOutcomeProcessor); ok {
			result, err = outcomeProcessor.ProcessWithResult(ctx, event)
		} else {
			err = processor.Process(ctx, event)
		}
		aggregate := boundedEventAggregate(event.AggregateType)
		if err != nil {
			metricsObserveWebhook(metrics, aggregate, observability.OutcomeRetryableFailure)
			logger.ErrorContext(r.Context(), "organization webhook processing failed",
				"request_id", RequestIDFromContext(r.Context()), "event_category", event.AggregateType)
			writeError(w, r, http.StatusServiceUnavailable, "service_unavailable", "service temporarily unavailable")
			return
		}
		metricsObserveWebhook(metrics, aggregate, string(result))
		w.WriteHeader(http.StatusNoContent)
	}
}

func rejectedAggregate(body []byte) string {
	var envelope struct {
		Type string `json:"type"`
	}
	if json.Unmarshal(body, &envelope) == nil && strings.HasPrefix(envelope.Type, "organizationMembership.") {
		return observability.AggregateMembership
	}
	return observability.AggregateOrganization
}

func boundedEventAggregate(aggregate string) string {
	if aggregate == organizationsync.AggregateMembership {
		return observability.AggregateMembership
	}
	return observability.AggregateOrganization
}

func metricsObserveWebhook(metrics Metrics, aggregate, outcome string) {
	if metrics != nil {
		metrics.ObserveWebhook(aggregate, outcome)
	}
}

var _ = clerkwebhook.ErrInvalidWebhook
