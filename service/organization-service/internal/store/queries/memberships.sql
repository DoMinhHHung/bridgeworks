-- name: GetMembershipByClerkID :one
SELECT id, clerk_membership_id, organization_id, clerk_user_id, clerk_role,
       application_role, status, created_at, updated_at
FROM organization.memberships
WHERE clerk_membership_id = $1;

-- name: GetActiveMembershipByOrganizationUser :one
SELECT m.id, m.clerk_membership_id, m.organization_id, m.clerk_user_id, m.clerk_role,
       m.application_role, m.status, m.created_at, m.updated_at
FROM organization.memberships m
WHERE m.organization_id = $1
  AND m.clerk_user_id = $2
  AND m.status = 'active'
  AND NOT EXISTS (
      SELECT 1
      FROM organization.membership_removal_intents r
      WHERE r.membership_id = m.id
  );

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
