package migrations_test

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/DoMinhHHung/bridgeworks/service/organization-service/internal/authorization"
	"github.com/DoMinhHHung/bridgeworks/service/organization-service/internal/membershipadmin"
	"github.com/DoMinhHHung/bridgeworks/service/organization-service/internal/platformreview"
	"github.com/DoMinhHHung/bridgeworks/service/organization-service/internal/store"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPlatformReviewConcurrencyAndAuditAtomicity(t *testing.T) {
	databaseURL := os.Getenv("ORGANIZATION_MIGRATION_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("ORGANIZATION_MIGRATION_TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	db, provider := resetOrganizationMigrationDatabase(t, ctx, databaseURL)
	defer func() { _ = db.Close() }()
	if _, err := provider.UpTo(ctx, 5); err != nil {
		t.Fatalf("apply migrations through v5: %v", err)
	}
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatalf("open pgx pool: %v", err)
	}
	defer pool.Close()
	repository := store.New(pool)

	t.Run("opposite decisions serialize to exactly one terminal audit", func(t *testing.T) {
		organizationID := uuid.MustParse("018f0c76-8f6c-7cc4-8000-0000000000d1")
		seedReviewOrganization(t, ctx, pool, organizationID, "pending")
		service := platformreview.New(nil, nil, nil, repository)
		reviewers := []platformreview.Reviewer{
			{IdentityUserID: uuid.MustParse("018f0c76-8f6c-7cc4-8000-0000000000d2")},
			{IdentityUserID: uuid.MustParse("018f0c76-8f6c-7cc4-8000-0000000000d3")},
		}
		decisions := []string{platformreview.DecisionVerified, platformreview.DecisionRejected}
		errorsOut := make([]error, 2)
		start := make(chan struct{})
		var wg sync.WaitGroup
		for i := range decisions {
			wg.Add(1)
			go func(index int) {
				defer wg.Done()
				<-start
				_, errorsOut[index] = service.Decide(ctx, reviewers[index], organizationID.String(), decisions[index])
			}(i)
		}
		close(start)
		wg.Wait()
		successes, conflicts := 0, 0
		for _, err := range errorsOut {
			switch {
			case err == nil:
				successes++
			case errors.Is(err, platformreview.ErrDecisionConflict):
				conflicts++
			default:
				t.Fatalf("unexpected decision error: %v", err)
			}
		}
		if successes != 1 || conflicts != 1 {
			t.Fatalf("success/conflict = %d/%d, want 1/1", successes, conflicts)
		}
		assertSingleReviewAudit(t, ctx, pool, organizationID)
	})

	t.Run("duplicate verified decisions are idempotent with one audit", func(t *testing.T) {
		organizationID := uuid.MustParse("018f0c76-8f6c-7cc4-8000-0000000000d4")
		seedReviewOrganization(t, ctx, pool, organizationID, "pending")
		service := platformreview.New(nil, nil, nil, repository)
		start := make(chan struct{})
		errorsOut := make([]error, 2)
		var wg sync.WaitGroup
		for i := range errorsOut {
			wg.Add(1)
			go func(index int) {
				defer wg.Done()
				<-start
				_, errorsOut[index] = service.Decide(ctx, platformreview.Reviewer{
					IdentityUserID: uuid.MustParse("018f0c76-8f6c-7cc4-8000-0000000000d5"),
				}, organizationID.String(), platformreview.DecisionVerified)
			}(i)
		}
		close(start)
		wg.Wait()
		for _, err := range errorsOut {
			if err != nil {
				t.Fatalf("duplicate decision error = %v", err)
			}
		}
		assertSingleReviewAudit(t, ctx, pool, organizationID)
	})

	t.Run("audit insertion failure rolls verification status back", func(t *testing.T) {
		organizationID := uuid.MustParse("018f0c76-8f6c-7cc4-8000-0000000000d6")
		seedReviewOrganization(t, ctx, pool, organizationID, "pending")
		installAuditFailureTrigger(t, ctx, pool)
		service := platformreview.New(nil, nil, nil, repository)
		_, err := service.Decide(ctx, platformreview.Reviewer{
			IdentityUserID: uuid.MustParse("018f0c76-8f6c-7cc4-8000-0000000000d7"),
		}, organizationID.String(), platformreview.DecisionVerified)
		if err == nil {
			t.Fatal("verification decision unexpectedly succeeded")
		}
		dropAuditFailureTrigger(t, ctx, pool)
		var status string
		var auditCount int
		if err := pool.QueryRow(ctx, "select verification_status from organization.organizations where id=$1", organizationID).Scan(&status); err != nil {
			t.Fatal(err)
		}
		if err := pool.QueryRow(ctx, "select count(*) from organization.audit_events where organization_id=$1", organizationID).Scan(&auditCount); err != nil {
			t.Fatal(err)
		}
		if status != "pending" || auditCount != 0 {
			t.Fatalf("rollback state status=%q audit_count=%d", status, auditCount)
		}
	})

	t.Run("membership role and audit commit or roll back together", func(t *testing.T) {
		organizationID := uuid.MustParse("018f0c76-8f6c-7cc4-8000-0000000000d8")
		ownerMembershipID := uuid.MustParse("018f0c76-8f6c-7cc4-8000-0000000000d9")
		targetMembershipID := uuid.MustParse("018f0c76-8f6c-7cc4-8000-0000000000da")
		identityID := uuid.MustParse("018f0c76-8f6c-7cc4-8000-0000000000db")
		seedMembershipAuditOrganization(t, ctx, pool, organizationID, ownerMembershipID, targetMembershipID)
		actor := authorization.NewActorContext(identityID, organizationID, ownerMembershipID, "owner", nil)
		service := membershipadmin.New(repository, nil, nil)

		installAuditFailureTrigger(t, ctx, pool)
		if err := service.SetRole(ctx, actor, targetMembershipID, "recruiter"); err == nil {
			t.Fatal("role update unexpectedly succeeded with failing audit insert")
		}
		dropAuditFailureTrigger(t, ctx, pool)
		assertMembershipRoleAndAuditCount(t, ctx, pool, organizationID, targetMembershipID, "viewer", 0)

		if err := service.SetRole(ctx, actor, targetMembershipID, "recruiter"); err != nil {
			t.Fatalf("successful SetRole() error = %v", err)
		}
		assertMembershipRoleAndAuditCount(t, ctx, pool, organizationID, targetMembershipID, "recruiter", 1)
	})

	t.Run("ownership transfer audit failure rolls all role mutations back", func(t *testing.T) {
		organizationID := uuid.MustParse("018f0c76-8f6c-7cc4-8000-0000000000dc")
		ownerMembershipID := uuid.MustParse("018f0c76-8f6c-7cc4-8000-0000000000dd")
		targetMembershipID := uuid.MustParse("018f0c76-8f6c-7cc4-8000-0000000000de")
		identityID := uuid.MustParse("018f0c76-8f6c-7cc4-8000-0000000000df")
		seedMembershipAuditOrganization(t, ctx, pool, organizationID, ownerMembershipID, targetMembershipID)
		actor := authorization.NewActorContext(identityID, organizationID, ownerMembershipID, "owner", nil)
		service := membershipadmin.New(repository, nil, nil)

		installAuditFailureTrigger(t, ctx, pool)
		if err := service.TransferOwnership(ctx, actor, targetMembershipID); err == nil {
			t.Fatal("ownership transfer unexpectedly succeeded with failing audit insert")
		}
		dropAuditFailureTrigger(t, ctx, pool)
		assertOwnershipTransferState(t, ctx, pool, organizationID, ownerMembershipID, targetMembershipID, "owner", "viewer", 0)
	})

	t.Run("invalid ownership transfer has no transfer audit", func(t *testing.T) {
		organizationID := uuid.MustParse("018f0c76-8f6c-7cc4-8000-0000000000e0")
		ownerMembershipID := uuid.MustParse("018f0c76-8f6c-7cc4-8000-0000000000e1")
		targetMembershipID := uuid.MustParse("018f0c76-8f6c-7cc4-8000-0000000000e2")
		seedMembershipAuditOrganization(t, ctx, pool, organizationID, ownerMembershipID, targetMembershipID)
		actor := authorization.NewActorContext(uuid.MustParse("018f0c76-8f6c-7cc4-8000-0000000000e3"), organizationID, ownerMembershipID, "owner", nil)
		if err := membershipadmin.New(repository, nil, nil).TransferOwnership(ctx, actor, ownerMembershipID); !errors.Is(err, membershipadmin.ErrInvalidTransferTarget) {
			t.Fatalf("TransferOwnership() error = %v", err)
		}
		assertOwnershipTransferState(t, ctx, pool, organizationID, ownerMembershipID, targetMembershipID, "owner", "viewer", 0)
	})
}

func seedReviewOrganization(t *testing.T, ctx context.Context, pool *pgxpool.Pool, id uuid.UUID, verification string) {
	t.Helper()
	if _, err := pool.Exec(ctx, `
		insert into organization.organizations (id, clerk_organization_id, name, status, verification_status, trust_status)
		values ($1, $2, 'Review Org', 'active', $3, 'unassessed')
	`, id, "org-review-"+id.String(), verification); err != nil {
		t.Fatal(err)
	}
}

func assertSingleReviewAudit(t *testing.T, ctx context.Context, pool *pgxpool.Pool, organizationID uuid.UUID) {
	t.Helper()
	var status string
	var count int
	if err := pool.QueryRow(ctx, "select verification_status from organization.organizations where id=$1", organizationID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "verified" && status != "rejected" {
		t.Fatalf("terminal status=%q", status)
	}
	if err := pool.QueryRow(ctx, `select count(*) from organization.audit_events where organization_id=$1 and event_type='organization.verification.reviewed'`, organizationID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("review audit count=%d, want 1", count)
	}
}

func installAuditFailureTrigger(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	if _, err := pool.Exec(ctx, `
		create or replace function organization.reject_test_audit_insert() returns trigger language plpgsql as $$
		begin raise exception 'forced audit failure'; end $$;
		create trigger reject_test_audit before insert on organization.audit_events
		for each row execute function organization.reject_test_audit_insert();
	`); err != nil {
		t.Fatalf("install audit failure trigger: %v", err)
	}
}

func dropAuditFailureTrigger(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	if _, err := pool.Exec(ctx, `
		drop trigger if exists reject_test_audit on organization.audit_events;
		drop function if exists organization.reject_test_audit_insert();
	`); err != nil {
		t.Fatalf("drop audit failure trigger: %v", err)
	}
}

func seedMembershipAuditOrganization(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	organizationID uuid.UUID,
	ownerMembershipID uuid.UUID,
	targetMembershipID uuid.UUID,
) {
	t.Helper()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin membership audit fixture transaction: %v", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback(ctx)
		}
	}()
	if _, err := tx.Exec(ctx, `
		insert into organization.organizations (id, clerk_organization_id, name, status)
		values ($1, $2, 'Membership Audit', 'active')
	`, organizationID, "org-membership-audit-"+organizationID.String()); err != nil {
		t.Fatalf("insert membership audit organization: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		insert into organization.memberships (id, clerk_membership_id, organization_id, clerk_user_id, application_role, status)
		values ($1, $2, $3, 'user-owner', 'owner', 'active')
	`, ownerMembershipID, "mem-owner-"+ownerMembershipID.String(), organizationID); err != nil {
		t.Fatalf("insert membership audit owner: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		insert into organization.memberships (id, clerk_membership_id, organization_id, clerk_user_id, application_role, status)
		values ($1, $2, $3, 'user-target', 'viewer', 'active')
	`, targetMembershipID, "mem-target-"+targetMembershipID.String(), organizationID); err != nil {
		t.Fatalf("insert membership audit target: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit membership audit fixture: %v", err)
	}
	committed = true
}

func assertMembershipRoleAndAuditCount(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	organizationID uuid.UUID,
	membershipID uuid.UUID,
	wantRole string,
	wantAudit int,
) {
	t.Helper()
	var role string
	var auditCount int
	if err := pool.QueryRow(ctx, "select application_role from organization.memberships where id=$1", membershipID).Scan(&role); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `select count(*) from organization.audit_events where organization_id=$1 and event_type='membership.role.changed'`, organizationID).Scan(&auditCount); err != nil {
		t.Fatal(err)
	}
	if role != wantRole || auditCount != wantAudit {
		t.Fatalf("role/audit=%q/%d, want %q/%d", role, auditCount, wantRole, wantAudit)
	}
}

func assertOwnershipTransferState(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	organizationID uuid.UUID,
	ownerMembershipID uuid.UUID,
	targetMembershipID uuid.UUID,
	wantOwnerRole string,
	wantTargetRole string,
	wantAudit int,
) {
	t.Helper()
	var ownerRole, targetRole string
	var auditCount int
	if err := pool.QueryRow(ctx, "select application_role from organization.memberships where id=$1", ownerMembershipID).Scan(&ownerRole); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, "select application_role from organization.memberships where id=$1", targetMembershipID).Scan(&targetRole); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `select count(*) from organization.audit_events where organization_id=$1 and event_type='organization.ownership.transferred'`, organizationID).Scan(&auditCount); err != nil {
		t.Fatal(err)
	}
	if ownerRole != wantOwnerRole || targetRole != wantTargetRole || auditCount != wantAudit {
		t.Fatalf("owner/target/audit=%q/%q/%d, want %q/%q/%d", ownerRole, targetRole, auditCount, wantOwnerRole, wantTargetRole, wantAudit)
	}
}
