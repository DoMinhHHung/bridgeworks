package organizationsync

import (
	"context"
	"errors"

	"github.com/google/uuid"
)

const (
	ConstraintMembershipClerkID        = "memberships_clerk_membership_id_uq"
	ConstraintActiveOrganizationMember = "memberships_active_organization_user_uq"
)

var ErrActiveMembershipConflict = errors.New("active organization membership conflict")

type Organization struct {
	ID                     uuid.UUID
	ClerkOrganizationID    string
	Name                   *string
	Slug                   *string
	Status                 string
	ClerkCreatedByUserID   *string
	OwnerBootstrapped      bool
	OwnerBootstrapEligible bool
}

type Membership struct {
	ID                uuid.UUID
	ClerkMembershipID string
	OrganizationID    uuid.UUID
	ClerkUserID       string
	ClerkRole         *string
	ApplicationRole   string
	Status            string
}

type UniqueConstraintError struct {
	Constraint string
	Cause      error
}

func (e *UniqueConstraintError) Error() string { return "unique constraint violation" }
func (e *UniqueConstraintError) Unwrap() error { return e.Cause }

type UnitOfWork interface {
	InsertInbox(context.Context, Event) (bool, error)
	AcquireOrganizationLock(context.Context, string) error
	LatestAggregateEvent(context.Context, string, string, string) (Event, bool, error)

	GetOrganization(context.Context, string) (Organization, bool, error)
	InsertOrganization(context.Context, Organization) error
	UpdateOrganizationProjection(context.Context, string, *string, *string, string) error
	SetOrganizationCreator(context.Context, string, string) error
	DisableOrganizationOwnerBootstrapEligibility(context.Context, uuid.UUID) error
	MarkOrganizationOwnerBootstrapped(context.Context, uuid.UUID) error
	MarkOrganizationDeleted(context.Context, string) error

	GetMembership(context.Context, string) (Membership, bool, error)
	GetActiveMembership(context.Context, uuid.UUID, string) (Membership, bool, error)
	HasDeletedMembership(context.Context, uuid.UUID, string) (bool, error)
	CreateMembershipInsertSavepoint(context.Context) error
	RollbackMembershipInsertSavepoint(context.Context) error
	ReleaseMembershipInsertSavepoint(context.Context) error
	InsertMembership(context.Context, Membership) error
	UpdateMembershipClerkRole(context.Context, string, *string) error
	UpdateMembershipApplicationRole(context.Context, uuid.UUID, string) error
	MarkMembershipDeleted(context.Context, string) error

	Commit(context.Context) error
	Rollback(context.Context) error
}

type UnitOfWorkFactory interface {
	Begin(context.Context) (UnitOfWork, error)
}
