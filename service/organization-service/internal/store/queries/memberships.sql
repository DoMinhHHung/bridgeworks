-- name: GetMembershipByClerkID :one
SELECT id, clerk_membership_id, organization_id, clerk_user_id, clerk_role,
       application_role, status, created_at, updated_at
FROM organization.memberships
WHERE clerk_membership_id = $1;

-- name: GetActiveMembershipByOrganizationUser :one
SELECT id, clerk_membership_id, organization_id, clerk_user_id, clerk_role,
       application_role, status, created_at, updated_at
FROM organization.memberships
WHERE organization_id = $1
  AND clerk_user_id = $2
  AND status = 'active';

-- name: HasDeletedMembershipByOrganizationUser :one
SELECT EXISTS (
    SELECT 1
    FROM organization.memberships
    WHERE organization_id = $1
      AND clerk_user_id = $2
      AND status = 'deleted'
);

-- name: InsertMembership :one
INSERT INTO organization.memberships (
    id, clerk_membership_id, organization_id, clerk_user_id,
    clerk_role, application_role, status
) VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING id, clerk_membership_id, organization_id, clerk_user_id, clerk_role,
          application_role, status, created_at, updated_at;

-- name: UpdateMembershipClerkRole :one
UPDATE organization.memberships
SET clerk_role = $2
WHERE clerk_membership_id = $1
  AND status = 'active'
RETURNING id, clerk_membership_id, organization_id, clerk_user_id, clerk_role,
          application_role, status, created_at, updated_at;

-- name: UpdateMembershipApplicationRole :one
UPDATE organization.memberships
SET application_role = $2
WHERE id = $1
  AND status = 'active'
RETURNING id, clerk_membership_id, organization_id, clerk_user_id, clerk_role,
          application_role, status, created_at, updated_at;

-- name: MarkMembershipDeleted :one
UPDATE organization.memberships
SET status = 'deleted'
WHERE clerk_membership_id = $1
RETURNING id, clerk_membership_id, organization_id, clerk_user_id, clerk_role,
          application_role, status, created_at, updated_at;

-- name: ListPermissionsForRole :many
SELECT permission_key
FROM organization.role_permissions
WHERE role_key = $1
ORDER BY permission_key;
