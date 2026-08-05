package usersync

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/DoMinhHHung/bridgeworks/service/identity-service/internal/clerkwebhook"
)

type ResultProcessor interface {
	ProcessWithResult(context.Context, clerkwebhook.Event) (Result, error)
}

type CurrentUserCacheInvalidator interface {
	Invalidate(context.Context, string) error
}

type InvalidatingProcessor struct {
	processor   ResultProcessor
	invalidator CurrentUserCacheInvalidator
	logger      *slog.Logger
	timeout     time.Duration
}

func NewInvalidatingProcessor(
	processor ResultProcessor,
	invalidator CurrentUserCacheInvalidator,
	logger *slog.Logger,
	timeout time.Duration,
) (*InvalidatingProcessor, error) {
	if processor == nil {
		return nil, errors.New("user synchronization processor is required")
	}
	if invalidator == nil {
		return nil, errors.New("current user cache invalidator is required")
	}
	if timeout <= 0 {
		return nil, errors.New("current user cache invalidation timeout must be greater than zero")
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &InvalidatingProcessor{
		processor:   processor,
		invalidator: invalidator,
		logger:      logger,
		timeout:     timeout,
	}, nil
}

func (p *InvalidatingProcessor) Process(ctx context.Context, event clerkwebhook.Event) error {
	_, err := p.ProcessWithResult(ctx, event)
	return err
}

func (p *InvalidatingProcessor) ProcessWithResult(
	ctx context.Context,
	event clerkwebhook.Event,
) (Result, error) {
	if p == nil || p.processor == nil || p.invalidator == nil {
		return "", errors.New("user synchronization invalidating processor is not initialized")
	}

	result, err := p.processor.ProcessWithResult(ctx, event)
	if err != nil {
		return "", err
	}
	if result != ResultProcessed && result != ResultDuplicate && result != ResultStale {
		return result, nil
	}

	invalidationContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), p.timeout)
	defer cancel()
	if err := p.invalidator.Invalidate(invalidationContext, event.ClerkUserID); err != nil {
		p.logger.WarnContext(
			context.WithoutCancel(ctx),
			"current user cache invalidation failed",
			"event_type", event.Type,
			"result", string(result),
			"reason", "cache_delete_failed",
		)
	}
	return result, nil
}
