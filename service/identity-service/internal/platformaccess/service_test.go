package platformaccess

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

type readerStub struct {
	user      User
	found     bool
	userErr   error
	roles     []string
	rolesErr  error
	roleCalls int
}

func (r *readerStub) GetPlatformAccessUserByClerkUserID(context.Context, string) (User, bool, error) {
	return r.user, r.found, r.userErr
}

func (r *readerStub) ListActivePlatformRolesByUserID(context.Context, uuid.UUID) ([]string, error) {
	r.roleCalls++
	return r.roles, r.rolesErr
}

func TestResolveOrdinaryActiveUserHasNoPlatformAccess(t *testing.T) {
	t.Parallel()

	reader := &readerStub{user: User{ID: uuid.New(), Status: "active"}, found: true}
	access, err := New(reader).Resolve(context.Background(), "user_provider")
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if len(access.Roles) != 0 || len(access.Permissions) != 0 {
		t.Fatalf("ordinary access = %+v", access)
	}
}

func TestResolveExplicitPlatformAdminMapsSinglePermission(t *testing.T) {
	t.Parallel()

	reader := &readerStub{
		user:  User{ID: uuid.New(), Status: "active"},
		found: true,
		roles: []string{RolePlatformAdmin},
	}
	access, err := New(reader).Resolve(context.Background(), "user_provider")
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if len(access.Roles) != 1 || access.Roles[0] != RolePlatformAdmin {
		t.Fatalf("roles = %#v", access.Roles)
	}
	if len(access.Permissions) != 1 || access.Permissions[0] != PermissionOrganizationVerificationReview {
		t.Fatalf("permissions = %#v", access.Permissions)
	}
}

func TestResolveRejectsInactiveUsersBeforeRoleLookup(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name   string
		status string
		want   error
	}{
		{name: "disabled", status: "disabled", want: ErrAccountDisabled},
		{name: "deleted", status: "deleted", want: ErrAccountDeleted},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			reader := &readerStub{
				user:  User{ID: uuid.New(), Status: test.status},
				found: true,
				roles: []string{RolePlatformAdmin},
			}
			_, err := New(reader).Resolve(context.Background(), "user_provider")
			if !errors.Is(err, test.want) {
				t.Fatalf("Resolve() error = %v, want %v", err, test.want)
			}
			if reader.roleCalls != 0 {
				t.Fatalf("role lookup calls = %d", reader.roleCalls)
			}
		})
	}
}

func TestResolveFailsClosedForUnknownOrDuplicateRole(t *testing.T) {
	t.Parallel()

	for _, roles := range [][]string{{"tenant_admin"}, {RolePlatformAdmin, RolePlatformAdmin}} {
		reader := &readerStub{
			user:  User{ID: uuid.New(), Status: "active"},
			found: true,
			roles: roles,
		}
		access, err := New(reader).Resolve(context.Background(), "user_provider")
		if err == nil {
			t.Fatalf("Resolve(%#v) expected error", roles)
		}
		if len(access.Roles) != 0 || len(access.Permissions) != 0 {
			t.Fatalf("failed resolution returned access: %+v", access)
		}
	}
}

func TestResolvePropagatesIdentityNotReadyAndDatabaseFailures(t *testing.T) {
	t.Parallel()

	reader := &readerStub{found: false}
	_, err := New(reader).Resolve(context.Background(), "user_provider")
	if !errors.Is(err, ErrIdentityNotReady) {
		t.Fatalf("missing user error = %v", err)
	}

	dependencyErr := errors.New("database unavailable")
	reader = &readerStub{userErr: dependencyErr}
	_, err = New(reader).Resolve(context.Background(), "user_provider")
	if !errors.Is(err, dependencyErr) {
		t.Fatalf("database error = %v", err)
	}
}
