package organizationsync

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

func TestServiceClerkMembershipConstraintEquivalentActiveProjection(t *testing.T) {
	uow := newFakeUnitOfWork()
	uow.organizationFound = true
	uow.organization = Organization{ID: testOrganizationID, Status: "active"}
	uow.insertMembershipErr = &UniqueConstraintError{
		Constraint: ConstraintMembershipClerkID,
		Cause:      errors.New("duplicate key"),
	}
	uow.membershipFound = true
	uow.membership = Membership{
		ID:                testMembershipID,
		ClerkMembershipID: "mem-1",
		OrganizationID:    testOrganizationID,
		ClerkUserID:       "user-1",
		ApplicationRole:   RoleAdmin,
		Status:            "active",
	}
	service := New(fakeFactory{uow: uow}, &fakeGenerator{ids: []uuid.UUID{testSecondID}})

	if err := service.Process(context.Background(), membershipEvent(EventMembershipCreated, nil)); err != nil {
		t.Fatalf("Process() error = %v", err)
	}
	want := []string{"savepoint", "insert_membership", "rollback_savepoint", "release_savepoint", "get_membership", "commit"}
	if !containsOrdered(uow.sequence, want) {
		t.Fatalf("sequence = %#v, want ordered %#v", uow.sequence, want)
	}
	if len(uow.membershipRoleUpdates) != 0 || uow.markMembershipDeletedCalls != 0 {
		t.Fatalf("equivalent projection was mutated: %#v", uow.sequence)
	}
}

func TestServiceClerkMembershipConstraintDeletedProjectionNeverReactivates(t *testing.T) {
	uow := newFakeUnitOfWork()
	uow.organizationFound = true
	uow.organization = Organization{ID: testOrganizationID, Status: "active"}
	uow.insertMembershipErr = &UniqueConstraintError{
		Constraint: ConstraintMembershipClerkID,
		Cause:      errors.New("duplicate key"),
	}
	uow.membershipFound = true
	uow.membership = Membership{
		ID:                testMembershipID,
		ClerkMembershipID: "mem-1",
		OrganizationID:    testOrganizationID,
		ClerkUserID:       "user-1",
		ApplicationRole:   RoleViewer,
		Status:            "deleted",
	}
	service := New(fakeFactory{uow: uow}, &fakeGenerator{ids: []uuid.UUID{testSecondID}})

	if err := service.Process(context.Background(), membershipEvent(EventMembershipCreated, nil)); err != nil {
		t.Fatalf("Process() error = %v", err)
	}
	if len(uow.membershipRoleUpdates) != 0 || uow.markMembershipDeletedCalls != 0 {
		t.Fatalf("deleted projection was reactivated or mutated: %#v", uow.sequence)
	}
	if uow.commitCalls != 1 || uow.rollbackCalls != 0 {
		t.Fatalf("commit/rollback = %d/%d", uow.commitCalls, uow.rollbackCalls)
	}
}

func TestServiceClerkMembershipConstraintInconsistentProjectionRollsBack(t *testing.T) {
	uow := newFakeUnitOfWork()
	uow.organizationFound = true
	uow.organization = Organization{ID: testOrganizationID, Status: "active"}
	uow.insertMembershipErr = &UniqueConstraintError{
		Constraint: ConstraintMembershipClerkID,
		Cause:      errors.New("duplicate key"),
	}
	uow.membershipFound = true
	uow.membership = Membership{
		ID:                testMembershipID,
		ClerkMembershipID: "mem-1",
		OrganizationID:    testOrganizationID,
		ClerkUserID:       "different-user",
		ApplicationRole:   RoleViewer,
		Status:            "active",
	}
	service := New(fakeFactory{uow: uow}, &fakeGenerator{ids: []uuid.UUID{testSecondID}})

	if err := service.Process(context.Background(), membershipEvent(EventMembershipCreated, nil)); err == nil {
		t.Fatal("Process() error = nil")
	}
	want := []string{"rollback_savepoint", "release_savepoint", "get_membership", "rollback"}
	if !containsOrdered(uow.sequence, want) {
		t.Fatalf("sequence = %#v, want ordered %#v", uow.sequence, want)
	}
	if uow.commitCalls != 0 || uow.rollbackCalls != 1 {
		t.Fatalf("commit/rollback = %d/%d", uow.commitCalls, uow.rollbackCalls)
	}
}
