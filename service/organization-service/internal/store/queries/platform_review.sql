-- name: ListPendingVerificationQueue :many
WITH pending AS (
    SELECT o.id,
           o.name,
           o.legal_name,
           o.website,
           o.country,
           o.company_type,
           o.verification_status,
           o.business_email_domain,
           o.business_email_verified_at,
           o.updated_at,
           COALESCE((
               SELECT max(a.occurred_at)
               FROM organization.audit_events a
               WHERE a.organization_id = o.id
                 AND a.event_type = 'organization.verification.requested'
           ), o.updated_at) AS requested_at
    FROM organization.organizations o
    WHERE o.status = 'active'
      AND o.verification_status = 'pending'
)
SELECT id,
       name,
       legal_name,
       website,
       country,
       company_type,
       verification_status,
       business_email_domain,
       business_email_verified_at,
       updated_at,
       requested_at
FROM pending
WHERE (requested_at, id) > ($1::timestamptz, $2::uuid)
ORDER BY requested_at ASC, id ASC
LIMIT $3;
