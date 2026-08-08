package organizationmaintenance

import (
	"context"
	"errors"
	"time"

	"github.com/DoMinhHHung/bridgeworks/service/organization-service/internal/organizationaudit"
	"github.com/DoMinhHHung/bridgeworks/service/organization-service/internal/platform/safeerr"
	"github.com/google/uuid"
)

var ErrProviderUnavailable = errors.New("organization membership provider unavailable")

type RemovalCandidate struct {
	MembershipID        uuid.UUID
	OrganizationID      uuid.UUID
	CreatedAt           time.Time
	ClerkOrganizationID string
	ClerkUserID         string
}

type Repository interface {
	PruneConsumedInvitationIntents(context.Context, time.Time, int32) (int64, error)
	ListRemovalIntentsForReconciliation(context.Context, time.Time, int32) ([]RemovalCandidate, error)
	BeginMaintenance(context.Context) (UnitOfWork, error)
}

type UnitOfWork interface {
	AcquireOrganizationLock(context.Context, uuid.UUID) error
	LockRemovalIntent(context.Context, uuid.UUID, uuid.UUID) (string, bool, error)
	MarkMembershipDeleted(context.Context, uuid.UUID, uuid.UUID) error
	DeleteRemovalIntent(context.Context, uuid.UUID, uuid.UUID) (bool, error)
	InsertAuditEvent(context.Context, organizationaudit.Event) error
	Commit(context.Context) error
	Rollback(context.Context) error
}

type MembershipPresenceProvider interface {
	MembershipExists(context.Context, string, string) (bool, error)
}

type Result struct {
	Examined   int
	Finalized  int
	Unresolved int
}

type Service struct {
	repository Repository
	provider   MembershipPresenceProvider
}

func New(repository Repository, provider MembershipPresenceProvider) *Service {
	return &Service{repository: repository, provider: provider}
}

func (s *Service) PruneConsumedInvitations(
	ctx context.Context,
	retention time.Duration,
	batchSize int32,
	now time.Time,
) (int64, error) {
	if s == nil || s.repository == nil {
		return 0, errors.New("organization maintenance repository is not initialized")
	}
	if retention <= 0 || batchSize <= 0 {
		return 0, errors.New("invalid invitation retention configuration")
	}
	count, err := s.repository.PruneConsumedInvitationIntents(ctx, now.UTC().Add(-retention), batchSize)
	if err != nil {
		return 0, safeerr.Wrap("prune consumed invitation intents", err)
	}
	return count, nil
}

func (s *Service) ReconcileRemovals(
	ctx context.Context,
	minimumAge time.Duration,
	batchSize int32,
	now time.Time,
) (Result, error) {
	if s == nil || s.repository == nil || s.provider == nil {
		return Result{}, errors.New("organization removal reconciliation is not initialized")
	}
	if minimumAge <= 0 || batchSize <= 0 {
		return Result{}, errors.New("invalid removal reconciliation configuration")
	}
	candidates, err := s.repository.ListRemovalIntentsForReconciliation(ctx, now.UTC().Add(-minimumAge), batchSize)
	if err != nil {
		return Result{}, safeerr.Wrap("list removal reconciliation candidates", err)
	}
	result := Result{}
	for _, candidate := range candidates {
		result.Examined++
		exists, err := s.provider.MembershipExists(ctx, candidate.ClerkOrganizationID, candidate.ClerkUserID)
		if err != nil {
			result.Unresolved++
			return result, ErrProviderUnavailable
		}
		if exists {
			result.Unresolved++
			continue
		}
		finalized, err := s.finalizeAbsentMembership(ctx, candidate)
		if err != nil {
			return result, err
		}
		if finalized {
			result.Finalized++
		}
	}
	return result, nil
}

func (s *Service) finalizeAbsentMembership(
	ctx context.Context,
	candidate RemovalCandidate,
) (finalized bool, err error) {
	uow, err := s.repository.BeginMaintenance(ctx)
	if err != nil {
		return false, safeerr.Wrap("begin removal reconciliation transaction", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = uow.Rollback(ctx)
		}
	}()
	if err := uow.AcquireOrganizationLock(ctx, candidate.OrganizationID); err != nil {
		return false, safeerr.Wrap("lock organization for removal reconciliation", err)
	}
	membershipStatus, found, err := uow.LockRemovalIntent(ctx, candidate.OrganizationID, candidate.MembershipID)
	if err != nil {
		return false, safeerr.Wrap("lock removal intent for reconciliation", err)
	}
	if !found {
		if err := uow.Commit(ctx); err != nil {
			return false, safeerr.Wrap("commit already reconciled removal", err)
		}
		committed = true
		return false, nil
	}
	if membershipStatus != "active" {
		deleted, err := uow.DeleteRemovalIntent(ctx, candidate.OrganizationID, candidate.MembershipID)
		if err != nil {
			return false, safeerr.Wrap("clear stale removal intent", err)
		}
		if !deleted {
			return false, errors.New("removal reconciliation lost locked stale intent")
		}
		if err := uow.Commit(ctx); err != nil {
			return false, safeerr.Wrap("commit stale removal intent cleanup", err)
		}
		committed = true
		return false, nil
	}
	if err := uow.MarkMembershipDeleted(ctx, candidate.OrganizationID, candidate.MembershipID); err != nil {
		return false, safeerr.Wrap("finalize absent membership projection", err)
	}
	deleted, err := uow.DeleteRemovalIntent(ctx, candidate.OrganizationID, candidate.MembershipID)
	if err != nil {
		return false, safeerr.Wrap("clear reconciled removal intent", err)
	}
	if !deleted {
		return false, errors.New("removal reconciliation lost locked intent")
	}
	subject := candidate.MembershipID
	event, err := organizationaudit.SystemEvent(
		candidate.OrganizationID,
		organizationaudit.EventMembershipRemovalCompleted,
		&subject,
		nil,
		nil,
	)
	if err != nil {
		return false, safeerr.Wrap("create removal reconciliation audit event", err)
	}
	if err := uow.InsertAuditEvent(ctx, event); err != nil {
		return false, safeerr.Wrap("append removal reconciliation audit event", err)
	}
	if err := uow.Commit(ctx); err != nil {
		return false, safeerr.Wrap("commit removal reconciliation", err)
	}
	committed = true
	return true, nil
}
