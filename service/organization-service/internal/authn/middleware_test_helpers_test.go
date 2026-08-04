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
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func newTestMiddleware(t *testing.T, key *rsa.PrivateKey) (func(http.Handler) http.Handler, *bytes.Buffer) {
	t.Helper()
	logs := &bytes.Buffer{}
	middleware, err := New(Config{JWTKey: publicKeyPEM(t, key), Issuer: testIssuer, AuthorizedParties: []string{testAuthorizedParty}, Leeway: 5 * time.Second}, slog.New(slog.NewTextHandler(logs, nil)), func(ctx context.Context) string {
		value, _ := ctx.Value(requestIDContextKey{}).(string)
		return value
	})
	if err != nil {
		t.Fatal(err)
	}
	return middleware, logs
}

func serveRequest(middleware func(http.Handler) http.Handler, authorizationHeader string, next http.Handler) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodGet, "/organizations/current", nil)
	request = request.WithContext(context.WithValue(request.Context(), requestIDContextKey{}, testRequestID))
	if authorizationHeader != "" {
		request.Header.Set("Authorization", authorizationHeader)
	}
	response := httptest.NewRecorder()
	middleware(next).ServeHTTP(response, request)
	return response
}

func assertUnauthorized(t *testing.T, response *httptest.ResponseRecorder) {
	t.Helper()
	if response.Code != http.StatusUnauthorized || response.Header().Get("WWW-Authenticate") != challenge {
		t.Fatalf("status=%d challenge=%q body=%s", response.Code, response.Header().Get("WWW-Authenticate"), response.Body.String())
	}
	var body errorEnvelope
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Code != "unauthorized" || body.Message != "authentication required" || body.RequestID != testRequestID || body.Details != nil {
		t.Fatalf("body=%+v", body)
	}
}

func generateRSAKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func publicKeyPEM(t *testing.T, key *rsa.PrivateKey) string {
	t.Helper()
	der, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}))
}

func signJWT(t *testing.T, key *rsa.PrivateKey, claims map[string]any) string {
	t.Helper()
	headerJSON, err := json.Marshal(map[string]any{"alg": "RS256", "typ": "JWT"})
	if err != nil {
		t.Fatal(err)
	}
	claimsJSON, err := json.Marshal(claims)
	if err != nil {
		t.Fatal(err)
	}
	unsigned := base64.RawURLEncoding.EncodeToString(headerJSON) + "." + base64.RawURLEncoding.EncodeToString(claimsJSON)
	digest := sha256.Sum256([]byte(unsigned))
	signature, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	return unsigned + "." + base64.RawURLEncoding.EncodeToString(signature)
}

func validClaims(now time.Time) map[string]any {
	return map[string]any{
		"iss":             testIssuer,
		"sub":             testUserID,
		"sid":             testSessionID,
		"azp":             testAuthorizedParty,
		"org_id":          testOrganizationID,
		"org_role":        "org:admin",
		"org_permissions": []string{"org:system:ignored"},
		"iat":             now.Add(-time.Minute).Unix(),
		"nbf":             now.Add(-time.Minute).Unix(),
		"exp":             now.Add(5 * time.Minute).Unix(),
	}
}

func withClaims(base map[string]any, overrides map[string]any) map[string]any {
	result := make(map[string]any, len(base)+len(overrides))
	for key, value := range base {
		result[key] = value
	}
	for key, value := range overrides {
		result[key] = value
	}
	return result
}

func withoutClaim(base map[string]any, key string) map[string]any {
	result := withClaims(base, nil)
	delete(result, key)
	return result
}
