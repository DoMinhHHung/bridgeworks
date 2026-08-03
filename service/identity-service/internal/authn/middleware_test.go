package authn

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const (
	testIssuer          = "https://clerk.bridgeworks.test"
	testAuthorizedParty = "http://localhost:3000"
	testClerkUserID     = "user_test_sensitive_identifier"
	testSessionID       = "sess_test_sensitive_identifier"
	testRequestID       = "auth-request-id"
)

type testRequestIDContextKey struct{}

func TestMiddlewareRejectsInvalidAuthentication(t *testing.T) {
	t.Parallel()

	key := generateRSAKey(t)
	otherKey := generateRSAKey(t)
	now := time.Now().UTC()

	tests := []struct {
		name          string
		authorization string
		claims        map[string]any
		signingKey    *rsa.PrivateKey
	}{
		{name: "missing Authorization"},
		{name: "wrong auth scheme", authorization: "Basic credentials"},
		{name: "empty bearer token", authorization: "Bearer "},
		{name: "malformed JWT", authorization: "Bearer not-a-jwt"},
		{
			name:       "invalid signature",
			claims:     validClaims(now),
			signingKey: otherKey,
		},
		{
			name: "expired token",
			claims: claimsWith(validClaims(now), map[string]any{
				"exp": now.Add(-2 * time.Minute).Unix(),
			}),
			signingKey: key,
		},
		{
			name: "not-before in future",
			claims: claimsWith(validClaims(now), map[string]any{
				"nbf": now.Add(2 * time.Minute).Unix(),
			}),
			signingKey: key,
		},
		{
			name: "wrong issuer",
			claims: claimsWith(validClaims(now), map[string]any{
				"iss": "https://clerk.wrong.test",
			}),
			signingKey: key,
		},
		{
			name:       "missing subject",
			claims:     claimsWithout(validClaims(now), "sub"),
			signingKey: key,
		},
		{
			name:       "missing session ID",
			claims:     claimsWithout(validClaims(now), "sid"),
			signingKey: key,
		},
		{
			name: "wrong authorized party",
			claims: claimsWith(validClaims(now), map[string]any{
				"azp": "https://attacker.example",
			}),
			signingKey: key,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			middleware, _ := newTestMiddleware(t, key, 5*time.Second)
			authorization := tt.authorization
			if tt.claims != nil {
				authorization = "Bearer " + signJWT(t, tt.signingKey, tt.claims)
			}

			response := serveAuthenticatedRequest(t, middleware, authorization, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				t.Fatal("protected handler must not run")
			}))
			assertUnauthorized(t, response)
		})
	}
}

func TestMiddlewareAcceptsValidClerkTokenAndStoresNarrowPrincipal(t *testing.T) {
	t.Parallel()

	key := generateRSAKey(t)
	middleware, _ := newTestMiddleware(t, key, 5*time.Second)
	token := signJWT(t, key, validClaims(time.Now().UTC()))

	var principal Principal
	response := serveAuthenticatedRequest(t, middleware, "Bearer "+token, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var ok bool
		principal, ok = PrincipalFromContext(r.Context())
		if !ok {
			t.Fatal("verified principal missing from context")
		}
		w.WriteHeader(http.StatusNoContent)
	}))

	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if principal != (Principal{ClerkUserID: testClerkUserID, SessionID: testSessionID}) {
		t.Fatalf("principal = %+v", principal)
	}
}

func TestMiddlewareHonorsConfiguredLeeway(t *testing.T) {
	t.Parallel()

	key := generateRSAKey(t)
	middleware, _ := newTestMiddleware(t, key, 5*time.Second)
	now := time.Now().UTC()
	claims := claimsWith(validClaims(now), map[string]any{
		"exp": now.Add(-time.Second).Unix(),
	})

	response := serveAuthenticatedRequest(t, middleware, "Bearer "+signJWT(t, key, claims), http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
}

func TestMiddlewareDoesNotLeakTokenKeyOrClaims(t *testing.T) {
	t.Parallel()

	key := generateRSAKey(t)
	middleware, logs := newTestMiddleware(t, key, 5*time.Second)
	token := signJWT(t, key, claimsWith(validClaims(time.Now().UTC()), map[string]any{
		"iss": "https://clerk.invalid.test",
	}))

	response := serveAuthenticatedRequest(t, middleware, "Bearer "+token, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("protected handler must not run")
	}))
	assertUnauthorized(t, response)

	combined := response.Body.String() + logs.String()
	for _, forbidden := range []string{
		token,
		publicKeyPEM(t, key),
		testClerkUserID,
		testSessionID,
		"https://clerk.invalid.test",
	} {
		if strings.Contains(combined, forbidden) {
			t.Fatalf("authentication output leaked sensitive value %q", forbidden)
		}
	}
}

func TestNewRejectsInvalidJWKWithoutEchoingIt(t *testing.T) {
	t.Parallel()

	const invalidKey = "not-a-public-key-sensitive-value"
	_, err := New(Config{
		JWTKey:            invalidKey,
		Issuer:            testIssuer,
		AuthorizedParties: []string{testAuthorizedParty},
		Leeway:            5 * time.Second,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)), func(context.Context) string { return "" })
	if err == nil {
		t.Fatal("expected invalid JWK error")
	}
	if strings.Contains(err.Error(), invalidKey) {
		t.Fatalf("error leaked JWK: %q", err)
	}
}

func newTestMiddleware(t *testing.T, key *rsa.PrivateKey, leeway time.Duration) (func(http.Handler) http.Handler, *bytes.Buffer) {
	t.Helper()

	logs := &bytes.Buffer{}
	middleware, err := New(Config{
		JWTKey:            publicKeyPEM(t, key),
		Issuer:            testIssuer,
		AuthorizedParties: []string{testAuthorizedParty},
		Leeway:            leeway,
	}, slog.New(slog.NewTextHandler(logs, nil)), func(ctx context.Context) string {
		requestID, _ := ctx.Value(testRequestIDContextKey{}).(string)
		return requestID
	})
	if err != nil {
		t.Fatalf("new authentication middleware: %v", err)
	}
	return middleware, logs
}

func serveAuthenticatedRequest(
	t *testing.T,
	middleware func(http.Handler) http.Handler,
	authorization string,
	next http.Handler,
) *httptest.ResponseRecorder {
	t.Helper()

	request := httptest.NewRequest(http.MethodGet, "/me", nil)
	request = request.WithContext(context.WithValue(request.Context(), testRequestIDContextKey{}, testRequestID))
	if authorization != "" {
		request.Header.Set("Authorization", authorization)
	}
	response := httptest.NewRecorder()
	middleware(next).ServeHTTP(response, request)
	return response
}

func assertUnauthorized(t *testing.T, response *httptest.ResponseRecorder) {
	t.Helper()

	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if response.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("Content-Type = %q", response.Header().Get("Content-Type"))
	}

	var body errorEnvelope
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if body.Code != unauthorizedCode || body.Message != unauthorizedMessage ||
		body.RequestID != testRequestID || body.Details != nil {
		t.Fatalf("unexpected error envelope: %+v", body)
	}
}

func generateRSAKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate RSA key: %v", err)
	}
	return key
}

func publicKeyPEM(t *testing.T, key *rsa.PrivateKey) string {
	t.Helper()

	der, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatalf("marshal public key: %v", err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}))
}

func signJWT(t *testing.T, key *rsa.PrivateKey, claims map[string]any) string {
	t.Helper()

	headerJSON, err := json.Marshal(map[string]any{"alg": "RS256", "typ": "JWT"})
	if err != nil {
		t.Fatalf("marshal JWT header: %v", err)
	}
	claimsJSON, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("marshal JWT claims: %v", err)
	}

	unsigned := base64.RawURLEncoding.EncodeToString(headerJSON) + "." +
		base64.RawURLEncoding.EncodeToString(claimsJSON)
	digest := sha256.Sum256([]byte(unsigned))
	signature, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatalf("sign JWT: %v", err)
	}
	return unsigned + "." + base64.RawURLEncoding.EncodeToString(signature)
}

func validClaims(now time.Time) map[string]any {
	return map[string]any{
		"iss": testIssuer,
		"sub": testClerkUserID,
		"sid": testSessionID,
		"azp": testAuthorizedParty,
		"iat": now.Add(-time.Minute).Unix(),
		"nbf": now.Add(-time.Minute).Unix(),
		"exp": now.Add(5 * time.Minute).Unix(),
	}
}

func claimsWith(base map[string]any, overrides map[string]any) map[string]any {
	result := make(map[string]any, len(base)+len(overrides))
	for key, value := range base {
		result[key] = value
	}
	for key, value := range overrides {
		result[key] = value
	}
	return result
}

func claimsWithout(base map[string]any, key string) map[string]any {
	result := claimsWith(base, nil)
	delete(result, key)
	return result
}
