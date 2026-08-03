package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type readinessCheckerFunc func(context.Context) error

func (f readinessCheckerFunc) Check(ctx context.Context) error {
	return f(ctx)
}

func TestLivenessDoesNotCallReadinessChecker(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	router := newTestRouter(readinessCheckerFunc(func(context.Context) error {
		calls.Add(1)
		return errors.New("database unavailable")
	}))

	request := httptest.NewRequest(http.MethodGet, "/health/live", nil)
	request.Header.Set(requestIDHeader, "live-request-id")
	response := httptest.NewRecorder()

	router.ServeHTTP(response, request)

	assertHealthResponse(t, response, http.StatusOK, "live-request-id")
	if calls.Load() != 0 {
		t.Fatalf("readiness checker called %d times", calls.Load())
	}
}

func TestReadinessSuccess(t *testing.T) {
	t.Parallel()

	router := newTestRouter(readinessCheckerFunc(func(context.Context) error {
		return nil
	}))

	request := httptest.NewRequest(http.MethodGet, "/health/ready", nil)
	request.Header.Set(requestIDHeader, "ready-request-id")
	response := httptest.NewRecorder()

	router.ServeHTTP(response, request)

	assertHealthResponse(t, response, http.StatusOK, "ready-request-id")
}

func TestReadinessFailureReturnsRedactedServiceUnavailable(t *testing.T) {
	t.Parallel()

	const rawDatabaseError = "failed to connect postgres://runtime:secret@identity-postgres:5432/bridgeworks"
	router := newTestRouter(readinessCheckerFunc(func(context.Context) error {
		return errors.New(rawDatabaseError)
	}))

	request := httptest.NewRequest(http.MethodGet, "/health/ready", nil)
	request.Header.Set(requestIDHeader, "failed-ready-request-id")
	response := httptest.NewRecorder()

	router.ServeHTTP(response, request)

	assertReadinessError(t, response, "failed-ready-request-id", rawDatabaseError)
}

func TestReadinessTimeoutReturnsServiceUnavailable(t *testing.T) {
	t.Parallel()

	router := NewRouter(
		RouterConfig{
			ServiceName:                "identity-service",
			ReadinessTimeout:           10 * time.Millisecond,
			ClerkWebhookProcessTimeout: time.Second,
			ClerkWebhookMaxBodyBytes:   1 << 20,
		},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		readinessCheckerFunc(func(ctx context.Context) error {
			<-ctx.Done()
			return ctx.Err()
		}),
		nil,
		nil,
	)

	request := httptest.NewRequest(http.MethodGet, "/health/ready", nil)
	request.Header.Set(requestIDHeader, "timeout-ready-request-id")
	response := httptest.NewRecorder()

	router.ServeHTTP(response, request)

	assertReadinessError(t, response, "timeout-ready-request-id", context.DeadlineExceeded.Error())
}

func TestUnknownRouteUsesErrorEnvelope(t *testing.T) {
	t.Parallel()

	router := newTestRouter(readinessCheckerFunc(func(context.Context) error {
		return nil
	}))
	request := httptest.NewRequest(http.MethodGet, "/missing", nil)
	request.Header.Set(requestIDHeader, "gateway-request-id")
	response := httptest.NewRecorder()

	router.ServeHTTP(response, request)

	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d", response.Code)
	}

	bodyBytes := response.Body.Bytes()
	var body errorEnvelope
	if err := json.Unmarshal(bodyBytes, &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Code != "not_found" || body.RequestID != "gateway-request-id" || body.Details != nil {
		t.Fatalf("unexpected error envelope: %+v", body)
	}
}

func newTestRouter(checker ReadinessChecker) http.Handler {
	return NewRouter(
		RouterConfig{
			ServiceName:                "identity-service",
			ReadinessTimeout:           time.Second,
			ClerkWebhookProcessTimeout: time.Second,
			ClerkWebhookMaxBodyBytes:   1 << 20,
		},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		checker,
		nil,
		nil,
	)
}

func assertHealthResponse(t *testing.T, response *httptest.ResponseRecorder, status int, requestID string) {
	t.Helper()

	if response.Code != status {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if response.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("Content-Type = %q", response.Header().Get("Content-Type"))
	}
	if response.Header().Get(requestIDHeader) != requestID {
		t.Fatalf("X-Request-Id = %q", response.Header().Get(requestIDHeader))
	}

	var body healthResponse
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Status != "ok" || body.Service != "identity-service" {
		t.Fatalf("unexpected body: %+v", body)
	}
}

func assertReadinessError(
	t *testing.T,
	response *httptest.ResponseRecorder,
	requestID string,
	forbidden string,
) {
	t.Helper()

	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if response.Header().Get(requestIDHeader) != requestID {
		t.Fatalf("X-Request-Id = %q", response.Header().Get(requestIDHeader))
	}

	bodyBytes := response.Body.Bytes()
	var body errorEnvelope
	if err := json.Unmarshal(bodyBytes, &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Code != "service_unavailable" ||
		body.Message != "service is not ready" ||
		body.RequestID != requestID ||
		body.Details != nil {
		t.Fatalf("unexpected error envelope: %+v", body)
	}
	if strings.Contains(string(bodyBytes), forbidden) {
		t.Fatalf("raw dependency error leaked: %s", bodyBytes)
	}
}
