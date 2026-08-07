package migrations_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/DoMinhHHung/bridgeworks/service/organization-service/internal/authorization"
	"github.com/DoMinhHHung/bridgeworks/service/organization-service/internal/membershipadmin"
	"github.com/DoMinhHHung/bridgeworks/service/organization-service/internal/store"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	administrationOrganizationID = "018f0c76-8f6c-7cc4-8000-000000000061"
	administrationMembershipID   = "018f0c76-8f6c-7cc4-8000-000000000062"
	administrationInvitationID   = "018f0c76-8f6c-7cc4-8000-000000000063"
	administrationActorID        = "018f0c76-8f6c-7cc4-8000-000000000064"
)

type concurrencyProvider struct{}

func (concurrencyProvider) CreateInvitation(context.Context, membershipadmin.InvitationProviderRequest) error {
	return nil
}

func (concurrencyProvider) DeleteMembership(context.Context, membershipadmin.MembershipDeleteProviderRequest) error {
	return nil
}

type concurrencyMember struct {
	ID             uuid.UUID
	IdentityUserID uuid.UUID
}

func TestMembershipAdministrationMigrationPreservesAuthorizationAndAddsPrivateState(t *testing.T) {
	databaseURL := os.Getenv("ORGANIZATION_MIGRATION_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("ORGANIZATION_MIGRATION_TEST_DATABASE_URL is not set")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
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

	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatalf("pgxpool.New() concurrency database: %v", err)
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		t.Fatalf("concurrency database ping: %v", err)
	}
	runOwnerConcurrencyRegressions(t, ctx, pool)
}

func runOwnerConcurrencyRegressions(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	t.Run("two owners cannot concurrently leave to zero effective owners", func(t *testing.T) {
		organizationID, members := seedConcurrencyOrganization(t, ctx, pool, []string{membershipadmin.RoleOwner, membershipadmin.RoleOwner})
		service := membershipadmin.New(store.New(pool), concurrencyProvider{}, nil)
		errs := runConcurrentOperations(
			func() error { return service.Leave(ctx, concurrencyActor(organizationID, members[0])) },
			func() error { return service.Leave(ctx, concurrencyActor(organizationID, members[1])) },
		)
		assertOneSuccessAndOne(t, errs, membershipadmin.ErrLastOwner)
		assertEffectiveOwners(t, ctx, pool, organizationID, 1)
	})

	t.Run("concurrent owner demotions cannot cross the last-owner gate", func(t *testing.T) {
		organizationID, members := seedConcurrencyOrganization(t, ctx, pool, []string{membershipadmin.RoleOwner, membershipadmin.RoleOwner})
		service := membershipadmin.New(store.New(pool), concurrencyProvider{}, nil)
		errs := runConcurrentOperations(
			func() error {
				return service.SetRole(ctx, concurrencyActor(organizationID, members[0]), members[0].ID, membershipadmin.RoleAdmin)
			},
			func() error {
				return service.SetRole(ctx, concurrencyActor(organizationID, members[1]), members[1].ID, membershipadmin.RoleAdmin)
			},
		)
		assertOneSuccessAndOne(t, errs, membershipadmin.ErrLastOwner)
		assertEffectiveOwners(t, ctx, pool, organizationID, 1)
	})

	t.Run("ownership transfer serializes with competing owner mutation", func(t *testing.T) {
		organizationID, members := seedConcurrencyOrganization(t, ctx, pool, []string{
			membershipadmin.RoleOwner,
			membershipadmin.RoleOwner,
			membershipadmin.RoleAdmin,
		})
		service := membershipadmin.New(store.New(pool), concurrencyProvider{}, nil)
		errs := runConcurrentOperations(
			func() error {
				return service.TransferOwnership(ctx, concurrencyActor(organizationID, members[0]), members[2].ID)
			},
			func() error {
				return service.SetRole(ctx, concurrencyActor(organizationID, members[1]), members[1].ID, membershipadmin.RoleAdmin)
			},
		)
		for _, err := range errs {
			if err != nil {
				t.Fatalf("concurrent transfer/mutation error = %v", err)
			}
		}
		assertEffectiveOwners(t, ctx, pool, organizationID, 1)
		assertMembershipRole(t, ctx, pool, members[0].ID, membershipadmin.RoleAdmin)
		assertMembershipRole(t, ctx, pool, members[1].ID, membershipadmin.RoleAdmin)
		assertMembershipRole(t, ctx, pool, members[2].ID, membershipadmin.RoleOwner)
	})

	t.Run("duplicate concurrent ownership transfer is deterministic", func(t *testing.T) {
		organizationID, members := seedConcurrencyOrganization(t, ctx, pool, []string{membershipadmin.RoleOwner, membershipadmin.RoleAdmin})
		service := membershipadmin.New(store.New(pool), concurrencyProvider{}, nil)
		actor := concurrencyActor(organizationID, members[0])
		errs := runConcurrentOperations(
			func() error { return service.TransferOwnership(ctx, actor, members[1].ID) },
			func() error { return service.TransferOwnership(ctx, actor, members[1].ID) },
		)
		assertOneSuccessAndOne(t, errs, membershipadmin.ErrOwnerMutationForbidden)
		assertEffectiveOwners(t, ctx, pool, organizationID, 1)
		assertMembershipRole(t, ctx, pool, members[0].ID, membershipadmin.RoleAdmin)
		assertMembershipRole(t, ctx, pool, members[1].ID, membershipadmin.RoleOwner)
	})
}

func seedConcurrencyOrganization(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	roles []string,
) (uuid.UUID, []concurrencyMember) {
	t.Helper()
	organizationID := newConcurrencyV7(t)
	clerkOrganizationID := "org_concurrency_" + organizationID.String()
	if _, err := pool.Exec(ctx, `
		insert into organization.organizations (id, clerk_organization_id, name, slug, status)
		values ($1, $2, 'Concurrency Test', $3, 'active')
	`, organizationID, clerkOrganizationID, "concurrency-"+organizationID.String()); err != nil {
		t.Fatalf("insert concurrency organization: %v", err)
	}
	members := make([]concurrencyMember, 0, len(roles))
	for index, role := range roles {
		membershipID := newConcurrencyV7(t)
		identityUserID := newConcurrencyV7(t)
		if _, err := pool.Exec(ctx, `
			insert into organization.memberships (
				id, clerk_membership_id, organization_id, clerk_user_id,
				clerk_role, application_role, status
			) values ($1, $2, $3, $4, 'org:member', $5, 'active')
		`,
			membershipID,
			fmt.Sprintf("mem_concurrency_%s_%d", organizationID.String(), index),
			organizationID,
			fmt.Sprintf("user_concurrency_%s_%d", organizationID.String(), index),
			role,
		); err != nil {
			t.Fatalf("insert concurrency membership: %v", err)
		}
		members = append(members, concurrencyMember{ID: membershipID, IdentityUserID: identityUserID})
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = pool.Exec(cleanupCtx, "delete from organization.membership_removal_intents where organization_id = $1", organizationID)
		_, _ = pool.Exec(cleanupCtx, "delete from organization.membership_invitation_intents where organization_id = $1", organizationID)
		_, _ = pool.Exec(cleanupCtx, "delete from organization.memberships where organization_id = $1", organizationID)
		_, _ = pool.Exec(cleanupCtx, "delete from organization.organizations where id = $1", organizationID)
	})
	return organizationID, members
}

func concurrencyActor(organizationID uuid.UUID, member concurrencyMember) authorization.ActorContext {
	return authorization.NewActorContext(
		member.IdentityUserID,
		organizationID,
		member.ID,
		membershipadmin.RoleOwner,
		nil,
	)
}

func runConcurrentOperations(operations ...func() error) []error {
	start := make(chan struct{})
	results := make(chan error, len(operations))
	var wait sync.WaitGroup
	wait.Add(len(operations))
	for _, operation := range operations {
		operation := operation
		go func() {
			defer wait.Done()
			<-start
			results <- operation()
		}()
	}
	close(start)
	wait.Wait()
	close(results)
	errs := make([]error, 0, len(operations))
	for err := range results {
		errs = append(errs, err)
	}
	return errs
}

func assertOneSuccessAndOne(t *testing.T, errs []error, want error) {
	t.Helper()
	if len(errs) != 2 {
		t.Fatalf("concurrency result count = %d, want 2", len(errs))
	}
	successes, matches := 0, 0
	for _, err := range errs {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, want):
			matches++
		default:
			t.Fatalf("unexpected concurrency error = %v", err)
		}
	}
	if successes != 1 || matches != 1 {
		t.Fatalf("concurrency success/match = %d/%d, want 1/1", successes, matches)
	}
}

func assertEffectiveOwners(t *testing.T, ctx context.Context, pool *pgxpool.Pool, organizationID uuid.UUID, want int64) {
	t.Helper()
	var got int64
	if err := pool.QueryRow(ctx, `
		select count(*)
		from organization.memberships m
		where m.organization_id = $1
		  and m.status = 'active'
		  and m.application_role = 'owner'
		  and not exists (
		      select 1 from organization.membership_removal_intents r
		      where r.membership_id = m.id
		  )
	`, organizationID).Scan(&got); err != nil {
		t.Fatalf("effective owner count query: %v", err)
	}
	if got != want {
		t.Fatalf("effective owner count = %d, want %d", got, want)
	}
}

func assertMembershipRole(t *testing.T, ctx context.Context, pool *pgxpool.Pool, membershipID uuid.UUID, want string) {
	t.Helper()
	var got string
	if err := pool.QueryRow(ctx, "select application_role from organization.memberships where id = $1", membershipID).Scan(&got); err != nil {
		t.Fatalf("membership role query: %v", err)
	}
	if got != want {
		t.Fatalf("membership role = %q, want %q", got, want)
	}
}

func newConcurrencyV7(t *testing.T) uuid.UUID {
	t.Helper()
	id, err := uuid.NewV7()
	if err != nil {
		t.Fatalf("uuid.NewV7(): %v", err)
	}
	return id
}
