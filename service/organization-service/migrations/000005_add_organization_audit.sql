-- +goose Up

create table organization.audit_events (
    id uuid primary key,
    organization_id uuid not null,
    event_type text not null,
    actor_kind text not null,
    actor_identity_user_id uuid,
    actor_membership_id uuid,
    subject_membership_id uuid,
    from_value text,
    to_value text,
    occurred_at timestamptz not null default now(),

    constraint audit_events_id_uuidv7_ck
        check (substring(id::text from 15 for 1) = '7'),
    constraint audit_events_organization_fk
        foreign key (organization_id) references organization.organizations (id),
    constraint audit_events_event_type_ck check (event_type in (
        'organization.profile.updated',
        'organization.verification.requested',
        'organization.business_email.verified',
        'membership.invitation.requested',
        'membership.role.changed',
        'membership.removal.requested',
        'membership.leave.requested',
        'organization.ownership.transferred',
        'organization.verification.reviewed',
        'membership.activated',
        'membership.removal.completed'
    )),
    constraint audit_events_actor_kind_ck
        check (actor_kind in ('tenant_user', 'platform_admin', 'system')),
    constraint audit_events_actor_consistency_ck check (
        (actor_kind = 'tenant_user'
            and actor_identity_user_id is not null
            and actor_membership_id is not null)
        or
        (actor_kind = 'platform_admin'
            and actor_identity_user_id is not null
            and actor_membership_id is null)
        or
        (actor_kind = 'system'
            and actor_identity_user_id is null
            and actor_membership_id is null)
    ),
    constraint audit_events_from_value_ck check (
        from_value is null
        or (btrim(from_value) <> '' and length(from_value) <= 64)
    ),
    constraint audit_events_to_value_ck check (
        to_value is null
        or (btrim(to_value) <> '' and length(to_value) <= 64)
    )
);

comment on table organization.audit_events is
    'Append-only BridgeWorks product/security audit history. Provider identifiers, email addresses, tokens, secrets, and raw payloads are forbidden.';
comment on column organization.audit_events.actor_identity_user_id is
    'External Identity Service UUID reference without a cross-service foreign key.';
comment on column organization.audit_events.actor_membership_id is
    'Historical local membership UUID reference intentionally stored without a foreign key so immutable audit retention is not coupled to membership lifecycle.';
comment on column organization.audit_events.subject_membership_id is
    'Historical local membership UUID reference intentionally stored without a foreign key.';

create index audit_events_organization_occurred_idx
    on organization.audit_events (organization_id, occurred_at desc, id desc);

insert into organization.permissions (key)
values ('organization.audit.read')
on conflict (key) do nothing;

insert into organization.role_permissions (role_key, permission_key) values
    ('owner', 'organization.audit.read'),
    ('admin', 'organization.audit.read')
on conflict (role_key, permission_key) do nothing;

revoke all on table organization.audit_events from public;
revoke all on table organization.permissions from public;
revoke all on table organization.role_permissions from public;

-- +goose Down

select 1 / 0;
