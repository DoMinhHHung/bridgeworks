package migrations_test

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"
)

const (
	auditMigrationOrganizationID       = "018f0c76-8f6c-7cc4-8000-000000000071"
	auditMigrationVerifiedOrgID        = "018f0c76-8f6c-7cc4-8000-000000000072"
	auditMigrationRejectedOrgID        = "018f0c76-8f6c-7cc4-8000-000000000073"
	auditMigrationOwnerMembershipID    = "018f0c76-8f6c-7cc4-8000-000000000074"
	auditMigrationAdminMembershipID    = "018f0c76-8f6c-7cc4-8000-000000000075"
	auditMigrationViewerMembershipID   = "018f0c76-8f6c-7cc4-8000-000000000076"
	auditMigrationInvitationID         = "018f0c76-8f6c-7cc4-8000-000000000077"
	auditMigrationConsumedInvitationID = "018f0c76-8f6c-7cc4-8000-000000000078"
	auditMigrationRemovalMembershipID  = "018f0c76-8f6c-7cc4-8000-000000000079"
	auditMigrationActorID              = "018f0c76-8f6c-7cc4-8000-00000000007a"
	auditMigrationEventID              = "018f0c76-8f6c-7cc4-8000-00000000007b"
)

func TestOrganizationAuditMigrationPreservesV4StateAndAddsBoundedAuditAuthority(t *testing.T) {
	databaseURL := os.Getenv("ORGANIZATION_MIGRATION_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("ORGANIZATION_MIGRATION_TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	db, provider := resetOrganizationMigrationDatabase(t, ctx, databaseURL)
	defer func() { _ = db.Close() }()

	if _, err := provider.UpTo(ctx, 4); err != nil {
		t.Fatalf("apply migrations through v4: %v", err)
	}
	seedOrganizationAuditV4State(t, ctx, db)

	if _, err := provider.UpTo(ctx, 5); err != nil {
		t.Fatalf("apply migration v5: %v", err)
	}
	version, err := provider.GetDBVersion(ctx)
	if err != nil {
		t.Fatalf("read migration version: %v", err)
	}
	if version != 5 {
		t.Fatalf("migration version = %d, want 5", version)
	}

	assertOrganizationAuditV4StatePreserved(t, ctx, db)

	var auditCount int
	if err := db.QueryRowContext(ctx, "select count(*) from organization.audit_events").Scan(&auditCount); err != nil {
		t.Fatalf("count audit events: %v", err)
	}
	if auditCount != 0 {
		t.Fatalf("historical audit events = %d, want 0", auditCount)
	}

	assertAuditPermissionMatrix(t, ctx, db)
	assertAuditPublicPrivilegesRevoked(t, ctx, db)
	assertAuditExternalIdentityHasNoForeignKey(t, ctx, db)
	assertAuditConstraints(t, ctx, db)
}

func seedOrganizationAuditV4State(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	for _, statement := range []string{
		`insert into organization.organizations (
			id, clerk_organization_id, name, slug, status, legal_name, website, country, company_type,
			verification_status, trust_status, clerk_created_by_user_id, owner_bootstrapped,
			owner_bootstrap_eligible, business_email_domain, business_email_verified_at,
			business_email_verified_by_user_id
		) values ('` + auditMigrationOrganizationID + `', 'org-audit-main', 'Audit Main', 'audit-main', 'active',
			'Audit Main LLC', 'https://audit.example', 'VN', 'private_company', 'pending', 'unassessed',
			'user-audit-owner', true, false, 'audit.example', now() - interval '2 days', '` + auditMigrationActorID + `')`,
		`insert into organization.organizations (id, clerk_organization_id, name, status, verification_status, trust_status)
		 values ('` + auditMigrationVerifiedOrgID + `', 'org-audit-verified', 'Verified', 'active', 'verified', 'unassessed')`,
		`insert into organization.organizations (id, clerk_organization_id, name, status, verification_status, trust_status)
		 values ('` + auditMigrationRejectedOrgID + `', 'org-audit-rejected', 'Rejected', 'active', 'rejected', 'unassessed')`,
		`insert into organization.memberships (id, clerk_membership_id, organization_id, clerk_user_id, application_role, status)
		 values ('` + auditMigrationOwnerMembershipID + `', 'mem-audit-owner', '` + auditMigrationOrganizationID + `', 'user-audit-owner', 'owner', 'active')`,
		`insert into organization.memberships (id, clerk_membership_id, organization_id, clerk_user_id, application_role, status)
		 values ('` + auditMigrationAdminMembershipID + `', 'mem-audit-admin', '` + auditMigrationOrganizationID + `', 'user-audit-admin', 'admin', 'active')`,
		`insert into organization.memberships (id, clerk_membership_id, organization_id, clerk_user_id, application_role, status)
		 values ('` + auditMigrationViewerMembershipID + `', 'mem-audit-viewer', '` + auditMigrationOrganizationID + `', 'user-audit-viewer', 'viewer', 'active')`,
		`insert into organization.memberships (id, clerk_membership_id, organization_id, clerk_user_id, application_role, status)
		 values ('` + auditMigrationRemovalMembershipID + `', 'mem-audit-removal', '` + auditMigrationOrganizationID + `', 'user-audit-removal', 'recruiter', 'active')`,
		`insert into organization.membership_invitation_intents (id, organization_id, application_role, created_by_identity_user_id)
		 values ('` + auditMigrationInvitationID + `', '` + auditMigrationOrganizationID + `', 'recruiter', '` + auditMigrationActorID + `')`,
		`insert into organization.membership_invitation_intents (
			id, organization_id, application_role, created_by_identity_user_id, consumed_membership_id, consumed_at
		) values ('` + auditMigrationConsumedInvitationID + `', '` + auditMigrationOrganizationID + `', 'viewer', '` + auditMigrationActorID + `',
		 '` + auditMigrationViewerMembershipID + `', now() - interval '3 days')`,
		`insert into organization.membership_removal_intents (membership_id, organization_id, requested_by_identity_user_id)
		 values ('` + auditMigrationRemovalMembershipID + `', '` + auditMigrationOrganizationID + `', '` + auditMigrationActorID + `')`,
	} {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			t.Fatalf("seed v4 audit fixture: %v", err)
		}
	}
}

func assertOrganizationAuditV4StatePreserved(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	var verification, trust, domain string
	var bootstrap, eligible bool
	if err := db.QueryRowContext(ctx, `
		select verification_status, trust_status, owner_bootstrapped, owner_bootstrap_eligible, business_email_domain
		from organization.organizations where id = $1
	`, auditMigrationOrganizationID).Scan(&verification, &trust, &bootstrap, &eligible, &domain); err != nil {
		t.Fatalf("read preserved organization: %v", err)
	}
	if verification != "pending" || trust != "unassessed" || !bootstrap || eligible || domain != "audit.example" {
		t.Fatalf("v5 changed organization state: verification=%q trust=%q bootstrap=%v eligible=%v domain=%q", verification, trust, bootstrap, eligible, domain)
	}
	var verified, rejected string
	if err := db.QueryRowContext(ctx, "select verification_status from organization.organizations where id=$1", auditMigrationVerifiedOrgID).Scan(&verified); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, "select verification_status from organization.organizations where id=$1", auditMigrationRejectedOrgID).Scan(&rejected); err != nil {
		t.Fatal(err)
	}
	if verified != "verified" || rejected != "rejected" {
		t.Fatalf("terminal verification state changed: %q %q", verified, rejected)
	}

	rows, err := db.QueryContext(ctx, `
		select application_role from organization.memberships
		where organization_id=$1 order by application_role
	`, auditMigrationOrganizationID)
	if err != nil {
		t.Fatalf("read membership roles: %v", err)
	}
	defer rows.Close()
	var roles []string
	for rows.Next() {
		var role string
		if err := rows.Scan(&role); err != nil {
			t.Fatal(err)
		}
		roles = append(roles, role)
	}
	want := []string{"admin", "owner", "recruiter", "viewer"}
	if len(roles) != len(want) {
		t.Fatalf("membership role count = %d, want %d", len(roles), len(want))
	}
	for i := range want {
		if roles[i] != want[i] {
			t.Fatalf("membership roles = %v, want %v", roles, want)
		}
	}

	var pending, consumed, removal int
	if err := db.QueryRowContext(ctx, `
		select
		  count(*) filter (where consumed_at is null),
		  count(*) filter (where consumed_at is not null)
		from organization.membership_invitation_intents where organization_id=$1
	`, auditMigrationOrganizationID).Scan(&pending, &consumed); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, "select count(*) from organization.membership_removal_intents where organization_id=$1", auditMigrationOrganizationID).Scan(&removal); err != nil {
		t.Fatal(err)
	}
	if pending != 1 || consumed != 1 || removal != 1 {
		t.Fatalf("intent state changed: pending=%d consumed=%d removal=%d", pending, consumed, removal)
	}
}

func assertAuditPermissionMatrix(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	var permissionCount int
	if err := db.QueryRowContext(ctx, `select count(*) from organization.permissions where key='organization.audit.read'`).Scan(&permissionCount); err != nil {
		t.Fatal(err)
	}
	if permissionCount != 1 {
		t.Fatalf("audit permission count=%d, want 1", permissionCount)
	}
	rows, err := db.QueryContext(ctx, `
		select role_key from organization.role_permissions
		where permission_key='organization.audit.read' order by role_key
	`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var roles []string
	for rows.Next() {
		var role string
		if err := rows.Scan(&role); err != nil {
			t.Fatal(err)
		}
		roles = append(roles, role)
	}
	if len(roles) != 2 || roles[0] != "admin" || roles[1] != "owner" {
		t.Fatalf("audit permission roles=%v, want [admin owner]", roles)
	}
}

func assertAuditPublicPrivilegesRevoked(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	var grants int
	if err := db.QueryRowContext(ctx, `
		select count(*) from information_schema.table_privileges
		where table_schema='organization' and table_name='audit_events' and grantee='PUBLIC'
	`).Scan(&grants); err != nil {
		t.Fatal(err)
	}
	if grants != 0 {
		t.Fatalf("audit PUBLIC grants=%d, want 0", grants)
	}
}

func assertAuditExternalIdentityHasNoForeignKey(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	var foreignKeys int
	if err := db.QueryRowContext(ctx, `
		select count(*)
		from information_schema.table_constraints tc
		join information_schema.key_column_usage kcu
		  on kcu.constraint_schema=tc.constraint_schema and kcu.constraint_name=tc.constraint_name
		where tc.constraint_schema='organization'
		  and tc.table_name='audit_events'
		  and tc.constraint_type='FOREIGN KEY'
		  and kcu.column_name='actor_identity_user_id'
	`).Scan(&foreignKeys); err != nil {
		t.Fatal(err)
	}
	if foreignKeys != 0 {
		t.Fatalf("audit actor Identity foreign keys=%d, want 0", foreignKeys)
	}
}

func assertAuditConstraints(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	assertConstraintRejects(t, ctx, db, `
		insert into organization.audit_events (id, organization_id, event_type, actor_kind, occurred_at)
		values ($1, $2, 'free.form.event', 'system', now())
	`, auditMigrationEventID, auditMigrationOrganizationID)
	assertConstraintRejects(t, ctx, db, `
		insert into organization.audit_events (id, organization_id, event_type, actor_kind, actor_identity_user_id, occurred_at)
		values ($1, $2, 'organization.verification.reviewed', 'system', $3, now())
	`, auditMigrationEventID, auditMigrationOrganizationID, auditMigrationActorID)
}
