-- name: InsertAuditEvent :exec
INSERT INTO organization.audit_events (
    id,
    organization_id,
    event_type,
    actor_kind,
    actor_identity_user_id,
    actor_membership_id,
    subject_membership_id,
    from_value,
    to_value,
    occurred_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10);

-- name: ListOrganizationAuditEvents :many
SELECT id,
       event_type,
       actor_kind,
       subject_membership_id,
       from_value,
       to_value,
       occurred_at
FROM organization.audit_events
WHERE organization_id = $1
  AND (occurred_at, id) < ($2::timestamptz, $3::uuid)
ORDER BY occurred_at DESC, id DESC
LIMIT $4;
