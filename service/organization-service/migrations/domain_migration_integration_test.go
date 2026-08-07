package migrations_test

import (
	"context"
	"database/sql"
	"os"
	"reflect"
	"sort"
	"testing"
	"time"

	organizationmigrations "github.com/DoMinhHHung/bridgeworks/service/organization-service/migrations"
	"github.com/pressly/goose/v3"

	_ "github.com/jackc/pgx/v5/stdlib"
)

const migrationTableName = "organization.goose_db_version"

func TestDomainMigrationUpgradesExistingRowsSafely(t *testing.T) {
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

	if _, err := provider.UpTo(ctx, 1); err != nil {
		t.Fatalf("apply migration v1: %v", err)
	}

	const organizationID = "018f0c76-8f6c-7cc4-8000-000000000021"
	const viewerMembershipID = "018f0c76-8f6c-7cc4-8000-000000000022"
	const adminMembershipID = "018f0c76-8f6c-7cc4-8000-000000000023"
	if _, err := db.ExecContext(ctx, `
		insert into organization.organizations (
			id, clerk_organization_id, name, slug, status
		) values ($1, 'org-migration-existing', 'Existing Organization', 'existing-organization', 'active')
	`, organizationID); err != nil {
		t.Fatalf("seed existing organization: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
		insert into organization.memberships (
			id, clerk_membership_id, organization_id, clerk_user_id,
			clerk_role, application_role, status
		) values
			($1, 'mem-migration-viewer', $3, 'user-migration-viewer', 'org:member', 'viewer', 'active'),
			($2, 'mem-migration-admin', $3, 'user-migration-admin', 'org:admin', 'admin', 'active')
	`, viewerMembershipID, adminMembershipID, organizationID); err != nil {
		t.Fatalf("seed existing memberships: %v", err)
	}

	if _, err := provider.UpTo(ctx, 2); err != nil {
		t.Fatalf("apply migration v2: %v", err)
	}

	version, err := provider.GetDBVersion(ctx)
	if err != nil {
		t.Fatalf("read migration version: %v", err)
	}
	if version != 2 {
		t.Fatalf("migration version = %d, want 2", version)
	}

	var (
		verificationStatus string
		trustStatus        string
		legalName          sql.NullString
		website            sql.NullString
		country            sql.NullString
		companyType        sql.NullString
	)
	if err := db.QueryRowContext(ctx, `
		select verification_status, trust_status, legal_name, website, country, company_type
		from organization.organizations
		where id = $1
	`, organizationID).Scan(
		&verificationStatus,
		&trustStatus,
		&legalName,
		&website,
		&country,
		&companyType,
	); err != nil {
		t.Fatalf("read migrated organization: %v", err)
	}
	if verificationStatus != "unverified" || trustStatus != "unassessed" {
		t.Fatalf("migrated statuses = %q/%q", verificationStatus, trustStatus)
	}
	if legalName.Valid || website.Valid || country.Valid || companyType.Valid {
		t.Fatal("new optional product fields must remain null for existing rows")
	}

	assertLegacyApplicationRole(t, ctx, db, viewerMembershipID, "viewer")
	assertLegacyApplicationRole(t, ctx, db, adminMembershipID, "admin")

	assertStringSet(t, ctx, db,
		"select key from organization.roles order by key",
		[]string{"admin", "delivery_manager", "owner", "recruiter", "viewer"},
	)
	assertStringSet(t, ctx, db,
		"select key from organization.permissions order by key",
		[]string{
			"membership.invite",
			"membership.manage",
			"membership.read",
			"membership.role.manage",
			"organization.manage",
			"organization.read",
			"organization.verify.request",
		},
	)

	assertRolePermissions(t, ctx, db, "owner", []string{
		"membership.invite",
		"membership.manage",
		"membership.read",
		"membership.role.manage",
		"organization.manage",
		"organization.read",
		"organization.verify.request",
	})
	assertRolePermissions(t, ctx, db, "admin", []string{
		"membership.invite",
		"membership.manage",
		"membership.read",
		"membership.role.manage",
		"organization.manage",
		"organization.read",
		"organization.verify.request",
	})
	assertRolePermissions(t, ctx, db, "recruiter", []string{"organization.read"})
	assertRolePermissions(t, ctx, db, "delivery_manager", []string{"organization.read"})
	assertRolePermissions(t, ctx, db, "viewer", []string{"organization.read"})

	assertConstraintRejects(t, ctx, db,
		"update organization.organizations set verification_status = 'approved' where id = $1",
		organizationID,
	)
	assertConstraintRejects(t, ctx, db,
		"update organization.organizations set trust_status = 'trusted' where id = $1",
		organizationID,
	)
	assertConstraintRejects(t, ctx, db,
		"update organization.organizations set legal_name = '   ' where id = $1",
		organizationID,
	)

	var publicGrantCount int
	if err := db.QueryRowContext(ctx, `
		select count(*)
		from information_schema.role_table_grants
		where table_schema = 'organization'
		  and grantee = 'PUBLIC'
	`).Scan(&publicGrantCount); err != nil {
		t.Fatalf("read PUBLIC grants: %v", err)
	}
	if publicGrantCount != 0 {
		t.Fatalf("PUBLIC table grants = %d, want 0", publicGrantCount)
	}
}

func assertLegacyApplicationRole(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
	membershipID string,
	want string,
) {
	t.Helper()
	var got string
	if err := db.QueryRowContext(ctx, `
		select application_role
		from organization.memberships
		where id = $1
	`, membershipID).Scan(&got); err != nil {
		t.Fatalf("read migrated membership role: %v", err)
	}
	if got != want {
		t.Fatalf("existing application role = %q, want %q", got, want)
	}
}

func assertStringSet(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
	query string,
	want []string,
	args ...any,
) {
	t.Helper()
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		t.Fatalf("query set: %v", err)
	}
	defer func() { _ = rows.Close() }()

	var got []string
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			t.Fatalf("scan set: %v", err)
		}
		got = append(got, value)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate set: %v", err)
	}
	wantSorted := append([]string(nil), want...)
	sort.Strings(wantSorted)
	if !reflect.DeepEqual(got, wantSorted) {
		t.Fatalf("set = %v, want %v", got, wantSorted)
	}
}

func assertRolePermissions(t *testing.T, ctx context.Context, db *sql.DB, role string, want []string) {
	t.Helper()
	assertStringSet(
		t,
		ctx,
		db,
		"select permission_key from organization.role_permissions where role_key = $1 order by permission_key",
		want,
		role,
	)
}

func assertConstraintRejects(t *testing.T, ctx context.Context, db *sql.DB, statement string, organizationID string) {
	t.Helper()
	if _, err := db.ExecContext(ctx, statement, organizationID); err == nil {
		t.Fatalf("constraint accepted invalid statement: %s", statement)
	}
}
