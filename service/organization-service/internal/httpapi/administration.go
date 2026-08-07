package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/DoMinhHHung/bridgeworks/service/organization-service/internal/authn"
	"github.com/DoMinhHHung/bridgeworks/service/organization-service/internal/authorization"
	"github.com/DoMinhHHung/bridgeworks/service/organization-service/internal/businessverification"
	"github.com/DoMinhHHung/bridgeworks/service/organization-service/internal/currentorganization"
	"github.com/DoMinhHHung/bridgeworks/service/organization-service/internal/membershipadmin"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

const administrationCommandTimeout = 5 * time.Second

type BusinessEmailVerification interface {
	Verify(context.Context, authorization.ActorContext, *string) (businessverification.Result, error)
}

type MembershipAdministration interface {
	Invite(context.Context, authorization.ActorContext, string, string, string) (membershipadmin.InvitationResult, error)
	SetRole(context.Context, authorization.ActorContext, uuid.UUID, string) error
	TransferOwnership(context.Context, authorization.ActorContext, uuid.UUID) error
	Remove(context.Context, authorization.ActorContext, uuid.UUID) error
	Leave(context.Context, authorization.ActorContext) error
}

type invitationRequest struct {
	Email           string `json:"email"`
	ApplicationRole string `json:"application_role"`
}

type invitationResponse struct {
	ID              string `json:"id"`
	ApplicationRole string `json:"application_role"`
	Status          string `json:"status"`
}

type roleMutationRequest struct {
	ApplicationRole string `json:"application_role"`
}

type ownershipTransferRequest struct {
	TargetMembershipID string `json:"target_membership_id"`
}

type businessEmailVerificationResponse struct {
	Domain     string    `json:"domain"`
	VerifiedAt time.Time `json:"verified_at"`
}

func VerifyCurrentOrganizationBusinessEmail(
	resolver CurrentOrganizationResolver,
	verification BusinessEmailVerification,
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		result, ok := resolveActor(w, r, resolver)
		if !ok {
			return
		}
		if err := currentorganization.RequirePermission(result, authorization.PermissionOrganizationVerifyRequest); err != nil {
			writeActorError(w, r, err)
			return
		}
		var request struct{}
		if err := decodeOptionalEmptyJSON(w, r, &request); err != nil {
			writeOnboardingDecodeError(w, r, err)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), administrationCommandTimeout)
		defer cancel()
		verified, err := verification.Verify(ctx, result.Actor, result.Identity.PrimaryEmail)
		if err != nil {
			writeAdministrationError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, businessEmailVerificationResponse{
			Domain:     verified.Domain,
			VerifiedAt: verified.VerifiedAt,
		})
	}
}

func CreateCurrentOrganizationInvitation(
	resolver CurrentOrganizationResolver,
	administration MembershipAdministration,
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		result, ok := resolveActor(w, r, resolver)
		if !ok {
			return
		}
		if err := currentorganization.RequirePermission(result, authorization.PermissionMembershipInvite); err != nil {
			writeActorError(w, r, err)
			return
		}
		principal, ok := authn.PrincipalFromContext(r.Context())
		if !ok {
			writeActorError(w, r, currentorganization.ErrUnauthorized)
			return
		}
		var request invitationRequest
		if err := decodeBoundedJSON(w, r, &request); err != nil {
			writeOnboardingDecodeError(w, r, err)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), administrationCommandTimeout)
		defer cancel()
		invitation, err := administration.Invite(
			ctx,
			result.Actor,
			principal.ClerkUserID,
			request.Email,
			request.ApplicationRole,
		)
		if err != nil {
			writeAdministrationError(w, r, err)
			return
		}
		writeJSON(w, http.StatusAccepted, invitationResponse{
			ID:              invitation.ID.String(),
			ApplicationRole: invitation.ApplicationRole,
			Status:          "pending",
		})
	}
}

func PatchCurrentOrganizationMemberRole(
	resolver CurrentOrganizationResolver,
	administration MembershipAdministration,
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		result, ok := resolveActor(w, r, resolver)
		if !ok {
			return
		}
		if err := currentorganization.RequirePermission(result, authorization.PermissionMembershipRoleManage); err != nil {
			writeActorError(w, r, err)
			return
		}
		targetID, err := parseLocalMembershipID(chi.URLParam(r, "membershipID"))
		if err != nil {
			writeError(w, r, http.StatusNotFound, "membership_not_found", "membership not found")
			return
		}
		var request roleMutationRequest
		if err := decodeBoundedJSON(w, r, &request); err != nil {
			writeOnboardingDecodeError(w, r, err)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), administrationCommandTimeout)
		defer cancel()
		if err := administration.SetRole(ctx, result.Actor, targetID, request.ApplicationRole); err != nil {
			writeAdministrationError(w, r, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func TransferCurrentOrganizationOwnership(
	resolver CurrentOrganizationResolver,
	administration MembershipAdministration,
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		result, ok := resolveActor(w, r, resolver)
		if !ok {
			return
		}
		if err := currentorganization.RequirePermission(result, authorization.PermissionMembershipRoleManage); err != nil {
			writeActorError(w, r, err)
			return
		}
		var request ownershipTransferRequest
		if err := decodeBoundedJSON(w, r, &request); err != nil {
			writeOnboardingDecodeError(w, r, err)
			return
		}
		targetID, err := parseLocalMembershipID(request.TargetMembershipID)
		if err != nil {
			writeError(w, r, http.StatusNotFound, "membership_not_found", "membership not found")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), administrationCommandTimeout)
		defer cancel()
		if err := administration.TransferOwnership(ctx, result.Actor, targetID); err != nil {
			writeAdministrationError(w, r, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func RemoveCurrentOrganizationMember(
	resolver CurrentOrganizationResolver,
	administration MembershipAdministration,
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		result, ok := resolveActor(w, r, resolver)
		if !ok {
			return
		}
		if err := currentorganization.RequirePermission(result, authorization.PermissionMembershipManage); err != nil {
			writeActorError(w, r, err)
			return
		}
		targetID, err := parseLocalMembershipID(chi.URLParam(r, "membershipID"))
		if err != nil {
			writeError(w, r, http.StatusNotFound, "membership_not_found", "membership not found")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), administrationCommandTimeout)
		defer cancel()
		if err := administration.Remove(ctx, result.Actor, targetID); err != nil {
			writeAdministrationError(w, r, err)
			return
		}
		w.WriteHeader(http.StatusAccepted)
	}
}

func LeaveCurrentOrganization(
	resolver CurrentOrganizationResolver,
	administration MembershipAdministration,
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		result, ok := resolveActor(w, r, resolver)
		if !ok {
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), administrationCommandTimeout)
		defer cancel()
		if err := administration.Leave(ctx, result.Actor); err != nil {
			writeAdministrationError(w, r, err)
			return
		}
		w.WriteHeader(http.StatusAccepted)
	}
}

func parseLocalMembershipID(raw string) (uuid.UUID, error) {
	id, err := uuid.Parse(strings.TrimSpace(raw))
	if err != nil || id == uuid.Nil {
		return uuid.Nil, errors.New("invalid membership ID")
	}
	return id, nil
}

func writeAdministrationError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, businessverification.ErrVerifiedPrimaryEmailRequired):
		writeError(w, r, http.StatusConflict, "verified_primary_email_required", "verified primary email is required")
	case errors.Is(err, businessverification.ErrInvalidBusinessEmail):
		writeError(w, r, http.StatusUnprocessableEntity, "business_email_not_eligible", "verified email is not eligible for business verification")
	case errors.Is(err, businessverification.ErrPersonalEmailDomain):
		writeError(w, r, http.StatusUnprocessableEntity, "personal_email_domain", "personal email domain is not eligible")
	case errors.Is(err, businessverification.ErrPermissionDenied),
		errors.Is(err, membershipadmin.ErrPermissionDenied),
		errors.Is(err, membershipadmin.ErrOwnerMutationForbidden):
		writeError(w, r, http.StatusForbidden, "permission_denied", "permission denied")
	case errors.Is(err, businessverification.ErrMembershipNotActive),
		errors.Is(err, membershipadmin.ErrMembershipInactive):
		writeError(w, r, http.StatusConflict, "membership_not_active", "membership is not active")
	case errors.Is(err, membershipadmin.ErrInvalidRole), errors.Is(err, membershipadmin.ErrInvalidEmail),
		errors.Is(err, membershipadmin.ErrInvalidTransferTarget), errors.Is(err, membershipadmin.ErrSelfRemoval):
		writeError(w, r, http.StatusBadRequest, "invalid_request", "invalid request")
	case errors.Is(err, membershipadmin.ErrMembershipNotFound):
		writeError(w, r, http.StatusNotFound, "membership_not_found", "membership not found")
	case errors.Is(err, membershipadmin.ErrLastOwner):
		writeError(w, r, http.StatusConflict, "last_owner_required", "operation would remove the last active owner")
	case errors.Is(err, membershipadmin.ErrMembershipRemovalPending):
		writeError(w, r, http.StatusConflict, "membership_removal_pending", "membership removal is pending provider reconciliation")
	case errors.Is(err, membershipadmin.ErrInvitationConflict):
		writeError(w, r, http.StatusConflict, "invitation_conflict", "invitation conflicts with provider state")
	case errors.Is(err, membershipadmin.ErrInvitationRejected):
		writeError(w, r, http.StatusUnprocessableEntity, "invitation_rejected", "invitation was rejected")
	case errors.Is(err, membershipadmin.ErrProviderStateConflict):
		writeError(w, r, http.StatusConflict, "provider_state_conflict", "membership provider state changed")
	case errors.Is(err, membershipadmin.ErrProviderUnavailable):
		writeError(w, r, http.StatusServiceUnavailable, "membership_provider_unavailable", "membership provider temporarily unavailable")
	default:
		writeActorError(w, r, err)
	}
}
