package organizationonboarding

import (
	"context"
	"errors"

	"github.com/DoMinhHHung/bridgeworks/service/organization-service/internal/authorization"
	"github.com/DoMinhHHung/bridgeworks/service/organization-service/internal/currentorganization"
	"github.com/DoMinhHHung/bridgeworks/service/organization-service/internal/organizationaudit"
	"github.com/DoMinhHHung/bridgeworks/service/organization-service/internal/organizationdomain"
	"github.com/DoMinhHHung/bridgeworks/service/organization-service/internal/platform/safeerr"
	"github.com/google/uuid"
)

var ErrVerificationTransitionNotAllowed = errors.New("verification transition not allowed")

type UnitOfWork interface {
	LockOrganization(context.Context, uuid.UUID) (currentorganization.Organization, bool, error)
	UpdateProductProfile(context.Context, uuid.UUID, *string, *string, *string, *string) (currentorganization.Organization, error)
	UpdateVerificationStatus(context.Context, uuid.UUID, string) (currentorganization.Organization, error)
	InsertAuditEvent(context.Context, organizationaudit.Event) error
	Commit(context.Context) error
	Rollback(context.Context) error
}

type UnitOfWorkFactory interface {
	BeginOnboarding(context.Context) (UnitOfWork, error)
}

type Service struct {
	factory UnitOfWorkFactory
}

func New(factory UnitOfWorkFactory) *Service {
	return &Service{factory: factory}
}

func (s *Service) UpdateProfile(
	ctx context.Context,
	actor authorization.ActorContext,
	patch ProfilePatch,
) (organization currentorganization.Organization, err error) {
	if !actor.HasPermission(authorization.PermissionOrganizationManage) {
		return currentorganization.Organization{}, currentorganization.ErrPermissionDenied
	}
	normalized, err := NormalizeProfilePatch(patch)
	if err != nil {
		return currentorganization.Organization{}, err
	}

	uow, err := s.factory.BeginOnboarding(ctx)
	if err != nil {
		return currentorganization.Organization{}, safeerr.Wrap("begin organization onboarding transaction", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = uow.Rollback(ctx)
		}
	}()

	organization, found, err := uow.LockOrganization(ctx, actor.OrganizationID)
	if err != nil {
		return currentorganization.Organization{}, safeerr.Wrap("lock organization onboarding row", err)
	}
	if !found {
		return currentorganization.Organization{}, currentorganization.ErrOrganizationNotReady
	}
	if err := requireActive(organization.Status); err != nil {
		return currentorganization.Organization{}, err
	}

	legalName := mergedValue(organization.LegalName, normalized.LegalName)
	website := mergedValue(organization.Website, normalized.Website)
	country := mergedValue(organization.Country, normalized.Country)
	companyType := mergedValue(organization.CompanyType, normalized.CompanyType)
	if equalOptional(organization.LegalName, legalName) &&
		equalOptional(organization.Website, website) &&
		equalOptional(organization.Country, country) &&
		equalOptional(organization.CompanyType, companyType) {
		if err := uow.Commit(ctx); err != nil {
			return currentorganization.Organization{}, safeerr.Wrap("commit unchanged organization profile", err)
		}
		committed = true
		return organization, nil
	}

	organization, err = uow.UpdateProductProfile(
		ctx,
		actor.OrganizationID,
		legalName,
		website,
		country,
		companyType,
	)
	if err != nil {
		return currentorganization.Organization{}, safeerr.Wrap("update organization product profile", err)
	}
	event, err := organizationaudit.TenantEvent(
		actor.OrganizationID,
		organizationaudit.EventOrganizationProfileUpdated,
		actor.IdentityUserID,
		actor.MembershipID,
		nil,
		nil,
		nil,
	)
	if err != nil {
		return currentorganization.Organization{}, safeerr.Wrap("create organization profile audit event", err)
	}
	if err := uow.InsertAuditEvent(ctx, event); err != nil {
		return currentorganization.Organization{}, safeerr.Wrap("append organization profile audit event", err)
	}
	if err := uow.Commit(ctx); err != nil {
		return currentorganization.Organization{}, safeerr.Wrap("commit organization product profile", err)
	}
	committed = true
	return organization, nil
}

func (s *Service) RequestVerification(
	ctx context.Context,
	actor authorization.ActorContext,
) (organization currentorganization.Organization, err error) {
	if !actor.HasPermission(authorization.PermissionOrganizationVerifyRequest) {
		return currentorganization.Organization{}, currentorganization.ErrPermissionDenied
	}

	uow, err := s.factory.BeginOnboarding(ctx)
	if err != nil {
		return currentorganization.Organization{}, safeerr.Wrap("begin verification request transaction", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = uow.Rollback(ctx)
		}
	}()

	organization, found, err := uow.LockOrganization(ctx, actor.OrganizationID)
	if err != nil {
		return currentorganization.Organization{}, safeerr.Wrap("lock organization verification row", err)
	}
	if !found {
		return currentorganization.Organization{}, currentorganization.ErrOrganizationNotReady
	}
	if err := requireActive(organization.Status); err != nil {
		return currentorganization.Organization{}, err
	}

	if organization.VerificationStatus == organizationdomain.VerificationStatusPending {
		if err := uow.Commit(ctx); err != nil {
			return currentorganization.Organization{}, safeerr.Wrap("commit idempotent verification request", err)
		}
		committed = true
		return organization, nil
	}
	if !organizationdomain.CanTransitionVerification(
		organization.VerificationStatus,
		organizationdomain.VerificationStatusPending,
	) {
		return currentorganization.Organization{}, ErrVerificationTransitionNotAllowed
	}

	previousStatus := organization.VerificationStatus
	organization, err = uow.UpdateVerificationStatus(
		ctx,
		actor.OrganizationID,
		organizationdomain.VerificationStatusPending,
	)
	if err != nil {
		return currentorganization.Organization{}, safeerr.Wrap("request organization verification", err)
	}
	pendingStatus := organizationdomain.VerificationStatusPending
	event, err := organizationaudit.TenantEvent(
		actor.OrganizationID,
		organizationaudit.EventOrganizationVerificationRequested,
		actor.IdentityUserID,
		actor.MembershipID,
		nil,
		&previousStatus,
		&pendingStatus,
	)
	if err != nil {
		return currentorganization.Organization{}, safeerr.Wrap("create verification request audit event", err)
	}
	if err := uow.InsertAuditEvent(ctx, event); err != nil {
		return currentorganization.Organization{}, safeerr.Wrap("append verification request audit event", err)
	}
	if err := uow.Commit(ctx); err != nil {
		return currentorganization.Organization{}, safeerr.Wrap("commit organization verification request", err)
	}
	committed = true
	return organization, nil
}

func requireActive(status string) error {
	switch status {
	case "active":
		return nil
	case "pending":
		return currentorganization.ErrOrganizationNotReady
	case "disabled":
		return currentorganization.ErrOrganizationDisabled
	case "deleted":
		return currentorganization.ErrOrganizationDeleted
	default:
		return errors.New("unsupported organization status")
	}
}

func mergedValue(current *string, patch StringPatch) *string {
	if !patch.Set {
		return current
	}
	return patch.Value
}

func equalOptional(left, right *string) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}
