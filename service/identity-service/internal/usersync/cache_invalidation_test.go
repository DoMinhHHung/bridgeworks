package usersync

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DoMinhHHung/bridgeworks/service/identity-service/internal/clerkwebhook"
)

type resultProcessorFunc func(context.Context, clerkwebhook.Event) (Result, error)

func (f resultProcessorFunc) ProcessWithResult(ctx context.Context, event clerkwebhook.Event) (Result, error) {
	return f(ctx, event)
}

type recordingInvalidator struct {
	mu        sync.Mutex
	calls     int
	userID    string
	err       error
	deadline  bool
	cancelled bool
}

func (i *recordingInvalidator) Invalidate(ctx context.Context, userID string) error {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.calls++
	i.userID = userID
	_, i.deadline = ctx.Deadline()
	i.cancelled = ctx.Err() != nil
	return i.err
}

func (i *recordingInvalidator) snapshot() (calls int, userID string, deadline, cancelled bool) {
	i.mu.Lock()
	defer i.mu.Unlock()
	return i.calls, i.userID, i.deadline, i.cancelled
}

func TestInvalidatingProcessorInvalidatesEveryCommittedSuccess(t *testing.T) {
	t.Parallel()

	for _, result := range []Result{ResultProcessed, ResultDuplicate, ResultStale} {
		result := result
		t.Run(string(result), func(t *testing.T) {
			t.Parallel()
			invalidator := &recordingInvalidator{}
			processor, err := NewInvalidatingProcessor(
				resultProcessorFunc(func(context.Context, clerkwebhook.Event) (Result, error) {
					return result, nil
				}),
				invalidator,
				slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)),
				150*time.Millisecond,
			)
			if err != nil {
				t.Fatalf("NewInvalidatingProcessor: %v", err)
			}
			event := clerkwebhook.Event{Type: clerkwebhook.EventUserUpdated, ClerkUserID: "user_committed"}
			got, err := processor.ProcessWithResult(context.Background(), event)
			if err != nil || got != result {
				t.Fatalf("result=%q err=%v", got, err)
			}
			calls, userID, deadline, cancelled := invalidator.snapshot()
			if calls != 1 || userID != event.ClerkUserID || !deadline || cancelled {
				t.Fatalf("invalidation calls=%d user=%q deadline=%v cancelled=%v", calls, userID, deadline, cancelled)
			}
		})
	}
}

func TestInvalidatingProcessorDoesNotInvalidateRollbackOrError(t *testing.T) {
	t.Parallel()

	invalidation := &recordingInvalidator{}
	processError := errors.New("transaction rolled back")
	processor, err := NewInvalidatingProcessor(
		resultProcessorFunc(func(context.Context, clerkwebhook.Event) (Result, error) {
			return "", processError
		}),
		invalidation,
		slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)),
		150*time.Millisecond,
	)
	if err != nil {
		t.Fatalf("NewInvalidatingProcessor: %v", err)
	}
	_, err = processor.ProcessWithResult(context.Background(), clerkwebhook.Event{ClerkUserID: "user_rollback"})
	if !errors.Is(err, processError) {
		t.Fatalf("error = %v", err)
	}
	calls, _, _, _ := invalidation.snapshot()
	if calls != 0 {
		t.Fatalf("invalidation calls = %d", calls)
	}
}

func TestInvalidationFailurePreservesCommittedResultAndRedactsDetails(t *testing.T) {
	t.Parallel()

	var logs bytes.Buffer
	invalidator := &recordingInvalidator{err: errors.New("dial redis-password-sensitive at cache.private:6379")}
	processor, err := NewInvalidatingProcessor(
		resultProcessorFunc(func(context.Context, clerkwebhook.Event) (Result, error) {
			return ResultProcessed, nil
		}),
		invalidator,
		slog.New(slog.NewJSONHandler(&logs, nil)),
		150*time.Millisecond,
	)
	if err != nil {
		t.Fatalf("NewInvalidatingProcessor: %v", err)
	}
	event := clerkwebhook.Event{
		Type:        clerkwebhook.EventUserDeleted,
		ClerkUserID: "user_provider_sensitive",
	}
	result, err := processor.ProcessWithResult(context.Background(), event)
	if err != nil || result != ResultProcessed {
		t.Fatalf("result=%q err=%v", result, err)
	}
	output := logs.String()
	for _, forbidden := range []string{
		"redis-password-sensitive",
		"cache.private:6379",
		event.ClerkUserID,
		"dial ",
	} {
		if strings.Contains(output, forbidden) {
			t.Fatalf("log leaked %q: %s", forbidden, output)
		}
	}
	for _, expected := range []string{
		`"msg":"current user cache invalidation failed"`,
		`"event_type":"user.deleted"`,
		`"result":"processed"`,
		`"reason":"cache_delete_failed"`,
	} {
		if !strings.Contains(output, expected) {
			t.Fatalf("log missing %q: %s", expected, output)
		}
	}
}

func TestInvalidationUsesDetachedBoundedContextAfterCommit(t *testing.T) {
	t.Parallel()

	invalidator := &recordingInvalidator{}
	processor, err := NewInvalidatingProcessor(
		resultProcessorFunc(func(context.Context, clerkwebhook.Event) (Result, error) {
			return ResultDuplicate, nil
		}),
		invalidator,
		slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)),
		150*time.Millisecond,
	)
	if err != nil {
		t.Fatalf("NewInvalidatingProcessor: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result, err := processor.ProcessWithResult(ctx, clerkwebhook.Event{ClerkUserID: "user_retry"})
	if err != nil || result != ResultDuplicate {
		t.Fatalf("result=%q err=%v", result, err)
	}
	calls, _, deadline, cancelled := invalidator.snapshot()
	if calls != 1 || !deadline || cancelled {
		t.Fatalf("calls=%d deadline=%v cancelled=%v", calls, deadline, cancelled)
	}
}
