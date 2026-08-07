package organizationsync

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

func (u *fakeUnitOfWork) SetOrganizationCreator(_ context.Context, _ string, clerkUserID string) error {
	u.organization.ClerkCreatedByUserID = &clerkUserID
	return nil
}

func (u *fakeUnitOfWork) MarkOrganizationOwnerBootstrapped(context.Context, uuid.UUID) error {
	u.organization.OwnerBootstrapped = true
	return nil
}

func (u *fakeUnitOfWork) HasMembership(context.Context, uuid.UUID, string) (bool, error) {
	return u.membershipFound || u.activeFound, nil
}

func (u *fakeUnitOfWork) UpdateMembershipApplicationRole(_ context.Context, _ uuid.UUID, role string) error {
	u.activeMembership.ApplicationRole = role
	return nil
}

func TestOwnerBootstrapOrganizationBeforeMembership(t *testing.T) {
	creator := "user-creator"
	uow := newFakeUnitOfWork()
	service := New(fakeFactory{uow: uow}, &fakeGenerator{ids: []uuid.UUID{testOrganizationID}})
	event := organizationEvent(EventOrganizationCreated)
	event.Organization.CreatedBy = &creator

	if err := service.Process(context.Background(), event); err != nil {
		t.Fatalf("organization Process() error = %v", err)
	}
	if len(uow.insertedOrganizations) != 1 || uow.insertedOrganizations[0].ClerkCreatedByUserID == nil {
		t.Fatalf("creator projection was not persisted: %#v", uow.insertedOrganizations)
	}

	uow.organizationFound = true
	uow.organization = uow.insertedOrganizations[0]
	uow.activeFound = true
	uow.activeMembership = Membership{
		ID: testMembershipID, OrganizationID: testOrganizationID,
		ClerkMembershipID: "mem-creator", ClerkUserID: creator,
		ApplicationRole: RoleAdmin, Status: "active",
	}
	uow.membershipFound = false
	generator := &fakeGenerator{ids: []uuid.UUID{testMembershipID}}
	service = New(fakeFactory{uow: uow}, generator)
	membership := membershipEvent(EventMembershipCreated, stringPointer(ClerkRoleAdmin))
	membership.Membership.ClerkUserID = creator
	membership.Membership.ClerkMembershipID = "mem-creator"
	membership.AggregateID = "mem-creator"

	if err := service.Process(context.Background(), membership); err != nil {
		t.Fatalf("membership Process() error = %v", err)
	}
	if uow.activeMembership.ApplicationRole != RoleOwner {
		t.Fatalf("creator role = %q, want owner", uow.activeMembership.ApplicationRole)
	}
	if !uow.organization.OwnerBootstrapped {
		t.Fatal("owner bootstrap marker was not completed")
	}
}

func TestOwnerBootstrapMembershipBeforeOrganization(t *testing.T) {
	creator := "user-1"
	uow := newFakeUnitOfWork()
	uow.organizationFound = true
	uow.organization = Organization{ID: testOrganizationID, Status: "pending"}
	uow.activeFound = true
	uow.activeMembership = Membership{
		ID: testMembershipID, OrganizationID: testOrganizationID,
		ClerkMembershipID: "mem-1", ClerkUserID: creator,
		ApplicationRole: RoleAdmin, Status: "active",
	}
	service := New(fakeFactory{uow: uow}, &fakeGenerator{})
	event := organizationEvent(EventOrganizationCreated)
	event.Organization.CreatedBy = &creator

	if err := service.Process(context.Background(), event); err != nil {
		t.Fatalf("Process() error = %v", err)
	}
	if uow.activeMembership.ApplicationRole != RoleOwner {
		t.Fatalf("creator role = %q, want owner", uow.activeMembership.ApplicationRole)
	}
	if !uow.organization.OwnerBootstrapped {
		t.Fatal("owner bootstrap marker was not completed")
	}
}

func TestOwnerBootstrapDoesNotPromoteArbitraryAdminWithoutCreatorSignal(t *testing.T) {
	uow := newFakeUnitOfWork()
	uow.organizationFound = true
	uow.organization = Organization{ID: testOrganizationID, Status: "active"}
	uow.activeFound = true
	uow.activeMembership = Membership{
		ID: testMembershipID, OrganizationID: testOrganizationID,
		ClerkMembershipID: "mem-1", ClerkUserID: "user-1",
		ApplicationRole: RoleAdmin, Status: "active",
	}
	service := New(fakeFactory{uow: uow}, &fakeGenerator{})

	if err := service.Process(context.Background(), organizationEvent(EventOrganizationUpdated)); err != nil {
		t.Fatalf("Process() error = %v", err)
	}
	if uow.activeMembership.ApplicationRole != RoleAdmin {
		t.Fatalf("arbitrary admin was promoted: %q", uow.activeMembership.ApplicationRole)
	}
}

func TestOwnerBootstrapRejectsCreatorMismatch(t *testing.T) {
	storedCreator := "user-original"
	incomingCreator := "user-different"
	uow := newFakeUnitOfWork()
	uow.organizationFound = true
	uow.organization = Organization{
		ID: testOrganizationID, Status: "active", ClerkCreatedByUserID: &storedCreator,
	}
	service := New(fakeFactory{uow: uow}, &fakeGenerator{})
	event := organizationEvent(EventOrganizationUpdated)
	event.Organization.CreatedBy = &incomingCreator

	if err := service.Process(context.Background(), event); err == nil {
		t.Fatal("Process() error = nil, want creator mismatch rejection")
	}
	if uow.commitCalls != 0 || uow.rollbackCalls != 1 {
		t.Fatalf("commit/rollback = %d/%d", uow.commitCalls, uow.rollbackCalls)
	}
}

func stringPointer(value string) *string { return &value }
