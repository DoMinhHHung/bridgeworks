package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DoMinhHHung/bridgeworks/service/identity-service/internal/platform/safeerr"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

func TestIsIDUserCollisionMatchesOnlyLockedConstraint(t *testing.T) {
	t.Parallel()

	repository := &Repository{}
	matching := &pgconn.PgError{Code: "23505", ConstraintName: idUserUniqueConstraint}
	if !repository.IsIDUserCollision(safeerr.Wrap("insert user", matching)) {
		t.Fatal("expected exact id_user constraint to match through wrapped error")
	}

	cases := []error{
		&pgconn.PgError{Code: "23505", ConstraintName: "app_users_clerk_user_id_uq"},
		&pgconn.PgError{Code: "23503", ConstraintName: idUserUniqueConstraint},
		errors.New("database unavailable"),
		nil,
	}
	for _, err := range cases {
		if repository.IsIDUserCollision(err) {
			t.Fatalf("unexpected collision match for %#v", err)
		}
	}
}

func TestGetCurrentUserByClerkUserIDReturnsNotFoundForNoRows(t *testing.T) {
	t.Parallel()

	repository := New(&fakeDatabase{row: fakeRow{err: pgx.ErrNoRows}})
	user, found, err := repository.GetCurrentUserByClerkUserID(context.Background(), "user_missing")
	if err != nil || found || user.ClerkUserID != "" {
		t.Fatalf("user=%+v found=%t err=%v", user, found, err)
	}
}

func TestGetCurrentUserByClerkUserIDPreservesDatabaseCause(t *testing.T) {
	t.Parallel()

	raw := errors.New("database unavailable")
	repository := New(&fakeDatabase{row: fakeRow{err: raw}})
	_, found, err := repository.GetCurrentUserByClerkUserID(context.Background(), "user_test")
	if found {
		t.Fatal("database error must not be reported as found")
	}
	if !errors.Is(err, raw) {
		t.Fatalf("error = %v, want wrapped cause", err)
	}
	if err.Error() != "read current local identity" {
		t.Fatalf("public error = %q", err)
	}
}

func TestGetCurrentUserByClerkUserIDMapsNarrowModel(t *testing.T) {
	t.Parallel()

	id := uuid.MustParse("0198f3be-bf6f-7b0a-8a25-f8433567e0c1")
	email := "developer@example.com"
	createdAt := time.Date(2026, time.August, 3, 3, 4, 5, 0, time.UTC)
	updatedAt := createdAt.Add(time.Hour)
	repository := New(&fakeDatabase{row: fakeRow{scan: func(destinations ...any) error {
		*destinations[0].(*uuid.UUID) = id
		*destinations[1].(*string) = "user_test"
		*destinations[2].(**string) = &email
		*destinations[3].(*string) = "bw012303082645"
		*destinations[4].(*string) = "active"
		*destinations[5].(*pgtype.Timestamptz) = pgtype.Timestamptz{Time: createdAt, Valid: true}
		*destinations[6].(*pgtype.Timestamptz) = pgtype.Timestamptz{Time: updatedAt, Valid: true}
		return nil
	}}})

	user, found, err := repository.GetCurrentUserByClerkUserID(context.Background(), "user_test")
	if err != nil || !found {
		t.Fatalf("found=%t err=%v", found, err)
	}
	if user.ID != id || user.ClerkUserID != "user_test" || user.IDUser != "bw012303082645" ||
		user.Status != "active" || user.PrimaryEmail == nil || *user.PrimaryEmail != email ||
		!user.CreatedAt.Equal(createdAt) || !user.UpdatedAt.Equal(updatedAt) {
		t.Fatalf("unexpected current user model: %+v", user)
	}
}

func TestEventRankMakesDeleteWinEqualTimestamp(t *testing.T) {
	t.Parallel()

	if eventRank("user.deleted") <= eventRank("user.updated") ||
		eventRank("user.updated") <= eventRank("user.created") {
		t.Fatal("event precedence must be deleted > updated > created")
	}
	if eventRank("unknown") != 0 {
		t.Fatal("unknown event rank must be zero")
	}
}

type fakeDatabase struct {
	row pgx.Row
}

func (d *fakeDatabase) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, nil
}

func (d *fakeDatabase) Query(context.Context, string, ...any) (pgx.Rows, error) {
	return nil, errors.New("unexpected Query call")
}

func (d *fakeDatabase) QueryRow(context.Context, string, ...any) pgx.Row {
	return d.row
}

func (d *fakeDatabase) Begin(context.Context) (pgx.Tx, error) {
	return nil, errors.New("unexpected Begin call")
}

type fakeRow struct {
	err  error
	scan func(...any) error
}

func (r fakeRow) Scan(destinations ...any) error {
	if r.scan != nil {
		return r.scan(destinations...)
	}
	return r.err
}
