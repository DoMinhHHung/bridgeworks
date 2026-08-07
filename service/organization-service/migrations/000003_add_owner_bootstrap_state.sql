-- +goose Up

-- Forward-only migration. Legacy organizations must never gain BridgeWorks owner
-- authority merely because a later Clerk organization payload exposes the
-- historical provider creator. Existing rows therefore receive an explicit
-- ineligible marker before the default is changed for rows inserted after v3.
alter table organization.organizations
    add column clerk_created_by_user_id text,
    add column owner_bootstrapped boolean not null default false,
    add column owner_bootstrap_eligible boolean not null default false;

alter table organization.organizations
    alter column owner_bootstrap_eligible set default true;

alter table organization.organizations
    add constraint organizations_clerk_created_by_user_id_not_blank_ck
        check (clerk_created_by_user_id is null or btrim(clerk_created_by_user_id) <> ''),
    add constraint organizations_owner_bootstrap_consistency_ck
        check (
            not owner_bootstrapped
            or (
                owner_bootstrap_eligible
                and clerk_created_by_user_id is not null
            )
        );

revoke all privileges on table organization.organizations from public;
