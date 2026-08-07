package store

import (
	"context"
	"errors"
	"time"

	"github.com/DoMinhHHung/bridgeworks/service/identity-service/internal/platform/safeerr"
	"github.com/DoMinhHHung/bridgeworks/service/identity-service/internal/platformaccess"
	"github.com/DoMinhHHung/bridgeworks/service/identity-service/internal/platformaccessoperator"
	"github.com/DoMinhHHung/bridgeworks/service/identity-service/internal/store/sqlcgen"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func (r *Repository) GetPlatformAccessUserByClerkUserID(
	ctx context.Context,
	clerkUserID string,
) (platformaccess.User, bool, error) {
	if r == nil || r.database == nil {
		return platformaccess.User{}, false, errors.New("identity repository is not initialized")
	}

	row, err := sqlcgen.New(r.database).GetPlatformAccessUserByClerkUserID(ctx, clerkUserID)
	if errors.Is(err, pgx.ErrNoRows) {
		return platformaccess.User{}, false, nil
	}
	if err != nil {
		return platformaccess.User{}, false, safeerr.Wrap("read platform access identity", err)
	}
	return platformaccess.User{ID: row.ID, Status: row.Status}, true, nil
}

func (r *Repository) ListActivePlatformRolesByUserID(ctx context.Context, identityUserID uuid.UUID) ([]string, error) {
	if r == nil || r.database == nil {
		return nil, errors.New("identity repository is not initialized")
	}
	roles, err := sqlcgen.New(r.database).ListActivePlatformRolesByUserID(ctx, identityUserID)
	if err != nil {
		return nil, safeerr.Wrap("read active platform roles", err)
	}
	return roles, nil
}

func (r *Repository) GetPlatformAccessUserByIDUser(
	ctx context.Context,
	idUser string,
) (platformaccessoperator.User, bool, error) {
	if r == nil || r.database == nil {
		return platformaccessoperator.User{}, false, errors.New("identity repository is not initialized")
	}
	row, err := sqlcgen.New(r.database).GetPlatformAccessUserByIDUser(ctx, idUser)
	if errors.Is(err, pgx.ErrNoRows) {
		return platformaccessoperator.User{}, false, nil
	}
	if err != nil {
		return platformaccessoperator.User{}, false, safeerr.Wrap("read platform access target", err)
	}
	return platformaccessoperator.User{ID: row.ID, Status: row.Status}, true, nil
}

func (r *Repository) GrantPlatformAccess(ctx context.Context, identityUserID uuid.UUID, role string) error {
	if r == nil || r.database == nil {
		return errors.New("identity repository is not initialized")
	}
	if err := sqlcgen.New(r.database).GrantPlatformAccess(ctx, sqlcgen.GrantPlatformAccessParams{
		IdentityUserID: identityUserID,
		Role:           role,
	}); err != nil {
		return safeerr.Wrap("grant platform access", err)
	}
	return nil
}

func (r *Repository) RevokePlatformAccess(ctx context.Context, identityUserID uuid.UUID, role string) error {
	if r == nil || r.database == nil {
		return errors.New("identity repository is not initialized")
	}
	if err := sqlcgen.New(r.database).RevokePlatformAccess(ctx, sqlcgen.RevokePlatformAccessParams{
		IdentityUserID: identityUserID,
		Role:           role,
	}); err != nil {
		return safeerr.Wrap("revoke platform access", err)
	}
	return nil
}

func (r *Repository) GetPlatformAccessAssignment(
	ctx context.Context,
	identityUserID uuid.UUID,
	role string,
) (platformaccessoperator.Assignment, bool, error) {
	if r == nil || r.database == nil {
		return platformaccessoperator.Assignment{}, false, errors.New("identity repository is not initialized")
	}
	row, err := sqlcgen.New(r.database).GetPlatformAccessAssignment(ctx, sqlcgen.GetPlatformAccessAssignmentParams{
		IdentityUserID: identityUserID,
		Role:           role,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return platformaccessoperator.Assignment{}, false, nil
	}
	if err != nil {
		return platformaccessoperator.Assignment{}, false, safeerr.Wrap("read platform access assignment", err)
	}

	assignment := platformaccessoperator.Assignment{
		GrantedAt: row.GrantedAt.Time.UTC(),
		UpdatedAt: row.UpdatedAt.Time.UTC(),
	}
	if row.RevokedAt.Valid {
		revokedAt := row.RevokedAt.Time.UTC()
		assignment.RevokedAt = &revokedAt
	}
	return assignment, true, nil
}

var _ = time.Time{}
