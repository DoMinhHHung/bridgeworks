package httpapi

import (
	"context"
	"encoding/json"
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

type currentUserGetterFunc func(context.Context, string) (currentuser.User, error)

func (f currentUserGetterFunc) Get(ctx context.Context, clerkUserID string) (currentuser.User, error) {
	return f(ctx, clerkUserID)
}

func TestCurrentUserActiveReturnsPublicProjection(t *testing.T) {
	t.Parallel()

	id := uuid.MustParse("0198f3be-bf6f-7b0a-8a25-f8433567e0c1")
	createdAt := time.Date(2026, time.August, 3, 3, 4, 5, 123000000, time.UTC)
	updatedAt := createdAt.Add(2 * time.Hour)
	user := currentuser.User{
		ID:           id,
		ClerkUserID:  "user_private_identifier",
		PrimaryEmail: stringPointer("developer@example.com"),
		IDUser:       "bw012303082645",
		Status:       "active",
		CreatedAt:    createdAt,
		UpdatedAt:    updatedAt,
	}

	var calls int
	response := serveCurrentUser(t, currentUserGetterFunc(func(ctx context.Context, clerkUserID string) (currentuser.User, error) {
		calls++
		if _, ok := ctx.Deadline(); !ok {
			t.Fatal("current user lookup has no deadline")
		}
		if clerkUserID != user.ClerkUserID {
			t.Fatalf("clerkUserID = %q", clerkUserID)
		}
		return user, nil
	}))

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if calls != 1 {
		t.Fatalf("lookup calls = %d", calls)
	}
	if strings.Contains(response.Body.String(), "clerk_user_id") ||
		strings.Contains(response.Body.String(), user.ClerkUserID) ||
		strings.Contains(response.Body.String(), "session_id") {
		t.Fatalf("private identity leaked: %s", response.Body.String())
	}

	var body currentUserResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode current user response: %v", err)
	}
	if body.ID != id.String() || body.IDUser != user.IDUser || body.Status != "active" {
		t.Fatalf("identity fields changed: %+v", body)
	}
	if body.PrimaryEmail == nil || *body.PrimaryEmail != "developer@example.com" {
		t.Fatalf("primary_email = %#v", body.PrimaryEmail)
	}
	if body.CreatedAt != createdAt.Format(time.RFC3339Nano) || body.UpdatedAt != updatedAt.Format(time.RFC3339Nano) {
		t.Fatalf("timestamps = %q / %q", body.CreatedAt, body.UpdatedAt)
	}
}

func TestCurrentUserSupportsNullableEmail(t *testing.T) {
	t.Parallel()

	response := serveCurrentUser(t, currentUserGetterFunc(func(context.Context, string) (currentuser.User, error) {
		return currentuser.User{
			ID:        uuid.MustParse("0198f3be-bf6f-7b0a-8a25-f8433567e0c1"),
			IDUser:    "bw012303082645",
			Status:    "active",
			CreatedAt: time.Date(2026, time.August, 3, 3, 4, 5, 0, time.UTC),
			UpdatedAt: time.Date(2026, time.August, 3, 3, 4, 5, 0, time.UTC),
		}, nil
	}))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}

	var body currentUserResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode current user response: %v", err)
	}
	if body.PrimaryEmail != nil {
		t.Fatalf("primary_email = %#v, want nil", body.PrimaryEmail)
	}
}

func TestCurrentUserEnforcesLifecycleAndFailureContracts(t *testing.T) {
	t.Parallel()

	const rawDatabaseError = "pgx: postgres://runtime:secret@identity-postgres:5432/bridgeworks"
	tests := []struct {
		name        string
		getError    error
		wantStatus  int
		wantCode    string
		wantMessage string
		wantRetry   string
	}{
		{name: "disabled", getError: currentuser.ErrAccountDisabled, wantStatus: http.StatusForbidden, wantCode: "account_disabled", wantMessage: "account is disabled"},
		{name: "deleted", getError: currentuser.ErrAccountDeleted, wantStatus: http.StatusForbidden, wantCode: "account_deleted", wantMessage: "account is deleted"},
		{name: "identity not ready", getError: currentuser.ErrIdentityNotReady, wantStatus: http.StatusConflict, wantCode: "identity_not_ready", wantMessage: "identity synchronization is not complete", wantRetry: identityRetryAfter},
		{name: "database error", getError: errors.New(rawDatabaseError), wantStatus: http.StatusServiceUnavailable, wantCode: "service_unavailable", wantMessage: "service temporarily unavailable"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var calls int
			response := serveCurrentUser(t, currentUserGetterFunc(func(context.Context, string) (currentuser.User, error) {
				calls++
				return currentuser.User{}, tt.getError
			}))
			if calls != 1 {
				t.Fatalf("lookup calls = %d", calls)
			}
			assertCurrentUserError(t, response, tt.wantStatus, tt.wantCode, tt.wantMessage)
			if response.Header().Get("Retry-After") != tt.wantRetry {
				t.Fatalf("Retry-After = %q, want %q", response.Header().Get("Retry-After"), tt.wantRetry)
			}
			if strings.Contains(response.Body.String(), rawDatabaseError) || strings.Contains(response.Body.String(), "pgx") {
				t.Fatalf("database detail leaked: %s", response.Body.String())
			}
		})
	}
}

func TestCurrentUserMissingPrincipalReturnsUnauthorizedWithoutLookup(t *testing.T) {
	t.Parallel()

	var calls int
	getter := currentUserGetterFunc(func(context.Context, string) (currentuser.User, error) {
		calls++
		return currentuser.User{}, nil
	})
	request := httptest.NewRequest(http.MethodGet, "/me", nil)
	request.Header.Set(requestIDHeader, "missing-principal-request")
	response := httptest.NewRecorder()
	RequestID(currentUserHandler(testLogger(), getter)).ServeHTTP(response, request)

	assertCurrentUserError(t, response, http.StatusUnauthorized, "unauthorized", "authentication required")
	if calls != 0 {
		t.Fatalf("lookup calls = %d", calls)
	}
}

func serveCurrentUser(t *testing.T, getter CurrentUserGetter) *httptest.ResponseRecorder {
	t.Helper()

	request := httptest.NewRequest(http.MethodGet, "/me", nil)
	request.Header.Set(requestIDHeader, "current-user-request")
	request = request.WithContext(authn.ContextWithPrincipal(request.Context(), authn.Principal{
		ClerkUserID: "user_private_identifier",
		SessionID:   "sess_private_identifier",
	}))
	response := httptest.NewRecorder()
	RequestID(currentUserHandler(testLogger(), getter)).ServeHTTP(response, request)
	return response
}

func assertCurrentUserError(
	t *testing.T,
	response *httptest.ResponseRecorder,
	status int,
	code string,
	message string,
) {
	t.Helper()

	if response.Code != status {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if response.Header().Get(requestIDHeader) == "" {
		t.Fatal("X-Request-Id response header is empty")
	}
	var body errorEnvelope
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error envelope: %v", err)
	}
	if body.Code != code || body.Message != message || body.RequestID == "" || body.Details != nil {
		t.Fatalf("unexpected error envelope: %+v", body)
	}
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
