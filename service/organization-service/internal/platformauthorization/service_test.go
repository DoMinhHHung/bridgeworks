package platformauthorization

import (
	"context"
	"errors"
	"testing"

	"github.com/DoMinhHHung/bridgeworks/service/organization-service/internal/authorization"
	"github.com/google/uuid"
)

type readerStub struct {
	access Access
	err    error
}

func (r readerStub) ResolvePlatformAccess(context.Context, string, string) (Access, error) {
	return r.access, r.err
}

func TestOrdinaryUserWithoutLocalAssignmentHasNoPlatformPermission(t *testing.T) {
	t.Parallel()

	access, err := New(readerStub{access: Access{Roles: []string{}, Permissions: []string{}}}).Resolve(
		context.Background(),
		"Bearer ordinary-session",
		"request-ordinary-isolation",
	)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if err := RequirePermission(access, PermissionOrganizationVerificationReview); !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("RequirePermission() error = %v", err)
	}
}

func TestTenantOwnerAndAdminAuthorityCannotBecomePlatformAuthority(t *testing.T) {
	t.Parallel()

	allTenantPermissions := []string{
		authorization.PermissionOrganizationRead,
		authorization.PermissionOrganizationManage,
		authorization.PermissionOrganizationVerifyRequest,
		authorization.PermissionMembershipRead,
		authorization.PermissionMembershipInvite,
		authorization.PermissionMembershipManage,
		authorization.PermissionMembershipRoleManage,
	}
	for _, role := range []string{"owner", "admin"} {
		role := role
		t.Run(role, func(t *testing.T) {
			t.Parallel()

			tenantActor := authorization.NewActorContext(
				uuid.New(),
				uuid.New(),
				uuid.New(),
				role,
				allTenantPermissions,
			)
			if tenantActor.HasPermission(PermissionOrganizationVerificationReview) {
				t.Fatalf("tenant %s unexpectedly has global review permission", role)
			}

			access, err := New(readerStub{access: Access{Roles: []string{}, Permissions: []string{}}}).Resolve(
				context.Background(),
				"Bearer tenant-session",
				"request-tenant-isolation",
			)
			if err != nil {
				t.Fatalf("Resolve() error = %v", err)
			}
			if err := RequirePermission(access, PermissionOrganizationVerificationReview); !errors.Is(err, ErrPermissionDenied) {
				t.Fatalf("tenant %s RequirePermission() error = %v", role, err)
			}
		})
	}
}

func TestExplicitLocalPlatformAdminGrantProvidesReviewPermission(t *testing.T) {
	t.Parallel()

	access, err := New(readerStub{access: Access{
		Roles:       []string{RolePlatformAdmin},
		Permissions: []string{PermissionOrganizationVerificationReview},
	}}).Resolve(context.Background(), "Bearer session", "request")
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if err := RequirePermission(access, PermissionOrganizationVerificationReview); err != nil {
		t.Fatalf("RequirePermission() error = %v", err)
	}
}

func TestPlatformAuthorizationNeedsNoOrganizationContext(t *testing.T) {
	t.Parallel()

	reader := readerStub{access: Access{
		Roles:       []string{RolePlatformAdmin},
		Permissions: []string{PermissionOrganizationVerificationReview},
	}}
	access, err := New(reader).Resolve(context.Background(), "Bearer no-org-session", "request-no-org")
	if err != nil {
		t.Fatalf("Resolve() without organization context error = %v", err)
	}
	if err := RequirePermission(access, PermissionOrganizationVerificationReview); err != nil {
		t.Fatalf("permission without organization context = %v", err)
	}
}

func TestPlatformAuthorizationFailsClosedOnDependencyAndMalformedProjection(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name   string
		reader readerStub
	}{
		{name: "dependency outage", reader: readerStub{err: errors.New("identity timeout")}},
		{name: "unknown role", reader: readerStub{access: Access{Roles: []string{"super_admin"}, Permissions: []string{PermissionOrganizationVerificationReview}}}},
		{name: "unknown permission", reader: readerStub{access: Access{Roles: []string{RolePlatformAdmin}, Permissions: []string{"organization.all"}}}},
		{name: "role without permission", reader: readerStub{access: Access{Roles: []string{RolePlatformAdmin}, Permissions: []string{}}}},
		{name: "permission without role", reader: readerStub{access: Access{Roles: []string{}, Permissions: []string{PermissionOrganizationVerificationReview}}}},
		{name: "duplicate role", reader: readerStub{access: Access{Roles: []string{RolePlatformAdmin, RolePlatformAdmin}, Permissions: []string{PermissionOrganizationVerificationReview}}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			access, err := New(test.reader).Resolve(context.Background(), "Bearer session", "request")
			if !errors.Is(err, ErrUnavailable) {
				t.Fatalf("Resolve() error = %v, want unavailable", err)
			}
			if len(access.Roles) != 0 || len(access.Permissions) != 0 {
				t.Fatalf("failed resolution returned access: %+v", access)
			}
		})
	}
}

func TestPlatformAuthorizationPreservesResolvedIdentityDenials(t *testing.T) {
	t.Parallel()

	for _, want := range []error{ErrUnauthorized, ErrAccountInactive, ErrIdentityNotReady} {
		_, err := New(readerStub{err: want}).Resolve(context.Background(), "Bearer session", "request")
		if !errors.Is(err, want) {
			t.Fatalf("Resolve() error = %v, want %v", err, want)
		}
	}
}
