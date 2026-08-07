-- name: PruneConsumedInvitationIntents :execrows
WITH candidates AS (
    SELECT id
    FROM organization.membership_invitation_intents
    WHERE consumed_at IS NOT NULL
      AND consumed_at < $1::timestamptz
    ORDER BY consumed_at ASC, id ASC
    LIMIT $2
)
DELETE FROM organization.membership_invitation_intents i
USING candidates c
WHERE i.id = c.id;

-- name: ListRemovalIntentsForReconciliation :many
SELECT r.membership_id,
       r.organization_id,
       r.created_at,
       o.clerk_organization_id,
       m.clerk_user_id
FROM organization.membership_removal_intents r
JOIN organization.memberships m
  ON m.id = r.membership_id
 AND m.organization_id = r.organization_id
JOIN organization.organizations o
  ON o.id = r.organization_id
WHERE r.created_at < $1::timestamptz
ORDER BY r.created_at ASC, r.membership_id ASC
LIMIT $2;

-- name: LockRemovalIntentForMaintenance :one
SELECT m.id,
       m.organization_id,
       m.status
FROM organization.membership_removal_intents r
JOIN organization.memberships m
  ON m.id = r.membership_id
 AND m.organization_id = r.organization_id
WHERE r.organization_id = $1
  AND r.membership_id = $2
FOR UPDATE OF r, m;

-- name: MarkMembershipDeletedByID :exec
UPDATE organization.memberships
SET status = 'deleted'
WHERE organization_id = $1
  AND id = $2
  AND status <> 'deleted';
