package store

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/DoMinhHHung/bridgeworks/service/organization-service/internal/organizationsync"
	"github.com/DoMinhHHung/bridgeworks/service/organization-service/internal/store/sqlcgen"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type fakeTransaction struct {
	execQueries []string
	rowQueries  []string
	rowErr      error
	execErr     error
	commitErr   error
	rollbackErr error
}

func (f *fakeTransaction) Exec(_ context.Context, query string, _ ...any) (pgconn.CommandTag, error) {
	f.execQueries = append(f.execQueries, query)
	return pgconn.CommandTag{}, f.execErr
}

func (f *fakeTransaction) Query(context.Context, string, ...any) (pgx.Rows, error) {
	return nil, errors.New("unexpected Query call")
}

func (f *fakeTransaction) QueryRow(_ context.Context, query string, _ ...any) pgx.Row {
	f.rowQueries = append(f.rowQueries, query)
	return fakeRow{err: f.rowErr}
}

func (f *fakeTransaction) Commit(context.Context) error   { return f.commitErr }
func (f *fakeTransaction) Rollback(context.Context) error { return f.rollbackErr }

type fakeRow struct{ err error }

func (r fakeRow) Scan(...any) error { return r.err }

func TestUnitOfWorkMembershipSavepointCommands(t *testing.T) {
	tx := &fakeTransaction{}
	uow := &unitOfWork{tx: tx}
	ctx := context.Background()

	if err := uow.CreateMembershipInsertSavepoint(ctx); err != nil {
		t.Fatalf("CreateMembershipInsertSavepoint() error = %v", err)
	}
	if err := uow.RollbackMembershipInsertSavepoint(ctx); err != nil {
		t.Fatalf("RollbackMembershipInsertSavepoint() error = %v", err)
	}
	if err := uow.ReleaseMembershipInsertSavepoint(ctx); err != nil {
		t.Fatalf("ReleaseMembershipInsertSavepoint() error = %v", err)
	}

	want := []string{
		"SAVEPOINT membership_insert",
		"ROLLBACK TO SAVEPOINT membership_insert",
		"RELEASE SAVEPOINT membership_insert",
	}
	if len(tx.execQueries) != len(want) {
		t.Fatalf("commands = %#v", tx.execQueries)
	}
	for i := range want {
		if tx.execQueries[i] != want[i] {
			t.Fatalf("command[%d] = %q, want %q", i, tx.execQueries[i], want[i])
		}
	}
}

func TestUnitOfWorkInsertMembershipMapsUniqueConstraint(t *testing.T) {
	tests := []struct {
		name       string
		constraint string
	}{
		{name: "Clerk membership", constraint: organizationsync.ConstraintMembershipClerkID},
		{name: "active organization user", constraint: organizationsync.ConstraintActiveOrganizationMember},
		{name: "unrelated unique", constraint: "memberships_pkey"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tx := &fakeTransaction{rowErr: &pgconn.PgError{Code: "23505", ConstraintName: tt.constraint}}
			uow := &unitOfWork{tx: tx, queries: sqlcQueries(tx)}

			err := uow.InsertMembership(context.Background(), organizationsync.Membership{
				ID:                uuid.MustParse("018f0c76-8f6c-7cc4-8000-000000000001"),
				ClerkMembershipID: "mem-1",
				OrganizationID:    uuid.MustParse("018f0c76-8f6c-7cc4-8000-000000000002"),
				ClerkUserID:       "user-1",
				ApplicationRole:   organizationsync.RoleViewer,
				Status:            "active",
			})
			var uniqueErr *organizationsync.UniqueConstraintError
			if !errors.As(err, &uniqueErr) {
				t.Fatalf("InsertMembership() error = %T %v", err, err)
			}
			if uniqueErr.Constraint != tt.constraint {
				t.Fatalf("constraint = %q, want %q", uniqueErr.Constraint, tt.constraint)
			}
			if len(tx.rowQueries) != 1 || !strings.Contains(tx.rowQueries[0], "INSERT INTO organization.memberships") {
				t.Fatalf("queries = %#v", tx.rowQueries)
			}
		})
	}
}

func TestUnitOfWorkInsertMembershipDoesNotClassifyOtherErrors(t *testing.T) {
	foreignKeyErr := &pgconn.PgError{Code: "23503", ConstraintName: "memberships_organization_fk"}
	tx := &fakeTransaction{rowErr: foreignKeyErr}
	uow := &unitOfWork{tx: tx, queries: sqlcQueries(tx)}

	err := uow.InsertMembership(context.Background(), organizationsync.Membership{
		ID:                uuid.MustParse("018f0c76-8f6c-7cc4-8000-000000000001"),
		ClerkMembershipID: "mem-1",
		OrganizationID:    uuid.MustParse("018f0c76-8f6c-7cc4-8000-000000000002"),
		ClerkUserID:       "user-1",
		ApplicationRole:   organizationsync.RoleViewer,
		Status:            "active",
	})
	var uniqueErr *organizationsync.UniqueConstraintError
	if errors.As(err, &uniqueErr) {
		t.Fatalf("foreign-key error classified as unique: %#v", uniqueErr)
	}
	if !errors.Is(err, foreignKeyErr) {
		t.Fatalf("InsertMembership() error = %v", err)
	}
}

func TestUnitOfWorkInsertInboxDuplicate(t *testing.T) {
	tx := &fakeTransaction{rowErr: pgx.ErrNoRows}
	uow := &unitOfWork{tx: tx, queries: sqlcQueries(tx)}
	inserted, err := uow.InsertInbox(context.Background(), organizationsync.Event{
		EventID: "evt-1", Type: organizationsync.EventOrganizationCreated,
		AggregateType: organizationsync.AggregateOrganization,
		AggregateID:   "org-1", ClerkOrganizationID: "org-1",
	})
	if err != nil || inserted {
		t.Fatalf("InsertInbox() = %v, %v", inserted, err)
	}
}

func sqlcQueries(tx *fakeTransaction) *sqlcgen.Queries { return sqlcgen.New(tx) }
