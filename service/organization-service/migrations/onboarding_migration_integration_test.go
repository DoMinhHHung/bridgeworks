package migrations_test

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"

	organizationmigrations "github.com/DoMinhHHung/bridgeworks/service/organization-service/migrations"
	"github.com/pressly/goose/v3"

	_ "github.com/jackc/pgx/v5/stdlib"
)

func TestOnboardingMigrationPreservesExistingAuthorization(t *testing.T) {
	databaseURL := os.Getenv("ORGANIZATION_MIGRATION_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("ORGANIZATION_MIGRATION_TEST_DATABASE_URL is not set")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatalf("open migration test database: %v", err)
	}
	defer func() { _ = db.Close() }()

	if _, err := db.ExecContext(ctx, "drop schema if exists organization cascade"); err != nil {
		t.Fatalf("reset organization schema: %v", err)
	}
	if _, err := db.ExecContext(ctx, "create schema organization"); err != nil {
		t.Fatalf("create organization schema: %v", err)
	}
	provider, err := goose.NewProvider(
		goose.DialectPostgres,
		db,
		organizationmigrations.FS,
		goose.WithTableName(migrationTableName),
	)
	if err != nil {
		t.Fatalf("create migration provider: %v", err)
	}
	if _, err := provider.UpTo(ctx, 2); err != nil {
		t.Fatalf("apply migrations through v2: %v", err)
	}

	const organizationID = "018f0c76-8f6c-7cc4-8000-000000000031"
	const adminMembershipID = "018f0c76-8f6c-7cc4-8000-000000000032"
	if _, err := db.ExecContext(ctx, `
		insert into organization.organizations (
			id, clerk_organization_id, name, slug, status
		) values ($1, 'org-onboarding-existing', 'Existing Organization', 'existing', 'active')
	`, organizationID); err != nil {
		t.Fatalf("seed existing organization: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
		insert into organization.memberships (
			id, clerk_membership_id, organization_id, clerk_user_id,
			clerk_role, application_role, status
		) values ($1, 'mem-onboarding-admin', $2, 'user-existing-admin', 'org:admin', 'admin', 'active')
	`, adminMembershipID, organizationID); err != nil {
		t.Fatalf("seed existing admin membership: %v", err)
	}

	if _, err := provider.UpTo(ctx, 3); err != nil {
		t.Fatalf("apply migration v3: %v", err)
	}
	version, err := provider.GetDBVersion(ctx)
	if err != nil {
		t.Fatalf("read migration version: %v", err)
	}
	if version != 3 {
		t.Fatalf("migration version = %d, want 3", version)
	}

	var creator sql.NullString
	var ownerBootstrapped bool
	if err := db.QueryRowContext(ctx, `
		select clerk_created_by_user_id, owner_bootstrapped
		from organization.organizations
		where id = $1
	`, organizationID).Scan(&creator, &ownerBootstrapped); err != nil {
		t.Fatalf("read onboarding migration fields: %v", err)
	}
	if creator.Valid || ownerBootstrapped {
		t.Fatalf("legacy owner bootstrap = creator:%v bootstrapped:%v", creator, ownerBootstrapped)
	}
	assertLegacyApplicationRole(t, ctx, db, adminMembershipID, "admin")

	assertConstraintRejects(t, ctx, db,
		"update organization.organizations set clerk_created_by_user_id = '   ' where id = $1",
		organizationID,
	)
	assertConstraintRejects(t, ctx, db,
		"update organization.organizations set owner_bootstrapped = true where id = $1",
		organizationID,
	)
	if _, err := db.ExecContext(ctx, `
		update organization.organizations
		set clerk_created_by_user_id = 'user-authoritative-creator',
		    owner_bootstrapped = true
		where id = $1
	`, organizationID); err != nil {
		t.Fatalf("valid owner bootstrap state rejected: %v", err)
	}

	var publicGrantCount int
	if err := db.QueryRowContext(ctx, `
		select count(*)
		from information_schema.table_privileges
		where table_schema = 'organization'
		  and grantee = 'PUBLIC'
	`).Scan(&publicGrantCount); err != nil {
		t.Fatalf("read PUBLIC grants: %v", err)
	}
	if publicGrantCount != 0 {
		t.Fatalf("PUBLIC table grants = %d, want 0", publicGrantCount)
	}
}
