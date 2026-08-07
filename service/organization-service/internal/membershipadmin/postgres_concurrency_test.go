package membershipadmin_test

import (
	"context"
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

type concurrencyProvider struct{}

func (concurrencyProvider) CreateInvitation(context.Context, membershipadmin.InvitationProviderRequest) error {
	return nil
}

func (concurrencyProvider) DeleteMembership(context.Context, membershipadmin.MembershipDeleteProviderRequest) error {
	return nil
}

type seededMembership struct {
	ID             uuid.UUID
	IdentityUserID uuid.UUID
	ClerkUserID    string
}

func TestPostgresOwnerAdministrationConcurrency(t *testing.T) {
	databaseURL := os.Getenv("ORGANIZATION_ADMIN_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("ORGANIZATION_ADMIN_TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatalf("pgxpool.New() error = %v", err)
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		t.Fatalf("Ping() error = %v", err)
	}

	t.Run("two owners cannot concurrently leave to zero owners", func(t *testing.T) {
		organizationID, members := seedOrganization(t, ctx, pool, []string{membershipadmin.RoleOwner, membershipadmin.RoleOwner})
		service := membershipadmin.New(store.New(pool), concurrencyProvider{}, nil)

		errs := runConcurrently(
			func() error { return service.Leave(ctx, actorFor(organizationID, members[0], membershipadmin.RoleOwner)) },
			func() error { return service.Leave(ctx, actorFor(organizationID, members[1], membershipadmin.RoleOwner)) },
		)
		assertOneSuccessOneError(t, errs, membershipadmin.ErrLastOwner)
		if got := effectiveOwnerCount(t, ctx, pool, organizationID); got != 1 {
			t.Fatalf("effective owner count = %d, want 1", got)
		}
	})

	t.Run("concurrent owner demotions cannot cross last owner gate", func(t *testing.T) {
		organizationID, members := seedOrganization(t, ctx, pool, []string{membershipadmin.RoleOwner, membershipadmin.RoleOwner})
		service := membershipadmin.New(store.New(pool), concurrencyProvider{}, nil)

		errs := runConcurrently(
			func() error {
				return service.SetRole(ctx, actorFor(organizationID, members[0], membershipadmin.RoleOwner), members[0].ID, membershipadmin.RoleAdmin)
			},
			func() error {
				return service.SetRole(ctx, actorFor(organizationID, members[1], membershipadmin.RoleOwner), members[1].ID, membershipadmin.RoleAdmin)
			},
		)
		assertOneSuccessOneError(t, errs, membershipadmin.ErrLastOwner)
		if got := effectiveOwnerCount(t, ctx, pool, organizationID); got != 1 {
			t.Fatalf("effective owner count = %d, want 1", got)
		}
	})

	t.Run("ownership transfer serializes with competing owner demotion", func(t *testing.T) {
		organizationID, members := seedOrganization(t, ctx, pool, []string{
			membershipadmin.RoleOwner,
			membershipadmin.RoleOwner,
			membershipadmin.RoleAdmin,
		})
		service := membershipadmin.New(store.New(pool), concurrencyProvider{}, nil)

		errs := runConcurrently(
			func() error {
				return service.TransferOwnership(ctx, actorFor(organizationID, members[0], membershipadmin.RoleOwner), members[2].ID)
			},
			func() error {
				return service.SetRole(ctx, actorFor(organizationID, members[1], membershipadmin.RoleOwner), members[1].ID, membershipadmin.RoleAdmin)
			},
		)
		for _, err := range errs {
			if err != nil {
				t.Fatalf("concurrent operation error = %v", err)
			}
		}
		if got := effectiveOwnerCount(t, ctx, pool, organizationID); got != 1 {
			t.Fatalf("effective owner count = %d, want 1", got)
		}
		if got := membershipRole(t, ctx, pool, members[0].ID); got != membershipadmin.RoleAdmin {
			t.Fatalf("transfer actor role = %q, want admin", got)
		}
		if got := membershipRole(t, ctx, pool, members[1].ID); got != membershipadmin.RoleAdmin {
			t.Fatalf("competing owner role = %q, want admin", got)
		}
		if got := membershipRole(t, ctx, pool, members[2].ID); got != membershipadmin.RoleOwner {
			t.Fatalf("transfer target role = %q, want owner", got)
		}
	})

	t.Run("duplicate concurrent ownership transfer is deterministic", func(t *testing.T) {
		organizationID, members := seedOrganization(t, ctx, pool, []string{membershipadmin.RoleOwner, membershipadmin.RoleAdmin})
		service := membershipadmin.New(store.New(pool), concurrencyProvider{}, nil)
		actor := actorFor(organizationID, members[0], membershipadmin.RoleOwner)

		errs := runConcurrently(
			func() error { return service.TransferOwnership(ctx, actor, members[1].ID) },
			func() error { return service.TransferOwnership(ctx, actor, members[1].ID) },
		)
		assertOneSuccessOneError(t, errs, membershipadmin.ErrOwnerMutationForbidden)
		if got := effectiveOwnerCount(t, ctx, pool, organizationID); got != 1 {
			t.Fatalf("effective owner count = %d, want 1", got)
		}
		if got := membershipRole(t, ctx, pool, members[0].ID); got != membershipadmin.RoleAdmin {
			t.Fatalf("transfer actor role = %q, want admin", got)
		}
		if got := membershipRole(t, ctx, pool, members[1].ID); got != membershipadmin.RoleOwner {
			t.Fatalf("transfer target role = %q, want owner", got)
		}
	})
}

func seedOrganization(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	roles []string,
) (uuid.UUID, []seededMembership) {
	t.Helper()
	organizationID := newV7(t)
	clerkOrganizationID := "org_concurrency_" + organizationID.String()
	if _, err := pool.Exec(ctx, `
		insert into organization.organizations (id, clerk_organization_id, name, slug, status)
		values ($1, $2, 'Concurrency Test', $3, 'active')
	`, organizationID, clerkOrganizationID, "concurrency-"+organizationID.String()); err != nil {
		t.Fatalf("insert organization error = %v", err)
	}

	members := make([]seededMembership, 0, len(roles))
	for i, role := range roles {
		membershipID := newV7(t)
		identityUserID := newV7(t)
		clerkUserID := fmt.Sprintf("user_concurrency_%s_%d", organizationID.String(), i)
		clerkMembershipID := fmt.Sprintf("mem_concurrency_%s_%d", organizationID.String(), i)
		if _, err := pool.Exec(ctx, `
			insert into organization.memberships (
				id, clerk_membership_id, organization_id, clerk_user_id,
				clerk_role, application_role, status
			) values ($1, $2, $3, $4, 'org:member', $5, 'active')
		`, membershipID, clerkMembershipID, organizationID, clerkUserID, role); err != nil {
			t.Fatalf("insert membership error = %v", err)
		}
		members = append(members, seededMembership{
			ID:             membershipID,
			IdentityUserID: identityUserID,
			ClerkUserID:    clerkUserID,
		})
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

func actorFor(organizationID uuid.UUID, member seededMembership, role string) authorization.ActorContext {
	return authorization.NewActorContext(
		member.IdentityUserID,
		organizationID,
		member.ID,
		role,
		nil,
	)
}

func runConcurrently(operations ...func() error) []error {
	start := make(chan struct{})
	results := make(chan error, len(operations))
	var wg sync.WaitGroup
	wg.Add(len(operations))
	for _, operation := range operations {
		operation := operation
		go func() {
			defer wg.Done()
			<-start
			results <- operation()
		}()
	}
	close(start)
	wg.Wait()
	close(results)

	errs := make([]error, 0, len(operations))
	for err := range results {
		errs = append(errs, err)
	}
	return errs
}

func assertOneSuccessOneError(t *testing.T, errs []error, want error) {
	t.Helper()
	if len(errs) != 2 {
		t.Fatalf("result count = %d, want 2", len(errs))
	}
	successes := 0
	matches := 0
	for _, err := range errs {
		if err == nil {
			successes++
			continue
		}
		if errors.Is(err, want) {
			matches++
			continue
		}
		t.Fatalf("unexpected error = %v", err)
	}
	if successes != 1 || matches != 1 {
		t.Fatalf("success/error counts = %d/%d, want 1/1", successes, matches)
	}
}

func effectiveOwnerCount(t *testing.T, ctx context.Context, pool *pgxpool.Pool, organizationID uuid.UUID) int64 {
	t.Helper()
	var count int64
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
	`, organizationID).Scan(&count); err != nil {
		t.Fatalf("effective owner count query error = %v", err)
	}
	return count
}

func membershipRole(t *testing.T, ctx context.Context, pool *pgxpool.Pool, membershipID uuid.UUID) string {
	t.Helper()
	var role string
	if err := pool.QueryRow(ctx, "select application_role from organization.memberships where id = $1", membershipID).Scan(&role); err != nil {
		t.Fatalf("membership role query error = %v", err)
	}
	return role
}

func newV7(t *testing.T) uuid.UUID {
	t.Helper()
	id, err := uuid.NewV7()
	if err != nil {
		t.Fatalf("uuid.NewV7() error = %v", err)
	}
	return id
}
