-- name: GetAppUserByClerkUserID :one
select
    id,
    clerk_user_id,
    primary_email,
    id_user,
    status,
    created_at,
    updated_at
from app.app_users
where clerk_user_id = sqlc.arg(clerk_user_id);

-- name: InsertAppUser :exec
insert into app.app_users (
    id,
    clerk_user_id,
    primary_email,
    id_user,
    status
) values (
    sqlc.arg(id),
    sqlc.arg(clerk_user_id),
    sqlc.narg(primary_email)::text::extensions.citext,
    sqlc.arg(id_user),
    sqlc.arg(status)
);

-- name: UpdateAppUserPrimaryEmail :exec
update app.app_users
set primary_email = sqlc.narg(primary_email)::text::extensions.citext
where clerk_user_id = sqlc.arg(clerk_user_id);

-- name: MarkAppUserDeleted :exec
update app.app_users
set
    status = 'deleted',
    primary_email = null
where clerk_user_id = sqlc.arg(clerk_user_id);
