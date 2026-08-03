-- name: LockClerkUser :exec
select pg_advisory_xact_lock(hashtextextended(sqlc.arg(clerk_user_id)::text, 0));

-- name: InsertClerkWebhookEvent :one
with inserted as (
    insert into app.clerk_webhook_events (
        event_id,
        event_type,
        clerk_user_id,
        occurred_at
    ) values (
        sqlc.arg(event_id),
        sqlc.arg(event_type),
        sqlc.arg(clerk_user_id),
        sqlc.arg(occurred_at)
    )
    on conflict (event_id) do nothing
    returning true as inserted
)
select coalesce((select inserted from inserted), false)::boolean as inserted;

-- name: HasSupersedingClerkWebhookEvent :one
select exists (
    select 1
    from app.clerk_webhook_events
    where clerk_user_id = sqlc.arg(clerk_user_id)
      and event_id <> sqlc.arg(event_id)
      and (
          occurred_at > sqlc.arg(occurred_at)
          or (
              occurred_at = sqlc.arg(occurred_at)
              and (
                  case event_type
                      when 'user.deleted' then 3
                      when 'user.updated' then 2
                      when 'user.created' then 1
                      else 0
                  end > sqlc.arg(event_rank)
                  or (
                      case event_type
                          when 'user.deleted' then 3
                          when 'user.updated' then 2
                          when 'user.created' then 1
                          else 0
                      end = sqlc.arg(event_rank)
                      and event_id > sqlc.arg(event_id)
                  )
              )
          )
      )
) as has_superseding_event;
