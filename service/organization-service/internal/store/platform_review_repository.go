package store

import (
	"context"
	"errors"
	"time"

	"github.com/DoMinhHHung/bridgeworks/service/organization-service/internal/currentorganization"
	"github.com/DoMinhHHung/bridgeworks/service/organization-service/internal/organizationaudit"
	"github.com/DoMinhHHung/bridgeworks/service/organization-service/internal/platform/safeerr"
	"github.com/DoMinhHHung/bridgeworks/service/organization-service/internal/platformreview"
	"github.com/DoMinhHHung/bridgeworks/service/organization-service/internal/store/sqlcgen"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func (r *Repository) BeginPlatformReview(ctx context.Context) (platformreview.ReviewUnitOfWork, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	return &platformReviewUnitOfWork{tx: tx, queries: r.queries.WithTx(tx)}, nil
}

func (r *Repository) ListPendingVerificationQueue(
	ctx context.Context,
	afterTime time.Time,
	afterID uuid.UUID,
	limit int32,
) ([]platformreview.QueueItem, error) {
	rows, err := r.queries.ListPendingVerificationQueue(ctx, sqlcgen.ListPendingVerificationQueueParams{
		AfterTime:   pgtype.Timestamptz{Time: afterTime.UTC(), Valid: true},
		AfterID:     afterID,
		ResultLimit: limit,
	})
	if err != nil {
		return nil, safeerr.Wrap("query pending verification queue", err)
	}
	items := make([]platformreview.QueueItem, 0, len(rows))
	for _, row := range rows {
		item := platformreview.QueueItem{
			ID:                 row.ID,
			Name:               row.Name,
			LegalName:          row.LegalName,
			Website:            row.Website,
			Country:            row.Country,
			CompanyType:        row.CompanyType,
			VerificationStatus: row.VerificationStatus,
			BusinessEmailDomain: row.BusinessEmailDomain,
			UpdatedAt:          row.UpdatedAt.Time.UTC(),
			RequestedAt:        row.RequestedAt.Time.UTC(),
		}
		if row.BusinessEmailVerifiedAt.Valid {
			value := row.BusinessEmailVerifiedAt.Time.UTC()
			item.BusinessEmailVerifiedAt = &value
		}
		items = append(items, item)
	}
	return items, nil
}

type platformReviewUnitOfWork struct {
	tx      transaction
	queries *sqlcgen.Queries
}

func (u *platformReviewUnitOfWork) LockOrganization(
	ctx context.Context,
	organizationID uuid.UUID,
) (currentorganization.Organization, bool, error) {
	row, err := u.queries.LockOrganizationByID(ctx, organizationID)
	if errors.Is(err, pgx.ErrNoRows) {
		return currentorganization.Organization{}, false, nil
	}
	if err != nil {
		return currentorganization.Organization{}, false, err
	}
	return currentOrganizationFromRow(row), true, nil
}

func (u *platformReviewUnitOfWork) UpdateVerificationStatus(
	ctx context.Context,
	organizationID uuid.UUID,
	status string,
) (currentorganization.Organization, error) {
	row, err := u.queries.UpdateOrganizationVerificationStatus(ctx, sqlcgen.UpdateOrganizationVerificationStatusParams{
		ID:                 organizationID,
		VerificationStatus: status,
	})
	if err != nil {
		return currentorganization.Organization{}, err
	}
	return currentOrganizationFromRow(row), nil
}

func (u *platformReviewUnitOfWork) InsertAuditEvent(ctx context.Context, event organizationaudit.Event) error {
	return insertAuditEvent(ctx, u.queries, event)
}

func (u *platformReviewUnitOfWork) Commit(ctx context.Context) error {
	return u.tx.Commit(ctx)
}

func (u *platformReviewUnitOfWork) Rollback(ctx context.Context) error {
	return u.tx.Rollback(ctx)
}
