package store

import (
	"context"
	"errors"
	"time"

	"github.com/DoMinhHHung/bridgeworks/service/organization-service/internal/organizationaudit"
	"github.com/DoMinhHHung/bridgeworks/service/organization-service/internal/organizationmaintenance"
	"github.com/DoMinhHHung/bridgeworks/service/organization-service/internal/platform/safeerr"
	"github.com/DoMinhHHung/bridgeworks/service/organization-service/internal/store/sqlcgen"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func (r *Repository) PruneConsumedInvitationIntents(
	ctx context.Context,
	cutoff time.Time,
	limit int32,
) (int64, error) {
	count, err := r.queries.PruneConsumedInvitationIntents(ctx, sqlcgen.PruneConsumedInvitationIntentsParams{
		Cutoff:      pgtype.Timestamptz{Time: cutoff.UTC(), Valid: true},
		ResultLimit: limit,
	})
	if err != nil {
		return 0, safeerr.Wrap("prune consumed membership invitation intents", err)
	}
	return count, nil
}

func (r *Repository) ListRemovalIntentsForReconciliation(
	ctx context.Context,
	cutoff time.Time,
	limit int32,
) ([]organizationmaintenance.RemovalCandidate, error) {
	rows, err := r.queries.ListRemovalIntentsForReconciliation(ctx, sqlcgen.ListRemovalIntentsForReconciliationParams{
		Cutoff:      pgtype.Timestamptz{Time: cutoff.UTC(), Valid: true},
		ResultLimit: limit,
	})
	if err != nil {
		return nil, safeerr.Wrap("query membership removal reconciliation candidates", err)
	}
	items := make([]organizationmaintenance.RemovalCandidate, 0, len(rows))
	for _, row := range rows {
		items = append(items, organizationmaintenance.RemovalCandidate{
			MembershipID:        row.MembershipID,
			OrganizationID:      row.OrganizationID,
			CreatedAt:           row.CreatedAt.Time.UTC(),
			ClerkOrganizationID: row.ClerkOrganizationID,
			ClerkUserID:         row.ClerkUserID,
		})
	}
	return items, nil
}

func (r *Repository) BeginMaintenance(ctx context.Context) (organizationmaintenance.UnitOfWork, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	return &maintenanceUnitOfWork{tx: tx, queries: r.queries.WithTx(tx)}, nil
}

type maintenanceUnitOfWork struct {
	tx      transaction
	queries *sqlcgen.Queries
}

func (u *maintenanceUnitOfWork) AcquireOrganizationLock(ctx context.Context, organizationID uuid.UUID) error {
	return u.queries.AcquireOrganizationAdvisoryLockByID(ctx, organizationID)
}

func (u *maintenanceUnitOfWork) LockRemovalIntent(
	ctx context.Context,
	organizationID uuid.UUID,
	membershipID uuid.UUID,
) (string, bool, error) {
	row, err := u.queries.LockRemovalIntentForMaintenance(ctx, sqlcgen.LockRemovalIntentForMaintenanceParams{
		OrganizationID: organizationID,
		MembershipID:   membershipID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return row.Status, true, nil
}

func (u *maintenanceUnitOfWork) MarkMembershipDeleted(
	ctx context.Context,
	organizationID uuid.UUID,
	membershipID uuid.UUID,
) error {
	return u.queries.MarkMembershipDeletedByID(ctx, sqlcgen.MarkMembershipDeletedByIDParams{
		OrganizationID: organizationID,
		ID:             membershipID,
	})
}

func (u *maintenanceUnitOfWork) DeleteRemovalIntent(
	ctx context.Context,
	organizationID uuid.UUID,
	membershipID uuid.UUID,
) (bool, error) {
	rows, err := u.queries.DeleteMembershipRemovalIntent(ctx, sqlcgen.DeleteMembershipRemovalIntentParams{
		OrganizationID: organizationID,
		MembershipID:   membershipID,
	})
	return rows > 0, err
}

func (u *maintenanceUnitOfWork) InsertAuditEvent(ctx context.Context, event organizationaudit.Event) error {
	return insertAuditEvent(ctx, u.queries, event)
}

func (u *maintenanceUnitOfWork) Commit(ctx context.Context) error {
	return u.tx.Commit(ctx)
}

func (u *maintenanceUnitOfWork) Rollback(ctx context.Context) error {
	return u.tx.Rollback(ctx)
}
