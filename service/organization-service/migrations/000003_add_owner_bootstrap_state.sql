-- +goose Up

-- Forward-only migration. The creator projection is private provider metadata used
-- only to bootstrap the first BridgeWorks owner exactly once. It is never exposed
-- as client authorization authority.

alter table organization.organizations
    add column clerk_created_by_user_id text,
    add column owner_bootstrapped boolean not null default false;

alter table organization.organizations
    add constraint organizations_clerk_created_by_user_id_not_blank_ck
        check (clerk_created_by_user_id is null or btrim(clerk_created_by_user_id) <> ''),
    add constraint organizations_owner_bootstrap_consistency_ck
        check (not owner_bootstrapped or clerk_created_by_user_id is not null);

revoke all privileges on table organization.organizations from public;
