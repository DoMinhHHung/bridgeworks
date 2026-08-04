package authn

import (
	"crypto/rsa"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/DoMinhHHung/bridgeworks/service/organization-service/internal/authorization"
)

func TestBearerTokenStrict(t *testing.T) {
	for _, value := range []string{"Bearer token", "bearer token"} {
		if token, ok := bearerToken(value); !ok || token != "token" {
			t.Fatalf("%q rejected", value)
		}
	}
	for _, value := range []string{"", "Basic token", "Bearer", "Bearer token extra"} {
		if _, ok := bearerToken(value); ok {
			t.Fatalf("%q accepted", value)
		}
	}
}

func TestMiddlewareRejectsAllInvalidAuthenticationWithChallenge(t *testing.T) {
	key := generateRSAKey(t)
	otherKey := generateRSAKey(t)
	now := time.Now().UTC()
	tests := []struct {
		name          string
		authorization string
		claims        map[string]any
		key           *rsa.PrivateKey
	}{
		{name: "missing"},
		{name: "wrong scheme", authorization: "Basic credentials"},
		{name: "empty bearer", authorization: "Bearer "},
		{name: "malformed", authorization: "Bearer not-a-jwt"},
		{name: "invalid signature", claims: validClaims(now), key: otherKey},
		{name: "expired", claims: withClaims(validClaims(now), map[string]any{"exp": now.Add(-2 * time.Minute).Unix()}), key: key},
		{name: "future nbf", claims: withClaims(validClaims(now), map[string]any{"nbf": now.Add(2 * time.Minute).Unix()}), key: key},
		{name: "wrong issuer", claims: withClaims(validClaims(now), map[string]any{"iss": "https://wrong.example"}), key: key},
		{name: "missing subject", claims: withoutClaim(validClaims(now), "sub"), key: key},
		{name: "missing session", claims: withoutClaim(validClaims(now), "sid"), key: key},
		{name: "wrong party", claims: withClaims(validClaims(now), map[string]any{"azp": "https://attacker.example"}), key: key},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			middleware, _ := newTestMiddleware(t, key)
			authorizationHeader := tc.authorization
			if tc.claims != nil {
				authorizationHeader = "Bearer " + signJWT(t, tc.key, tc.claims)
			}
			response := serveRequest(middleware, authorizationHeader, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				t.Fatal("protected handler ran")
			}))
			assertUnauthorized(t, response)
		})
	}
}

func TestMiddlewareStoresVerifiedActiveOrganizationPrincipal(t *testing.T) {
	key := generateRSAKey(t)
	middleware, _ := newTestMiddleware(t, key)
	response := serveRequest(middleware, "Bearer "+signJWT(t, key, validClaims(time.Now().UTC())), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		principal, ok := PrincipalFromContext(r.Context())
		if !ok {
			t.Fatal("principal missing")
		}
		want := authorization.Principal{ClerkUserID: testUserID, SessionID: testSessionID, ClerkOrganizationID: testOrganizationID}
		if principal != want {
			t.Fatalf("principal=%+v want=%+v", principal, want)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	if response.Code != http.StatusNoContent {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestMiddlewareAllowsVerifiedTokenWithoutOrganizationForApplication409(t *testing.T) {
	key := generateRSAKey(t)
	middleware, _ := newTestMiddleware(t, key)
	claims := withoutClaim(validClaims(time.Now().UTC()), "org_id")
	response := serveRequest(middleware, "Bearer "+signJWT(t, key, claims), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		principal, ok := PrincipalFromContext(r.Context())
		if !ok || principal.ClerkOrganizationID != "" {
			t.Fatalf("principal=%+v ok=%v", principal, ok)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	if response.Code != http.StatusNoContent {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestMiddlewareDoesNotLeakTokenClaimsOrKey(t *testing.T) {
	key := generateRSAKey(t)
	middleware, logs := newTestMiddleware(t, key)
	token := signJWT(t, key, withClaims(validClaims(time.Now().UTC()), map[string]any{"iss": "https://invalid.example"}))
	response := serveRequest(middleware, "Bearer "+token, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("protected handler ran")
	}))
	assertUnauthorized(t, response)
	combined := response.Body.String() + logs.String()
	for _, forbidden := range []string{token, publicKeyPEM(t, key), testUserID, testSessionID, testOrganizationID, "https://invalid.example"} {
		if strings.Contains(combined, forbidden) {
			t.Fatalf("leaked %q", forbidden)
		}
	}
}

func TestNewRejectsInvalidKeyWithoutEchoingIt(t *testing.T) {
	const invalidKey = "not-a-key-sensitive"
	_, err := New(Config{JWTKey: invalidKey, Issuer: testIssuer, AuthorizedParties: []string{testAuthorizedParty}, Leeway: 5 * time.Second}, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	if err == nil || strings.Contains(err.Error(), invalidKey) {
		t.Fatalf("err=%v", err)
	}
}
