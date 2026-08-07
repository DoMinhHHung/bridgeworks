package identityclient

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/DoMinhHHung/bridgeworks/service/organization-service/internal/platformauthorization"
)

func TestResolvePlatformAccessPreservesClerkAndCloudRunBoundaries(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/internal/v1/platform-access/me" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer clerk-session" {
			t.Fatalf("Authorization = %q", r.Header.Get("Authorization"))
		}
		if r.Header.Get("X-Serverless-Authorization") != "Bearer google-id-token" {
			t.Fatalf("X-Serverless-Authorization = %q", r.Header.Get("X-Serverless-Authorization"))
		}
		if r.Header.Get("X-Request-Id") != "platform-request" {
			t.Fatalf("X-Request-Id = %q", r.Header.Get("X-Request-Id"))
		}
		if r.Header.Get("X-Platform-Admin") != "" {
			t.Fatalf("forged platform header was forwarded")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"roles":["platform_admin"],"permissions":["organization.verification.review"]}`))
	}))
	defer server.Close()

	client, err := New(server.URL, time.Second, withPlatformTokenProvider(testPlatformTokenProvider{token: "google-id-token"}))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	access, err := client.ResolvePlatformAccess(context.Background(), "Bearer clerk-session", "platform-request")
	if err != nil {
		t.Fatalf("ResolvePlatformAccess() error = %v", err)
	}
	if len(access.Roles) != 1 || access.Roles[0] != platformauthorization.RolePlatformAdmin ||
		len(access.Permissions) != 1 || access.Permissions[0] != platformauthorization.PermissionOrganizationVerificationReview {
		t.Fatalf("access = %+v", access)
	}
}

func TestResolvePlatformAccessMapsStableIdentityDenials(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name   string
		status int
		body   string
		want   error
	}{
		{name: "unauthorized", status: http.StatusUnauthorized, body: `{"code":"unauthorized"}`, want: platformauthorization.ErrUnauthorized},
		{name: "disabled", status: http.StatusForbidden, body: `{"code":"account_disabled"}`, want: platformauthorization.ErrAccountInactive},
		{name: "deleted", status: http.StatusForbidden, body: `{"code":"account_deleted"}`, want: platformauthorization.ErrAccountInactive},
		{name: "not ready", status: http.StatusConflict, body: `{"code":"identity_not_ready"}`, want: platformauthorization.ErrIdentityNotReady},
		{name: "upstream unavailable", status: http.StatusServiceUnavailable, body: `{"code":"service_unavailable"}`, want: platformauthorization.ErrUnavailable},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(test.status)
				_, _ = w.Write([]byte(test.body))
			}))
			defer server.Close()
			client, err := New(server.URL, time.Second)
			if err != nil {
				t.Fatalf("New() error = %v", err)
			}
			_, err = client.ResolvePlatformAccess(context.Background(), "Bearer session", "request")
			if !errors.Is(err, test.want) {
				t.Fatalf("error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestResolvePlatformAccessRejectsMalformedContractAndDoesNotLeakToken(t *testing.T) {
	t.Parallel()

	for _, body := range []string{
		`not-json`,
		`{"roles":[]}`,
		`{"permissions":[]}`,
		`{"roles":[],"permissions":[],"email":"private@example.test"}`,
		`{"roles":[],"permissions":[]} {"roles":[],"permissions":[]}`,
	} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(body))
		}))
		client, err := New(server.URL, time.Second)
		if err != nil {
			server.Close()
			t.Fatalf("New() error = %v", err)
		}
		_, err = client.ResolvePlatformAccess(context.Background(), "Bearer sensitive-session-token", "request")
		server.Close()
		if !errors.Is(err, platformauthorization.ErrUnavailable) {
			t.Fatalf("body=%q error=%v", body, err)
		}
		if strings.Contains(err.Error(), "sensitive-session-token") || strings.Contains(err.Error(), body) {
			t.Fatalf("error leaked request/response data: %v", err)
		}
	}
}

func TestResolvePlatformAccessFailsBeforeUpstreamWhenPlatformTokenFails(t *testing.T) {
	t.Parallel()

	called := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	client, err := New(server.URL, time.Second, withPlatformTokenProvider(testPlatformTokenProvider{err: errors.New("metadata unavailable")}))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	_, err = client.ResolvePlatformAccess(context.Background(), "Bearer sensitive-session-token", "request")
	if err == nil || called {
		t.Fatalf("error=%v upstream-called=%v", err, called)
	}
	if strings.Contains(err.Error(), "sensitive-session-token") {
		t.Fatalf("error leaked Clerk token: %v", err)
	}
}
