package httpapi

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/DoMinhHHung/bridgeworks/service/organization-service/internal/authn"
	"github.com/DoMinhHHung/bridgeworks/service/organization-service/internal/authorization"
	"github.com/DoMinhHHung/bridgeworks/service/organization-service/internal/currentorganization"
)

const actorResolutionTimeout = 4 * time.Second

type CurrentOrganizationResolver interface {
	Resolve(context.Context, authorization.Principal, string, string) (currentorganization.Result, error)
}

type organizationResponse struct {
	ID     string  `json:"id"`
	Name   *string `json:"name"`
	Slug   *string `json:"slug"`
	Status string  `json:"status"`
}

type membershipResponse struct {
	ID             string   `json:"id"`
	OrganizationID string   `json:"organization_id"`
	Role           string   `json:"role"`
	Permissions    []string `json:"permissions"`
}

func CurrentOrganization(resolver CurrentOrganizationResolver) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		result, ok := resolveActor(w, r, resolver)
		if !ok {
			return
		}
		if err := currentorganization.RequirePermission(result, authorization.PermissionOrganizationRead); err != nil {
			writeActorError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, organizationResponse{
			ID: result.Organization.ID.String(), Name: result.Organization.Name,
			Slug: result.Organization.Slug, Status: result.Organization.Status,
		})
	}
}

func CurrentMembership(resolver CurrentOrganizationResolver) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		result, ok := resolveActor(w, r, resolver)
		if !ok {
			return
		}
		writeJSON(w, http.StatusOK, membershipResponse{
			ID:             result.Membership.ID.String(),
			OrganizationID: result.Membership.OrganizationID.String(),
			Role:           result.Actor.Role,
			Permissions:    result.Actor.Permissions(),
		})
	}
}

func resolveActor(w http.ResponseWriter, r *http.Request, resolver CurrentOrganizationResolver) (currentorganization.Result, bool) {
	principal, ok := authn.PrincipalFromContext(r.Context())
	if !ok {
		w.Header().Set("WWW-Authenticate", `Bearer realm="bridgeworks"`)
		writeError(w, r, http.StatusUnauthorized, "unauthorized", "authentication required")
		return currentorganization.Result{}, false
	}
	ctx, cancel := context.WithTimeout(r.Context(), actorResolutionTimeout)
	defer cancel()
	result, err := resolver.Resolve(
		ctx,
		principal,
		r.Header.Get("Authorization"),
		RequestIDFromContext(r.Context()),
	)
	if err != nil {
		writeActorError(w, r, err)
		return currentorganization.Result{}, false
	}
	return result, true
}

func writeActorError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, currentorganization.ErrUnauthorized):
		w.Header().Set("WWW-Authenticate", `Bearer realm="bridgeworks"`)
		writeError(w, r, http.StatusUnauthorized, "unauthorized", "authentication required")
	case errors.Is(err, currentorganization.ErrAccountDisabled):
		writeError(w, r, http.StatusForbidden, "account_disabled", "account is disabled")
	case errors.Is(err, currentorganization.ErrAccountDeleted):
		writeError(w, r, http.StatusForbidden, "account_deleted", "account is deleted")
	case errors.Is(err, currentorganization.ErrIdentityNotReady):
		w.Header().Set("Retry-After", "2")
		writeError(w, r, http.StatusConflict, "identity_not_ready", "identity is not ready")
	case errors.Is(err, currentorganization.ErrOrganizationContextRequired):
		writeError(w, r, http.StatusConflict, "organization_context_required", "active organization context required")
	case errors.Is(err, currentorganization.ErrOrganizationNotReady):
		w.Header().Set("Retry-After", "2")
		writeError(w, r, http.StatusConflict, "organization_not_ready", "organization is not ready")
	case errors.Is(err, currentorganization.ErrOrganizationDisabled):
		writeError(w, r, http.StatusForbidden, "organization_disabled", "organization access is disabled")
	case errors.Is(err, currentorganization.ErrOrganizationDeleted):
		writeError(w, r, http.StatusForbidden, "organization_deleted", "organization is deleted")
	case errors.Is(err, currentorganization.ErrMembershipRequired):
		writeError(w, r, http.StatusForbidden, "membership_required", "active organization membership required")
	case errors.Is(err, currentorganization.ErrPermissionDenied):
		writeError(w, r, http.StatusForbidden, "permission_denied", "permission denied")
	default:
		writeError(w, r, http.StatusServiceUnavailable, "service_unavailable", "service temporarily unavailable")
	}
}
