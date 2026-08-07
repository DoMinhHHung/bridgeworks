-- +goose Up

-- Forward-only migration: intentionally no -- +goose Down section. Removing these
-- columns or catalog rows would discard BridgeWorks-owned organization state and can
-- invalidate existing memberships.

alter table organization.organizations
    add column legal_name text,
    add column website text,
    add column country text,
    add column company_type text,
    add column verification_status text not null default 'unverified',
    add column trust_status text not null default 'unassessed';

alter table organization.organizations
    add constraint organizations_legal_name_not_blank_ck
        check (legal_name is null or btrim(legal_name) <> ''),
    add constraint organizations_website_not_blank_ck
        check (website is null or btrim(website) <> ''),
    add constraint organizations_country_not_blank_ck
        check (country is null or btrim(country) <> ''),
    add constraint organizations_company_type_not_blank_ck
        check (company_type is null or btrim(company_type) <> ''),
    add constraint organizations_verification_status_ck
        check (verification_status in ('unverified', 'pending', 'verified', 'rejected')),
    add constraint organizations_trust_status_ck
        check (trust_status in ('unassessed'));

insert into organization.roles (key) values
    ('owner'),
    ('recruiter'),
    ('delivery_manager')
on conflict (key) do nothing;

insert into organization.permissions (key) values
    ('organization.verify.request'),
    ('membership.invite'),
    ('membership.role.manage')
on conflict (key) do nothing;

insert into organization.role_permissions (role_key, permission_key) values
    ('owner', 'organization.read'),
    ('owner', 'organization.manage'),
    ('owner', 'organization.verify.request'),
    ('owner', 'membership.read'),
    ('owner', 'membership.invite'),
    ('owner', 'membership.manage'),
    ('owner', 'membership.role.manage'),
    ('admin', 'organization.verify.request'),
    ('admin', 'membership.invite'),
    ('admin', 'membership.role.manage'),
    ('recruiter', 'organization.read'),
    ('delivery_manager', 'organization.read')
on conflict (role_key, permission_key) do nothing;

revoke all privileges on table organization.organizations from public;
revoke all privileges on table organization.roles from public;
revoke all privileges on table organization.permissions from public;
revoke all privileges on table organization.role_permissions from public;
revoke all privileges on table organization.memberships from public;
revoke all privileges on table organization.clerk_webhook_events from public;
