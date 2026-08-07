package httpapi

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/DoMinhHHung/bridgeworks/service/identity-service/internal/authn"
	"github.com/DoMinhHHung/bridgeworks/service/identity-service/internal/platformaccess"
)

const platformAccessLookupTimeout = 2 * time.Second

type PlatformAccessResolver interface {
	Resolve(context.Context, string) (platformaccess.Access, error)
}

type platformAccessResponse struct {
	Roles       []string `json:"roles"`
	Permissions []string `json:"permissions"`
}

func platformAccessResponseHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		appendVaryHeader(w.Header(), "Authorization")
		next.ServeHTTP(w, r)
	})
}

func appendVaryHeader(header http.Header, value string) {
	for _, existing := range header.Values("Vary") {
		for _, part := range strings.Split(existing, ",") {
			if strings.EqualFold(strings.TrimSpace(part), value) {
				return
			}
		}
	}
	header.Add("Vary", value)
}

func platformAccessHandler(logger *slog.Logger, resolver PlatformAccessResolver) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		principal, ok := authn.PrincipalFromContext(r.Context())
		if !ok || principal.ClerkUserID == "" {
			writeError(w, r, http.StatusUnauthorized, "unauthorized", "authentication required", nil)
			return
		}
		if resolver == nil {
			writePlatformAccessUnavailable(logger, w, r)
			return
		}

		lookupContext, cancel := context.WithTimeout(r.Context(), platformAccessLookupTimeout)
		defer cancel()
		access, err := resolver.Resolve(lookupContext, principal.ClerkUserID)
		switch {
		case err == nil:
			if access.Roles == nil {
				access.Roles = []string{}
			}
			if access.Permissions == nil {
				access.Permissions = []string{}
			}
			writeJSON(w, http.StatusOK, platformAccessResponse{
				Roles:       access.Roles,
				Permissions: access.Permissions,
			})
		case errors.Is(err, platformaccess.ErrAccountDisabled):
			writeError(w, r, http.StatusForbidden, "account_disabled", "account is disabled", nil)
		case errors.Is(err, platformaccess.ErrAccountDeleted):
			writeError(w, r, http.StatusForbidden, "account_deleted", "account is deleted", nil)
		case errors.Is(err, platformaccess.ErrIdentityNotReady):
			w.Header().Set("Retry-After", identityRetryAfter)
			writeError(w, r, http.StatusConflict, "identity_not_ready", "identity synchronization is not complete", nil)
		default:
			writePlatformAccessUnavailable(logger, w, r)
		}
	}
}

func writePlatformAccessUnavailable(logger *slog.Logger, w http.ResponseWriter, r *http.Request) {
	if logger != nil {
		logger.ErrorContext(
			r.Context(),
			"platform access lookup failed",
			"request_id", RequestIDFromContext(r.Context()),
			"reason", "dependency_unavailable",
		)
	}
	writeError(w, r, http.StatusServiceUnavailable, "service_unavailable", "service temporarily unavailable", nil)
}
