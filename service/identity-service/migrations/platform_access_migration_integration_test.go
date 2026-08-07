package migrations_test

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"testing"
	"time"

	identitymigrations "github.com/DoMinhHHung/bridgeworks/service/identity-service/migrations"
	"github.com/DoMinhHHung/bridgeworks/service/identity-service/internal/platformaccess"
	"github.com/DoMinhHHung/bridgeworks/service/identity-service/internal/platformaccessoperator"
	"github.com/DoMinhHHung/bridgeworks/service/identity-service/internal/store"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pressly/goose/v3"

	_ "github.com/jackc/pgx/v5/stdlib"
)

const identityMigrationTable = "app.goose_db_version"

func TestPlatformAccessMigrationPreservesUsersAndAddsRevocableAuthority(t *testing.T) {
	databaseURL := os.Getenv("IDENTITY_PLATFORM_ACCESS_MIGRATION_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("IDENTITY_PLATFORM_ACCESS_MIGRATION_TEST_DATABASE_URL is not set")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatalf("open migration database: %v", err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.ExecContext(ctx, "drop schema if exists app cascade; drop schema if exists extensions cascade; create schema app"); err != nil {
		t.Fatalf("reset identity migration schemas: %v", err)
	}
	provider, err := goose.NewProvider(
		goose.DialectPostgres,
		db,
		identitymigrations.FS,
		goose.WithTableName(identityMigrationTable),
	)
	if err != nil {
		t.Fatalf("new migration provider: %v", err)
	}
	if _, err := provider.UpTo(ctx, 1); err != nil {
		t.Fatalf("apply Identity v1: %v", err)
	}

	seedIdentityUser(t, ctx, db, "018f0c76-8f6c-7cc4-8000-000000000101", "user_platform_active", "bw100001012601", "active")
	seedIdentityUser(t, ctx, db, "018f0c76-8f6c-7cc4-8000-000000000102", "user_platform_disabled", "bw100101012601", "disabled")
	seedIdentityUser(t, ctx, db, "018f0c76-8f6c-7cc4-8000-000000000103", "user_platform_deleted", "bw100201012601", "deleted")

	if _, err := provider.UpTo(ctx, 2); err != nil {
		t.Fatalf("apply Identity v2: %v", err)
	}
	version, err := provider.GetDBVersion(ctx)
	if err != nil {
		t.Fatalf("get migration version: %v", err)
	}
	if version != 2 {
		t.Fatalf("migration version = %d, want 2", version)
	}

	var userCount, assignmentCount int
	if err := db.QueryRowContext(ctx, "select count(*) from app.app_users").Scan(&userCount); err != nil {
		t.Fatalf("count preserved users: %v", err)
	}
	if userCount != 3 {
		t.Fatalf("preserved users = %d, want 3", userCount)
	}
	if err := db.QueryRowContext(ctx, "select count(*) from app.platform_access_assignments").Scan(&assignmentCount); err != nil {
		t.Fatalf("count automatic platform assignments: %v", err)
	}
	if assignmentCount != 0 {
		t.Fatalf("migration silently promoted %d users", assignmentCount)
	}

	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatalf("open platform access pool: %v", err)
	}
	defer pool.Close()
	repository := store.New(pool)
	operator := platformaccessoperator.New(repository)

	granted, err := operator.Grant(ctx, "bw100001012601")
	if err != nil {
		t.Fatalf("grant platform admin: %v", err)
	}
	if !granted.Active || granted.Role != platformaccess.RolePlatformAdmin {
		t.Fatalf("grant status = %+v", granted)
	}
	if _, err := operator.Grant(ctx, "bw100001012601"); err != nil {
		t.Fatalf("idempotent grant: %v", err)
	}
	access, err := platformaccess.New(repository).Resolve(ctx, "user_platform_active")
	if err != nil {
		t.Fatalf("resolve granted platform access: %v", err)
	}
	if len(access.Permissions) != 1 || access.Permissions[0] != platformaccess.PermissionOrganizationVerificationReview {
		t.Fatalf("granted permissions = %#v", access.Permissions)
	}

	revoked, err := operator.Revoke(ctx, "bw100001012601")
	if err != nil {
		t.Fatalf("revoke platform admin: %v", err)
	}
	if revoked.Active || !revoked.Assigned {
		t.Fatalf("revoke status = %+v", revoked)
	}
	if _, err := operator.Revoke(ctx, "bw100001012601"); err != nil {
		t.Fatalf("idempotent revoke: %v", err)
	}
	access, err = platformaccess.New(repository).Resolve(ctx, "user_platform_active")
	if err != nil {
		t.Fatalf("resolve revoked platform access: %v", err)
	}
	if len(access.Roles) != 0 || len(access.Permissions) != 0 {
		t.Fatalf("revoked access = %+v", access)
	}
	if _, err := operator.Grant(ctx, "bw100001012601"); err != nil {
		t.Fatalf("re-grant platform admin: %v", err)
	}
	access, err = platformaccess.New(repository).Resolve(ctx, "user_platform_active")
	if err != nil || len(access.Permissions) != 1 {
		t.Fatalf("re-granted access = %+v error=%v", access, err)
	}

	for _, idUser := range []string{"bw100101012601", "bw100201012601"} {
		if _, err := operator.Grant(ctx, idUser); !errors.Is(err, platformaccessoperator.ErrUserInactive) {
			t.Fatalf("grant inactive %s error=%v", idUser, err)
		}
	}

	if _, err := db.ExecContext(ctx, `
		insert into app.platform_access_assignments (identity_user_id, role)
		select id, 'unknown_admin' from app.app_users where id_user = 'bw100101012601'
	`); err == nil {
		t.Fatal("unknown platform role bypassed schema constraint")
	}
	if _, err := db.ExecContext(ctx, `
		insert into app.platform_access_assignments (identity_user_id, role, granted_at, revoked_at)
		select id, 'platform_admin', now(), now() - interval '1 minute'
		from app.app_users where id_user = 'bw100101012601'
	`); err == nil {
		t.Fatal("invalid revoked/granted timestamp state bypassed constraint")
	}

	if _, err := db.ExecContext(ctx, `
		insert into app.platform_access_assignments (identity_user_id, role)
		select id, 'platform_admin' from app.app_users where id_user = 'bw100101012601'
	`); err != nil {
		t.Fatalf("seed disabled platform assignment: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
		insert into app.platform_access_assignments (identity_user_id, role)
		select id, 'platform_admin' from app.app_users where id_user = 'bw100201012601'
	`); err != nil {
		t.Fatalf("seed deleted platform assignment: %v", err)
	}
	if _, err := platformaccess.New(repository).Resolve(ctx, "user_platform_disabled"); !errors.Is(err, platformaccess.ErrAccountDisabled) {
		t.Fatalf("disabled account resolution error=%v", err)
	}
	if _, err := platformaccess.New(repository).Resolve(ctx, "user_platform_deleted"); !errors.Is(err, platformaccess.ErrAccountDeleted) {
		t.Fatalf("deleted account resolution error=%v", err)
	}

	var publicGrants int
	if err := db.QueryRowContext(ctx, `
		select count(*)
		from information_schema.table_privileges
		where table_schema = 'app'
		  and table_name = 'platform_access_assignments'
		  and grantee = 'PUBLIC'
	`).Scan(&publicGrants); err != nil {
		t.Fatalf("read PUBLIC grants: %v", err)
	}
	if publicGrants != 0 {
		t.Fatalf("platform access PUBLIC grants = %d, want 0", publicGrants)
	}

	var crossServiceFKs int
	if err := db.QueryRowContext(ctx, `
		select count(*)
		from information_schema.referential_constraints rc
		join information_schema.key_column_usage kcu
		  on kcu.constraint_catalog = rc.constraint_catalog
		 and kcu.constraint_schema = rc.constraint_schema
		 and kcu.constraint_name = rc.constraint_name
		where rc.constraint_schema = 'app'
		  and kcu.table_name = 'platform_access_assignments'
		  and rc.unique_constraint_schema <> 'app'
	`).Scan(&crossServiceFKs); err != nil {
		t.Fatalf("read platform access foreign keys: %v", err)
	}
	if crossServiceFKs != 0 {
		t.Fatalf("platform access cross-service foreign keys = %d, want 0", crossServiceFKs)
	}
}

func seedIdentityUser(t *testing.T, ctx context.Context, db *sql.DB, id, clerkUserID, idUser, status string) {
	t.Helper()
	if _, err := db.ExecContext(ctx, `
		insert into app.app_users (id, clerk_user_id, id_user, status)
		values ($1, $2, $3, $4)
	`, id, clerkUserID, idUser, status); err != nil {
		t.Fatalf("seed Identity v1 user %s: %v", idUser, err)
	}
}
