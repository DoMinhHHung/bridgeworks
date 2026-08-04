package organizationsync

import (
	"context"
	"errors"

	"github.com/DoMinhHHung/bridgeworks/service/organization-service/internal/organizationid"
	"github.com/DoMinhHHung/bridgeworks/service/organization-service/internal/platform/safeerr"
	"github.com/google/uuid"
)

type Service struct {
	factory   UnitOfWorkFactory
	generator organizationid.Generator
}

func New(factory UnitOfWorkFactory, generator organizationid.Generator) *Service {
	return &Service{factory: factory, generator: generator}
}

func (s *Service) Process(ctx context.Context, event Event) (err error) {
	uow, err := s.factory.Begin(ctx)
	if err != nil {
		return safeerr.Wrap("begin organization synchronization transaction", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = uow.Rollback(ctx)
		}
	}()

	inserted, err := uow.InsertInbox(ctx, event)
	if err != nil {
		return safeerr.Wrap("insert organization webhook inbox event", err)
	}
	if !inserted {
		if err := uow.Commit(ctx); err != nil {
			return safeerr.Wrap("commit duplicate organization event", err)
		}
		committed = true
		return nil
	}

	if err := uow.AcquireOrganizationLock(ctx, event.ClerkOrganizationID); err != nil {
		return safeerr.Wrap("acquire organization advisory lock", err)
	}
	latest, found, err := uow.LatestAggregateEvent(ctx, event.AggregateType, event.AggregateID, event.EventID)
	if err != nil {
		return safeerr.Wrap("load latest organization aggregate event", err)
	}
	if found && IsStale(event, latest) {
		if err := uow.Commit(ctx); err != nil {
			return safeerr.Wrap("commit stale organization event", err)
		}
		committed = true
		return nil
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
		return err
	}
	if err := uow.Commit(ctx); err != nil {
		return safeerr.Wrap("commit organization synchronization transaction", err)
	}
	committed = true
	return nil
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
				ID:                  id,
				ClerkOrganizationID: event.ClerkOrganizationID,
				Status:              "deleted",
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
		return wrapPersistence("insert organization projection", uow.InsertOrganization(ctx, Organization{
			ID:                  id,
			ClerkOrganizationID: event.ClerkOrganizationID,
			Name:                event.Organization.Name,
			Slug:                event.Organization.Slug,
			Status:              "active",
		}))
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
	return wrapPersistence("update organization projection", uow.UpdateOrganizationProjection(
		ctx,
		event.ClerkOrganizationID,
		event.Organization.Name,
		event.Organization.Slug,
		nextStatus,
	))
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
			ID:                  id,
			ClerkOrganizationID: event.ClerkOrganizationID,
			Status:              "pending",
		}
		if err := uow.InsertOrganization(ctx, organization); err != nil {
			return safeerr.Wrap("insert pending organization projection", err)
		}
	}

	existing, found, err := uow.GetMembership(ctx, event.Membership.ClerkMembershipID)
	if err != nil {
		return safeerr.Wrap("load membership for synchronization", err)
	}
	if found {
		if err := validateMembershipOwnership(existing, organization.ID, event.Membership.ClerkUserID); err != nil {
			return err
		}
	}

	if event.Type == EventMembershipDeleted {
		if !found {
			id, err := s.newID("generate membership tombstone ID")
			if err != nil {
				return err
			}
			return s.insertMembership(ctx, uow, Membership{
				ID:                id,
				ClerkMembershipID: event.Membership.ClerkMembershipID,
				OrganizationID:    organization.ID,
				ClerkUserID:       event.Membership.ClerkUserID,
				ClerkRole:         event.Membership.ClerkRole,
				ApplicationRole:   RoleViewer,
				Status:            "deleted",
			})
		}
		if existing.Status == "deleted" {
			return nil
		}
		return wrapPersistence("mark membership deleted", uow.MarkMembershipDeleted(ctx, event.Membership.ClerkMembershipID))
	}

	if found {
		if existing.Status == "deleted" {
			return nil
		}
		if existing.Status != "active" {
			return safeerr.New("unsupported membership status")
		}
		return wrapPersistence("update membership Clerk role", uow.UpdateMembershipClerkRole(
			ctx,
			event.Membership.ClerkMembershipID,
			event.Membership.ClerkRole,
		))
	}

	id, err := s.newID("generate membership ID")
	if err != nil {
		return err
	}
	return s.insertMembership(ctx, uow, Membership{
		ID:                id,
		ClerkMembershipID: event.Membership.ClerkMembershipID,
		OrganizationID:    organization.ID,
		ClerkUserID:       event.Membership.ClerkUserID,
		ClerkRole:         event.Membership.ClerkRole,
		ApplicationRole:   InitialApplicationRole(event.Membership.ClerkRole),
		Status:            "active",
	})
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
