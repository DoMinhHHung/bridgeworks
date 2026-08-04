package store

import (
	"context"
	"errors"

	"github.com/DoMinhHHung/bridgeworks/service/organization-service/internal/currentorganization"
	"github.com/DoMinhHHung/bridgeworks/service/organization-service/internal/organizationid"
	"github.com/DoMinhHHung/bridgeworks/service/organization-service/internal/organizationsync"
	"github.com/DoMinhHHung/bridgeworks/service/organization-service/internal/platform/safeerr"
	"github.com/DoMinhHHung/bridgeworks/service/organization-service/internal/store/sqlcgen"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

type Beginner interface {
	Begin(context.Context) (pgx.Tx, error)
}

type Repository struct {
	db      Beginner
	queries *sqlcgen.Queries
}

func New(db interface {
	Begin(context.Context) (pgx.Tx, error)
	sqlcgen.DBTX
}) *Repository {
	return &Repository{db: db, queries: sqlcgen.New(db)}
}

func (r *Repository) GetOrganizationByClerkID(ctx context.Context, clerkOrganizationID string) (currentorganization.Organization, bool, error) {
	row, err := r.queries.GetOrganizationByClerkID(ctx, clerkOrganizationID)
	if errors.Is(err, pgx.ErrNoRows) {
		return currentorganization.Organization{}, false, nil
	}
	if err != nil {
		return currentorganization.Organization{}, false, safeerr.Wrap("get organization projection", err)
	}
	return currentorganization.Organization{
		ID: row.ID, Name: row.Name, Slug: row.Slug, Status: row.Status,
	}, true, nil
}

func (r *Repository) GetActiveMembership(ctx context.Context, organizationID uuid.UUID, clerkUserID string) (currentorganization.Membership, bool, error) {
	row, err := r.queries.GetActiveMembershipByOrganizationUser(ctx, sqlcgen.GetActiveMembershipByOrganizationUserParams{
		OrganizationID: organizationID,
		ClerkUserID:    clerkUserID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return currentorganization.Membership{}, false, nil
	}
	if err != nil {
		return currentorganization.Membership{}, false, safeerr.Wrap("get active organization membership", err)
	}
	return currentorganization.Membership{
		ID: row.ID, OrganizationID: row.OrganizationID,
		ApplicationRole: row.ApplicationRole, Status: row.Status,
	}, true, nil
}

func (r *Repository) ListPermissions(ctx context.Context, role string) ([]string, error) {
	permissions, err := r.queries.ListPermissionsForRole(ctx, role)
	if err != nil {
		return nil, safeerr.Wrap("list role permissions", err)
	}
	return permissions, nil
}

func (r *Repository) ProcessEvent(ctx context.Context, event organizationsync.Event, generator organizationid.Generator) (err error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return safeerr.Wrap("begin organization synchronization transaction", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback(ctx)
		}
	}()
	queries := r.queries.WithTx(tx)

	_, err = queries.InsertWebhookEvent(ctx, sqlcgen.InsertWebhookEventParams{
		EventID: event.EventID, EventType: event.Type,
		AggregateType: event.AggregateType, AggregateID: event.AggregateID,
		ClerkOrganizationID: event.ClerkOrganizationID,
		OccurredAt: pgtype.Timestamptz{
			Time:  event.OccurredAt.UTC(),
			Valid: true,
		},
	})
	if errors.Is(err, pgx.ErrNoRows) {
		if commitErr := tx.Commit(ctx); commitErr != nil {
			return safeerr.Wrap("commit duplicate organization event", commitErr)
		}
		return nil
	}
	if err != nil {
		return safeerr.Wrap("insert organization webhook inbox event", err)
	}

	if err = queries.AcquireOrganizationAdvisoryLock(ctx, event.ClerkOrganizationID); err != nil {
		return safeerr.Wrap("acquire organization advisory lock", err)
	}
	latest, latestErr := queries.GetLatestAggregateEvent(ctx, sqlcgen.GetLatestAggregateEventParams{
		AggregateType: event.AggregateType,
		AggregateID:   event.AggregateID,
		EventID:       event.EventID,
	})
	if latestErr != nil && !errors.Is(latestErr, pgx.ErrNoRows) {
		return safeerr.Wrap("load latest organization aggregate event", latestErr)
	}
	if latestErr == nil && organizationsync.IsStale(event, organizationsync.Event{
		EventID: latest.EventID,
		Type:    latest.EventType,
		OccurredAt: latest.OccurredAt.Time.UTC(),
	}) {
		if commitErr := tx.Commit(ctx); commitErr != nil {
			return safeerr.Wrap("commit stale organization event", commitErr)
		}
		return nil
	}

	if event.AggregateType == organizationsync.AggregateOrganization {
		err = applyOrganizationEvent(ctx, queries, event, generator)
	} else {
		err = applyMembershipEvent(ctx, queries, event, generator)
	}
	if err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return safeerr.Wrap("commit organization synchronization transaction", err)
	}
	return nil
}

func applyOrganizationEvent(ctx context.Context, queries *sqlcgen.Queries, event organizationsync.Event, generator organizationid.Generator) error {
	existing, err := queries.GetOrganizationByClerkID(ctx, event.ClerkOrganizationID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return safeerr.Wrap("load organization for synchronization", err)
	}
	if event.Type == organizationsync.EventOrganizationDeleted {
		if errors.Is(err, pgx.ErrNoRows) {
			id, generateErr := generator.New()
			if generateErr != nil {
				return safeerr.Wrap("generate organization ID", generateErr)
			}
			_, insertErr := queries.InsertOrganization(ctx, sqlcgen.InsertOrganizationParams{
				ID: id, ClerkOrganizationID: event.ClerkOrganizationID, Status: "deleted",
			})
			if insertErr != nil {
				return safeerr.Wrap("insert deleted organization tombstone", insertErr)
			}
			return nil
		}
		_, err = queries.MarkOrganizationDeleted(ctx, event.ClerkOrganizationID)
		if err != nil {
			return safeerr.Wrap("mark organization deleted", err)
		}
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		id, generateErr := generator.New()
		if generateErr != nil {
			return safeerr.Wrap("generate organization ID", generateErr)
		}
		_, insertErr := queries.InsertOrganization(ctx, sqlcgen.InsertOrganizationParams{
			ID: id, ClerkOrganizationID: event.ClerkOrganizationID,
			Name: event.Organization.Name, Slug: event.Organization.Slug, Status: "active",
		})
		if insertErr != nil {
			return safeerr.Wrap("insert organization projection", insertErr)
		}
		return nil
	}
	if existing.Status == "deleted" {
		return nil
	}
	_, err = queries.UpdateOrganizationProjection(ctx, sqlcgen.UpdateOrganizationProjectionParams{
		ClerkOrganizationID: event.ClerkOrganizationID,
		Name:                event.Organization.Name,
		Slug:                event.Organization.Slug,
	})
	if err != nil {
		return safeerr.Wrap("update organization projection", err)
	}
	return nil
}

func applyMembershipEvent(ctx context.Context, queries *sqlcgen.Queries, event organizationsync.Event, generator organizationid.Generator) error {
	organization, err := queries.GetOrganizationByClerkID(ctx, event.ClerkOrganizationID)
	if errors.Is(err, pgx.ErrNoRows) {
		id, generateErr := generator.New()
		if generateErr != nil {
			return safeerr.Wrap("generate pending organization ID", generateErr)
		}
		organization, err = queries.InsertOrganization(ctx, sqlcgen.InsertOrganizationParams{
			ID: id, ClerkOrganizationID: event.ClerkOrganizationID, Status: "pending",
		})
	}
	if err != nil {
		return safeerr.Wrap("ensure membership organization projection", err)
	}

	existing, err := queries.GetMembershipByClerkID(ctx, event.Membership.ClerkMembershipID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return safeerr.Wrap("load membership for synchronization", err)
	}
	if event.Type == organizationsync.EventMembershipDeleted {
		if errors.Is(err, pgx.ErrNoRows) {
			id, generateErr := generator.New()
			if generateErr != nil {
				return safeerr.Wrap("generate membership tombstone ID", generateErr)
			}
			_, insertErr := queries.InsertMembership(ctx, sqlcgen.InsertMembershipParams{
				ID: id, ClerkMembershipID: event.Membership.ClerkMembershipID,
				OrganizationID: organization.ID, ClerkUserID: event.Membership.ClerkUserID,
				ClerkRole: event.Membership.ClerkRole, ApplicationRole: organizationsync.RoleViewer,
				Status: "deleted",
			})
			if insertErr != nil {
				return safeerr.Wrap("insert deleted membership tombstone", insertErr)
			}
			return nil
		}
		if existing.Status == "deleted" {
			return nil
		}
		_, err = queries.MarkMembershipDeleted(ctx, event.Membership.ClerkMembershipID)
		if err != nil {
			return safeerr.Wrap("mark membership deleted", err)
		}
		return nil
	}
	if err == nil {
		if existing.Status == "deleted" {
			return nil
		}
		_, updateErr := queries.UpdateMembershipClerkRole(ctx, sqlcgen.UpdateMembershipClerkRoleParams{
			ClerkMembershipID: event.Membership.ClerkMembershipID,
			ClerkRole:         event.Membership.ClerkRole,
		})
		if updateErr != nil {
			return safeerr.Wrap("update membership Clerk role", updateErr)
		}
		return nil
	}

	id, generateErr := generator.New()
	if generateErr != nil {
		return safeerr.Wrap("generate membership ID", generateErr)
	}
	_, insertErr := queries.InsertMembership(ctx, sqlcgen.InsertMembershipParams{
		ID: id, ClerkMembershipID: event.Membership.ClerkMembershipID,
		OrganizationID: organization.ID, ClerkUserID: event.Membership.ClerkUserID,
		ClerkRole: event.Membership.ClerkRole,
		ApplicationRole: organizationsync.InitialApplicationRole(event.Membership.ClerkRole),
		Status: "active",
	})
	if insertErr == nil {
		return nil
	}
	var pgErr *pgconn.PgError
	if errors.As(insertErr, &pgErr) && pgErr.Code == "23505" {
		conflicting, loadErr := queries.GetActiveMembershipByOrganizationUser(ctx, sqlcgen.GetActiveMembershipByOrganizationUserParams{
			OrganizationID: organization.ID,
			ClerkUserID:    event.Membership.ClerkUserID,
		})
		if loadErr == nil && conflicting.ClerkMembershipID == event.Membership.ClerkMembershipID {
			return nil
		}
		if loadErr != nil && !errors.Is(loadErr, pgx.ErrNoRows) {
			return safeerr.Wrap("load conflicting active membership", loadErr)
		}
		return errors.New("conflicting active organization membership")
	}
	return safeerr.Wrap("insert active organization membership", insertErr)
}
