package membershipadmin

import (
	"context"
	"errors"
	"strings"
	"unicode"

	"github.com/DoMinhHHung/bridgeworks/service/organization-service/internal/authorization"
	"github.com/DoMinhHHung/bridgeworks/service/organization-service/internal/platform/safeerr"
	"github.com/google/uuid"
)

const (
	RoleOwner           = "owner"
	RoleAdmin           = "admin"
	RoleRecruiter       = "recruiter"
	RoleDeliveryManager = "delivery_manager"
	RoleViewer          = "viewer"
)

var (
	ErrPermissionDenied         = errors.New("membership administration permission denied")
	ErrInvalidRole              = errors.New("invalid application role")
	ErrInvalidEmail             = errors.New("invalid invitation email")
	ErrMembershipNotFound       = errors.New("membership not found")
	ErrMembershipInactive       = errors.New("membership inactive")
	ErrMembershipRemovalPending = errors.New("membership removal pending")
	ErrOwnerMutationForbidden   = errors.New("owner mutation forbidden")
	ErrLastOwner                = errors.New("last owner operation forbidden")
	ErrSelfRemoval              = errors.New("use leave operation for self removal")
	ErrInvalidTransferTarget    = errors.New("invalid ownership transfer target")
	ErrInvitationConflict       = errors.New("invitation conflict")
	ErrInvitationRejected       = errors.New("invitation rejected by provider")
	ErrProviderUnavailable      = errors.New("membership provider unavailable")
	ErrProviderStateConflict    = errors.New("membership provider state conflict")
)

// Provider errors are deliberately coarse so Clerk response bodies, trace IDs,
// and provider-specific identifiers never cross the adapter boundary.
var (
	ProviderErrConflict    = errors.New("provider conflict")
	ProviderErrRejected    = errors.New("provider rejected request")
	ProviderErrUnavailable = errors.New("provider unavailable")
	ProviderErrNotFound    = errors.New("provider membership not found")
)

type Organization struct {
	ID                  uuid.UUID
	ClerkOrganizationID string
}

type Membership struct {
	ID                uuid.UUID
	OrganizationID    uuid.UUID
	ClerkUserID       string
	ApplicationRole   string
	Status            string
	RemovalPending    bool
}

type InvitationIntent struct {
	ID                      uuid.UUID
	OrganizationID          uuid.UUID
	ApplicationRole         string
	CreatedByIdentityUserID uuid.UUID
}

type UnitOfWork interface {
	AcquireOrganizationLock(context.Context, uuid.UUID) error
	GetOrganization(context.Context, uuid.UUID) (Organization, bool, error)
	LockMembership(context.Context, uuid.UUID, uuid.UUID) (Membership, bool, error)
	CountEffectiveOwners(context.Context, uuid.UUID) (int64, error)
	InsertInvitationIntent(context.Context, InvitationIntent) error
	DeleteInvitationIntent(context.Context, uuid.UUID, uuid.UUID) error
	UpdateMembershipRole(context.Context, uuid.UUID, uuid.UUID, string) error
	InsertRemovalIntent(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) (bool, error)
	DeleteRemovalIntent(context.Context, uuid.UUID, uuid.UUID) error
	Commit(context.Context) error
	Rollback(context.Context) error
}

type UnitOfWorkFactory interface {
	BeginMembershipAdministration(context.Context) (UnitOfWork, error)
}

type IDGenerator interface {
	New() (uuid.UUID, error)
}

type InvitationProviderRequest struct {
	ClerkOrganizationID string
	ClerkInviterUserID  string
	EmailAddress         string
	InvitationIntentID  uuid.UUID
}

type MembershipDeleteProviderRequest struct {
	ClerkOrganizationID string
	ClerkUserID         string
}

type Provider interface {
	CreateInvitation(context.Context, InvitationProviderRequest) error
	DeleteMembership(context.Context, MembershipDeleteProviderRequest) error
}

type InvitationResult struct {
	ID              uuid.UUID
	ApplicationRole string
}

type Service struct {
	factory   UnitOfWorkFactory
	provider  Provider
	generator IDGenerator
}

func New(factory UnitOfWorkFactory, provider Provider, generator IDGenerator) *Service {
	return &Service{factory: factory, provider: provider, generator: generator}
}

func (s *Service) Invite(
	ctx context.Context,
	actor authorization.ActorContext,
	clerkInviterUserID string,
	rawEmail string,
	requestedRole string,
) (InvitationResult, error) {
	role, err := validateRole(requestedRole)
	if err != nil {
		return InvitationResult{}, err
	}
	email, err := normalizeInvitationEmail(rawEmail)
	if err != nil {
		return InvitationResult{}, err
	}
	intentID, err := s.generator.New()
	if err != nil {
		return InvitationResult{}, safeerr.Wrap("generate invitation intent ID", err)
	}

	organization, err := s.createInvitationIntent(ctx, actor, role, intentID)
	if err != nil {
		return InvitationResult{}, err
	}

	providerErr := s.provider.CreateInvitation(ctx, InvitationProviderRequest{
		ClerkOrganizationID: organization.ClerkOrganizationID,
		ClerkInviterUserID:  clerkInviterUserID,
		EmailAddress:         email,
		InvitationIntentID:  intentID,
	})
	if providerErr == nil {
		return InvitationResult{ID: intentID, ApplicationRole: role}, nil
	}

	switch {
	case errors.Is(providerErr, ProviderErrUnavailable):
		// The provider may have accepted the request before the timeout. Preserve
		// the intent so a later verified membership webhook can still reconcile it.
		return InvitationResult{}, ErrProviderUnavailable
	case errors.Is(providerErr, ProviderErrConflict):
		_ = s.deleteInvitationIntent(ctx, actor.OrganizationID, intentID)
		return InvitationResult{}, ErrInvitationConflict
	case errors.Is(providerErr, ProviderErrRejected), errors.Is(providerErr, ProviderErrNotFound):
		_ = s.deleteInvitationIntent(ctx, actor.OrganizationID, intentID)
		return InvitationResult{}, ErrInvitationRejected
	default:
		return InvitationResult{}, ErrProviderUnavailable
	}
}

func (s *Service) SetRole(
	ctx context.Context,
	actor authorization.ActorContext,
	targetMembershipID uuid.UUID,
	requestedRole string,
) error {
	role, err := validateRole(requestedRole)
	if err != nil {
		return err
	}
	uow, actorMembership, target, err := s.beginLockedMemberships(ctx, actor, targetMembershipID)
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_ = uow.Rollback(ctx)
		}
	}()

	if target.RemovalPending {
		return ErrMembershipRemovalPending
	}
	if err := authorizeRoleMutation(actorMembership, target, role); err != nil {
		return err
	}
	if target.ApplicationRole == RoleOwner && role != RoleOwner {
		owners, err := uow.CountEffectiveOwners(ctx, actor.OrganizationID)
		if err != nil {
			return safeerr.Wrap("count owners before role mutation", err)
		}
		if owners <= 1 {
			return ErrLastOwner
		}
	}
	if target.ApplicationRole != role {
		if err := uow.UpdateMembershipRole(ctx, actor.OrganizationID, target.ID, role); err != nil {
			return safeerr.Wrap("update local membership role", err)
		}
	}
	if err := uow.Commit(ctx); err != nil {
		return safeerr.Wrap("commit local membership role update", err)
	}
	committed = true
	return nil
}

func (s *Service) TransferOwnership(
	ctx context.Context,
	actor authorization.ActorContext,
	targetMembershipID uuid.UUID,
) error {
	if actor.MembershipID == targetMembershipID {
		return ErrInvalidTransferTarget
	}
	uow, actorMembership, target, err := s.beginLockedMemberships(ctx, actor, targetMembershipID)
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_ = uow.Rollback(ctx)
		}
	}()

	if actorMembership.ApplicationRole != RoleOwner {
		return ErrOwnerMutationForbidden
	}
	if target.RemovalPending {
		return ErrMembershipRemovalPending
	}
	if target.ApplicationRole != RoleOwner {
		if err := uow.UpdateMembershipRole(ctx, actor.OrganizationID, target.ID, RoleOwner); err != nil {
			return safeerr.Wrap("promote ownership transfer target", err)
		}
	}
	if err := uow.UpdateMembershipRole(ctx, actor.OrganizationID, actorMembership.ID, RoleAdmin); err != nil {
		return safeerr.Wrap("demote ownership transfer actor", err)
	}
	if err := uow.Commit(ctx); err != nil {
		return safeerr.Wrap("commit ownership transfer", err)
	}
	committed = true
	return nil
}

func (s *Service) Remove(
	ctx context.Context,
	actor authorization.ActorContext,
	targetMembershipID uuid.UUID,
) error {
	if actor.MembershipID == targetMembershipID {
		return ErrSelfRemoval
	}
	return s.requestRemoval(ctx, actor, targetMembershipID, false)
}

func (s *Service) Leave(ctx context.Context, actor authorization.ActorContext) error {
	return s.requestRemoval(ctx, actor, actor.MembershipID, true)
}

func (s *Service) createInvitationIntent(
	ctx context.Context,
	actor authorization.ActorContext,
	role string,
	intentID uuid.UUID,
) (organization Organization, err error) {
	uow, err := s.factory.BeginMembershipAdministration(ctx)
	if err != nil {
		return Organization{}, safeerr.Wrap("begin invitation transaction", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = uow.Rollback(ctx)
		}
	}()
	if err := uow.AcquireOrganizationLock(ctx, actor.OrganizationID); err != nil {
		return Organization{}, safeerr.Wrap("lock organization for invitation", err)
	}
	organization, found, err := uow.GetOrganization(ctx, actor.OrganizationID)
	if err != nil {
		return Organization{}, safeerr.Wrap("load organization for invitation", err)
	}
	if !found {
		return Organization{}, ErrMembershipNotFound
	}
	actorMembership, found, err := uow.LockMembership(ctx, actor.OrganizationID, actor.MembershipID)
	if err != nil {
		return Organization{}, safeerr.Wrap("lock invitation actor membership", err)
	}
	if !found || actorMembership.Status != "active" || actorMembership.RemovalPending {
		return Organization{}, ErrMembershipInactive
	}
	if err := authorizeInvitation(actorMembership.ApplicationRole, role); err != nil {
		return Organization{}, err
	}
	if err := uow.InsertInvitationIntent(ctx, InvitationIntent{
		ID:                      intentID,
		OrganizationID:          actor.OrganizationID,
		ApplicationRole:         role,
		CreatedByIdentityUserID: actor.IdentityUserID,
	}); err != nil {
		return Organization{}, safeerr.Wrap("insert invitation intent", err)
	}
	if err := uow.Commit(ctx); err != nil {
		return Organization{}, safeerr.Wrap("commit invitation intent", err)
	}
	committed = true
	return organization, nil
}

func (s *Service) deleteInvitationIntent(ctx context.Context, organizationID, intentID uuid.UUID) error {
	uow, err := s.factory.BeginMembershipAdministration(ctx)
	if err != nil {
		return safeerr.Wrap("begin invitation cleanup transaction", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = uow.Rollback(ctx)
		}
	}()
	if err := uow.AcquireOrganizationLock(ctx, organizationID); err != nil {
		return safeerr.Wrap("lock organization for invitation cleanup", err)
	}
	if err := uow.DeleteInvitationIntent(ctx, organizationID, intentID); err != nil {
		return safeerr.Wrap("delete invitation intent", err)
	}
	if err := uow.Commit(ctx); err != nil {
		return safeerr.Wrap("commit invitation cleanup", err)
	}
	committed = true
	return nil
}

func (s *Service) beginLockedMemberships(
	ctx context.Context,
	actor authorization.ActorContext,
	targetMembershipID uuid.UUID,
) (UnitOfWork, Membership, Membership, error) {
	uow, err := s.factory.BeginMembershipAdministration(ctx)
	if err != nil {
		return nil, Membership{}, Membership{}, safeerr.Wrap("begin membership administration transaction", err)
	}
	if err := uow.AcquireOrganizationLock(ctx, actor.OrganizationID); err != nil {
		_ = uow.Rollback(ctx)
		return nil, Membership{}, Membership{}, safeerr.Wrap("lock organization for membership administration", err)
	}
	actorMembership, found, err := uow.LockMembership(ctx, actor.OrganizationID, actor.MembershipID)
	if err != nil {
		_ = uow.Rollback(ctx)
		return nil, Membership{}, Membership{}, safeerr.Wrap("lock actor membership", err)
	}
	if !found || actorMembership.Status != "active" || actorMembership.RemovalPending {
		_ = uow.Rollback(ctx)
		return nil, Membership{}, Membership{}, ErrMembershipInactive
	}
	if targetMembershipID == actor.MembershipID {
		return uow, actorMembership, actorMembership, nil
	}
	target, found, err := uow.LockMembership(ctx, actor.OrganizationID, targetMembershipID)
	if err != nil {
		_ = uow.Rollback(ctx)
		return nil, Membership{}, Membership{}, safeerr.Wrap("lock target membership", err)
	}
	if !found {
		_ = uow.Rollback(ctx)
		return nil, Membership{}, Membership{}, ErrMembershipNotFound
	}
	if target.Status != "active" {
		_ = uow.Rollback(ctx)
		return nil, Membership{}, Membership{}, ErrMembershipInactive
	}
	return uow, actorMembership, target, nil
}

func (s *Service) requestRemoval(
	ctx context.Context,
	actor authorization.ActorContext,
	targetMembershipID uuid.UUID,
	self bool,
) error {
	uow, actorMembership, target, err := s.beginLockedMemberships(ctx, actor, targetMembershipID)
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_ = uow.Rollback(ctx)
		}
	}()

	if self {
		if target.ApplicationRole == RoleOwner {
			owners, err := uow.CountEffectiveOwners(ctx, actor.OrganizationID)
			if err != nil {
				return safeerr.Wrap("count owners before leave", err)
			}
			if owners <= 1 {
				return ErrLastOwner
			}
		}
	} else {
		if err := authorizeRemoval(actorMembership.ApplicationRole, target.ApplicationRole); err != nil {
			return err
		}
		if target.ApplicationRole == RoleOwner {
			owners, err := uow.CountEffectiveOwners(ctx, actor.OrganizationID)
			if err != nil {
				return safeerr.Wrap("count owners before removal", err)
			}
			if owners <= 1 {
				return ErrLastOwner
			}
		}
	}
	organization, found, err := uow.GetOrganization(ctx, actor.OrganizationID)
	if err != nil {
		return safeerr.Wrap("load provider organization for membership removal", err)
	}
	if !found {
		return ErrMembershipNotFound
	}
	if !target.RemovalPending {
		if _, err := uow.InsertRemovalIntent(ctx, target.ID, actor.OrganizationID, actor.IdentityUserID); err != nil {
			return safeerr.Wrap("reserve membership removal", err)
		}
	}
	if err := uow.Commit(ctx); err != nil {
		return safeerr.Wrap("commit membership removal reservation", err)
	}
	committed = true

	providerErr := s.provider.DeleteMembership(ctx, MembershipDeleteProviderRequest{
		ClerkOrganizationID: organization.ClerkOrganizationID,
		ClerkUserID:         target.ClerkUserID,
	})
	switch {
	case providerErr == nil, errors.Is(providerErr, ProviderErrNotFound):
		// Local status remains active only in the provider projection table. The
		// removal reservation excludes authorization until the signed delete
		// webhook reconciles and clears the reservation.
		return nil
	case errors.Is(providerErr, ProviderErrUnavailable):
		// Keep the reservation. A later identical command retries the provider
		// delete instead of silently treating the pending state as completed.
		return ErrProviderUnavailable
	case errors.Is(providerErr, ProviderErrConflict), errors.Is(providerErr, ProviderErrRejected):
		if cleanupErr := s.deleteRemovalIntent(ctx, actor.OrganizationID, target.ID); cleanupErr != nil {
			return cleanupErr
		}
		return ErrProviderStateConflict
	default:
		return ErrProviderUnavailable
	}
}

func (s *Service) deleteRemovalIntent(ctx context.Context, organizationID, membershipID uuid.UUID) error {
	uow, err := s.factory.BeginMembershipAdministration(ctx)
	if err != nil {
		return safeerr.Wrap("begin membership removal cleanup", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = uow.Rollback(ctx)
		}
	}()
	if err := uow.AcquireOrganizationLock(ctx, organizationID); err != nil {
		return safeerr.Wrap("lock organization for membership removal cleanup", err)
	}
	if err := uow.DeleteRemovalIntent(ctx, organizationID, membershipID); err != nil {
		return safeerr.Wrap("delete membership removal reservation", err)
	}
	if err := uow.Commit(ctx); err != nil {
		return safeerr.Wrap("commit membership removal cleanup", err)
	}
	committed = true
	return nil
}

func authorizeInvitation(actorRole, requestedRole string) error {
	switch actorRole {
	case RoleOwner:
		return nil
	case RoleAdmin:
		if requestedRole == RoleOwner {
			return ErrOwnerMutationForbidden
		}
		return nil
	default:
		return ErrPermissionDenied
	}
}

func authorizeRoleMutation(actor Membership, target Membership, requestedRole string) error {
	switch actor.ApplicationRole {
	case RoleOwner:
		return nil
	case RoleAdmin:
		if target.ApplicationRole == RoleOwner || requestedRole == RoleOwner {
			return ErrOwnerMutationForbidden
		}
		return nil
	default:
		return ErrPermissionDenied
	}
}

func authorizeRemoval(actorRole, targetRole string) error {
	switch actorRole {
	case RoleOwner:
		return nil
	case RoleAdmin:
		if targetRole == RoleOwner {
			return ErrOwnerMutationForbidden
		}
		return nil
	default:
		return ErrPermissionDenied
	}
}

func validateRole(raw string) (string, error) {
	role := strings.TrimSpace(raw)
	switch role {
	case RoleOwner, RoleAdmin, RoleRecruiter, RoleDeliveryManager, RoleViewer:
		return role, nil
	default:
		return "", ErrInvalidRole
	}
}

func normalizeInvitationEmail(raw string) (string, error) {
	email := strings.TrimSpace(raw)
	if email == "" || len(email) > 320 || strings.Count(email, "@") != 1 {
		return "", ErrInvalidEmail
	}
	local, domain, ok := strings.Cut(email, "@")
	if !ok || local == "" || domain == "" || len(local) > 64 {
		return "", ErrInvalidEmail
	}
	for _, r := range local {
		if r > unicode.MaxASCII || unicode.IsSpace(r) || unicode.IsControl(r) {
			return "", ErrInvalidEmail
		}
	}
	domain = strings.ToLower(strings.TrimSpace(domain))
	if domain == "" || len(domain) > 253 || strings.HasSuffix(domain, ".") || !strings.Contains(domain, ".") {
		return "", ErrInvalidEmail
	}
	for _, label := range strings.Split(domain, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return "", ErrInvalidEmail
		}
		for _, r := range label {
			if r > unicode.MaxASCII || !((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-') {
				return "", ErrInvalidEmail
			}
	}
	}
	return local + "@" + domain, nil
}
