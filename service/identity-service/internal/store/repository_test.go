package store

import (
	"errors"
	"testing"

	"github.com/DoMinhHHung/bridgeworks/service/identity-service/internal/platform/safeerr"
	"github.com/jackc/pgx/v5/pgconn"
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
