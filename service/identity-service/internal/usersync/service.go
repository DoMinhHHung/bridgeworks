package usersync

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/DoMinhHHung/bridgeworks/service/identity-service/internal/clerkwebhook"
	"github.com/google/uuid"
)

const (
	maxIDUserAttempts = 5
	rollbackTimeout   = 2 * time.Second
)

var ErrIDUserCollisionExhausted = errors.New("unable to generate a unique id_user")

type User struct {
	ID           uuid.UUID
	ClerkUserID  string
	PrimaryEmail *string
	IDUser       string
	Status       string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

type Transaction interface {
	LockClerkUser(context.Context, string) error
	InsertInboxEvent(context.Context, clerkwebhook.Event) (bool, error)
	HasSupersedingEvent(context.Context, clerkwebhook.Event) (bool, error)
	GetUserByClerkID(context.Context, string) (User, bool, error)
	InsertUser(context.Context, User) error
	UpdatePrimaryEmail(context.Context, string, *string) error
	MarkDeleted(context.Context, string) error
	Commit(context.Context) error
	Rollback(context.Context) error
}

type Repository interface {
	Begin(context.Context) (Transaction, error)
	IsIDUserCollision(error) bool
}

type UUIDGenerator interface {
	New() (uuid.UUID, error)
}

type IDUserGenerator interface {
	Generate() (string, error)
}

type Service struct {
	repository      Repository
	uuidGenerator   UUIDGenerator
	idUserGenerator IDUserGenerator
}

func New(repository Repository, uuidGenerator UUIDGenerator, idUserGenerator IDUserGenerator) *Service {
	return &Service{
		repository:      repository,
		uuidGenerator:   uuidGenerator,
		idUserGenerator: idUserGenerator,
	}
}

func (s *Service) Process(ctx context.Context, event clerkwebhook.Event) error {
	if s == nil || s.repository == nil || s.uuidGenerator == nil || s.idUserGenerator == nil {
		return errors.New("user synchronization service is not initialized")
	}

	transaction, err := s.repository.Begin(ctx)
	if err != nil {
		return err
	}
	defer rollback(transaction, ctx)

	inserted, err := transaction.InsertInboxEvent(ctx, event)
	if err != nil {
		return err
	}
	if !inserted {
		return transaction.Commit(ctx)
	}

	if err := transaction.LockClerkUser(ctx, event.ClerkUserID); err != nil {
		return err
	}

	stale, err := transaction.HasSupersedingEvent(ctx, event)
	if err != nil {
		return err
	}
	if stale {
		return transaction.Commit(ctx)
	}

	_, exists, err := transaction.GetUserByClerkID(ctx, event.ClerkUserID)
	if err != nil {
		return err
	}

	switch event.Type {
	case clerkwebhook.EventUserCreated, clerkwebhook.EventUserUpdated:
		if exists {
			if err := transaction.UpdatePrimaryEmail(ctx, event.ClerkUserID, event.PrimaryEmail); err != nil {
				return err
			}
		} else if err := s.insertUser(ctx, transaction, event, "active", event.PrimaryEmail); err != nil {
			return err
		}
	case clerkwebhook.EventUserDeleted:
		if exists {
			if err := transaction.MarkDeleted(ctx, event.ClerkUserID); err != nil {
				return err
			}
		} else if err := s.insertUser(ctx, transaction, event, "deleted", nil); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unsupported synchronized event type %q", event.Type)
	}

	return transaction.Commit(ctx)
}

func (s *Service) insertUser(
	ctx context.Context,
	transaction Transaction,
	event clerkwebhook.Event,
	status string,
	primaryEmail *string,
) error {
	userID, err := s.uuidGenerator.New()
	if err != nil {
		return fmt.Errorf("generate UUIDv7: %w", err)
	}

	for attempt := 1; attempt <= maxIDUserAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}

		idUser, err := s.idUserGenerator.Generate()
		if err != nil {
			return fmt.Errorf("generate id_user: %w", err)
		}

		err = transaction.InsertUser(ctx, User{
			ID:           userID,
			ClerkUserID:  event.ClerkUserID,
			PrimaryEmail: primaryEmail,
			IDUser:       idUser,
			Status:       status,
		})
		if err == nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if !s.repository.IsIDUserCollision(err) {
			return err
		}
	}

	return ErrIDUserCollisionExhausted
}

func rollback(transaction Transaction, parent context.Context) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), rollbackTimeout)
	defer cancel()
	_ = transaction.Rollback(ctx)
}
