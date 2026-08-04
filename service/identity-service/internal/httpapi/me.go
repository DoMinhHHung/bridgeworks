package httpapi

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/DoMinhHHung/bridgeworks/service/identity-service/internal/authn"
	"github.com/DoMinhHHung/bridgeworks/service/identity-service/internal/currentuser"
)

const (
	currentUserLookupTimeout = 2 * time.Second
	identityRetryAfter       = "2"
)

type CurrentUserGetter interface {
	Get(context.Context, string) (currentuser.User, error)
}

type currentUserResponse struct {
	ID           string  `json:"id"`
	IDUser       string  `json:"id_user"`
	PrimaryEmail *string `json:"primary_email"`
	Status       string  `json:"status"`
	CreatedAt    string  `json:"created_at"`
	UpdatedAt    string  `json:"updated_at"`
}

func currentUserHandler(logger *slog.Logger, getter CurrentUserGetter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		principal, ok := authn.PrincipalFromContext(r.Context())
		if !ok || principal.ClerkUserID == "" {
			logger.WarnContext(
				r.Context(),
				"current user request rejected",
				"request_id", RequestIDFromContext(r.Context()),
				"reason", "principal_missing",
			)
			writeError(w, r, http.StatusUnauthorized, "unauthorized", "authentication required", nil)
			return
		}
		if getter == nil {
			logger.ErrorContext(
				r.Context(),
				"current user lookup failed",
				"request_id", RequestIDFromContext(r.Context()),
				"reason", "service_unavailable",
			)
			writeError(w, r, http.StatusServiceUnavailable, "service_unavailable", "service temporarily unavailable", nil)
			return
		}

		lookupContext, cancel := context.WithTimeout(r.Context(), currentUserLookupTimeout)
		defer cancel()

		user, err := getter.Get(lookupContext, principal.ClerkUserID)
		switch {
		case err == nil:
			writeJSON(w, http.StatusOK, currentUserResponse{
				ID:           user.ID.String(),
				IDUser:       user.IDUser,
				PrimaryEmail: user.PrimaryEmail,
				Status:       user.Status,
				CreatedAt:    user.CreatedAt.UTC().Format(time.RFC3339Nano),
				UpdatedAt:    user.UpdatedAt.UTC().Format(time.RFC3339Nano),
			})
		case errors.Is(err, currentuser.ErrAccountDisabled):
			writeError(w, r, http.StatusForbidden, "account_disabled", "account is disabled", nil)
		case errors.Is(err, currentuser.ErrAccountDeleted):
			writeError(w, r, http.StatusForbidden, "account_deleted", "account is deleted", nil)
		case errors.Is(err, currentuser.ErrIdentityNotReady):
			w.Header().Set("Retry-After", identityRetryAfter)
			writeError(w, r, http.StatusConflict, "identity_not_ready", "identity synchronization is not complete", nil)
		default:
			logger.ErrorContext(
				r.Context(),
				"current user lookup failed",
				"request_id", RequestIDFromContext(r.Context()),
				"reason", "dependency_unavailable",
			)
			writeError(w, r, http.StatusServiceUnavailable, "service_unavailable", "service temporarily unavailable", nil)
		}
	}
}
