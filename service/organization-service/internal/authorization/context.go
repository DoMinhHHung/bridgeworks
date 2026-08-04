package authorization

import (
	"sort"

	"github.com/google/uuid"
)

const (
	PermissionOrganizationRead   = "organization.read"
	PermissionOrganizationManage = "organization.manage"
	PermissionMembershipRead     = "membership.read"
	PermissionMembershipManage   = "membership.manage"
)

type Principal struct {
	ClerkUserID         string
	SessionID           string
	ClerkOrganizationID string
}

type ActorContext struct {
	IdentityUserID uuid.UUID
	OrganizationID uuid.UUID
	MembershipID   uuid.UUID
	Role           string
	permissions    map[string]struct{}
}

func NewActorContext(identityUserID, organizationID, membershipID uuid.UUID, role string, permissions []string) ActorContext {
	permissionSet := make(map[string]struct{}, len(permissions))
	for _, permission := range permissions {
		permissionSet[permission] = struct{}{}
	}
	return ActorContext{IdentityUserID: identityUserID, OrganizationID: organizationID, MembershipID: membershipID, Role: role, permissions: permissionSet}
}
func (a ActorContext) HasPermission(permission string) bool {
	_, ok := a.permissions[permission]
	return ok
}
func (a ActorContext) Permissions() []string {
	permissions := make([]string, 0, len(a.permissions))
	for permission := range a.permissions {
		permissions = append(permissions, permission)
	}
	sort.Strings(permissions)
	return permissions
}
