-- +goose Up

create table app.platform_access_assignments (
    identity_user_id uuid not null,
    role text not null,
    granted_at timestamptz not null default now(),
    revoked_at timestamptz,
    updated_at timestamptz not null default now(),

    constraint platform_access_assignments_pk
        primary key (identity_user_id, role),
    constraint platform_access_assignments_identity_user_fk
        foreign key (identity_user_id) references app.app_users (id),
    constraint platform_access_assignments_role_ck
        check (role in ('platform_admin')),
    constraint platform_access_assignments_timestamps_ck
        check (
            updated_at >= granted_at
            and (revoked_at is null or revoked_at >= granted_at)
        )
);

comment on table app.platform_access_assignments is
    'BridgeWorks-owned global platform access. Tenant organization roles are not authority for this table.';
comment on column app.platform_access_assignments.identity_user_id is
    'Identity Service local UUID authority key. This relation never references Organization Service data.';
comment on column app.platform_access_assignments.role is
    'Global BridgeWorks platform role. PR3.5 supports only platform_admin.';

create index platform_access_assignments_active_user_idx
    on app.platform_access_assignments (identity_user_id)
    where revoked_at is null;

create trigger platform_access_assignments_set_updated_at
before update on app.platform_access_assignments
for each row
execute function app.set_updated_at();

revoke all on table app.platform_access_assignments from public;

-- +goose Down

select 1 / 0;
