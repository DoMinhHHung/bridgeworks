-- name: AcquireOrganizationAdvisoryLockByID :exec
SELECT pg_advisory_xact_lock(hashtextextended(clerk_organization_id, 0))
FROM organization.organizations
WHERE id = $1;

-- name: GetOrganizationProviderReferenceByID :one
SELECT id, clerk_organization_id
FROM organization.organizations
WHERE id = $1;

-- name: LockMembershipForAdministration :one
SELECT m.id, m.clerk_membership_id, m.organization_id, m.clerk_user_id,
       m.clerk_role, m.application_role, m.status, m.created_at, m.updated_at,
       EXISTS (
           SELECT 1
           FROM organization.membership_removal_intents r
           WHERE r.membership_id = m.id
       ) AS removal_pending
FROM organization.memberships m
WHERE m.organization_id = $1
  AND m.id = $2
FOR UPDATE OF m;

-- name: CountEffectiveOwners :one
SELECT count(*)
FROM organization.memberships m
WHERE m.organization_id = $1
  AND m.status = 'active'
  AND m.application_role = 'owner'
  AND NOT EXISTS (
      SELECT 1
      FROM organization.membership_removal_intents r
      WHERE r.membership_id = m.id
  );

-- name: InsertMembershipInvitationIntent :exec
INSERT INTO organization.membership_invitation_intents (
    id, organization_id, application_role, created_by_identity_user_id
) VALUES ($1, $2, $3, $4);

-- name: DeleteMembershipInvitationIntent :exec
DELETE FROM organization.membership_invitation_intents
WHERE organization_id = $1
  AND id = $2
  AND consumed_at IS NULL;

-- name: GetPendingMembershipInvitationIntent :one
SELECT id, organization_id, application_role, created_by_identity_user_id,
       consumed_membership_id, consumed_at, created_at, updated_at
FROM organization.membership_invitation_intents
WHERE organization_id = $1
  AND id = $2
  AND consumed_at IS NULL;

-- name: ConsumeMembershipInvitationIntent :execrows
UPDATE organization.membership_invitation_intents
SET consumed_membership_id = $3,
    consumed_at = now()
WHERE organization_id = $1
  AND id = $2
  AND consumed_at IS NULL;

-- name: UpdateMembershipApplicationRoleByOrganization :exec
UPDATE organization.memberships
SET application_role = $3
WHERE organization_id = $1
  AND id = $2
  AND status = 'active';

-- name: InsertMembershipRemovalIntent :one
WITH target AS (
    SELECT clerk_user_id
    FROM organization.memberships
    WHERE id = $1
      AND organization_id = $2
), cancelled_bootstrap AS (
    UPDATE organization.organizations o
    SET owner_bootstrap_eligible = false
    FROM target t
    WHERE o.id = $2
      AND o.owner_bootstrap_eligible = true
      AND o.owner_bootstrapped = false
      AND o.clerk_created_by_user_id = t.clerk_user_id
)
INSERT INTO organization.membership_removal_intents (
    membership_id, organization_id, requested_by_identity_user_id
) VALUES ($1, $2, $3)
ON CONFLICT (membership_id) DO NOTHING
RETURNING membership_id;

-- name: DeleteMembershipRemovalIntent :execrows
DELETE FROM organization.membership_removal_intents
WHERE organization_id = $1
  AND membership_id = $2;
