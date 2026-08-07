-- +goose Up

alter table organization.organizations
    add column business_email_domain text,
    add column business_email_verified_at timestamptz,
    add column business_email_verified_by_user_id uuid;

alter table organization.organizations
    add constraint organizations_business_email_verification_ck check (
        (business_email_domain is null
            and business_email_verified_at is null
            and business_email_verified_by_user_id is null)
        or
        (business_email_domain is not null
            and btrim(business_email_domain) = business_email_domain
            and business_email_domain = lower(business_email_domain)
            and btrim(business_email_domain) <> ''
            and business_email_verified_at is not null
            and business_email_verified_by_user_id is not null)
    );

create table organization.membership_invitation_intents (
    id uuid primary key,
    organization_id uuid not null,
    application_role text not null,
    created_by_identity_user_id uuid not null,
    consumed_membership_id uuid,
    consumed_at timestamptz,
    created_at timestamptz not null default now(),
    updated_at timestamptz not null default now(),

    constraint membership_invitation_intents_id_uuidv7_ck
        check (substring(id::text from 15 for 1) = '7'),
    constraint membership_invitation_intents_organization_fk
        foreign key (organization_id) references organization.organizations (id),
    constraint membership_invitation_intents_role_fk
        foreign key (application_role) references organization.roles (key),
    constraint membership_invitation_intents_consumption_ck
        check (
            (consumed_membership_id is null and consumed_at is null)
            or
            (consumed_membership_id is not null and consumed_at is not null)
        ),
    constraint membership_invitation_intents_consumed_membership_fk
        foreign key (consumed_membership_id) references organization.memberships (id),
    constraint membership_invitation_intents_timestamps_ck
        check (updated_at >= created_at)
);

create table organization.membership_removal_intents (
    membership_id uuid primary key,
    organization_id uuid not null,
    requested_by_identity_user_id uuid not null,
    created_at timestamptz not null default now(),

    constraint membership_removal_intents_membership_fk
        foreign key (membership_id) references organization.memberships (id),
    constraint membership_removal_intents_organization_fk
        foreign key (organization_id) references organization.organizations (id)
);

-- +goose StatementBegin
create trigger membership_invitation_intents_set_updated_at
before update on organization.membership_invitation_intents
for each row execute function organization.set_updated_at();
-- +goose StatementEnd

revoke all on table organization.membership_invitation_intents from public;
revoke all on table organization.membership_removal_intents from public;

-- +goose Down

select 1 / 0;
