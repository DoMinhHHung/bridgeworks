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

const (
	legacyOrganizationID       = "018f0c76-8f6c-7cc4-8000-000000000031"
	legacyAdminMembershipID    = "018f0c76-8f6c-7cc4-8000-000000000032"
	legacyViewerMembershipID   = "018f0c76-8f6c-7cc4-8000-000000000033"
	postV3OrganizationID       = "018f0c76-8f6c-7cc4-8000-000000000034"
	legacyClerkOrganizationID  = "org_onboarding_legacy"
	legacyAdminClerkUserID     = "user_onboarding_legacy_admin"
	legacyViewerClerkUserID    = "user_onboarding_legacy_creator"
	legacyAdminClerkMembership = "mem_onboarding_legacy_admin"
	legacyViewerClerkMembership = "mem_onboarding_legacy_viewer"
)

func TestOnboardingMigrationPreservesExistingAuthorization(t *testing.T) {
	databaseURL := os.Getenv("ORGANIZATION_MIGRATION_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("ORGANIZATION_MIGRATION_TEST_DATABASE_URL is not set")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db, provider := resetOrganizationMigrationDatabase(t, ctx, databaseURL)
	defer func() { _ = db.Close() }()

	if _, err := provider.UpTo(ctx, 2); err != nil {
		t.Fatalf("apply migrations through v2: %v", err)
	}
	seedLegacyOwnerBootstrapRows(t, ctx, db)

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

	assertLegacyBootstrapState(t, ctx, db)
	assertLegacyApplicationRole(t, ctx, db, legacyAdminMembershipID, "admin")
	assertLegacyApplicationRole(t, ctx, db, legacyViewerMembershipID, "viewer")

	var newEligible bool
	var newBootstrapped bool
	var newCreator sql.NullString
	if err := db.QueryRowContext(ctx, `
		insert into organization.organizations (
			id, clerk_organization_id, name, slug, status
		) values ($1, 'org-onboarding-post-v3', 'Post V3 Organization', 'post-v3', 'active')
		returning owner_bootstrap_eligible, owner_bootstrapped, clerk_created_by_user_id
	`, postV3OrganizationID).Scan(&newEligible, &newBootstrapped, &newCreator); err != nil {
		t.Fatalf("insert post-v3 organization: %v", err)
	}
	if !newEligible || newBootstrapped || newCreator.Valid {
		t.Fatalf("post-v3 bootstrap defaults = eligible:%v bootstrapped:%v creator:%v", newEligible, newBootstrapped, newCreator)
	}

	assertConstraintRejects(t, ctx, db,
		"update organization.organizations set clerk_created_by_user_id = '   ' where id = $1",
		legacyOrganizationID,
	)
	assertConstraintRejects(t, ctx, db,
		"update organization.organizations set clerk_created_by_user_id = 'user-authoritative-creator', owner_bootstrapped = true where id = $1",
		legacyOrganizationID,
	)
	if _, err := db.ExecContext(ctx, `
		update organization.organizations
		set clerk_created_by_user_id = 'user-authoritative-creator',
		    owner_bootstrapped = true
		where id = $1
	`, postV3OrganizationID); err != nil {
		t.Fatalf("valid eligible owner bootstrap state rejected: %v", err)
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

// TestPrepareLegacyOwnerBootstrapWebhookFixture intentionally leaves the migrated
// database intact. Organization CI runs it against the same PostgreSQL database
// that the subsequent signed Clerk webhook regression uses, proving the legacy
// row really existed at v2 before v3 marked it ineligible.
func TestPrepareLegacyOwnerBootstrapWebhookFixture(t *testing.T) {
	databaseURL := os.Getenv("ORGANIZATION_LEGACY_WEBHOOK_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("ORGANIZATION_LEGACY_WEBHOOK_TEST_DATABASE_URL is not set")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db, provider := resetOrganizationMigrationDatabase(t, ctx, databaseURL)
	defer func() { _ = db.Close() }()

	if _, err := provider.UpTo(ctx, 2); err != nil {
		t.Fatalf("apply migrations through v2: %v", err)
	}
	seedLegacyOwnerBootstrapRows(t, ctx, db)
	if _, err := provider.UpTo(ctx, 3); err != nil {
		t.Fatalf("apply migration v3: %v", err)
	}

	assertLegacyBootstrapState(t, ctx, db)
	assertLegacyApplicationRole(t, ctx, db, legacyAdminMembershipID, "admin")
	assertLegacyApplicationRole(t, ctx, db, legacyViewerMembershipID, "viewer")
}

func resetOrganizationMigrationDatabase(t *testing.T, ctx context.Context, databaseURL string) (*sql.DB, *goose.Provider) {
	t.Helper()
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatalf("open migration test database: %v", err)
	}
	if _, err := db.ExecContext(ctx, "drop schema if exists organization cascade"); err != nil {
		_ = db.Close()
		t.Fatalf("reset organization schema: %v", err)
	}
	if _, err := db.ExecContext(ctx, "create schema organization"); err != nil {
		_ = db.Close()
		t.Fatalf("create organization schema: %v", err)
	}
	provider, err := goose.NewProvider(
		goose.DialectPostgres,
		db,
		organizationmigrations.FS,
		goose.WithTableName(migrationTableName),
	)
	if err != nil {
		_ = db.Close()
		t.Fatalf("create migration provider: %v", err)
	}
	return db, provider
}

func seedLegacyOwnerBootstrapRows(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	if _, err := db.ExecContext(ctx, `
		insert into organization.organizations (
			id, clerk_organization_id, name, slug, status
		) values ($1, $2, 'Existing Organization', 'existing', 'active')
	`, legacyOrganizationID, legacyClerkOrganizationID); err != nil {
		t.Fatalf("seed existing organization: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
		insert into organization.memberships (
			id, clerk_membership_id, organization_id, clerk_user_id,
			clerk_role, application_role, status
		) values
			($1, $2, $3, $4, 'org:admin', 'admin', 'active'),
			($5, $6, $3, $7, 'org:member', 'viewer', 'active')
	`,
		legacyAdminMembershipID,
		legacyAdminClerkMembership,
		legacyOrganizationID,
		legacyAdminClerkUserID,
		legacyViewerMembershipID,
		legacyViewerClerkMembership,
		legacyViewerClerkUserID,
	); err != nil {
		t.Fatalf("seed existing memberships: %v", err)
	}
}

func assertLegacyBootstrapState(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	var creator sql.NullString
	var ownerBootstrapped bool
	var ownerBootstrapEligible bool
	if err := db.QueryRowContext(ctx, `
		select clerk_created_by_user_id, owner_bootstrapped, owner_bootstrap_eligible
		from organization.organizations
		where id = $1
	`, legacyOrganizationID).Scan(&creator, &ownerBootstrapped, &ownerBootstrapEligible); err != nil {
		t.Fatalf("read onboarding migration fields: %v", err)
	}
	if creator.Valid || ownerBootstrapped || ownerBootstrapEligible {
		t.Fatalf(
			"legacy owner bootstrap = creator:%v bootstrapped:%v eligible:%v",
			creator,
			ownerBootstrapped,
			ownerBootstrapEligible,
		)
	}
}
