-- name: InsertWebhookEvent :one
INSERT INTO organization.clerk_webhook_events (
    event_id, event_type, aggregate_type, aggregate_id,
    clerk_organization_id, occurred_at
) VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (event_id) DO NOTHING
RETURNING event_id;

-- name: AcquireOrganizationAdvisoryLock :exec
SELECT pg_advisory_xact_lock(hashtextextended($1, 0));

-- name: GetLatestAggregateEvent :one
SELECT event_id, event_type, aggregate_type, aggregate_id,
       clerk_organization_id, occurred_at, processed_at
FROM organization.clerk_webhook_events
WHERE aggregate_type = $1
  AND aggregate_id = $2
  AND event_id <> $3
ORDER BY occurred_at DESC,
         CASE
           WHEN event_type LIKE '%.deleted' THEN 3
           WHEN event_type LIKE '%.updated' THEN 2
           WHEN event_type LIKE '%.created' THEN 1
           ELSE 0
         END DESC,
         event_id DESC
LIMIT 1;
