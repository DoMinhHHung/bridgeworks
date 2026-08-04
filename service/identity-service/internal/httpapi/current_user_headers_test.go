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

	"github.com/DoMinhHHung/bridgeworks/service/identity-service/internal/authn"
	"github.com/DoMinhHHung/bridgeworks/service/identity-service/internal/currentuser"
	"github.com/google/uuid"
)

func TestCurrentUserRouteDisablesCachingForEveryResponseCategory(t *testing.T) {
	t.Parallel()

	activeUser := currentuser.User{
		ID:        uuid.MustParse("0198f3be-bf6f-7b0a-8a25-f8433567e0c1"),
		IDUser:    "bw012303082645",
		Status:    "active",
		CreatedAt: time.Date(2026, time.August, 3, 3, 4, 5, 0, time.UTC),
		UpdatedAt: time.Date(2026, time.August, 3, 3, 4, 5, 0, time.UTC),
	}

	tests := []struct {
		name         string
		unauthorized bool
		user         currentuser.User
		getError     error
		wantStatus   int
	}{
		{name: "success", user: activeUser, wantStatus: http.StatusOK},
		{name: "unauthorized", unauthorized: true, wantStatus: http.StatusUnauthorized},
		{name: "disabled", getError: currentuser.ErrAccountDisabled, wantStatus: http.StatusForbidden},
		{name: "deleted", getError: currentuser.ErrAccountDeleted, wantStatus: http.StatusForbidden},
		{name: "identity not ready", getError: currentuser.ErrIdentityNotReady, wantStatus: http.StatusConflict},
		{name: "database unavailable", getError: errors.New("database unavailable"), wantStatus: http.StatusServiceUnavailable},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			authenticate := func(next http.Handler) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Add("Vary", "Origin")
					if tt.unauthorized {
						writeError(w, r, http.StatusUnauthorized, "unauthorized", "authentication required", nil)
						return
					}
					ctx := authn.ContextWithPrincipal(r.Context(), authn.Principal{
						ClerkUserID: "user_test",
						SessionID:   "sess_test",
					})
					next.ServeHTTP(w, r.WithContext(ctx))
				})
			}
			getter := currentUserGetterFunc(func(context.Context, string) (currentuser.User, error) {
				return tt.user, tt.getError
			})
			router := NewRouter(
				RouterConfig{
					ServiceName:                "identity-service",
					ReadinessTimeout:           time.Second,
					ClerkWebhookProcessTimeout: time.Second,
					ClerkWebhookMaxBodyBytes:   1 << 20,
				},
				slog.New(slog.NewTextHandler(io.Discard, nil)),
				nil,
				nil,
				nil,
				authenticate,
				getter,
			)

			request := httptest.NewRequest(http.MethodGet, "/me", nil)
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)

			if response.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d, body = %s", response.Code, tt.wantStatus, response.Body.String())
			}
			if response.Header().Get("Cache-Control") != currentUserCacheControl {
				t.Fatalf("Cache-Control = %q", response.Header().Get("Cache-Control"))
			}
			assertHeaderToken(t, response.Header(), "Vary", "Origin")
			assertHeaderToken(t, response.Header(), "Vary", "Authorization")
			if countHeaderToken(response.Header(), "Vary", "Authorization") != 1 {
				t.Fatalf("Vary Authorization count = %d", countHeaderToken(response.Header(), "Vary", "Authorization"))
			}
		})
	}
}

func assertHeaderToken(t *testing.T, header http.Header, name, want string) {
	t.Helper()
	if countHeaderToken(header, name, want) == 0 {
		t.Fatalf("%s = %q, missing %q", name, header.Values(name), want)
	}
}

func countHeaderToken(header http.Header, name, want string) int {
	count := 0
	for _, value := range header.Values(name) {
		for _, token := range strings.Split(value, ",") {
			if strings.EqualFold(strings.TrimSpace(token), want) {
				count++
			}
		}
	}
	return count
}
