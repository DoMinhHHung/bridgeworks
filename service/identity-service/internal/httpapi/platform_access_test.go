package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/DoMinhHHung/bridgeworks/service/identity-service/internal/authn"
	"github.com/DoMinhHHung/bridgeworks/service/identity-service/internal/platformaccess"
)

type platformAccessResolverFunc func(context.Context, string) (platformaccess.Access, error)

func (f platformAccessResolverFunc) Resolve(ctx context.Context, clerkUserID string) (platformaccess.Access, error) {
	return f(ctx, clerkUserID)
}

func TestPlatformAccessReturnsOnlyGlobalRolesAndPermissions(t *testing.T) {
	t.Parallel()

	response := servePlatformAccess(t, platformAccessResolverFunc(func(ctx context.Context, clerkUserID string) (platformaccess.Access, error) {
		if _, ok := ctx.Deadline(); !ok {
			t.Fatal("platform access lookup has no deadline")
		}
		if clerkUserID != "user_private_identifier" {
			t.Fatalf("clerk user ID = %q", clerkUserID)
		}
		return platformaccess.Access{
			Roles:       []string{platformaccess.RolePlatformAdmin},
			Permissions: []string{platformaccess.PermissionOrganizationVerificationReview},
		}, nil
	}), nil)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", response.Code, response.Body.String())
	}
	if response.Header().Get("Cache-Control") != "no-store" || !strings.Contains(response.Header().Get("Vary"), "Authorization") {
		t.Fatalf("cache headers = %#v", response.Header())
	}
	if response.Header().Get(requestIDHeader) != "platform-access-request" {
		t.Fatalf("request ID = %q", response.Header().Get(requestIDHeader))
	}
	var body platformAccessResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(body.Roles) != 1 || body.Roles[0] != platformaccess.RolePlatformAdmin ||
		len(body.Permissions) != 1 || body.Permissions[0] != platformaccess.PermissionOrganizationVerificationReview {
		t.Fatalf("body = %+v", body)
	}
	for _, forbidden := range []string{"email", "clerk", "user_private_identifier", "session"} {
		if strings.Contains(strings.ToLower(response.Body.String()), strings.ToLower(forbidden)) {
			t.Fatalf("private response leaked %q: %s", forbidden, response.Body.String())
		}
	}
}

func TestPlatformAccessOrdinaryUserReturnsEmptyArraysAndIgnoresForgedHeader(t *testing.T) {
	t.Parallel()

	response := servePlatformAccess(t, platformAccessResolverFunc(func(context.Context, string) (platformaccess.Access, error) {
		return platformaccess.Access{}, nil
	}), map[string]string{"X-Platform-Admin": "true"})
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", response.Code, response.Body.String())
	}
	if strings.TrimSpace(response.Body.String()) != `{"roles":[],"permissions":[]}` {
		t.Fatalf("body = %s", response.Body.String())
	}
}

func TestPlatformAccessMapsLifecycleAndOperationalFailures(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
	}{
		{name: "disabled", err: platformaccess.ErrAccountDisabled, wantStatus: http.StatusForbidden, wantCode: "account_disabled"},
		{name: "deleted", err: platformaccess.ErrAccountDeleted, wantStatus: http.StatusForbidden, wantCode: "account_deleted"},
		{name: "not ready", err: platformaccess.ErrIdentityNotReady, wantStatus: http.StatusConflict, wantCode: "identity_not_ready"},
		{name: "database outage", err: errors.New("postgres://runtime:secret@identity-postgres/db"), wantStatus: http.StatusServiceUnavailable, wantCode: "service_unavailable"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			response := servePlatformAccess(t, platformAccessResolverFunc(func(context.Context, string) (platformaccess.Access, error) {
				return platformaccess.Access{}, test.err
			}), nil)
			if response.Code != test.wantStatus {
				t.Fatalf("status = %d body=%s", response.Code, response.Body.String())
			}
			if !strings.Contains(response.Body.String(), `"code":"`+test.wantCode+`"`) {
				t.Fatalf("body = %s", response.Body.String())
			}
			if strings.Contains(response.Body.String(), "postgres://") || strings.Contains(response.Body.String(), "secret") {
				t.Fatalf("dependency detail leaked: %s", response.Body.String())
			}
		})
	}
}

func TestPlatformAccessMissingPrincipalDoesNotResolve(t *testing.T) {
	t.Parallel()

	called := false
	request := httptest.NewRequest(http.MethodGet, "/internal/v1/platform-access/me", nil)
	request.Header.Set(requestIDHeader, "missing-platform-principal")
	response := httptest.NewRecorder()
	RequestID(platformAccessResponseHeaders(platformAccessHandler(testLogger(), platformAccessResolverFunc(func(context.Context, string) (platformaccess.Access, error) {
		called = true
		return platformaccess.Access{}, nil
	})))).ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized || called {
		t.Fatalf("status=%d called=%v body=%s", response.Code, called, response.Body.String())
	}
}

func servePlatformAccess(t *testing.T, resolver PlatformAccessResolver, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, "/internal/v1/platform-access/me", nil)
	request.Header.Set(requestIDHeader, "platform-access-request")
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	request = request.WithContext(authn.ContextWithPrincipal(request.Context(), authn.Principal{
		ClerkUserID: "user_private_identifier",
		SessionID:   "session_private_identifier",
	}))
	response := httptest.NewRecorder()
	RequestID(platformAccessResponseHeaders(platformAccessHandler(testLogger(), resolver))).ServeHTTP(response, request)
	return response
}
