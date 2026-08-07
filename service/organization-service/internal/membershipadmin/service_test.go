package membershipadmin

import (
	"context"
	"errors"
	"testing"

	"github.com/DoMinhHHung/bridgeworks/service/organization-service/internal/authorization"
	"github.com/google/uuid"
)

var (
	adminTestIdentityID     = uuid.MustParse("018f0c76-8f6c-7cc4-8000-000000000081")
	adminTestOrganizationID = uuid.MustParse("018f0c76-8f6c-7cc4-8000-000000000082")
	adminTestActorID        = uuid.MustParse("018f0c76-8f6c-7cc4-8000-000000000083")
	adminTestTargetID       = uuid.MustParse("018f0c76-8f6c-7cc4-8000-000000000084")
	adminTestInvitationID   = uuid.MustParse("018f0c76-8f6c-7cc4-8000-000000000085")
	adminOtherOrgID         = uuid.MustParse("018f0c76-8f6c-7cc4-8000-000000000086")
)

type fakeAdminFactory struct{ uow *fakeAdminUOW }

func (f fakeAdminFactory) BeginMembershipAdministration(context.Context) (UnitOfWork, error) {
	return f.uow, nil
}

type fakeAdminGenerator struct{ id uuid.UUID }

func (g fakeAdminGenerator) New() (uuid.UUID, error) { return g.id, nil }

type fakeAdminProvider struct {
	inviteCalls int
	deleteCalls int
	inviteErr   error
	deleteErr   error
	lastInvite  InvitationProviderRequest
	lastDelete  MembershipDeleteProviderRequest
	sequence    *[]string
}

func (p *fakeAdminProvider) CreateInvitation(_ context.Context, request InvitationProviderRequest) error {
	p.inviteCalls++
	p.lastInvite = request
	if p.sequence != nil {
		*p.sequence = append(*p.sequence, "provider_invite")
	}
	return p.inviteErr
}
func (p *fakeAdminProvider) DeleteMembership(_ context.Context, request MembershipDeleteProviderRequest) error {
	p.deleteCalls++
	p.lastDelete = request
	if p.sequence != nil {
		*p.sequence = append(*p.sequence, "provider_delete")
	}
	return p.deleteErr
}

type fakeAdminUOW struct {
	organization Organization
	memberships  map[uuid.UUID]Membership
	owners       int64
	invitation   *InvitationIntent
	roleUpdates  []struct {
		id   uuid.UUID
		role string
	}
	removalPending map[uuid.UUID]bool
	sequence       []string
	commits        int
	rollbacks      int
}

func newFakeAdminUOW(actorRole, targetRole string) *fakeAdminUOW {
	return &fakeAdminUOW{
		organization: Organization{ID: adminTestOrganizationID, ClerkOrganizationID: "org-admin-test"},
		memberships: map[uuid.UUID]Membership{
			adminTestActorID: {
				ID: adminTestActorID, OrganizationID: adminTestOrganizationID,
				ClerkUserID: "user-actor", ApplicationRole: actorRole, Status: "active",
			},
			adminTestTargetID: {
				ID: adminTestTargetID, OrganizationID: adminTestOrganizationID,
				ClerkUserID: "user-target", ApplicationRole: targetRole, Status: "active",
			},
		},
		owners:         1,
		removalPending: make(map[uuid.UUID]bool),
	}
}

func (u *fakeAdminUOW) AcquireOrganizationLock(_ context.Context, organizationID uuid.UUID) error {
	u.sequence = append(u.sequence, "lock_org")
	if organizationID != adminTestOrganizationID {
		return errors.New("unexpected organization")
	}
	return nil
}
func (u *fakeAdminUOW) GetOrganization(_ context.Context, organizationID uuid.UUID) (Organization, bool, error) {
	if organizationID != u.organization.ID {
		return Organization{}, false, nil
	}
	return u.organization, true, nil
}
func (u *fakeAdminUOW) LockMembership(_ context.Context, organizationID, membershipID uuid.UUID) (Membership, bool, error) {
	u.sequence = append(u.sequence, "lock_member")
	membership, ok := u.memberships[membershipID]
	if !ok || membership.OrganizationID != organizationID {
		return Membership{}, false, nil
	}
	membership.RemovalPending = u.removalPending[membershipID]
	return membership, true, nil
}
func (u *fakeAdminUOW) CountEffectiveOwners(context.Context, uuid.UUID) (int64, error) {
	u.sequence = append(u.sequence, "count_owners")
	return u.owners, nil
}
func (u *fakeAdminUOW) InsertInvitationIntent(_ context.Context, intent InvitationIntent) error {
	u.sequence = append(u.sequence, "insert_invitation")
	u.invitation = &intent
	return nil
}
func (u *fakeAdminUOW) DeleteInvitationIntent(context.Context, uuid.UUID, uuid.UUID) error {
	u.sequence = append(u.sequence, "delete_invitation")
	u.invitation = nil
	return nil
}
func (u *fakeAdminUOW) UpdateMembershipRole(_ context.Context, organizationID, membershipID uuid.UUID, role string) error {
	membership, ok := u.memberships[membershipID]
	if !ok || membership.OrganizationID != organizationID {
		return errors.New("membership not scoped")
	}
	u.sequence = append(u.sequence, "update_role")
	u.roleUpdates = append(u.roleUpdates, struct {
		id   uuid.UUID
		role string
	}{id: membershipID, role: role})
	membership.ApplicationRole = role
	u.memberships[membershipID] = membership
	return nil
}
func (u *fakeAdminUOW) InsertRemovalIntent(_ context.Context, membershipID, organizationID, _ uuid.UUID) (bool, error) {
	membership, ok := u.memberships[membershipID]
	if !ok || membership.OrganizationID != organizationID {
		return false, errors.New("membership not scoped")
	}
	u.sequence = append(u.sequence, "insert_removal")
	if u.removalPending[membershipID] {
		return false, nil
	}
	u.removalPending[membershipID] = true
	return true, nil
}
func (u *fakeAdminUOW) DeleteRemovalIntent(_ context.Context, organizationID, membershipID uuid.UUID) error {
	membership, ok := u.memberships[membershipID]
	if !ok || membership.OrganizationID != organizationID {
		return errors.New("membership not scoped")
	}
	u.sequence = append(u.sequence, "delete_removal")
	delete(u.removalPending, membershipID)
	return nil
}
func (u *fakeAdminUOW) Commit(context.Context) error {
	u.sequence = append(u.sequence, "commit")
	u.commits++
	return nil
}
func (u *fakeAdminUOW) Rollback(context.Context) error {
	u.sequence = append(u.sequence, "rollback")
	u.rollbacks++
	return nil
}

func adminActor() authorization.ActorContext {
	return authorization.NewActorContext(
		adminTestIdentityID,
		adminTestOrganizationID,
		adminTestActorID,
		RoleOwner,
		nil,
	)
}

func TestInvitationPolicyUsesCurrentLocalRole(t *testing.T) {
	tests := []struct {
		name          string
		actorRole     string
		requestedRole string
		wantErr       error
	}{
		{name: "owner invites owner", actorRole: RoleOwner, requestedRole: RoleOwner},
		{name: "owner invites recruiter", actorRole: RoleOwner, requestedRole: RoleRecruiter},
		{name: "admin invites viewer", actorRole: RoleAdmin, requestedRole: RoleViewer},
		{name: "admin cannot invite owner", actorRole: RoleAdmin, requestedRole: RoleOwner, wantErr: ErrOwnerMutationForbidden},
		{name: "viewer cannot invite", actorRole: RoleViewer, requestedRole: RoleViewer, wantErr: ErrPermissionDenied},
		{name: "recruiter cannot invite", actorRole: RoleRecruiter, requestedRole: RoleViewer, wantErr: ErrPermissionDenied},
		{name: "delivery manager cannot invite", actorRole: RoleDeliveryManager, requestedRole: RoleViewer, wantErr: ErrPermissionDenied},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			uow := newFakeAdminUOW(testCase.actorRole, RoleViewer)
			provider := &fakeAdminProvider{sequence: &uow.sequence}
			service := New(fakeAdminFactory{uow: uow}, provider, fakeAdminGenerator{id: adminTestInvitationID})
			_, err := service.Invite(context.Background(), adminActor(), "user-actor", " Person@Company.Example ", testCase.requestedRole)
			if testCase.wantErr != nil {
				if !errors.Is(err, testCase.wantErr) {
					t.Fatalf("Invite() error = %v, want %v", err, testCase.wantErr)
				}
				if provider.inviteCalls != 0 {
					t.Fatalf("provider calls = %d", provider.inviteCalls)
				}
				return
			}
			if err != nil {
				t.Fatalf("Invite() error = %v", err)
			}
			if provider.inviteCalls != 1 || provider.lastInvite.EmailAddress != "Person@company.example" {
				t.Fatalf("provider invitation = %#v", provider.lastInvite)
			}
			if uow.invitation == nil || uow.invitation.ApplicationRole != testCase.requestedRole {
				t.Fatalf("local invitation intent = %#v", uow.invitation)
			}
			if len(uow.sequence) < 2 || uow.sequence[len(uow.sequence)-2] != "commit" || uow.sequence[len(uow.sequence)-1] != "provider_invite" {
				t.Fatalf("provider called inside/before transaction commit: %#v", uow.sequence)
			}
		})
	}
}

func TestAdminRoleMutationCannotTouchOwnerOrCreateOwner(t *testing.T) {
	t.Run("viewer to recruiter", func(t *testing.T) {
		uow := newFakeAdminUOW(RoleAdmin, RoleViewer)
		service := New(fakeAdminFactory{uow: uow}, &fakeAdminProvider{}, fakeAdminGenerator{})
		if err := service.SetRole(context.Background(), adminActor(), adminTestTargetID, RoleRecruiter); err != nil {
			t.Fatalf("SetRole() error = %v", err)
		}
		if got := uow.memberships[adminTestTargetID].ApplicationRole; got != RoleRecruiter {
			t.Fatalf("target role = %q", got)
		}
	})
	for _, testCase := range []struct {
		name       string
		targetRole string
		newRole    string
		targetID   uuid.UUID
	}{
		{name: "promote target owner", targetRole: RoleViewer, newRole: RoleOwner, targetID: adminTestTargetID},
		{name: "mutate existing owner", targetRole: RoleOwner, newRole: RoleAdmin, targetID: adminTestTargetID},
		{name: "promote self owner", targetRole: RoleViewer, newRole: RoleOwner, targetID: adminTestActorID},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			uow := newFakeAdminUOW(RoleAdmin, testCase.targetRole)
			service := New(fakeAdminFactory{uow: uow}, &fakeAdminProvider{}, fakeAdminGenerator{})
			if err := service.SetRole(context.Background(), adminActor(), testCase.targetID, testCase.newRole); !errors.Is(err, ErrOwnerMutationForbidden) {
				t.Fatalf("SetRole() error = %v", err)
			}
		})
	}
}

func TestOwnerCanCreateAdditionalOwnerAndLastOwnerCannotDemote(t *testing.T) {
	t.Run("promote additional owner", func(t *testing.T) {
		uow := newFakeAdminUOW(RoleOwner, RoleViewer)
		service := New(fakeAdminFactory{uow: uow}, &fakeAdminProvider{}, fakeAdminGenerator{})
		if err := service.SetRole(context.Background(), adminActor(), adminTestTargetID, RoleOwner); err != nil {
			t.Fatalf("SetRole() error = %v", err)
		}
		if uow.memberships[adminTestTargetID].ApplicationRole != RoleOwner {
			t.Fatal("target was not promoted to owner")
		}
	})
	for _, owners := range []int64{0, 1} {
		t.Run("last owner", func(t *testing.T) {
			uow := newFakeAdminUOW(RoleOwner, RoleViewer)
			uow.owners = owners
			service := New(fakeAdminFactory{uow: uow}, &fakeAdminProvider{}, fakeAdminGenerator{})
			if err := service.SetRole(context.Background(), adminActor(), adminTestActorID, RoleAdmin); !errors.Is(err, ErrLastOwner) {
				t.Fatalf("SetRole() error = %v", err)
			}
		})
	}
}

func TestLastOwnerCannotLeaveOrBeRemovedButTwoOwnersCan(t *testing.T) {
	t.Run("last owner leave", func(t *testing.T) {
		uow := newFakeAdminUOW(RoleOwner, RoleViewer)
		uow.owners = 1
		provider := &fakeAdminProvider{}
		service := New(fakeAdminFactory{uow: uow}, provider, fakeAdminGenerator{})
		if err := service.Leave(context.Background(), adminActor()); !errors.Is(err, ErrLastOwner) {
			t.Fatalf("Leave() error = %v", err)
		}
		if provider.deleteCalls != 0 {
			t.Fatal("provider delete called for last owner")
		}
	})
	t.Run("last owner removed", func(t *testing.T) {
		uow := newFakeAdminUOW(RoleOwner, RoleOwner)
		uow.owners = 1
		provider := &fakeAdminProvider{}
		service := New(fakeAdminFactory{uow: uow}, provider, fakeAdminGenerator{})
		if err := service.Remove(context.Background(), adminActor(), adminTestTargetID); !errors.Is(err, ErrLastOwner) {
			t.Fatalf("Remove() error = %v", err)
		}
		if provider.deleteCalls != 0 {
			t.Fatal("provider delete called for last owner")
		}
	})
	t.Run("two owners can leave", func(t *testing.T) {
		uow := newFakeAdminUOW(RoleOwner, RoleOwner)
		uow.owners = 2
		provider := &fakeAdminProvider{sequence: &uow.sequence}
		service := New(fakeAdminFactory{uow: uow}, provider, fakeAdminGenerator{})
		if err := service.Leave(context.Background(), adminActor()); err != nil {
			t.Fatalf("Leave() error = %v", err)
		}
		if provider.deleteCalls != 1 || !uow.removalPending[adminTestActorID] {
			t.Fatalf("delete calls=%d pending=%v", provider.deleteCalls, uow.removalPending)
		}
		if len(uow.sequence) < 2 || uow.sequence[len(uow.sequence)-2] != "commit" || uow.sequence[len(uow.sequence)-1] != "provider_delete" {
			t.Fatalf("provider called before transaction commit: %#v", uow.sequence)
		}
	})
}

func TestOwnershipTransferIsOneLockedTransaction(t *testing.T) {
	uow := newFakeAdminUOW(RoleOwner, RoleAdmin)
	uow.owners = 1
	service := New(fakeAdminFactory{uow: uow}, &fakeAdminProvider{}, fakeAdminGenerator{})
	if err := service.TransferOwnership(context.Background(), adminActor(), adminTestTargetID); err != nil {
		t.Fatalf("TransferOwnership() error = %v", err)
	}
	if len(uow.roleUpdates) != 2 || uow.roleUpdates[0].id != adminTestTargetID || uow.roleUpdates[0].role != RoleOwner || uow.roleUpdates[1].id != adminTestActorID || uow.roleUpdates[1].role != RoleAdmin {
		t.Fatalf("role updates = %#v", uow.roleUpdates)
	}
	if uow.commits != 1 || uow.rollbacks != 0 {
		t.Fatalf("commit/rollback = %d/%d", uow.commits, uow.rollbacks)
	}
}

func TestCrossTenantMembershipTargetIsNotFound(t *testing.T) {
	uow := newFakeAdminUOW(RoleOwner, RoleViewer)
	foreignID := uuid.MustParse("018f0c76-8f6c-7cc4-8000-000000000087")
	uow.memberships[foreignID] = Membership{
		ID: foreignID, OrganizationID: adminOtherOrgID, ClerkUserID: "foreign",
		ApplicationRole: RoleViewer, Status: "active",
	}
	service := New(fakeAdminFactory{uow: uow}, &fakeAdminProvider{}, fakeAdminGenerator{})
	if err := service.SetRole(context.Background(), adminActor(), foreignID, RoleRecruiter); !errors.Is(err, ErrMembershipNotFound) {
		t.Fatalf("SetRole() error = %v", err)
	}
	if err := service.TransferOwnership(context.Background(), adminActor(), foreignID); !errors.Is(err, ErrMembershipNotFound) {
		t.Fatalf("TransferOwnership() error = %v", err)
	}
	if err := service.Remove(context.Background(), adminActor(), foreignID); !errors.Is(err, ErrMembershipNotFound) {
		t.Fatalf("Remove() error = %v", err)
	}
}

func TestPendingRemovalRetriesProviderAndTimeoutKeepsAuthorizationFence(t *testing.T) {
	uow := newFakeAdminUOW(RoleOwner, RoleViewer)
	uow.removalPending[adminTestTargetID] = true
	provider := &fakeAdminProvider{deleteErr: ProviderErrUnavailable}
	service := New(fakeAdminFactory{uow: uow}, provider, fakeAdminGenerator{})
	if err := service.Remove(context.Background(), adminActor(), adminTestTargetID); !errors.Is(err, ErrProviderUnavailable) {
		t.Fatalf("Remove() error = %v", err)
	}
	if provider.deleteCalls != 1 || !uow.removalPending[adminTestTargetID] {
		t.Fatalf("delete calls=%d pending=%v", provider.deleteCalls, uow.removalPending)
	}
	provider.deleteErr = nil
	if err := service.Remove(context.Background(), adminActor(), adminTestTargetID); err != nil {
		t.Fatalf("retry Remove() error = %v", err)
	}
	if provider.deleteCalls != 2 {
		t.Fatalf("provider delete calls = %d", provider.deleteCalls)
	}
}

func TestInvitationProviderFailureSemantics(t *testing.T) {
	for _, testCase := range []struct {
		name          string
		providerErr   error
		wantErr       error
		wantIntent    bool
	}{
		{name: "timeout preserves intent", providerErr: ProviderErrUnavailable, wantErr: ErrProviderUnavailable, wantIntent: true},
		{name: "duplicate cleans intent", providerErr: ProviderErrConflict, wantErr: ErrInvitationConflict},
		{name: "rejected cleans intent", providerErr: ProviderErrRejected, wantErr: ErrInvitationRejected},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			uow := newFakeAdminUOW(RoleOwner, RoleViewer)
			provider := &fakeAdminProvider{inviteErr: testCase.providerErr}
			service := New(fakeAdminFactory{uow: uow}, provider, fakeAdminGenerator{id: adminTestInvitationID})
			_, err := service.Invite(context.Background(), adminActor(), "user-actor", "person@company.example", RoleViewer)
			if !errors.Is(err, testCase.wantErr) {
				t.Fatalf("Invite() error = %v, want %v", err, testCase.wantErr)
			}
			if (uow.invitation != nil) != testCase.wantIntent {
				t.Fatalf("intent present = %v, want %v", uow.invitation != nil, testCase.wantIntent)
			}
		})
	}
}
