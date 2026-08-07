package store

import (
	"context"
	"errors"

	"github.com/DoMinhHHung/bridgeworks/service/organization-service/internal/currentorganization"
	"github.com/DoMinhHHung/bridgeworks/service/organization-service/internal/organizationonboarding"
	"github.com/DoMinhHHung/bridgeworks/service/organization-service/internal/organizationsync"
	"github.com/DoMinhHHung/bridgeworks/service/organization-service/internal/platform/safeerr"
	"github.com/DoMinhHHung/bridgeworks/service/organization-service/internal/store/sqlcgen"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

const (
	createMembershipInsertSavepoint   = "SAVEPOINT membership_insert"
	rollbackMembershipInsertSavepoint = "ROLLBACK TO SAVEPOINT membership_insert"
	releaseMembershipInsertSavepoint  = "RELEASE SAVEPOINT membership_insert"
)

type database interface {
	Begin(context.Context) (pgx.Tx, error)
	sqlcgen.DBTX
}

type transaction interface {
	sqlcgen.DBTX
	Commit(context.Context) error
	Rollback(context.Context) error
}

type Repository struct {
	db      database
	queries *sqlcgen.Queries
}

func New(db database) *Repository {
	return &Repository{db: db, queries: sqlcgen.New(db)}
}

func (r *Repository) Begin(ctx context.Context) (organizationsync.UnitOfWork, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	return &unitOfWork{tx: tx, queries: r.queries.WithTx(tx)}, nil
}

func (r *Repository) BeginOnboarding(ctx context.Context) (organizationonboarding.UnitOfWork, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	return &onboardingUnitOfWork{tx: tx, queries: r.queries.WithTx(tx)}, nil
}

func (r *Repository) GetOrganizationByClerkID(ctx context.Context, clerkOrganizationID string) (currentorganization.Organization, bool, error) {
	row, err := r.queries.GetOrganizationByClerkID(ctx, clerkOrganizationID)
	if errors.Is(err, pgx.ErrNoRows) {
		return currentorganization.Organization{}, false, nil
	}
	if err != nil {
		return currentorganization.Organization{}, false, safeerr.Wrap("get organization projection", err)
	}
	return currentOrganizationFromRow(row), true, nil
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

type unitOfWork struct {
	tx      transaction
	queries *sqlcgen.Queries
}

func (u *unitOfWork) InsertInbox(ctx context.Context, event organizationsync.Event) (bool, error) {
	_, err := u.queries.InsertWebhookEvent(ctx, sqlcgen.InsertWebhookEventParams{
		EventID:             event.EventID,
		EventType:           event.Type,
		AggregateType:       event.AggregateType,
		AggregateID:         event.AggregateID,
		ClerkOrganizationID: event.ClerkOrganizationID,
		OccurredAt: pgtype.Timestamptz{
			Time:  event.OccurredAt.UTC(),
			Valid: true,
		},
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

func (u *unitOfWork) AcquireOrganizationLock(ctx context.Context, clerkOrganizationID string) error {
	return u.queries.AcquireOrganizationAdvisoryLock(ctx, clerkOrganizationID)
}

func (u *unitOfWork) LatestAggregateEvent(
	ctx context.Context,
	aggregateType string,
	aggregateID string,
	eventID string,
) (organizationsync.Event, bool, error) {
	row, err := u.queries.GetLatestAggregateEvent(ctx, sqlcgen.GetLatestAggregateEventParams{
		AggregateType: aggregateType,
		AggregateID:   aggregateID,
		EventID:       eventID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return organizationsync.Event{}, false, nil
	}
	if err != nil {
		return organizationsync.Event{}, false, err
	}
	return organizationsync.Event{
		EventID:    row.EventID,
		Type:       row.EventType,
		OccurredAt: row.OccurredAt.Time.UTC(),
	}, true, nil
}

func (u *unitOfWork) GetOrganization(ctx context.Context, clerkOrganizationID string) (organizationsync.Organization, bool, error) {
	row, err := u.queries.GetOrganizationByClerkID(ctx, clerkOrganizationID)
	if errors.Is(err, pgx.ErrNoRows) {
		return organizationsync.Organization{}, false, nil
	}
	if err != nil {
		return organizationsync.Organization{}, false, err
	}
	return organizationFromRow(row), true, nil
}

func (u *unitOfWork) InsertOrganization(ctx context.Context, organization organizationsync.Organization) error {
	_, err := u.queries.InsertOrganization(ctx, sqlcgen.InsertOrganizationParams{
		ID:                   organization.ID,
		ClerkOrganizationID:  organization.ClerkOrganizationID,
		Name:                 organization.Name,
		Slug:                 organization.Slug,
		Status:               organization.Status,
		ClerkCreatedByUserID: organization.ClerkCreatedByUserID,
	})
	return err
}

func (u *unitOfWork) UpdateOrganizationProjection(
	ctx context.Context,
	clerkOrganizationID string,
	name *string,
	slug *string,
	status string,
) error {
	return u.queries.UpdateOrganizationProjection(ctx, sqlcgen.UpdateOrganizationProjectionParams{
		ClerkOrganizationID: clerkOrganizationID,
		Name:                name,
		Slug:                slug,
		Status:              status,
	})
}

func (u *unitOfWork) SetOrganizationCreator(ctx context.Context, clerkOrganizationID, clerkUserID string) error {
	_, err := u.queries.SetOrganizationCreator(ctx, sqlcgen.SetOrganizationCreatorParams{
		ClerkOrganizationID:  clerkOrganizationID,
		ClerkCreatedByUserID: &clerkUserID,
	})
	return err
}

func (u *unitOfWork) DisableOrganizationOwnerBootstrapEligibility(ctx context.Context, organizationID uuid.UUID) error {
	return u.queries.DisableOrganizationOwnerBootstrapEligibility(ctx, organizationID)
}

func (u *unitOfWork) MarkOrganizationOwnerBootstrapped(ctx context.Context, organizationID uuid.UUID) error {
	return u.queries.MarkOrganizationOwnerBootstrapped(ctx, organizationID)
}

func (u *unitOfWork) MarkOrganizationDeleted(ctx context.Context, clerkOrganizationID string) error {
	_, err := u.queries.MarkOrganizationDeleted(ctx, clerkOrganizationID)
	return err
}

func (u *unitOfWork) GetMembership(ctx context.Context, clerkMembershipID string) (organizationsync.Membership, bool, error) {
	row, err := u.queries.GetMembershipByClerkID(ctx, clerkMembershipID)
	if errors.Is(err, pgx.ErrNoRows) {
		return organizationsync.Membership{}, false, nil
	}
	if err != nil {
		return organizationsync.Membership{}, false, err
	}
	return membershipFromRow(row), true, nil
}

func (u *unitOfWork) GetActiveMembership(
	ctx context.Context,
	organizationID uuid.UUID,
	clerkUserID string,
) (organizationsync.Membership, bool, error) {
	row, err := u.queries.GetActiveMembershipByOrganizationUser(ctx, sqlcgen.GetActiveMembershipByOrganizationUserParams{
		OrganizationID: organizationID,
		ClerkUserID:    clerkUserID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return organizationsync.Membership{}, false, nil
	}
	if err != nil {
		return organizationsync.Membership{}, false, err
	}
	return membershipFromRow(row), true, nil
}

func (u *unitOfWork) HasDeletedMembership(ctx context.Context, organizationID uuid.UUID, clerkUserID string) (bool, error) {
	return u.queries.HasDeletedMembershipByOrganizationUser(ctx, sqlcgen.HasDeletedMembershipByOrganizationUserParams{
		OrganizationID: organizationID,
		ClerkUserID:    clerkUserID,
	})
}

func (u *unitOfWork) CreateMembershipInsertSavepoint(ctx context.Context) error {
	_, err := u.tx.Exec(ctx, createMembershipInsertSavepoint)
	return err
}

func (u *unitOfWork) RollbackMembershipInsertSavepoint(ctx context.Context) error {
	_, err := u.tx.Exec(ctx, rollbackMembershipInsertSavepoint)
	return err
}

func (u *unitOfWork) ReleaseMembershipInsertSavepoint(ctx context.Context) error {
	_, err := u.tx.Exec(ctx, releaseMembershipInsertSavepoint)
	return err
}

func (u *unitOfWork) InsertMembership(ctx context.Context, membership organizationsync.Membership) error {
	_, err := u.queries.InsertMembership(ctx, sqlcgen.InsertMembershipParams{
		ID:                membership.ID,
		ClerkMembershipID: membership.ClerkMembershipID,
		OrganizationID:    membership.OrganizationID,
		ClerkUserID:       membership.ClerkUserID,
		ClerkRole:         membership.ClerkRole,
		ApplicationRole:   membership.ApplicationRole,
		Status:            membership.Status,
	})
	if err == nil {
		return nil
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return &organizationsync.UniqueConstraintError{
			Constraint: pgErr.ConstraintName,
			Cause:      err,
		}
	}
	return err
}

func (u *unitOfWork) UpdateMembershipClerkRole(ctx context.Context, clerkMembershipID string, clerkRole *string) error {
	_, err := u.queries.UpdateMembershipClerkRole(ctx, sqlcgen.UpdateMembershipClerkRoleParams{
		ClerkMembershipID: clerkMembershipID,
		ClerkRole:         clerkRole,
	})
	return err
}

func (u *unitOfWork) UpdateMembershipApplicationRole(ctx context.Context, membershipID uuid.UUID, role string) error {
	_, err := u.queries.UpdateMembershipApplicationRole(ctx, sqlcgen.UpdateMembershipApplicationRoleParams{
		ID:              membershipID,
		ApplicationRole: role,
	})
	return err
}

func (u *unitOfWork) MarkMembershipDeleted(ctx context.Context, clerkMembershipID string) error {
	_, err := u.queries.MarkMembershipDeleted(ctx, clerkMembershipID)
	return err
}

func (u *unitOfWork) Commit(ctx context.Context) error   { return u.tx.Commit(ctx) }
func (u *unitOfWork) Rollback(ctx context.Context) error { return u.tx.Rollback(ctx) }

type onboardingUnitOfWork struct {
	tx      transaction
	queries *sqlcgen.Queries
}

func (u *onboardingUnitOfWork) LockOrganization(ctx context.Context, organizationID uuid.UUID) (currentorganization.Organization, bool, error) {
	row, err := u.queries.LockOrganizationByID(ctx, organizationID)
	if errors.Is(err, pgx.ErrNoRows) {
		return currentorganization.Organization{}, false, nil
	}
	if err != nil {
		return currentorganization.Organization{}, false, err
	}
	return currentOrganizationFromRow(row), true, nil
}

func (u *onboardingUnitOfWork) UpdateProductProfile(
	ctx context.Context,
	organizationID uuid.UUID,
	legalName *string,
	website *string,
	country *string,
	companyType *string,
) (currentorganization.Organization, error) {
	row, err := u.queries.UpdateOrganizationProductProfile(ctx, sqlcgen.UpdateOrganizationProductProfileParams{
		ID:          organizationID,
		LegalName:   legalName,
		Website:     website,
		Country:     country,
		CompanyType: companyType,
	})
	if err != nil {
		return currentorganization.Organization{}, err
	}
	return currentOrganizationFromRow(row), nil
}

func (u *onboardingUnitOfWork) UpdateVerificationStatus(
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

func (u *onboardingUnitOfWork) Commit(ctx context.Context) error   { return u.tx.Commit(ctx) }
func (u *onboardingUnitOfWork) Rollback(ctx context.Context) error { return u.tx.Rollback(ctx) }

func currentOrganizationFromRow(row sqlcgen.OrganizationOrganization) currentorganization.Organization {
	return currentorganization.Organization{
		ID:                 row.ID,
		Name:               row.Name,
		Slug:               row.Slug,
		Status:             row.Status,
		LegalName:          row.LegalName,
		Website:            row.Website,
		Country:            row.Country,
		CompanyType:        row.CompanyType,
		VerificationStatus: row.VerificationStatus,
		TrustStatus:        row.TrustStatus,
	}
}

func organizationFromRow(row sqlcgen.OrganizationOrganization) organizationsync.Organization {
	return organizationsync.Organization{
		ID:                     row.ID,
		ClerkOrganizationID:    row.ClerkOrganizationID,
		Name:                   row.Name,
		Slug:                   row.Slug,
		Status:                 row.Status,
		ClerkCreatedByUserID:   row.ClerkCreatedByUserID,
		OwnerBootstrapped:      row.OwnerBootstrapped,
		OwnerBootstrapEligible: row.OwnerBootstrapEligible,
	}
}

func membershipFromRow(row sqlcgen.OrganizationMembership) organizationsync.Membership {
	return organizationsync.Membership{
		ID:                row.ID,
		ClerkMembershipID: row.ClerkMembershipID,
		OrganizationID:    row.OrganizationID,
		ClerkUserID:       row.ClerkUserID,
		ClerkRole:         row.ClerkRole,
		ApplicationRole:   row.ApplicationRole,
		Status:            row.Status,
	}
}
