package store

import (
	"context"
	"errors"
	"time"

	"github.com/DoMinhHHung/bridgeworks/service/organization-service/internal/businessverification"
	"github.com/DoMinhHHung/bridgeworks/service/organization-service/internal/membershipadmin"
	"github.com/DoMinhHHung/bridgeworks/service/organization-service/internal/organizationsync"
	"github.com/DoMinhHHung/bridgeworks/service/organization-service/internal/store/sqlcgen"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func (r *Repository) BeginBusinessVerification(ctx context.Context) (businessverification.UnitOfWork, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	return &businessVerificationUnitOfWork{tx: tx, queries: r.queries.WithTx(tx)}, nil
}

func (r *Repository) BeginMembershipAdministration(ctx context.Context) (membershipadmin.UnitOfWork, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	return &membershipAdministrationUnitOfWork{tx: tx, queries: r.queries.WithTx(tx)}, nil
}

type businessVerificationUnitOfWork struct {
	tx      transaction
	queries *sqlcgen.Queries
}

func (u *businessVerificationUnitOfWork) AcquireOrganizationLock(ctx context.Context, organizationID uuid.UUID) error {
	return u.queries.AcquireOrganizationAdvisoryLockByID(ctx, organizationID)
}

func (u *businessVerificationUnitOfWork) LockMembership(
	ctx context.Context,
	organizationID uuid.UUID,
	membershipID uuid.UUID,
) (businessverification.Membership, bool, error) {
	row, err := u.queries.LockMembershipForAdministration(ctx, sqlcgen.LockMembershipForAdministrationParams{
		OrganizationID: organizationID,
		ID:             membershipID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return businessverification.Membership{}, false, nil
	}
	if err != nil {
		return businessverification.Membership{}, false, err
	}
	return businessverification.Membership{
		ID:              row.ID,
		OrganizationID:  row.OrganizationID,
		ApplicationRole: row.ApplicationRole,
		Status:          row.Status,
		RemovalPending:  row.RemovalPending,
	}, true, nil
}

func (u *businessVerificationUnitOfWork) UpdateBusinessEmailVerification(
	ctx context.Context,
	organizationID uuid.UUID,
	domain string,
	verifiedAt time.Time,
	identityUserID uuid.UUID,
) error {
	return u.queries.UpdateOrganizationBusinessEmailVerification(
		ctx,
		sqlcgen.UpdateOrganizationBusinessEmailVerificationParams{
			ID:                      organizationID,
			BusinessEmailDomain:     &domain,
			BusinessEmailVerifiedAt: pgtype.Timestamptz{Time: verifiedAt.UTC(), Valid: true},
			BusinessEmailVerifiedByUserID: pgtype.UUID{
				Bytes: identityUserID,
				Valid: true,
			},
		},
	)
}

func (u *businessVerificationUnitOfWork) Commit(ctx context.Context) error {
	return u.tx.Commit(ctx)
}

func (u *businessVerificationUnitOfWork) Rollback(ctx context.Context) error {
	return u.tx.Rollback(ctx)
}

type membershipAdministrationUnitOfWork struct {
	tx      transaction
	queries *sqlcgen.Queries
}

func (u *membershipAdministrationUnitOfWork) AcquireOrganizationLock(ctx context.Context, organizationID uuid.UUID) error {
	return u.queries.AcquireOrganizationAdvisoryLockByID(ctx, organizationID)
}

func (u *membershipAdministrationUnitOfWork) GetOrganization(
	ctx context.Context,
	organizationID uuid.UUID,
) (membershipadmin.Organization, bool, error) {
	row, err := u.queries.GetOrganizationProviderReferenceByID(ctx, organizationID)
	if errors.Is(err, pgx.ErrNoRows) {
		return membershipadmin.Organization{}, false, nil
	}
	if err != nil {
		return membershipadmin.Organization{}, false, err
	}
	return membershipadmin.Organization{
		ID:                  row.ID,
		ClerkOrganizationID: row.ClerkOrganizationID,
	}, true, nil
}

func (u *membershipAdministrationUnitOfWork) LockMembership(
	ctx context.Context,
	organizationID uuid.UUID,
	membershipID uuid.UUID,
) (membershipadmin.Membership, bool, error) {
	row, err := u.queries.LockMembershipForAdministration(ctx, sqlcgen.LockMembershipForAdministrationParams{
		OrganizationID: organizationID,
		ID:             membershipID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return membershipadmin.Membership{}, false, nil
	}
	if err != nil {
		return membershipadmin.Membership{}, false, err
	}
	return membershipadmin.Membership{
		ID:              row.ID,
		OrganizationID:  row.OrganizationID,
		ClerkUserID:     row.ClerkUserID,
		ApplicationRole: row.ApplicationRole,
		Status:          row.Status,
		RemovalPending:  row.RemovalPending,
	}, true, nil
}

func (u *membershipAdministrationUnitOfWork) CountEffectiveOwners(ctx context.Context, organizationID uuid.UUID) (int64, error) {
	return u.queries.CountEffectiveOwners(ctx, organizationID)
}

func (u *membershipAdministrationUnitOfWork) InsertInvitationIntent(
	ctx context.Context,
	intent membershipadmin.InvitationIntent,
) error {
	return u.queries.InsertMembershipInvitationIntent(ctx, sqlcgen.InsertMembershipInvitationIntentParams{
		ID:                      intent.ID,
		OrganizationID:          intent.OrganizationID,
		ApplicationRole:         intent.ApplicationRole,
		CreatedByIdentityUserID: intent.CreatedByIdentityUserID,
	})
}

func (u *membershipAdministrationUnitOfWork) DeleteInvitationIntent(
	ctx context.Context,
	organizationID uuid.UUID,
	intentID uuid.UUID,
) error {
	return u.queries.DeleteMembershipInvitationIntent(ctx, sqlcgen.DeleteMembershipInvitationIntentParams{
		OrganizationID: organizationID,
		ID:             intentID,
	})
}

func (u *membershipAdministrationUnitOfWork) UpdateMembershipRole(
	ctx context.Context,
	organizationID uuid.UUID,
	membershipID uuid.UUID,
	role string,
) error {
	return u.queries.UpdateMembershipApplicationRoleByOrganization(
		ctx,
		sqlcgen.UpdateMembershipApplicationRoleByOrganizationParams{
			OrganizationID:  organizationID,
			ID:              membershipID,
			ApplicationRole: role,
		},
	)
}

func (u *membershipAdministrationUnitOfWork) InsertRemovalIntent(
	ctx context.Context,
	membershipID uuid.UUID,
	organizationID uuid.UUID,
	requestedByIdentityUserID uuid.UUID,
) (bool, error) {
	_, err := u.queries.InsertMembershipRemovalIntent(ctx, sqlcgen.InsertMembershipRemovalIntentParams{
		MembershipID:              membershipID,
		OrganizationID:            organizationID,
		RequestedByIdentityUserID: requestedByIdentityUserID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

func (u *membershipAdministrationUnitOfWork) DeleteRemovalIntent(
	ctx context.Context,
	organizationID uuid.UUID,
	membershipID uuid.UUID,
) error {
	return u.queries.DeleteMembershipRemovalIntent(ctx, sqlcgen.DeleteMembershipRemovalIntentParams{
		OrganizationID: organizationID,
		MembershipID:   membershipID,
	})
}

func (u *membershipAdministrationUnitOfWork) Commit(ctx context.Context) error {
	return u.tx.Commit(ctx)
}

func (u *membershipAdministrationUnitOfWork) Rollback(ctx context.Context) error {
	return u.tx.Rollback(ctx)
}

func (u *unitOfWork) GetPendingInvitationIntent(
	ctx context.Context,
	organizationID uuid.UUID,
	intentID uuid.UUID,
) (organizationsync.InvitationIntent, bool, error) {
	row, err := u.queries.GetPendingMembershipInvitationIntent(
		ctx,
		sqlcgen.GetPendingMembershipInvitationIntentParams{
			OrganizationID: organizationID,
			ID:             intentID,
		},
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return organizationsync.InvitationIntent{}, false, nil
	}
	if err != nil {
		return organizationsync.InvitationIntent{}, false, err
	}
	return organizationsync.InvitationIntent{
		ID:              row.ID,
		OrganizationID:  row.OrganizationID,
		ApplicationRole: row.ApplicationRole,
	}, true, nil
}

func (u *unitOfWork) ConsumeInvitationIntent(
	ctx context.Context,
	organizationID uuid.UUID,
	intentID uuid.UUID,
	membershipID uuid.UUID,
) error {
	return u.queries.ConsumeMembershipInvitationIntent(
		ctx,
		sqlcgen.ConsumeMembershipInvitationIntentParams{
			OrganizationID: organizationID,
			ID:             intentID,
			ConsumedMembershipID: pgtype.UUID{
				Bytes: membershipID,
				Valid: true,
			},
		},
	)
}

func (u *unitOfWork) DeleteMembershipRemovalIntent(
	ctx context.Context,
	organizationID uuid.UUID,
	membershipID uuid.UUID,
) error {
	return u.queries.DeleteMembershipRemovalIntent(ctx, sqlcgen.DeleteMembershipRemovalIntentParams{
		OrganizationID: organizationID,
		MembershipID:   membershipID,
	})
}
