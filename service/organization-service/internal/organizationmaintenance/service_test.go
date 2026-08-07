package organizationmaintenance

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DoMinhHHung/bridgeworks/service/organization-service/internal/organizationaudit"
	"github.com/google/uuid"
)

var (
	maintenanceOrgID = uuid.MustParse("018f0c76-8f6c-7cc4-8000-0000000000c1")
	maintenanceMemID = uuid.MustParse("018f0c76-8f6c-7cc4-8000-0000000000c2")
)

type fakeMaintenanceRepository struct {
	pruned     int64
	candidates []RemovalCandidate
	uow        *fakeMaintenanceUOW
}

func (f *fakeMaintenanceRepository) PruneConsumedInvitationIntents(context.Context, time.Time, int32) (int64, error) {
	return f.pruned, nil
}
func (f *fakeMaintenanceRepository) ListRemovalIntentsForReconciliation(context.Context, time.Time, int32) ([]RemovalCandidate, error) {
	return f.candidates, nil
}
func (f *fakeMaintenanceRepository) BeginMaintenance(context.Context) (UnitOfWork, error) {
	return f.uow, nil
}

type fakePresenceProvider struct {
	exists bool
	err    error
	calls  int
}

func (f *fakePresenceProvider) MembershipExists(context.Context, string, string) (bool, error) {
	f.calls++
	return f.exists, f.err
}

type fakeMaintenanceUOW struct {
	found     bool
	marked    int
	deleted   bool
	audits    []organizationaudit.Event
	commits   int
	rollbacks int
	sequence  []string
}

func (u *fakeMaintenanceUOW) AcquireOrganizationLock(context.Context, uuid.UUID) error {
	u.sequence = append(u.sequence, "lock")
	return nil
}
func (u *fakeMaintenanceUOW) LockRemovalIntent(context.Context, uuid.UUID, uuid.UUID) (bool, error) {
	u.sequence = append(u.sequence, "lock_intent")
	return u.found, nil
}
func (u *fakeMaintenanceUOW) MarkMembershipDeleted(context.Context, uuid.UUID, uuid.UUID) error {
	u.sequence = append(u.sequence, "mark_deleted")
	u.marked++
	return nil
}
func (u *fakeMaintenanceUOW) DeleteRemovalIntent(context.Context, uuid.UUID, uuid.UUID) (bool, error) {
	u.sequence = append(u.sequence, "delete_intent")
	return u.deleted, nil
}
func (u *fakeMaintenanceUOW) InsertAuditEvent(_ context.Context, event organizationaudit.Event) error {
	u.sequence = append(u.sequence, "audit")
	u.audits = append(u.audits, event)
	return nil
}
func (u *fakeMaintenanceUOW) Commit(context.Context) error {
	u.sequence = append(u.sequence, "commit")
	u.commits++
	return nil
}
func (u *fakeMaintenanceUOW) Rollback(context.Context) error {
	u.sequence = append(u.sequence, "rollback")
	u.rollbacks++
	return nil
}

func removalCandidate() RemovalCandidate {
	return RemovalCandidate{
		MembershipID: maintenanceMemID, OrganizationID: maintenanceOrgID,
		CreatedAt: time.Now().Add(-time.Hour), ClerkOrganizationID: "org-maintenance", ClerkUserID: "user-maintenance",
	}
}

func TestRemovalProviderPresentLeavesIntentPending(t *testing.T) {
	repo := &fakeMaintenanceRepository{candidates: []RemovalCandidate{removalCandidate()}, uow: &fakeMaintenanceUOW{found: true, deleted: true}}
	provider := &fakePresenceProvider{exists: true}
	result, err := New(repo, provider).ReconcileRemovals(context.Background(), time.Minute, 10, time.Now())
	if err != nil {
		t.Fatalf("ReconcileRemovals() error = %v", err)
	}
	if result.Examined != 1 || result.Finalized != 0 || result.Unresolved != 1 {
		t.Fatalf("result = %#v", result)
	}
	if repo.uow.marked != 0 || repo.uow.commits != 0 {
		t.Fatalf("provider-present mutation = %#v", repo.uow)
	}
}

func TestRemovalProviderOutageFailsClosed(t *testing.T) {
	repo := &fakeMaintenanceRepository{candidates: []RemovalCandidate{removalCandidate()}, uow: &fakeMaintenanceUOW{found: true, deleted: true}}
	provider := &fakePresenceProvider{err: errors.New("timeout")}
	result, err := New(repo, provider).ReconcileRemovals(context.Background(), time.Minute, 10, time.Now())
	if !errors.Is(err, ErrProviderUnavailable) {
		t.Fatalf("ReconcileRemovals() error = %v", err)
	}
	if result.Unresolved != 1 || repo.uow.marked != 0 || repo.uow.commits != 0 {
		t.Fatalf("outage state result=%#v uow=%#v", result, repo.uow)
	}
}

func TestAuthoritativeAbsenceFinalizesAndAuditsOnce(t *testing.T) {
	uow := &fakeMaintenanceUOW{found: true, deleted: true}
	repo := &fakeMaintenanceRepository{candidates: []RemovalCandidate{removalCandidate()}, uow: uow}
	provider := &fakePresenceProvider{exists: false}
	result, err := New(repo, provider).ReconcileRemovals(context.Background(), time.Minute, 10, time.Now())
	if err != nil {
		t.Fatalf("ReconcileRemovals() error = %v", err)
	}
	if result.Finalized != 1 || uow.marked != 1 || uow.commits != 1 || uow.rollbacks != 0 || len(uow.audits) != 1 {
		t.Fatalf("result=%#v uow=%#v", result, uow)
	}
	if uow.audits[0].EventType != organizationaudit.EventMembershipRemovalCompleted || uow.audits[0].ActorKind != organizationaudit.ActorSystem {
		t.Fatalf("audit = %#v", uow.audits[0])
	}
	want := []string{"lock", "lock_intent", "mark_deleted", "delete_intent", "audit", "commit"}
	if len(uow.sequence) != len(want) {
		t.Fatalf("sequence = %v", uow.sequence)
	}
	for i := range want {
		if uow.sequence[i] != want[i] {
			t.Fatalf("sequence = %v, want %v", uow.sequence, want)
		}
	}
}

func TestAlreadyReconciledRemovalIsIdempotent(t *testing.T) {
	uow := &fakeMaintenanceUOW{found: false}
	repo := &fakeMaintenanceRepository{candidates: []RemovalCandidate{removalCandidate()}, uow: uow}
	result, err := New(repo, &fakePresenceProvider{exists: false}).ReconcileRemovals(context.Background(), time.Minute, 10, time.Now())
	if err != nil {
		t.Fatalf("ReconcileRemovals() error = %v", err)
	}
	if result.Finalized != 0 || uow.marked != 0 || len(uow.audits) != 0 || uow.commits != 1 {
		t.Fatalf("result=%#v uow=%#v", result, uow)
	}
}

func TestPruneUsesConfiguredRetentionAndBoundedBatch(t *testing.T) {
	repo := &fakeMaintenanceRepository{pruned: 7}
	count, err := New(repo, nil).PruneConsumedInvitations(context.Background(), 30*24*time.Hour, 100, time.Now())
	if err != nil || count != 7 {
		t.Fatalf("PruneConsumedInvitations() = %d, %v", count, err)
	}
}
