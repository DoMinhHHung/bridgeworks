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
