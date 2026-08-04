package httpapi

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/DoMinhHHung/bridgeworks/service/organization-service/internal/clerkwebhook"
	"github.com/DoMinhHHung/bridgeworks/service/organization-service/internal/organizationsync"
)

type WebhookVerifier interface {
	VerifyAndParse([]byte, http.Header) (organizationsync.Event, bool, error)
}

type EventProcessor interface {
	Process(context.Context, organizationsync.Event) error
}

func ClerkWebhook(
	verifier WebhookVerifier,
	processor EventProcessor,
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
			logger.WarnContext(r.Context(), "organization webhook rejected",
				"request_id", RequestIDFromContext(r.Context()),
				"reason", "verification_failed",
			)
			writeError(w, r, http.StatusBadRequest, "invalid_webhook", "invalid webhook request")
			return
		}
		if !supported {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), processTimeout)
		defer cancel()
		if err := processor.Process(ctx, event); err != nil {
			logger.ErrorContext(r.Context(), "organization webhook processing failed",
				"request_id", RequestIDFromContext(r.Context()),
				"event_category", event.AggregateType,
			)
			writeError(w, r, http.StatusServiceUnavailable, "service_unavailable", "service temporarily unavailable")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

var _ = clerkwebhook.ErrInvalidWebhook
