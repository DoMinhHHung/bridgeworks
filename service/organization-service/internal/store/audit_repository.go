package store

import (
	"context"
	"time"

	"github.com/DoMinhHHung/bridgeworks/service/organization-service/internal/organizationaudit"
	"github.com/DoMinhHHung/bridgeworks/service/organization-service/internal/platform/safeerr"
	"github.com/DoMinhHHung/bridgeworks/service/organization-service/internal/store/sqlcgen"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

func (u *unitOfWork) insertAuditEvent(ctx context.Context, event organizationaudit.Event) error {
	return insertAuditEvent(ctx, u.queries, event)
}

func (u *onboardingUnitOfWork) InsertAuditEvent(ctx context.Context, event organizationaudit.Event) error {
	return insertAuditEvent(ctx, u.queries, event)
}

func (u *businessVerificationUnitOfWork) InsertAuditEvent(ctx context.Context, event organizationaudit.Event) error {
	return insertAuditEvent(ctx, u.queries, event)
}

func (u *membershipAdministrationUnitOfWork) InsertAuditEvent(ctx context.Context, event organizationaudit.Event) error {
	return insertAuditEvent(ctx, u.queries, event)
}

func insertAuditEvent(ctx context.Context, queries *sqlcgen.Queries, event organizationaudit.Event) error {
	return queries.InsertAuditEvent(ctx, sqlcgen.InsertAuditEventParams{
		ID:                  event.ID,
		OrganizationID:      event.OrganizationID,
		EventType:           event.EventType,
		ActorKind:           event.ActorKind,
		ActorIdentityUserID: nullableUUID(event.ActorIdentityUserID),
		ActorMembershipID:   nullableUUID(event.ActorMembershipID),
		SubjectMembershipID: nullableUUID(event.SubjectMembershipID),
		FromValue:           event.FromValue,
		ToValue:             event.ToValue,
		OccurredAt:          pgtype.Timestamptz{Time: event.OccurredAt.UTC(), Valid: true},
	})
}

func (r *Repository) ListOrganizationAuditEvents(
	ctx context.Context,
	organizationID uuid.UUID,
	beforeTime time.Time,
	beforeID uuid.UUID,
	limit int32,
) ([]organizationaudit.Record, error) {
	rows, err := r.queries.ListOrganizationAuditEvents(ctx, sqlcgen.ListOrganizationAuditEventsParams{
		OrganizationID: organizationID,
		BeforeTime:     pgtype.Timestamptz{Time: beforeTime.UTC(), Valid: true},
		BeforeID:       beforeID,
		ResultLimit:    limit,
	})
	if err != nil {
		return nil, safeerr.Wrap("query organization audit events", err)
	}
	items := make([]organizationaudit.Record, 0, len(rows))
	for _, row := range rows {
		var subject *uuid.UUID
		if row.SubjectMembershipID.Valid {
			value := uuid.UUID(row.SubjectMembershipID.Bytes)
			subject = &value
		}
		items = append(items, organizationaudit.Record{
			ID:                  row.ID,
			EventType:           row.EventType,
			ActorKind:           row.ActorKind,
			SubjectMembershipID: subject,
			FromValue:           row.FromValue,
			ToValue:             row.ToValue,
			OccurredAt:          row.OccurredAt.Time.UTC(),
		})
	}
	return items, nil
}

func nullableUUID(value *uuid.UUID) pgtype.UUID {
	if value == nil {
		return pgtype.UUID{}
	}
	return pgtype.UUID{Bytes: *value, Valid: true}
}
