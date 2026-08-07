-- name: GetPlatformAccessUserByClerkUserID :one
select
    id,
    status
from app.app_users
where clerk_user_id = sqlc.arg(clerk_user_id);

-- name: GetPlatformAccessUserByIDUser :one
select
    id,
    status
from app.app_users
where id_user = sqlc.arg(id_user);

-- name: ListActivePlatformRolesByUserID :many
select role
from app.platform_access_assignments
where identity_user_id = sqlc.arg(identity_user_id)
  and revoked_at is null
order by role;

-- name: GrantPlatformAccess :exec
insert into app.platform_access_assignments (
    identity_user_id,
    role
) values (
    sqlc.arg(identity_user_id),
    sqlc.arg(role)
)
on conflict (identity_user_id, role) do update
set
    granted_at = now(),
    revoked_at = null
where app.platform_access_assignments.revoked_at is not null;

-- name: RevokePlatformAccess :exec
update app.platform_access_assignments
set revoked_at = now()
where identity_user_id = sqlc.arg(identity_user_id)
  and role = sqlc.arg(role)
  and revoked_at is null;

-- name: GetPlatformAccessAssignment :one
select
    granted_at,
    revoked_at,
    updated_at
from app.platform_access_assignments
where identity_user_id = sqlc.arg(identity_user_id)
  and role = sqlc.arg(role);
