package httpapi

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/DoMinhHHung/bridgeworks/service/organization-service/internal/authn"
	"github.com/DoMinhHHung/bridgeworks/service/organization-service/internal/authorization"
	"github.com/DoMinhHHung/bridgeworks/service/organization-service/internal/currentorganization"
	"github.com/DoMinhHHung/bridgeworks/service/organization-service/internal/observability"
	"github.com/DoMinhHHung/bridgeworks/service/organization-service/internal/organizationsync"
	"github.com/google/uuid"
)

type fakeWebhookVerifier struct {
	event     organizationsync.Event
	supported bool
	err       error
}

func (f fakeWebhookVerifier) VerifyAndParse([]byte, http.Header) (organizationsync.Event, bool, error) {
	return f.event, f.supported, f.err
}

type fakeEventProcessor struct {
	calls  int
	result organizationsync.Result
	err    error
}

func (f *fakeEventProcessor) Process(ctx context.Context, event organizationsync.Event) error {
	_, err := f.process(ctx, event)
	return err
}

func (f *fakeEventProcessor) ProcessWithResult(ctx context.Context, event organizationsync.Event) (organizationsync.Result, error) {
	return f.process(ctx, event)
}

func (f *fakeEventProcessor) process(context.Context, organizationsync.Event) (organizationsync.Result, error) {
	f.calls++
	if f.result == "" {
		f.result = organizationsync.ResultProcessed
	}
	return f.result, f.err
}

var _ EventProcessor = (*fakeEventProcessor)(nil)
var _ EventOutcomeProcessor = (*fakeEventProcessor)(nil)

type fakeCurrentResolver struct {
	result currentorganization.Result
	err    error
	calls  int
}

func (f *fakeCurrentResolver) Resolve(context.Context, authorization.Principal, string, string) (currentorganization.Result, error) {
	f.calls++
	return f.result, f.err
}

func TestClerkWebhookResponses(t *testing.T) {
	event := organizationsync.Event{AggregateType: organizationsync.AggregateMembership}
	tests := []struct {
		name       string
		verifier   fakeWebhookVerifier
		processor  *fakeEventProcessor
		body       string
		maxBytes   int64
		wantStatus int
		wantCode   string
		wantCalls  int
	}{
		{name: "success", verifier: fakeWebhookVerifier{event: event, supported: true}, processor: &fakeEventProcessor{}, body: "{}", maxBytes: 16, wantStatus: 204, wantCalls: 1},
		{name: "unsupported", verifier: fakeWebhookVerifier{supported: false}, processor: &fakeEventProcessor{}, body: "{}", maxBytes: 16, wantStatus: 204},
		{name: "verification failure", verifier: fakeWebhookVerifier{err: errors.New("invalid")}, processor: &fakeEventProcessor{}, body: "{}", maxBytes: 16, wantStatus: 400, wantCode: "invalid_webhook"},
		{name: "processing failure", verifier: fakeWebhookVerifier{event: event, supported: true}, processor: &fakeEventProcessor{err: errors.New("database unavailable")}, body: "{}", maxBytes: 16, wantStatus: 503, wantCode: "service_unavailable", wantCalls: 1},
		{name: "oversized", verifier: fakeWebhookVerifier{}, processor: &fakeEventProcessor{}, body: "too-large", maxBytes: 2, wantStatus: 413, wantCode: "request_too_large"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := RequestID(ClerkWebhook(tt.verifier, tt.processor, slog.New(slog.NewTextHandler(io.Discard, nil)), tt.maxBytes, time.Second))
			request := httptest.NewRequest(http.MethodPost, "/webhooks/clerk", strings.NewReader(tt.body))
			request.Header.Set("X-Request-Id", "request-test")
			response := httptest.NewRecorder()

			handler.ServeHTTP(response, request)

			if response.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", response.Code, tt.wantStatus, response.Body.String())
			}
			if tt.wantCode != "" && !strings.Contains(response.Body.String(), `"code":"`+tt.wantCode+`"`) {
				t.Fatalf("body = %s", response.Body.String())
			}
			if tt.processor.calls != tt.wantCalls {
				t.Fatalf("processor calls = %d, want %d", tt.processor.calls, tt.wantCalls)
			}
		})
	}
}

func TestClerkWebhookWithMetricsUsesApplicationOutcome(t *testing.T) {
	processor := &fakeEventProcessor{result: organizationsync.ResultStale}
	metrics := &fakeMetrics{}
	handler := RequestID(ClerkWebhookWithMetrics(
		fakeWebhookVerifier{
			event:     organizationsync.Event{AggregateType: organizationsync.AggregateMembership},
			supported: true,
		},
		processor,
		metrics,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		16,
		time.Second,
	))
	request := httptest.NewRequest(http.MethodPost, "/webhooks/clerk", strings.NewReader("{}"))
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d", response.Code)
	}
	if len(metrics.webhooks) != 1 || metrics.webhooks[0] != [2]string{observability.AggregateMembership, observability.OutcomeStale} {
		t.Fatalf("webhook metrics = %#v", metrics.webhooks)
	}
}

func TestCurrentOrganizationResponseAndSecurityHeaders(t *testing.T) {
	organizationID := uuid.MustParse("018f0c76-8f6c-7cc4-8000-000000000001")
	membershipID := uuid.MustParse("018f0c76-8f6c-7cc4-8000-000000000002")
	identityID := uuid.MustParse("018f0c76-8f6c-7cc4-8000-000000000003")
	name := "BridgeWorks"
	slug := "bridgeworks"
	resolver := &fakeCurrentResolver{result: currentorganization.Result{
		Actor:        authorization.NewActorContext(identityID, organizationID, membershipID, organizationsync.RoleViewer, []string{authorization.PermissionOrganizationRead}),
		Organization: currentorganization.Organization{ID: organizationID, Name: &name, Slug: &slug, Status: "active"},
		Membership:   currentorganization.Membership{ID: membershipID, OrganizationID: organizationID, ApplicationRole: organizationsync.RoleViewer, Status: "active"},
	}}
	handler := RequestID(AuthenticatedResponseHeaders(CurrentOrganization(resolver)))
	request := httptest.NewRequest(http.MethodGet, "/organizations/current", nil)
	request.Header.Set("Authorization", "Bearer redacted")
	request.Header.Set("X-Request-Id", "current-request")
	request = request.WithContext(authn.ContextWithPrincipal(request.Context(), authorization.Principal{
		ClerkUserID: "user-1", ClerkOrganizationID: "org-1",
	}))
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", response.Code, response.Body.String())
	}
	if response.Header().Get("Cache-Control") != "no-store" || !strings.Contains(response.Header().Get("Vary"), "Authorization") {
		t.Fatalf("headers = %#v", response.Header())
	}
	if strings.Contains(response.Body.String(), "clerk") || strings.Contains(response.Body.String(), identityID.String()) {
		t.Fatalf("response leaked provider/internal identifiers: %s", response.Body.String())
	}
	if !strings.Contains(response.Body.String(), organizationID.String()) {
		t.Fatalf("body = %s", response.Body.String())
	}
}

func TestCurrentOrganizationErrorMapping(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
	}{
		{name: "not ready", err: currentorganization.ErrOrganizationNotReady, wantStatus: 409, wantCode: "organization_not_ready"},
		{name: "membership", err: currentorganization.ErrMembershipRequired, wantStatus: 403, wantCode: "membership_required"},
		{name: "dependency", err: errors.New("dependency failed"), wantStatus: 503, wantCode: "service_unavailable"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resolver := &fakeCurrentResolver{err: tt.err}
			handler := RequestID(AuthenticatedResponseHeaders(CurrentOrganization(resolver)))
			request := httptest.NewRequest(http.MethodGet, "/organizations/current", nil)
			request = request.WithContext(authn.ContextWithPrincipal(request.Context(), authorization.Principal{
				ClerkUserID: "user-1", ClerkOrganizationID: "org-1",
			}))
			response := httptest.NewRecorder()

			handler.ServeHTTP(response, request)

			if response.Code != tt.wantStatus || !strings.Contains(response.Body.String(), `"code":"`+tt.wantCode+`"`) {
				t.Fatalf("status/body = %d %s", response.Code, response.Body.String())
			}
		})
	}
}

func TestRequestIDAndRecoverer(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	handler := RequestID(Recoverer(logger)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("sensitive panic value")
	})))
	request := httptest.NewRequest(http.MethodGet, "/panic", nil)
	request.Header.Set("X-Request-Id", "bad id with spaces")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d", response.Code)
	}
	requestID := response.Header().Get("X-Request-Id")
	if requestID == "" || requestID == "bad id with spaces" {
		t.Fatalf("request ID = %q", requestID)
	}
	if strings.Contains(response.Body.String(), "sensitive panic value") {
		t.Fatalf("panic leaked: %s", response.Body.String())
	}
}
