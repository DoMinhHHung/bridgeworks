package migrations_test

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"
)

const (
	administrationOrganizationID = "018f0c76-8f6c-7cc4-8000-000000000061"
	administrationMembershipID   = "018f0c76-8f6c-7cc4-8000-000000000062"
	administrationInvitationID   = "018f0c76-8f6c-7cc4-8000-000000000063"
	administrationActorID        = "018f0c76-8f6c-7cc4-8000-000000000064"
)

func TestMembershipAdministrationMigrationPreservesAuthorizationAndAddsPrivateState(t *testing.T) {
	databaseURL := os.Getenv("ORGANIZATION_MIGRATION_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("ORGANIZATION_MIGRATION_TEST_DATABASE_URL is not set")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db, provider := resetOrganizationMigrationDatabase(t, ctx, databaseURL)
	defer func() { _ = db.Close() }()

	if _, err := provider.UpTo(ctx, 3); err != nil {
		t.Fatalf("apply migrations through v3: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
		insert into organization.organizations (
			id, clerk_organization_id, name, slug, status,
			clerk_created_by_user_id, owner_bootstrapped, owner_bootstrap_eligible
		) values ($1, 'org-administration-migration', 'Administration Migration', 'administration-migration',
		          'active', 'user-administration-owner', true, true)
	`, administrationOrganizationID); err != nil {
		t.Fatalf("seed v3 organization: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
		insert into organization.memberships (
			id, clerk_membership_id, organization_id, clerk_user_id,
			clerk_role, application_role, status
		) values ($1, 'mem-administration-owner', $2, 'user-administration-owner',
		          'org:admin', 'owner', 'active')
	`, administrationMembershipID, administrationOrganizationID); err != nil {
		t.Fatalf("seed v3 owner membership: %v", err)
	}

	if _, err := provider.UpTo(ctx, 4); err != nil {
		t.Fatalf("apply migration v4: %v", err)
	}
	version, err := provider.GetDBVersion(ctx)
	if err != nil {
		t.Fatalf("read migration version: %v", err)
	}
	if version != 4 {
		t.Fatalf("migration version = %d, want 4", version)
	}

	var role, creator string
	var bootstrapped, eligible bool
	var domain, verifiedAt, verifiedBy sql.NullString
	if err := db.QueryRowContext(ctx, `
		select m.application_role, o.clerk_created_by_user_id,
		       o.owner_bootstrapped, o.owner_bootstrap_eligible,
		       o.business_email_domain,
		       o.business_email_verified_at::text,
		       o.business_email_verified_by_user_id::text
		from organization.organizations o
		join organization.memberships m on m.organization_id = o.id
		where o.id = $1 and m.id = $2
	`, administrationOrganizationID, administrationMembershipID).Scan(
		&role, &creator, &bootstrapped, &eligible, &domain, &verifiedAt, &verifiedBy,
	); err != nil {
		t.Fatalf("read upgraded authorization state: %v", err)
	}
	if role != "owner" || creator != "user-administration-owner" || !bootstrapped || !eligible {
		t.Fatalf("v4 changed owner authorization state: role=%q creator=%q bootstrapped=%v eligible=%v", role, creator, bootstrapped, eligible)
	}
	if domain.Valid || verifiedAt.Valid || verifiedBy.Valid {
		t.Fatalf("v4 business proof defaults must be null: %v %v %v", domain, verifiedAt, verifiedBy)
	}

	for _, table := range []string{"membership_invitation_intents", "membership_removal_intents"} {
		var exists bool
		if err := db.QueryRowContext(ctx, `
			select to_regclass('organization.' || $1) is not null
		`, table).Scan(&exists); err != nil {
			t.Fatalf("read %s existence: %v", table, err)
		}
		if !exists {
			t.Fatalf("table %s missing", table)
		}
	}

	assertConstraintRejects(t, ctx, db,
		"update organization.organizations set business_email_domain = 'company.example' where id = $1",
		administrationOrganizationID,
	)
	if _, err := db.ExecContext(ctx, `
		update organization.organizations
		set business_email_domain = 'company.example',
		    business_email_verified_at = now(),
		    business_email_verified_by_user_id = $2
		where id = $1
	`, administrationOrganizationID, administrationActorID); err != nil {
		t.Fatalf("valid business email proof rejected: %v", err)
	}

	if _, err := db.ExecContext(ctx, `
		insert into organization.membership_invitation_intents (
			id, organization_id, application_role, created_by_identity_user_id
		) values ($1, $2, 'recruiter', $3)
	`, administrationInvitationID, administrationOrganizationID, administrationActorID); err != nil {
		t.Fatalf("insert invitation intent: %v", err)
	}

	var identityForeignKeys int
	if err := db.QueryRowContext(ctx, `
		select count(*)
		from information_schema.referential_constraints rc
		join information_schema.key_column_usage kcu
		  on kcu.constraint_catalog = rc.constraint_catalog
		 and kcu.constraint_schema = rc.constraint_schema
		 and kcu.constraint_name = rc.constraint_name
		where rc.constraint_schema = 'organization'
		  and kcu.column_name in ('created_by_identity_user_id', 'requested_by_identity_user_id', 'business_email_verified_by_user_id')
	`).Scan(&identityForeignKeys); err != nil {
		t.Fatalf("read external-reference foreign keys: %v", err)
	}
	if identityForeignKeys != 0 {
		t.Fatalf("external Identity references have %d database foreign keys, want 0", identityForeignKeys)
	}

	var publicGrantCount int
	if err := db.QueryRowContext(ctx, `
		select count(*)
		from information_schema.table_privileges
		where table_schema = 'organization'
		  and table_name in ('membership_invitation_intents', 'membership_removal_intents')
		  and grantee = 'PUBLIC'
	`).Scan(&publicGrantCount); err != nil {
		t.Fatalf("read administration PUBLIC grants: %v", err)
	}
	if publicGrantCount != 0 {
		t.Fatalf("administration PUBLIC grants = %d, want 0", publicGrantCount)
	}
}
