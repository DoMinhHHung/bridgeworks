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

func (u *fakeUnitOfWork) DisableOrganizationOwnerBootstrapEligibility(context.Context, uuid.UUID) error {
	u.organization.OwnerBootstrapEligible = false
	return nil
}

func (u *fakeUnitOfWork) MarkOrganizationOwnerBootstrapped(context.Context, uuid.UUID) error {
	u.organization.OwnerBootstrapped = true
	return nil
}

func (u *fakeUnitOfWork) HasDeletedMembership(_ context.Context, organizationID uuid.UUID, clerkUserID string) (bool, error) {
	if u.membershipFound &&
		u.membership.OrganizationID == organizationID &&
		u.membership.ClerkUserID == clerkUserID &&
		(u.membership.Status == "deleted" || u.markMembershipDeletedCalls > 0) {
		return true, nil
	}
	for _, membership := range u.insertedMemberships {
		if membership.OrganizationID == organizationID &&
			membership.ClerkUserID == clerkUserID &&
			membership.Status == "deleted" {
			return true, nil
		}
	}
	return false, nil
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
	if !uow.insertedOrganizations[0].OwnerBootstrapEligible {
		t.Fatal("new organization was not owner-bootstrap eligible")
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
	uow.organization = Organization{
		ID: testOrganizationID, Status: "pending", OwnerBootstrapEligible: true,
	}
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

func TestOwnerBootstrapCreatorWithoutMembershipHistoryRemainsEligible(t *testing.T) {
	creator := "user-creator"
	uow := newFakeUnitOfWork()
	uow.organizationFound = true
	uow.organization = Organization{
		ID:                     testOrganizationID,
		Status:                 "active",
		ClerkCreatedByUserID:   &creator,
		OwnerBootstrapEligible: true,
	}
	service := New(fakeFactory{uow: uow}, &fakeGenerator{})

	if err := service.Process(context.Background(), organizationEvent(EventOrganizationUpdated)); err != nil {
		t.Fatalf("Process() error = %v", err)
	}
	if !uow.organization.OwnerBootstrapEligible {
		t.Fatal("creator with no membership history lost initial bootstrap eligibility")
	}
	if uow.organization.OwnerBootstrapped {
		t.Fatal("owner bootstrap completed without an active creator membership")
	}
}

func TestOwnerBootstrapLegacyOrganizationNeverElevatesHistoricalCreator(t *testing.T) {
	creator := "user-legacy-creator"
	uow := newFakeUnitOfWork()
	uow.organizationFound = true
	uow.organization = Organization{
		ID: testOrganizationID, Status: "active", OwnerBootstrapEligible: false,
	}
	uow.activeFound = true
	uow.activeMembership = Membership{
		ID: testMembershipID, OrganizationID: testOrganizationID,
		ClerkMembershipID: "mem-legacy", ClerkUserID: creator,
		ApplicationRole: RoleViewer, Status: "active",
	}
	service := New(fakeFactory{uow: uow}, &fakeGenerator{})
	event := organizationEvent(EventOrganizationUpdated)
	event.Organization.CreatedBy = &creator

	if err := service.Process(context.Background(), event); err != nil {
		t.Fatalf("Process() error = %v", err)
	}
	if uow.organization.ClerkCreatedByUserID == nil || *uow.organization.ClerkCreatedByUserID != creator {
		t.Fatalf("legacy creator projection = %#v", uow.organization.ClerkCreatedByUserID)
	}
	if uow.activeMembership.ApplicationRole != RoleViewer {
		t.Fatalf("legacy viewer was elevated: %q", uow.activeMembership.ApplicationRole)
	}
	if uow.organization.OwnerBootstrapped {
		t.Fatal("legacy organization completed owner bootstrap")
	}
}

func TestOwnerBootstrapRequiresActiveOrganization(t *testing.T) {
	creator := "user-creator"
	for _, status := range []string{"disabled", "deleted"} {
		t.Run(status, func(t *testing.T) {
			uow := newFakeUnitOfWork()
			uow.organizationFound = true
			uow.organization = Organization{
				ID:                     testOrganizationID,
				Status:                 status,
				ClerkCreatedByUserID:   &creator,
				OwnerBootstrapEligible: true,
			}
			uow.activeFound = true
			uow.activeMembership = Membership{
				ID: testMembershipID, OrganizationID: testOrganizationID,
				ClerkMembershipID: "mem-creator", ClerkUserID: creator,
				ApplicationRole: RoleAdmin, Status: "active",
			}
			service := New(fakeFactory{uow: uow}, &fakeGenerator{})
			if err := service.Process(context.Background(), organizationEvent(EventOrganizationUpdated)); err != nil {
				t.Fatalf("Process() error = %v", err)
			}
			if uow.activeMembership.ApplicationRole != RoleAdmin {
				t.Fatalf("%s organization elevated creator: %q", status, uow.activeMembership.ApplicationRole)
			}
			if uow.organization.OwnerBootstrapped {
				t.Fatalf("%s organization completed owner bootstrap", status)
			}
		})
	}
}

func TestOwnerBootstrapDeletedCreatorHistoryCancelsEligibility(t *testing.T) {
	creator := "user-inactive-creator"
	uow := newFakeUnitOfWork()
	uow.organizationFound = true
	uow.organization = Organization{
		ID:                     testOrganizationID,
		Status:                 "active",
		ClerkCreatedByUserID:   &creator,
		OwnerBootstrapEligible: true,
	}
	uow.membershipFound = true
	uow.membership = Membership{
		ID: testMembershipID, OrganizationID: testOrganizationID,
		ClerkMembershipID: "mem-inactive", ClerkUserID: creator,
		ApplicationRole: RoleViewer, Status: "deleted",
	}
	service := New(fakeFactory{uow: uow}, &fakeGenerator{})

	if err := service.Process(context.Background(), organizationEvent(EventOrganizationUpdated)); err != nil {
		t.Fatalf("Process() error = %v", err)
	}
	if uow.organization.OwnerBootstrapEligible {
		t.Fatal("deleted creator history did not permanently cancel bootstrap eligibility")
	}
	if uow.organization.OwnerBootstrapped {
		t.Fatal("deleted creator history completed owner bootstrap")
	}
}

func TestOwnerBootstrapDeletedHistoryBlocksRejoin(t *testing.T) {
	creator := "user-rejoining-creator"
	uow := newFakeUnitOfWork()
	uow.organizationFound = true
	uow.organization = Organization{
		ID:                     testOrganizationID,
		Status:                 "active",
		ClerkCreatedByUserID:   &creator,
		OwnerBootstrapEligible: true,
	}
	uow.membershipFound = true
	uow.membership = Membership{
		ID: testMembershipID, OrganizationID: testOrganizationID,
		ClerkMembershipID: "mem-old", ClerkUserID: creator,
		ApplicationRole: RoleAdmin, Status: "deleted",
	}
	uow.activeFound = true
	uow.activeMembership = Membership{
		ID: testSecondID, OrganizationID: testOrganizationID,
		ClerkMembershipID: "mem-rejoin", ClerkUserID: creator,
		ApplicationRole: RoleViewer, Status: "active",
	}
	service := New(fakeFactory{uow: uow}, &fakeGenerator{})

	if err := service.Process(context.Background(), organizationEvent(EventOrganizationUpdated)); err != nil {
		t.Fatalf("Process() error = %v", err)
	}
	if uow.organization.OwnerBootstrapEligible {
		t.Fatal("rejoin did not cancel historical creator bootstrap eligibility")
	}
	if uow.organization.OwnerBootstrapped {
		t.Fatal("rejoining creator completed owner bootstrap")
	}
	if uow.activeMembership.ApplicationRole != RoleViewer {
		t.Fatalf("rejoining creator role = %q, want viewer", uow.activeMembership.ApplicationRole)
	}
}

func TestCreatorMembershipDeleteCancelsEligibilityInSameTransaction(t *testing.T) {
	creator := "user-1"
	uow := newFakeUnitOfWork()
	uow.organizationFound = true
	uow.organization = Organization{
		ID:                     testOrganizationID,
		Status:                 "disabled",
		ClerkCreatedByUserID:   &creator,
		OwnerBootstrapEligible: true,
	}
	uow.membershipFound = true
	uow.membership = Membership{
		ID: testMembershipID, OrganizationID: testOrganizationID,
		ClerkMembershipID: "mem-1", ClerkUserID: creator,
		ApplicationRole: RoleAdmin, Status: "active",
	}
	service := New(fakeFactory{uow: uow}, &fakeGenerator{})

	if err := service.Process(context.Background(), membershipEvent(EventMembershipDeleted, stringPointer(ClerkRoleAdmin))); err != nil {
		t.Fatalf("Process() error = %v", err)
	}
	if uow.markMembershipDeletedCalls != 1 {
		t.Fatalf("membership delete calls = %d, want 1", uow.markMembershipDeletedCalls)
	}
	if uow.organization.OwnerBootstrapEligible {
		t.Fatal("creator membership deletion did not cancel bootstrap eligibility")
	}
	if uow.organization.OwnerBootstrapped {
		t.Fatal("creator membership deletion completed owner bootstrap")
	}
}

func TestOwnerBootstrapOneTimeFenceDoesNotRegrantRole(t *testing.T) {
	creator := "user-creator"
	uow := newFakeUnitOfWork()
	uow.organizationFound = true
	uow.organization = Organization{
		ID:                     testOrganizationID,
		Status:                 "active",
		ClerkCreatedByUserID:   &creator,
		OwnerBootstrapped:      true,
		OwnerBootstrapEligible: true,
	}
	uow.activeFound = true
	uow.activeMembership = Membership{
		ID: testMembershipID, OrganizationID: testOrganizationID,
		ClerkMembershipID: "mem-creator", ClerkUserID: creator,
		ApplicationRole: RoleViewer, Status: "active",
	}
	service := New(fakeFactory{uow: uow}, &fakeGenerator{})

	if err := service.Process(context.Background(), organizationEvent(EventOrganizationUpdated)); err != nil {
		t.Fatalf("Process() error = %v", err)
	}
	if uow.activeMembership.ApplicationRole != RoleViewer {
		t.Fatalf("one-time fence re-granted owner: %q", uow.activeMembership.ApplicationRole)
	}
}

func TestOwnerBootstrapDoesNotPromoteArbitraryAdminWithoutCreatorSignal(t *testing.T) {
	uow := newFakeUnitOfWork()
	uow.organizationFound = true
	uow.organization = Organization{
		ID: testOrganizationID, Status: "active", OwnerBootstrapEligible: true,
	}
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
		ID:                     testOrganizationID,
		Status:                 "active",
		ClerkCreatedByUserID:   &storedCreator,
		OwnerBootstrapEligible: true,
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
