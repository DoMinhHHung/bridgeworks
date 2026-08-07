package organizationsync

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

type invitationIntentUnitOfWork struct {
	*fakeUnitOfWork
	intent       InvitationIntent
	pending      bool
	lookupOrgID  uuid.UUID
	consumeCalls int
}

func (u *invitationIntentUnitOfWork) GetPendingInvitationIntent(
	_ context.Context,
	organizationID uuid.UUID,
	intentID uuid.UUID,
) (InvitationIntent, bool, error) {
	u.lookupOrgID = organizationID
	if !u.pending || u.intent.OrganizationID != organizationID || u.intent.ID != intentID {
		return InvitationIntent{}, false, nil
	}
	return u.intent, true, nil
}

func (u *invitationIntentUnitOfWork) ConsumeInvitationIntent(
	_ context.Context,
	organizationID uuid.UUID,
	intentID uuid.UUID,
	_ uuid.UUID,
) error {
	if organizationID == u.intent.OrganizationID && intentID == u.intent.ID {
		u.consumeCalls++
	}
	return nil
}

func TestMembershipInvitationIntentAppliesOnlyWithinSameOrganization(t *testing.T) {
	intentID := uuid.MustParse("018f0c76-8f6c-7cc4-8000-000000000092")
	foreignOrganizationID := uuid.MustParse("018f0c76-8f6c-7cc4-8000-000000000093")
	base := newFakeUnitOfWork()
	base.organizationFound = true
	base.organization = Organization{ID: testOrganizationID, Status: "active"}
	uow := &invitationIntentUnitOfWork{
		fakeUnitOfWork: base,
		intent: InvitationIntent{
			ID:              intentID,
			OrganizationID:  foreignOrganizationID,
			ApplicationRole: RoleOwner,
		},
		pending: true,
	}
	service := New(unitOfWorkTestFactory{uow: uow}, &fakeGenerator{ids: []uuid.UUID{testMembershipID}})
	event := membershipEvent(EventMembershipCreated, nil)
	event.Membership.InvitationIntent = &intentID

	if err := service.Process(context.Background(), event); err != nil {
		t.Fatalf("Process() error = %v", err)
	}
	if uow.lookupOrgID != testOrganizationID {
		t.Fatalf("intent lookup organization = %s, want %s", uow.lookupOrgID, testOrganizationID)
	}
	if len(uow.insertedMemberships) != 1 || uow.insertedMemberships[0].ApplicationRole != RoleViewer {
		t.Fatalf("cross-organization intent granted role: %#v", uow.insertedMemberships)
	}
	if uow.consumeCalls != 0 {
		t.Fatalf("cross-organization intent consume calls = %d", uow.consumeCalls)
	}
}

func TestConsumedOrUnknownInvitationIntentFallsBackToCompatibilityRole(t *testing.T) {
	intentID := uuid.MustParse("018f0c76-8f6c-7cc4-8000-000000000094")
	adminRole := ClerkRoleAdmin
	base := newFakeUnitOfWork()
	base.organizationFound = true
	base.organization = Organization{ID: testOrganizationID, Status: "active"}
	uow := &invitationIntentUnitOfWork{
		fakeUnitOfWork: base,
		intent: InvitationIntent{
			ID:              intentID,
			OrganizationID:  testOrganizationID,
			ApplicationRole: RoleOwner,
		},
		pending: false,
	}
	service := New(unitOfWorkTestFactory{uow: uow}, &fakeGenerator{ids: []uuid.UUID{testMembershipID}})
	event := membershipEvent(EventMembershipCreated, &adminRole)
	event.Membership.InvitationIntent = &intentID

	if err := service.Process(context.Background(), event); err != nil {
		t.Fatalf("Process() error = %v", err)
	}
	if len(uow.insertedMemberships) != 1 || uow.insertedMemberships[0].ApplicationRole != RoleAdmin {
		t.Fatalf("consumed/unknown intent changed compatibility role: %#v", uow.insertedMemberships)
	}
	if uow.consumeCalls != 0 {
		t.Fatalf("consumed/unknown intent consume calls = %d", uow.consumeCalls)
	}
}

func TestPendingSameOrganizationInvitationIntentAssignsLocalRoleAndConsumes(t *testing.T) {
	intentID := uuid.MustParse("018f0c76-8f6c-7cc4-8000-000000000095")
	base := newFakeUnitOfWork()
	base.organizationFound = true
	base.organization = Organization{ID: testOrganizationID, Status: "active"}
	uow := &invitationIntentUnitOfWork{
		fakeUnitOfWork: base,
		intent: InvitationIntent{
			ID:              intentID,
			OrganizationID:  testOrganizationID,
			ApplicationRole: "recruiter",
		},
		pending: true,
	}
	service := New(unitOfWorkTestFactory{uow: uow}, &fakeGenerator{ids: []uuid.UUID{testMembershipID}})
	event := membershipEvent(EventMembershipCreated, nil)
	event.Membership.InvitationIntent = &intentID

	if err := service.Process(context.Background(), event); err != nil {
		t.Fatalf("Process() error = %v", err)
	}
	if len(uow.insertedMemberships) != 1 || uow.insertedMemberships[0].ApplicationRole != "recruiter" {
		t.Fatalf("valid invitation intent role = %#v", uow.insertedMemberships)
	}
	if uow.consumeCalls != 1 {
		t.Fatalf("valid invitation intent consume calls = %d, want 1", uow.consumeCalls)
	}
}
