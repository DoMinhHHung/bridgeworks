package currentorganization

import (
	"context"
	"errors"

	"github.com/DoMinhHHung/bridgeworks/service/organization-service/internal/authorization"
	"github.com/google/uuid"
)

var (
	ErrUnauthorized                = errors.New("unauthorized")
	ErrAccountDisabled             = errors.New("account disabled")
	ErrAccountDeleted              = errors.New("account deleted")
	ErrIdentityNotReady            = errors.New("identity not ready")
	ErrOrganizationContextRequired = errors.New("organization context required")
	ErrOrganizationNotReady        = errors.New("organization not ready")
	ErrOrganizationDisabled        = errors.New("organization disabled")
	ErrOrganizationDeleted         = errors.New("organization deleted")
	ErrMembershipRequired          = errors.New("membership required")
	ErrPermissionDenied            = errors.New("permission denied")
)

type Identity struct {
	ID uuid.UUID
}

type Organization struct {
	ID     uuid.UUID
	Name   *string
	Slug   *string
	Status string
}

type Membership struct {
	ID              uuid.UUID
	OrganizationID  uuid.UUID
	ApplicationRole string
	Status          string
}

type IdentityReader interface {
	Resolve(context.Context, string, string) (Identity, error)
	CloseIdleConnections()
}

type Repository interface {
	GetOrganizationByClerkID(context.Context, string) (Organization, bool, error)
	GetActiveMembership(context.Context, uuid.UUID, string) (Membership, bool, error)
	ListPermissions(context.Context, string) ([]string, error)
}

type Result struct {
	Actor        authorization.ActorContext
	Organization Organization
	Membership   Membership
}

type Service struct {
	identity   IdentityReader
	repository Repository
}

func New(identity IdentityReader, repository Repository) *Service {
	return &Service{identity: identity, repository: repository}
}

func (s *Service) Resolve(
	ctx context.Context,
	principal authorization.Principal,
	authorizationHeader string,
	requestID string,
) (Result, error) {
	if principal.ClerkOrganizationID == "" {
		return Result{}, ErrOrganizationContextRequired
	}
	identity, err := s.identity.Resolve(ctx, authorizationHeader, requestID)
	if err != nil {
		return Result{}, err
	}
	organization, found, err := s.repository.GetOrganizationByClerkID(ctx, principal.ClerkOrganizationID)
	if err != nil {
		return Result{}, err
	}
	if !found || organization.Status == "pending" {
		return Result{}, ErrOrganizationNotReady
	}
	switch organization.Status {
	case "active":
	case "disabled":
		return Result{}, ErrOrganizationDisabled
	case "deleted":
		return Result{}, ErrOrganizationDeleted
	default:
		return Result{}, errors.New("unsupported organization status")
	}
	membership, found, err := s.repository.GetActiveMembership(ctx, organization.ID, principal.ClerkUserID)
	if err != nil {
		return Result{}, err
	}
	if !found || membership.Status != "active" {
		return Result{}, ErrMembershipRequired
	}
	permissions, err := s.repository.ListPermissions(ctx, membership.ApplicationRole)
	if err != nil {
		return Result{}, err
	}
	actor := authorization.NewActorContext(
		identity.ID,
		organization.ID,
		membership.ID,
		membership.ApplicationRole,
		permissions,
	)
	return Result{Actor: actor, Organization: organization, Membership: membership}, nil
}

func RequirePermission(result Result, permission string) error {
	if !result.Actor.HasPermission(permission) {
		return ErrPermissionDenied
	}
	return nil
}
