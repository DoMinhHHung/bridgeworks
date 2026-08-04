package currentorganization

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/DoMinhHHung/bridgeworks/service/organization-service/internal/authorization"
	"github.com/google/uuid"
)

type fakeIdentity struct {
	identity Identity
	err      error
}

func (f *fakeIdentity) Resolve(context.Context, string, string) (Identity, error) {
	return f.identity, f.err
}
func (f *fakeIdentity) CloseIdleConnections() {}

type fakeRepository struct {
	organization      Organization
	organizationFound bool
	membership        Membership
	membershipFound   bool
	permissions       []string
	err               error
}

func (f *fakeRepository) GetOrganizationByClerkID(context.Context, string) (Organization, bool, error) {
	return f.organization, f.organizationFound, f.err
}
func (f *fakeRepository) GetActiveMembership(context.Context, uuid.UUID, string) (Membership, bool, error) {
	return f.membership, f.membershipFound, f.err
}
func (f *fakeRepository) ListPermissions(context.Context, string) ([]string, error) {
	return f.permissions, f.err
}

func TestResolveUsesLocalRoleAndPermissions(t *testing.T) {
	identityID := uuid.MustParse("0198f3be-bf6f-7b0a-8a25-f8433567e0c1")
	organizationID := uuid.MustParse("0198f3be-bf6f-7b0a-8a25-f8433567e0c2")
	membershipID := uuid.MustParse("0198f3be-bf6f-7b0a-8a25-f8433567e0c3")
	repository := &fakeRepository{
		organization: Organization{ID: organizationID, Status: "active"}, organizationFound: true,
		membership: Membership{ID: membershipID, OrganizationID: organizationID, ApplicationRole: "viewer", Status: "active"}, membershipFound: true,
		permissions: []string{authorization.PermissionOrganizationRead},
	}
	service := New(&fakeIdentity{identity: Identity{ID: identityID}}, repository)
	result, err := service.Resolve(context.Background(), authorization.Principal{
		ClerkUserID: "user_1", SessionID: "sess_1", ClerkOrganizationID: "org_1",
	}, "Bearer token", "request-1")
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if result.Actor.Role != "viewer" || result.Actor.HasPermission(authorization.PermissionOrganizationManage) {
		t.Fatalf("JWT role must not override local role: %#v", result.Actor)
	}
	if got := result.Actor.Permissions(); !reflect.DeepEqual(got, []string{authorization.PermissionOrganizationRead}) {
		t.Fatalf("permissions = %#v", got)
	}
}

func TestResolveStatusFailures(t *testing.T) {
	organizationID := uuid.MustParse("0198f3be-bf6f-7b0a-8a25-f8433567e0c2")
	cases := []struct {
		name      string
		principal authorization.Principal
		repository fakeRepository
		want      error
	}{
		{name: "missing context", principal: authorization.Principal{ClerkUserID: "user"}, want: ErrOrganizationContextRequired},
		{name: "pending", principal: authorization.Principal{ClerkUserID: "user", ClerkOrganizationID: "org"}, repository: fakeRepository{organization: Organization{ID: organizationID, Status: "pending"}, organizationFound: true}, want: ErrOrganizationNotReady},
		{name: "disabled", principal: authorization.Principal{ClerkUserID: "user", ClerkOrganizationID: "org"}, repository: fakeRepository{organization: Organization{ID: organizationID, Status: "disabled"}, organizationFound: true}, want: ErrOrganizationDisabled},
		{name: "deleted", principal: authorization.Principal{ClerkUserID: "user", ClerkOrganizationID: "org"}, repository: fakeRepository{organization: Organization{ID: organizationID, Status: "deleted"}, organizationFound: true}, want: ErrOrganizationDeleted},
		{name: "missing membership", principal: authorization.Principal{ClerkUserID: "user", ClerkOrganizationID: "org"}, repository: fakeRepository{organization: Organization{ID: organizationID, Status: "active"}, organizationFound: true}, want: ErrMembershipRequired},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			service := New(&fakeIdentity{identity: Identity{ID: uuid.New()}}, &testCase.repository)
			_, err := service.Resolve(context.Background(), testCase.principal, "Bearer token", "request")
			if !errors.Is(err, testCase.want) {
				t.Fatalf("Resolve() error = %v, want %v", err, testCase.want)
			}
		})
	}
}

func TestRequirePermission(t *testing.T) {
	result := Result{Actor: authorization.NewActorContext(uuid.New(), uuid.New(), uuid.New(), "viewer", []string{authorization.PermissionOrganizationRead})}
	if err := RequirePermission(result, authorization.PermissionOrganizationRead); err != nil {
		t.Fatalf("RequirePermission() error = %v", err)
	}
	if !errors.Is(RequirePermission(result, authorization.PermissionMembershipRead), ErrPermissionDenied) {
		t.Fatal("missing permission must be denied")
	}
}
