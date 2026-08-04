package organizationsync_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/DoMinhHHung/bridgeworks/service/organization-service/internal/organizationsync"
	"github.com/DoMinhHHung/bridgeworks/service/organization-service/internal/store"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type fixedGenerator struct{ id uuid.UUID }

func (g fixedGenerator) New() (uuid.UUID, error) { return g.id, nil }

func TestPostgresUnrelatedUniqueViolationRollsBackInbox(t *testing.T) {
	databaseURL := os.Getenv("ORGANIZATION_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("ORGANIZATION_TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatalf("pgxpool.New() error = %v", err)
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		t.Fatalf("Ping() error = %v", err)
	}

	organizationID, err := uuid.NewV7()
	if err != nil {
		t.Fatalf("uuid.NewV7() organization error = %v", err)
	}
	duplicateMembershipID, err := uuid.NewV7()
	if err != nil {
		t.Fatalf("uuid.NewV7() membership error = %v", err)
	}
	suffix := organizationID.String()
	clerkOrganizationID := "org_pg_unique_" + suffix
	existingClerkMembershipID := "mem_pg_existing_" + suffix
	incomingClerkMembershipID := "mem_pg_incoming_" + suffix
	existingUserID := "user_pg_existing_" + suffix
	incomingUserID := "user_pg_incoming_" + suffix
	eventID := "evt_pg_unique_" + suffix

	defer func() {
		cleanupContext, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		_, _ = pool.Exec(cleanupContext, "delete from organization.clerk_webhook_events where event_id = $1", eventID)
		_, _ = pool.Exec(cleanupContext, "delete from organization.memberships where organization_id = $1", organizationID)
		_, _ = pool.Exec(cleanupContext, "delete from organization.organizations where id = $1", organizationID)
	}()

	if _, err := pool.Exec(ctx, `
		insert into organization.organizations (id, clerk_organization_id, name, slug, status)
		values ($1, $2, 'Postgres Unique Regression', $3, 'active')
	`, organizationID, clerkOrganizationID, "pg-unique-"+suffix); err != nil {
		t.Fatalf("insert organization error = %v", err)
	}
	if _, err := pool.Exec(ctx, `
		insert into organization.memberships (
			id, clerk_membership_id, organization_id, clerk_user_id,
			clerk_role, application_role, status
		) values ($1, $2, $3, $4, null, 'viewer', 'active')
	`, duplicateMembershipID, existingClerkMembershipID, organizationID, existingUserID); err != nil {
		t.Fatalf("insert existing membership error = %v", err)
	}

	service := organizationsync.New(store.New(pool), fixedGenerator{id: duplicateMembershipID})
	err = service.Process(ctx, organizationsync.Event{
		EventID:             eventID,
		Type:                organizationsync.EventMembershipCreated,
		AggregateType:       organizationsync.AggregateMembership,
		AggregateID:         incomingClerkMembershipID,
		ClerkOrganizationID: clerkOrganizationID,
		OccurredAt:          time.Now().UTC(),
		Membership: organizationsync.MembershipProjection{
			ClerkMembershipID: incomingClerkMembershipID,
			ClerkUserID:       incomingUserID,
		},
	})
	if err == nil {
		t.Fatal("Process() error = nil for unrelated memberships_pkey violation")
	}

	var inboxCount int
	if err := pool.QueryRow(ctx, "select count(*) from organization.clerk_webhook_events where event_id = $1", eventID).Scan(&inboxCount); err != nil {
		t.Fatalf("query inbox count error = %v", err)
	}
	if inboxCount != 0 {
		t.Fatalf("inbox count = %d, want 0", inboxCount)
	}
	var incomingCount int
	if err := pool.QueryRow(ctx, "select count(*) from organization.memberships where clerk_membership_id = $1", incomingClerkMembershipID).Scan(&incomingCount); err != nil {
		t.Fatalf("query incoming membership count error = %v", err)
	}
	if incomingCount != 0 {
		t.Fatalf("incoming membership count = %d, want 0", incomingCount)
	}
	var existingCount int
	if err := pool.QueryRow(ctx, "select count(*) from organization.memberships where clerk_membership_id = $1 and status = 'active'", existingClerkMembershipID).Scan(&existingCount); err != nil {
		t.Fatalf("query existing membership count error = %v", err)
	}
	if existingCount != 1 {
		t.Fatalf("existing active membership count = %d, want 1", existingCount)
	}
}
