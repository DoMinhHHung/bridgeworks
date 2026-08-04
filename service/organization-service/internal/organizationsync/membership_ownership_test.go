package organizationsync

import (
	"context"
	"testing"
)

func TestServiceRejectsMismatchedMembershipOwnership(t *testing.T) {
	memberRole := "org:member"
	tests := []struct {
		name      string
		eventType string
		existing  Membership
	}{
		{
			name:      "created membership belongs to another organization",
			eventType: EventMembershipCreated,
			existing: Membership{
				ID: testMembershipID, ClerkMembershipID: "mem-1", OrganizationID: testSecondID,
				ClerkUserID: "user-1", Status: "active",
			},
		},
		{
			name:      "active membership belongs to another organization on update",
			eventType: EventMembershipUpdated,
			existing: Membership{
				ID: testMembershipID, ClerkMembershipID: "mem-1", OrganizationID: testSecondID,
				ClerkUserID: "user-1", Status: "active",
			},
		},
		{
			name:      "active membership belongs to another Clerk user on update",
			eventType: EventMembershipUpdated,
			existing: Membership{
				ID: testMembershipID, ClerkMembershipID: "mem-1", OrganizationID: testOrganizationID,
				ClerkUserID: "user-2", Status: "active",
			},
		},
		{
			name:      "active membership belongs to another organization on delete",
			eventType: EventMembershipDeleted,
			existing: Membership{
				ID: testMembershipID, ClerkMembershipID: "mem-1", OrganizationID: testSecondID,
				ClerkUserID: "user-1", Status: "active",
			},
		},
		{
			name:      "active membership belongs to another Clerk user on delete",
			eventType: EventMembershipDeleted,
			existing: Membership{
				ID: testMembershipID, ClerkMembershipID: "mem-1", OrganizationID: testOrganizationID,
				ClerkUserID: "user-2", Status: "active",
			},
		},
		{
			name:      "deleted membership ownership mismatch is not idempotent",
			eventType: EventMembershipCreated,
			existing: Membership{
				ID: testMembershipID, ClerkMembershipID: "mem-1", OrganizationID: testSecondID,
				ClerkUserID: "user-1", Status: "deleted",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			uow := newFakeUnitOfWork()
			uow.organizationFound = true
			uow.organization = Organization{ID: testOrganizationID, Status: "active"}
			uow.membershipFound = true
			uow.membership = tt.existing
			service := New(fakeFactory{uow: uow}, &fakeGenerator{})

			err := service.Process(context.Background(), membershipEvent(tt.eventType, &memberRole))
			if err == nil || err.Error() != "inconsistent Clerk membership projection" {
				t.Fatalf("Process() error = %v", err)
			}
			if len(uow.membershipRoleUpdates) != 0 {
				t.Fatalf("mismatched membership role updates = %#v", uow.membershipRoleUpdates)
			}
			if uow.markMembershipDeletedCalls != 0 {
				t.Fatalf("mismatched membership delete calls = %d", uow.markMembershipDeletedCalls)
			}
			if len(uow.insertedMemberships) != 0 {
				t.Fatalf("mismatched membership inserts = %#v", uow.insertedMemberships)
			}
			if uow.commitCalls != 0 || uow.rollbackCalls != 1 {
				t.Fatalf("commit/rollback = %d/%d", uow.commitCalls, uow.rollbackCalls)
			}
			if !containsOrdered(uow.sequence, []string{"insert_inbox", "lock", "get_organization", "get_membership", "rollback"}) {
				t.Fatalf("sequence = %#v", uow.sequence)
			}
		})
	}
}

func TestServiceMatchingMembershipOwnershipKeepsUpdateAndDeleteBehavior(t *testing.T) {
	memberRole := "org:member"

	t.Run("update", func(t *testing.T) {
		uow := newFakeUnitOfWork()
		uow.organizationFound = true
		uow.organization = Organization{ID: testOrganizationID, Status: "active"}
		uow.membershipFound = true
		uow.membership = Membership{
			ID: testMembershipID, ClerkMembershipID: "mem-1", OrganizationID: testOrganizationID,
			ClerkUserID: "user-1", ApplicationRole: RoleAdmin, Status: "active",
		}
		service := New(fakeFactory{uow: uow}, &fakeGenerator{})

		if err := service.Process(context.Background(), membershipEvent(EventMembershipUpdated, &memberRole)); err != nil {
			t.Fatalf("Process() error = %v", err)
		}
		if len(uow.membershipRoleUpdates) != 1 || uow.markMembershipDeletedCalls != 0 {
			t.Fatalf("update/delete calls = %d/%d", len(uow.membershipRoleUpdates), uow.markMembershipDeletedCalls)
		}
		if uow.commitCalls != 1 || uow.rollbackCalls != 0 {
			t.Fatalf("commit/rollback = %d/%d", uow.commitCalls, uow.rollbackCalls)
		}
	})

	t.Run("delete", func(t *testing.T) {
		uow := newFakeUnitOfWork()
		uow.organizationFound = true
		uow.organization = Organization{ID: testOrganizationID, Status: "active"}
		uow.membershipFound = true
		uow.membership = Membership{
			ID: testMembershipID, ClerkMembershipID: "mem-1", OrganizationID: testOrganizationID,
			ClerkUserID: "user-1", ApplicationRole: RoleAdmin, Status: "active",
		}
		service := New(fakeFactory{uow: uow}, &fakeGenerator{})

		if err := service.Process(context.Background(), membershipEvent(EventMembershipDeleted, &memberRole)); err != nil {
			t.Fatalf("Process() error = %v", err)
		}
		if len(uow.membershipRoleUpdates) != 0 || uow.markMembershipDeletedCalls != 1 {
			t.Fatalf("update/delete calls = %d/%d", len(uow.membershipRoleUpdates), uow.markMembershipDeletedCalls)
		}
		if uow.commitCalls != 1 || uow.rollbackCalls != 0 {
			t.Fatalf("commit/rollback = %d/%d", uow.commitCalls, uow.rollbackCalls)
		}
	})
}
