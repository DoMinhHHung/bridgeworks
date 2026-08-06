package identityclient

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/DoMinhHHung/bridgeworks/service/organization-service/internal/currentorganization"
)

func TestResolveForwardsOnlyRequiredHeaders(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/me" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer secret-token" {
			t.Fatalf("Authorization not forwarded")
		}
		if r.Header.Get("X-Request-Id") != "request-1" {
			t.Fatalf("request ID not forwarded")
		}
		if r.Header.Get("X-Unrelated") != "" {
			t.Fatal("unrelated header forwarded")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"0198f3be-bf6f-7b0a-8a25-f8433567e0c1"}`))
	}))
	defer server.Close()
	client, err := New(server.URL, time.Second)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	identity, err := client.Resolve(context.Background(), "Bearer secret-token", "request-1")
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if identity.ID.String() != "0198f3be-bf6f-7b0a-8a25-f8433567e0c1" {
		t.Fatalf("identity ID = %s", identity.ID)
	}
}

func TestResolveMapsStableStatuses(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		want   error
	}{
		{name: "unauthorized", status: http.StatusUnauthorized, body: `{"code":"unauthorized"}`, want: currentorganization.ErrUnauthorized},
		{name: "disabled", status: http.StatusForbidden, body: `{"code":"account_disabled"}`, want: currentorganization.ErrAccountDisabled},
		{name: "deleted", status: http.StatusForbidden, body: `{"code":"account_deleted"}`, want: currentorganization.ErrAccountDeleted},
		{name: "not ready", status: http.StatusConflict, body: `{"code":"identity_not_ready"}`, want: currentorganization.ErrIdentityNotReady},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(testCase.status)
				_, _ = w.Write([]byte(testCase.body))
			}))
			defer server.Close()
			client, err := New(server.URL, time.Second)
			if err != nil {
				t.Fatalf("New() error = %v", err)
			}
			_, err = client.Resolve(context.Background(), "Bearer token", "request")
			if !errors.Is(err, testCase.want) {
				t.Fatalf("Resolve() error = %v, want %v", err, testCase.want)
			}
		})
	}
}

func TestResolveRejectsMalformedAndOversizedResponses(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{name: "malformed", body: `not-json`},
		{name: "oversized", body: strings.Repeat("x", int(maxResponseBodyBytes)+1)},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(testCase.body))
			}))
			defer server.Close()
			client, err := New(server.URL, time.Second)
			if err != nil {
				t.Fatalf("New() error = %v", err)
			}
			_, err = client.Resolve(context.Background(), "Bearer sensitive-token", "request")
			if err == nil {
				t.Fatal("expected error")
			}
			if strings.Contains(err.Error(), "sensitive-token") || strings.Contains(err.Error(), testCase.body) {
				t.Fatal("error leaked token or upstream body")
			}
		})
	}
}

func TestResolveTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(50 * time.Millisecond)
		_, _ = w.Write([]byte(`{"id":"0198f3be-bf6f-7b0a-8a25-f8433567e0c1"}`))
	}))
	defer server.Close()
	client, err := New(server.URL, 10*time.Millisecond)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	_, err = client.Resolve(context.Background(), "Bearer token", "request")
	if err == nil {
		t.Fatal("expected timeout")
	}
}

type testPlatformTokenProvider struct {
	token string
	err   error
}

func (p testPlatformTokenProvider) Token(
	_ context.Context,
) (string, error) {
	return p.token, p.err
}

func TestResolveForwardsCloudRunAndClerkTokensSeparately(
	t *testing.T,
) {
	server := httptest.NewServer(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if got := r.Header.Get("Authorization"); got != "Bearer clerk-token" {
				t.Fatalf("Authorization = %q", got)
			}
			if got := r.Header.Get(
				"X-Serverless-Authorization",
			); got != "Bearer google-id-token" {
				t.Fatalf("X-Serverless-Authorization = %q", got)
			}
			if got := r.Header.Get("X-Request-Id"); got != "request-cloud-run" {
				t.Fatalf("X-Request-Id = %q", got)
			}

			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(
				[]byte(
					`{"id":"0198f3be-bf6f-7b0a-8a25-f8433567e0c1"}`,
				),
			)
		}),
	)
	defer server.Close()

	client, err := New(
		server.URL,
		time.Second,
		withPlatformTokenProvider(
			testPlatformTokenProvider{
				token: "google-id-token",
			},
		),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	identity, err := client.Resolve(
		context.Background(),
		"Bearer clerk-token",
		"request-cloud-run",
	)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if identity.ID.String() != "0198f3be-bf6f-7b0a-8a25-f8433567e0c1" {
		t.Fatalf("identity ID = %s", identity.ID)
	}
}

func TestResolveDoesNotCallIdentityWhenPlatformTokenFails(
	t *testing.T,
) {
	upstreamCalled := make(chan struct{}, 1)

	server := httptest.NewServer(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			upstreamCalled <- struct{}{}
			w.WriteHeader(http.StatusInternalServerError)
		}),
	)
	defer server.Close()

	client, err := New(
		server.URL,
		time.Second,
		withPlatformTokenProvider(
			testPlatformTokenProvider{
				err: errors.New("metadata unavailable"),
			},
		),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	_, err = client.Resolve(
		context.Background(),
		"Bearer sensitive-clerk-token",
		"request-platform-failure",
	)
	if err == nil {
		t.Fatal("expected platform token error")
	}
	if strings.Contains(err.Error(), "sensitive-clerk-token") {
		t.Fatal("platform token error leaked Clerk token")
	}

	select {
	case <-upstreamCalled:
		t.Fatal("Identity upstream was called after platform token failure")
	default:
	}
}

func TestResolveTreatsPlatformUnauthorizedAsUpstreamFailure(
	t *testing.T,
) {
	testCases := []struct {
		name string
		body string
	}{
		{
			name: "empty Cloud Run response",
			body: "",
		},
		{
			name: "unknown upstream error code",
			body: `{"code":"platform_unauthorized"}`,
		},
		{
			name: "non-JSON Cloud Run response",
			body: "Unauthorized",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			server := httptest.NewServer(
				http.HandlerFunc(
					func(w http.ResponseWriter, _ *http.Request) {
						w.WriteHeader(http.StatusUnauthorized)
						_, _ = w.Write([]byte(testCase.body))
					},
				),
			)
			defer server.Close()

			client, err := New(
				server.URL,
				time.Second,
				withPlatformTokenProvider(
					testPlatformTokenProvider{
						token: "google-id-token",
					},
				),
			)
			if err != nil {
				t.Fatalf("New() error = %v", err)
			}

			_, err = client.Resolve(
				context.Background(),
				"Bearer sensitive-clerk-token",
				"request-platform-unauthorized",
			)
			if err == nil {
				t.Fatal("expected upstream authentication error")
			}
			if errors.Is(
				err,
				currentorganization.ErrUnauthorized,
			) {
				t.Fatal(
					"platform authentication failure mapped to user unauthorized",
				)
			}
			if strings.Contains(
				err.Error(),
				"sensitive-clerk-token",
			) {
				t.Fatal(
					"upstream authentication error leaked Clerk token",
				)
			}
			if testCase.body != "" && strings.Contains(
				err.Error(),
				testCase.body,
			) {
				t.Fatal(
					"upstream authentication error leaked response body",
				)
			}
		})
	}
}
