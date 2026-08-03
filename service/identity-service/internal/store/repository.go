package store

import (
	"context"
	"errors"

	"github.com/DoMinhHHung/bridgeworks/service/identity-service/internal/clerkwebhook"
	"github.com/DoMinhHHung/bridgeworks/service/identity-service/internal/platform/safeerr"
	"github.com/DoMinhHHung/bridgeworks/service/identity-service/internal/store/sqlcgen"
	"github.com/DoMinhHHung/bridgeworks/service/identity-service/internal/usersync"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const idUserUniqueConstraint = "app_users_id_user_uq"

type transactionBeginner interface {
	Begin(context.Context) (pgx.Tx, error)
}

type Repository struct {
	database transactionBeginner
}

func New(database transactionBeginner) *Repository {
	return &Repository{database: database}
}

func (r *Repository) Begin(ctx context.Context) (usersync.Transaction, error) {
	if r == nil || r.database == nil {
		return nil, errors.New("identity repository is not initialized")
	}

	tx, err := r.database.Begin(ctx)
	if err != nil {
		return nil, safeerr.Wrap("begin identity transaction", err)
	}

	return &transaction{
		tx:      tx,
		queries: sqlcgen.New(tx),
	}, nil
}

func (r *Repository) IsIDUserCollision(err error) bool {
	var postgresError *pgconn.PgError
	return errors.As(err, &postgresError) &&
		postgresError.Code == "23505" &&
		postgresError.ConstraintName == idUserUniqueConstraint
}

type transaction struct {
	tx      pgx.Tx
	queries *sqlcgen.Queries
}

func (t *transaction) LockClerkUser(ctx context.Context, clerkUserID string) error {
	if err := t.queries.LockClerkUser(ctx, clerkUserID); err != nil {
		return safeerr.Wrap("lock Clerk user projection", err)
	}
	return nil
}

func (t *transaction) InsertInboxEvent(ctx context.Context, event clerkwebhook.Event) (bool, error) {
	inserted, err := t.queries.InsertClerkWebhookEvent(ctx, sqlcgen.InsertClerkWebhookEventParams{
		EventID:     event.EventID,
		EventType:   event.Type,
		ClerkUserID: event.ClerkUserID,
		OccurredAt:  event.OccurredAt,
	})
	if err != nil {
		return false, safeerr.Wrap("insert Clerk webhook inbox event", err)
	}
	return inserted, nil
}

func (t *transaction) HasSupersedingEvent(ctx context.Context, event clerkwebhook.Event) (bool, error) {
	hasSupersedingEvent, err := t.queries.HasSupersedingClerkWebhookEvent(ctx, sqlcgen.HasSupersedingClerkWebhookEventParams{
		ClerkUserID: event.ClerkUserID,
		EventID:     event.EventID,
		OccurredAt:  event.OccurredAt,
		EventRank:   eventRank(event.Type),
	})
	if err != nil {
		return false, safeerr.Wrap("check Clerk webhook event ordering", err)
	}
	return hasSupersedingEvent, nil
}

func (t *transaction) GetUserByClerkID(ctx context.Context, clerkUserID string) (usersync.User, bool, error) {
	row, err := t.queries.GetAppUserByClerkID(ctx, clerkUserID)
	if errors.Is(err, pgx.ErrNoRows) {
		return usersync.User{}, false, nil
	}
	if err != nil {
		return usersync.User{}, false, safeerr.Wrap("read local Clerk user projection", err)
	}

	return usersync.User{
		ID:           row.ID,
		ClerkUserID:  row.ClerkUserID,
		PrimaryEmail: row.PrimaryEmail,
		IDUser:       row.IDUser,
		Status:       row.Status,
		CreatedAt:    row.CreatedAt,
		UpdatedAt:    row.UpdatedAt,
	}, true, nil
}

func (t *transaction) InsertUser(ctx context.Context, user usersync.User) error {
	// pgx implements nested transactions as savepoints. A savepoint keeps the
	// outer inbox transaction usable after an id_user unique violation.
	savepoint, err := t.tx.Begin(ctx)
	if err != nil {
		return safeerr.Wrap("begin app user insert savepoint", err)
	}

	queries := sqlcgen.New(savepoint)
	if err := queries.InsertAppUser(ctx, sqlcgen.InsertAppUserParams{
		ID:           user.ID,
		ClerkUserID:  user.ClerkUserID,
		PrimaryEmail: user.PrimaryEmail,
		IDUser:       user.IDUser,
		Status:       user.Status,
	}); err != nil {
		if rollbackErr := savepoint.Rollback(ctx); rollbackErr != nil && !errors.Is(rollbackErr, pgx.ErrTxClosed) {
			return safeerr.Wrap("rollback app user insert savepoint", rollbackErr)
		}
		return safeerr.Wrap("insert local Clerk user projection", err)
	}

	if err := savepoint.Commit(ctx); err != nil {
		return safeerr.Wrap("commit app user insert savepoint", err)
	}
	return nil
}

func (t *transaction) UpdatePrimaryEmail(ctx context.Context, clerkUserID string, primaryEmail *string) error {
	if err := t.queries.UpdateAppUserPrimaryEmail(ctx, sqlcgen.UpdateAppUserPrimaryEmailParams{
		PrimaryEmail: primaryEmail,
		ClerkUserID:  clerkUserID,
	}); err != nil {
		return safeerr.Wrap("update local primary email projection", err)
	}
	return nil
}

func (t *transaction) MarkDeleted(ctx context.Context, clerkUserID string) error {
	if err := t.queries.MarkAppUserDeleted(ctx, clerkUserID); err != nil {
		return safeerr.Wrap("mark local Clerk user projection deleted", err)
	}
	return nil
}

func (t *transaction) Commit(ctx context.Context) error {
	if err := t.tx.Commit(ctx); err != nil {
		return safeerr.Wrap("commit identity transaction", err)
	}
	return nil
}

func (t *transaction) Rollback(ctx context.Context) error {
	if err := t.tx.Rollback(ctx); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
		return safeerr.Wrap("rollback identity transaction", err)
	}
	return nil
}

func eventRank(eventType string) int32 {
	switch eventType {
	case clerkwebhook.EventUserDeleted:
		return 3
	case clerkwebhook.EventUserUpdated:
		return 2
	case clerkwebhook.EventUserCreated:
		return 1
	default:
		return 0
	}
}
