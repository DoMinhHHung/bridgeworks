package authn

import (
	"net/http"
	"testing"
	"time"
)

func TestMiddlewareIgnoresPlatformAndOrganizationRoleLikeClaims(t *testing.T) {
	t.Parallel()

	key := generateRSAKey(t)
	middleware, _ := newTestMiddleware(t, key, 5*time.Second)
	claims := claimsWith(validClaims(time.Now().UTC()), map[string]any{
		"platform_admin": true,
		"role":           "platform_admin",
		"org_role":       "org:admin",
		"org_permissions": []string{
			"organization:manage",
			"organization.verification.review",
		},
		"metadata": map[string]any{"role": "platform_admin"},
	})
	token := signJWT(t, key, claims)

	var principal Principal
	response := serveAuthenticatedRequest(t, middleware, "Bearer "+token, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var ok bool
		principal, ok = PrincipalFromContext(r.Context())
		if !ok {
			t.Fatal("principal missing")
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d body=%s", response.Code, response.Body.String())
	}
	if principal != (Principal{ClerkUserID: testClerkUserID, SessionID: testSessionID}) {
		t.Fatalf("role-like claims changed narrow principal: %+v", principal)
	}
}
