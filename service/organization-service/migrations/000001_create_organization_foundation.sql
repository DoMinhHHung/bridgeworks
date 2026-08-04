-- +goose Up

create schema if not exists organization;
comment on schema organization is
    'Owned exclusively by BridgeWorks organization-service. Other services must not access its tables directly.';

create table organization.organizations (
    id uuid primary key,
    clerk_organization_id text not null,
    name text,
    slug text,
    status text not null,
    created_at timestamptz not null default now(),
    updated_at timestamptz not null default now(),

    constraint organizations_clerk_organization_id_uq unique (clerk_organization_id),
    constraint organizations_id_uuidv7_ck check (substring(id::text from 15 for 1) = '7'),
    constraint organizations_clerk_organization_id_not_blank_ck check (btrim(clerk_organization_id) <> ''),
    constraint organizations_name_not_blank_ck check (name is null or btrim(name) <> ''),
    constraint organizations_slug_not_blank_ck check (slug is null or btrim(slug) <> ''),
    constraint organizations_status_ck check (status in ('pending', 'active', 'disabled', 'deleted')),
    constraint organizations_timestamps_ck check (updated_at >= created_at)
);

create index organizations_status_idx on organization.organizations (status);

create table organization.roles (
    key text primary key,
    created_at timestamptz not null default now(),
    updated_at timestamptz not null default now(),

    constraint roles_key_not_blank_ck check (btrim(key) <> ''),
    constraint roles_timestamps_ck check (updated_at >= created_at)
);

create table organization.permissions (
    key text primary key,
    created_at timestamptz not null default now(),
    updated_at timestamptz not null default now(),

    constraint permissions_key_not_blank_ck check (btrim(key) <> ''),
    constraint permissions_timestamps_ck check (updated_at >= created_at)
);

create table organization.role_permissions (
    role_key text not null,
    permission_key text not null,
    created_at timestamptz not null default now(),

    constraint role_permissions_pk primary key (role_key, permission_key),
    constraint role_permissions_role_fk foreign key (role_key) references organization.roles (key),
    constraint role_permissions_permission_fk foreign key (permission_key) references organization.permissions (key)
);

create table organization.memberships (
    id uuid primary key,
    clerk_membership_id text not null,
    organization_id uuid not null,
    clerk_user_id text not null,
    clerk_role text,
    application_role text not null,
    status text not null,
    created_at timestamptz not null default now(),
    updated_at timestamptz not null default now(),

    constraint memberships_clerk_membership_id_uq unique (clerk_membership_id),
    constraint memberships_id_uuidv7_ck check (substring(id::text from 15 for 1) = '7'),
    constraint memberships_organization_fk foreign key (organization_id) references organization.organizations (id),
    constraint memberships_application_role_fk foreign key (application_role) references organization.roles (key),
    constraint memberships_clerk_membership_id_not_blank_ck check (btrim(clerk_membership_id) <> ''),
    constraint memberships_clerk_user_id_not_blank_ck check (btrim(clerk_user_id) <> ''),
    constraint memberships_clerk_role_not_blank_ck check (clerk_role is null or btrim(clerk_role) <> ''),
    constraint memberships_status_ck check (status in ('active', 'deleted')),
    constraint memberships_timestamps_ck check (updated_at >= created_at)
);

create unique index memberships_active_organization_user_uq
    on organization.memberships (organization_id, clerk_user_id)
    where status = 'active';
create index memberships_organization_user_status_idx
    on organization.memberships (organization_id, clerk_user_id, status);
create index memberships_organization_status_idx
    on organization.memberships (organization_id, status);
create index memberships_clerk_user_id_idx
    on organization.memberships (clerk_user_id);

create table organization.clerk_webhook_events (
    event_id text primary key,
    event_type text not null,
    aggregate_type text not null,
    aggregate_id text not null,
    clerk_organization_id text not null,
    occurred_at timestamptz not null,
    processed_at timestamptz not null default now(),

    constraint clerk_webhook_events_event_id_not_blank_ck check (btrim(event_id) <> ''),
    constraint clerk_webhook_events_event_type_ck check (event_type in (
        'organization.created',
        'organization.updated',
        'organization.deleted',
        'organization_membership.created',
        'organization_membership.updated',
        'organization_membership.deleted'
    )),
    constraint clerk_webhook_events_aggregate_type_ck check (aggregate_type in ('organization', 'membership')),
    constraint clerk_webhook_events_aggregate_id_not_blank_ck check (btrim(aggregate_id) <> ''),
    constraint clerk_webhook_events_clerk_organization_id_not_blank_ck check (btrim(clerk_organization_id) <> '')
);

create index clerk_webhook_events_aggregate_occurred_idx
    on organization.clerk_webhook_events (aggregate_type, aggregate_id, occurred_at desc);

insert into organization.roles (key) values ('admin'), ('viewer');

insert into organization.permissions (key) values
    ('organization.read'),
    ('organization.manage'),
    ('membership.read'),
    ('membership.manage');

insert into organization.role_permissions (role_key, permission_key) values
    ('admin', 'organization.read'),
    ('admin', 'organization.manage'),
    ('admin', 'membership.read'),
    ('admin', 'membership.manage'),
    ('viewer', 'organization.read');

-- +goose StatementBegin
create or replace function organization.set_updated_at()
returns trigger
language plpgsql
security invoker
set search_path = ''
as $$
begin
    new.updated_at = now();
    return new;
end;
$$;
-- +goose StatementEnd

create trigger organizations_set_updated_at
before update on organization.organizations
for each row execute function organization.set_updated_at();

create trigger roles_set_updated_at
before update on organization.roles
for each row execute function organization.set_updated_at();

create trigger permissions_set_updated_at
before update on organization.permissions
for each row execute function organization.set_updated_at();

create trigger memberships_set_updated_at
before update on organization.memberships
for each row execute function organization.set_updated_at();

revoke all on schema organization from public;
revoke all on all tables in schema organization from public;
revoke all on function organization.set_updated_at() from public;

-- +goose Down

drop trigger if exists memberships_set_updated_at on organization.memberships;
drop trigger if exists permissions_set_updated_at on organization.permissions;
drop trigger if exists roles_set_updated_at on organization.roles;
drop trigger if exists organizations_set_updated_at on organization.organizations;
drop table if exists organization.clerk_webhook_events;
drop table if exists organization.memberships;
drop table if exists organization.role_permissions;
drop table if exists organization.permissions;
drop table if exists organization.roles;
drop table if exists organization.organizations;
drop function if exists organization.set_updated_at();
