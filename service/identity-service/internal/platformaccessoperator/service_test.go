package platformaccessoperator

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DoMinhHHung/bridgeworks/service/identity-service/internal/platformaccess"
	"github.com/google/uuid"
)

type repositoryStub struct {
	user            User
	foundUser       bool
	userErr         error
	assignment      Assignment
	foundAssignment bool
	assignmentErr   error
	grantCalls      int
	revokeCalls     int
}

func (r *repositoryStub) GetPlatformAccessUserByIDUser(context.Context, string) (User, bool, error) {
	return r.user, r.foundUser, r.userErr
}
func (r *repositoryStub) GrantPlatformAccess(context.Context, uuid.UUID, string) error {
	r.grantCalls++
	return nil
}
func (r *repositoryStub) RevokePlatformAccess(context.Context, uuid.UUID, string) error {
	r.revokeCalls++
	return nil
}
func (r *repositoryStub) GetPlatformAccessAssignment(context.Context, uuid.UUID, string) (Assignment, bool, error) {
	return r.assignment, r.foundAssignment, r.assignmentErr
}

func TestGrantRequiresActiveExistingLocalUser(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name string
		repo *repositoryStub
		want error
	}{
		{name: "missing", repo: &repositoryStub{}, want: ErrUserNotFound},
		{name: "disabled", repo: &repositoryStub{user: User{ID: uuid.New(), Status: "disabled"}, foundUser: true}, want: ErrUserInactive},
		{name: "deleted", repo: &repositoryStub{user: User{ID: uuid.New(), Status: "deleted"}, foundUser: true}, want: ErrUserInactive},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := New(test.repo).Grant(context.Background(), "bw000001012600")
			if !errors.Is(err, test.want) {
				t.Fatalf("Grant() error = %v, want %v", err, test.want)
			}
			if test.repo.grantCalls != 0 {
				t.Fatalf("grant calls = %d", test.repo.grantCalls)
			}
		})
	}
}

func TestGrantRevokeAndStatusUseOnlyPlatformAdmin(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	repo := &repositoryStub{
		user:            User{ID: uuid.New(), Status: "active"},
		foundUser:       true,
		assignment:      Assignment{GrantedAt: now, UpdatedAt: now},
		foundAssignment: true,
	}
	service := New(repo)
	granted, err := service.Grant(context.Background(), "bw000001012600")
	if err != nil {
		t.Fatalf("Grant() error = %v", err)
	}
	if repo.grantCalls != 1 || granted.Role != platformaccess.RolePlatformAdmin || !granted.Active {
		t.Fatalf("grant result = %+v calls=%d", granted, repo.grantCalls)
	}

	revokedAt := now.Add(time.Second)
	repo.assignment.RevokedAt = &revokedAt
	repo.assignment.UpdatedAt = revokedAt
	revoked, err := service.Revoke(context.Background(), "bw000001012600")
	if err != nil {
		t.Fatalf("Revoke() error = %v", err)
	}
	if repo.revokeCalls != 1 || revoked.Role != platformaccess.RolePlatformAdmin || revoked.Active {
		t.Fatalf("revoke result = %+v calls=%d", revoked, repo.revokeCalls)
	}

	status, err := service.Status(context.Background(), "bw000001012600")
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}
	if !status.Assigned || status.Active || status.Role != platformaccess.RolePlatformAdmin {
		t.Fatalf("status = %+v", status)
	}
}

func TestRevokeIsSafeForInactiveUser(t *testing.T) {
	t.Parallel()

	repo := &repositoryStub{user: User{ID: uuid.New(), Status: "deleted"}, foundUser: true}
	_, err := New(repo).Revoke(context.Background(), "bw000001012600")
	if err != nil {
		t.Fatalf("Revoke() error = %v", err)
	}
	if repo.revokeCalls != 1 {
		t.Fatalf("revoke calls = %d", repo.revokeCalls)
	}
}
