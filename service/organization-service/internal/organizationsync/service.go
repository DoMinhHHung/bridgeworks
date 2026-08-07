package organizationsync

import (
	"context"
	"errors"

	"github.com/DoMinhHHung/bridgeworks/service/organization-service/internal/organizationid"
	"github.com/DoMinhHHung/bridgeworks/service/organization-service/internal/platform/safeerr"
	"github.com/google/uuid"
)

type Result string

const (
	ResultProcessed Result = "processed"
	ResultDuplicate Result = "duplicate"
	ResultStale     Result = "stale"
)

type Service struct {
	factory   UnitOfWorkFactory
	generator organizationid.Generator
}

func New(factory UnitOfWorkFactory, generator organizationid.Generator) *Service {
	return &Service{factory: factory, generator: generator}
}

func (s *Service) Process(ctx context.Context, event Event) error {
	_, err := s.ProcessWithResult(ctx, event)
	return err
}

func (s *Service) ProcessWithResult(ctx context.Context, event Event) (result Result, err error) {
	uow, err := s.factory.Begin(ctx)
	if err != nil {
		return "", safeerr.Wrap("begin organization synchronization transaction", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = uow.Rollback(ctx)
		}
	}()

	inserted, err := uow.InsertInbox(ctx, event)
	if err != nil {
		return "", safeerr.Wrap("insert organization webhook inbox event", err)
	}
	if !inserted {
		if err := uow.Commit(ctx); err != nil {
			return "", safeerr.Wrap("commit duplicate organization event", err)
		}
		committed = true
		return ResultDuplicate, nil
	}

	if err := uow.AcquireOrganizationLock(ctx, event.ClerkOrganizationID); err != nil {
		return "", safeerr.Wrap("acquire organization advisory lock", err)
	}
	latest, found, err := uow.LatestAggregateEvent(ctx, event.AggregateType, event.AggregateID, event.EventID)
	if err != nil {
		return "", safeerr.Wrap("load latest organization aggregate event", err)
	}
	if found && IsStale(event, latest) {
		if err := uow.Commit(ctx); err != nil {
			return "", safeerr.Wrap("commit stale organization event", err)
		}
		committed = true
		return ResultStale, nil
	}

	switch event.AggregateType {
	case AggregateOrganization:
		err = s.applyOrganizationEvent(ctx, uow, event)
	case AggregateMembership:
		err = s.applyMembershipEvent(ctx, uow, event)
	default:
		err = safeerr.New("unsupported organization aggregate type")
	}
	if err != nil {
		return "", err
	}
	if err := uow.Commit(ctx); err != nil {
		return "", safeerr.Wrap("commit organization synchronization transaction", err)
	}
	committed = true
	return ResultProcessed, nil
}

func (s *Service) applyOrganizationEvent(ctx context.Context, uow UnitOfWork, event Event) error {
	existing, found, err := uow.GetOrganization(ctx, event.ClerkOrganizationID)
	if err != nil {
		return safeerr.Wrap("load organization for synchronization", err)
	}

	if event.Type == EventOrganizationDeleted {
		if !found {
			id, err := s.newID("generate organization ID")
			if err != nil {
				return err
			}
			return wrapPersistence("insert deleted organization tombstone", uow.InsertOrganization(ctx, Organization{
				ID:                     id,
				ClerkOrganizationID:    event.ClerkOrganizationID,
				Status:                 "deleted",
				OwnerBootstrapEligible: true,
			}))
		}
		if existing.Status == "deleted" {
			return nil
		}
		return wrapPersistence("mark organization deleted", uow.MarkOrganizationDeleted(ctx, event.ClerkOrganizationID))
	}

	if !found {
		id, err := s.newID("generate organization ID")
		if err != nil {
			return err
		}
		existing = Organization{
			ID:                     id,
			ClerkOrganizationID:    event.ClerkOrganizationID,
			Name:                   event.Organization.Name,
			Slug:                   event.Organization.Slug,
			Status:                 "active",
			ClerkCreatedByUserID:   event.Organization.CreatedBy,
			OwnerBootstrapEligible: true,
		}
		if err := uow.InsertOrganization(ctx, existing); err != nil {
			return safeerr.Wrap("insert organization projection", err)
		}
		return s.bootstrapInitialOwner(ctx, uow, existing)
	}

	var nextStatus string
	switch existing.Status {
	case "pending", "active":
		nextStatus = "active"
	case "disabled":
		nextStatus = "disabled"
	case "deleted":
		return nil
	default:
		return safeerr.New("unsupported organization status")
	}

	if event.Organization.CreatedBy != nil {
		if existing.ClerkCreatedByUserID != nil && *existing.ClerkCreatedByUserID != *event.Organization.CreatedBy {
			return safeerr.New("inconsistent Clerk organization creator")
		}
		if existing.ClerkCreatedByUserID == nil {
			if err := uow.SetOrganizationCreator(ctx, event.ClerkOrganizationID, *event.Organization.CreatedBy); err != nil {
				return safeerr.Wrap("persist organization creator projection", err)
			}
			existing.ClerkCreatedByUserID = event.Organization.CreatedBy
		}
	}

	if err := uow.UpdateOrganizationProjection(
		ctx,
		event.ClerkOrganizationID,
		event.Organization.Name,
		event.Organization.Slug,
		nextStatus,
	); err != nil {
		return safeerr.Wrap("update organization projection", err)
	}
	existing.Status = nextStatus
	return s.bootstrapInitialOwner(ctx, uow, existing)
}

func (s *Service) applyMembershipEvent(ctx context.Context, uow UnitOfWork, event Event) error {
	organization, found, err := uow.GetOrganization(ctx, event.ClerkOrganizationID)
	if err != nil {
		return safeerr.Wrap("load membership organization projection", err)
	}
	if !found {
		id, err := s.newID("generate pending organization ID")
		if err != nil {
			return err
		}
		organization = Organization{
			ID:                     id,
			ClerkOrganizationID:    event.ClerkOrganizationID,
			Status:                 "pending",
			OwnerBootstrapEligible: true,
		}
		if err := uow.InsertOrganization(ctx, organization); err != nil {
			return safeerr.Wrap("insert pending organization projection", err)
		}
	}

	existing, membershipFound, err := uow.GetMembership(ctx, event.Membership.ClerkMembershipID)
	if err != nil {
		return safeerr.Wrap("load membership for synchronization", err)
	}
	if membershipFound {
		if err := validateMembershipOwnership(existing, organization.ID, event.Membership.ClerkUserID); err != nil {
			return err
		}
	}

	if event.Type == EventMembershipDeleted {
		if !membershipFound {
			id, err := s.newID("generate membership tombstone ID")
			if err != nil {
				return err
			}
			if err := s.insertMembership(ctx, uow, Membership{
				ID:                id,
				ClerkMembershipID: event.Membership.ClerkMembershipID,
				OrganizationID:    organization.ID,
				ClerkUserID:       event.Membership.ClerkUserID,
				ClerkRole:         event.Membership.ClerkRole,
				ApplicationRole:   RoleViewer,
				Status:            "deleted",
			}); err != nil {
				return err
			}
		} else if existing.Status != "deleted" {
			if err := uow.MarkMembershipDeleted(ctx, event.Membership.ClerkMembershipID); err != nil {
				return safeerr.Wrap("mark membership deleted", err)
			}
		}
		return s.bootstrapInitialOwner(ctx, uow, organization)
	}

	if membershipFound {
		if existing.Status == "deleted" {
			return s.bootstrapInitialOwner(ctx, uow, organization)
		}
		if existing.Status != "active" {
			return safeerr.New("unsupported membership status")
		}
		if err := uow.UpdateMembershipClerkRole(
			ctx,
			event.Membership.ClerkMembershipID,
			event.Membership.ClerkRole,
		); err != nil {
			return safeerr.Wrap("update membership Clerk role", err)
		}
		return s.bootstrapInitialOwner(ctx, uow, organization)
	}

	id, err := s.newID("generate membership ID")
	if err != nil {
		return err
	}
	if err := s.insertMembership(ctx, uow, Membership{
		ID:                id,
		ClerkMembershipID: event.Membership.ClerkMembershipID,
		OrganizationID:    organization.ID,
		ClerkUserID:       event.Membership.ClerkUserID,
		ClerkRole:         event.Membership.ClerkRole,
		ApplicationRole:   InitialApplicationRole(event.Membership.ClerkRole),
		Status:            "active",
	}); err != nil {
		return err
	}
	return s.bootstrapInitialOwner(ctx, uow, organization)
}

func (s *Service) bootstrapInitialOwner(ctx context.Context, uow UnitOfWork, organization Organization) error {
	if !organization.OwnerBootstrapEligible || organization.OwnerBootstrapped || organization.ClerkCreatedByUserID == nil {
		return nil
	}
	creatorID := *organization.ClerkCreatedByUserID
	hasDeletedMembership, err := uow.HasDeletedMembership(ctx, organization.ID, creatorID)
	if err != nil {
		return safeerr.Wrap("load deleted creator membership history for owner bootstrap", err)
	}
	if hasDeletedMembership {
		if err := uow.DisableOrganizationOwnerBootstrapEligibility(ctx, organization.ID); err != nil {
			return safeerr.Wrap("disable organization owner bootstrap eligibility", err)
		}
		return nil
	}
	if organization.Status != "active" {
		return nil
	}
	membership, found, err := uow.GetActiveMembership(ctx, organization.ID, creatorID)
	if err != nil {
		return safeerr.Wrap("load creator membership for owner bootstrap", err)
	}
	if !found {
		return nil
	}
	if membership.ApplicationRole != RoleOwner {
		if err := uow.UpdateMembershipApplicationRole(ctx, membership.ID, RoleOwner); err != nil {
			return safeerr.Wrap("bootstrap organization owner role", err)
		}
	}
	if err := uow.MarkOrganizationOwnerBootstrapped(ctx, organization.ID); err != nil {
		return safeerr.Wrap("mark organization owner bootstrapped", err)
	}
	return nil
}

func (s *Service) insertMembership(ctx context.Context, uow UnitOfWork, intended Membership) error {
	if err := uow.CreateMembershipInsertSavepoint(ctx); err != nil {
		return safeerr.Wrap("create membership insert savepoint", err)
	}
	insertErr := uow.InsertMembership(ctx, intended)
	if insertErr == nil {
		if err := uow.ReleaseMembershipInsertSavepoint(ctx); err != nil {
			return safeerr.Wrap("release membership insert savepoint", err)
		}
		return nil
	}

	if err := uow.RollbackMembershipInsertSavepoint(ctx); err != nil {
		return safeerr.Wrap("rollback membership insert savepoint", err)
	}
	if err := uow.ReleaseMembershipInsertSavepoint(ctx); err != nil {
		return safeerr.Wrap("release membership insert savepoint", err)
	}

	var uniqueErr *UniqueConstraintError
	if !errors.As(insertErr, &uniqueErr) {
		return safeerr.Wrap("insert organization membership", insertErr)
	}

	switch uniqueErr.Constraint {
	case ConstraintMembershipClerkID:
		return s.resolveClerkMembershipConflict(ctx, uow, intended)
	case ConstraintActiveOrganizationMember:
		return s.resolveActiveMembershipConflict(ctx, uow, intended)
	default:
		return safeerr.Wrap("insert organization membership", insertErr)
	}
}

func (s *Service) resolveClerkMembershipConflict(ctx context.Context, uow UnitOfWork, intended Membership) error {
	existing, found, err := uow.GetMembership(ctx, intended.ClerkMembershipID)
	if err != nil {
		return safeerr.Wrap("load membership after Clerk membership conflict", err)
	}
	if !found {
		return safeerr.New("inconsistent Clerk membership projection")
	}
	if err := validateMembershipOwnership(existing, intended.OrganizationID, intended.ClerkUserID); err != nil {
		return err
	}

	if intended.Status == "deleted" {
		switch existing.Status {
		case "deleted":
			return nil
		case "active":
			return wrapPersistence("mark membership deleted after insert conflict", uow.MarkMembershipDeleted(ctx, intended.ClerkMembershipID))
		default:
			return safeerr.New("unsupported membership status")
		}
	}

	switch existing.Status {
	case "active", "deleted":
		return nil
	default:
		return safeerr.New("unsupported membership status")
	}
}

func validateMembershipOwnership(existing Membership, organizationID uuid.UUID, clerkUserID string) error {
	if existing.OrganizationID != organizationID || existing.ClerkUserID != clerkUserID {
		return safeerr.New("inconsistent Clerk membership projection")
	}
	return nil
}

func (s *Service) resolveActiveMembershipConflict(ctx context.Context, uow UnitOfWork, intended Membership) error {
	if intended.Status != "active" {
		return safeerr.New("unexpected active membership constraint conflict")
	}
	conflicting, found, err := uow.GetActiveMembership(ctx, intended.OrganizationID, intended.ClerkUserID)
	if err != nil {
		return safeerr.Wrap("load conflicting active membership", err)
	}
	if !found {
		return safeerr.New("active membership conflict without active projection")
	}
	if conflicting.ClerkMembershipID == intended.ClerkMembershipID {
		return nil
	}
	return ErrActiveMembershipConflict
}

func (s *Service) newID(operation string) (uuid.UUID, error) {
	id, err := s.generator.New()
	if err != nil {
		return uuid.Nil, safeerr.Wrap(operation, err)
	}
	return id, nil
}

func wrapPersistence(operation string, err error) error {
	if err != nil {
		return safeerr.Wrap(operation, err)
	}
	return nil
}
