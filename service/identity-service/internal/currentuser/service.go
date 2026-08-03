package currentuser

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
)

var (
	ErrIdentityNotReady = errors.New("identity synchronization is not complete")
	ErrAccountDisabled  = errors.New("account is disabled")
	ErrAccountDeleted   = errors.New("account is deleted")
)

type User struct {
	ID           uuid.UUID
	ClerkUserID  string
	PrimaryEmail *string
	IDUser       string
	Status       string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

type Reader interface {
	GetCurrentUserByClerkUserID(context.Context, string) (User, bool, error)
}

type Service struct {
	reader Reader
}

func New(reader Reader) *Service {
	return &Service{reader: reader}
}

func (s *Service) Get(ctx context.Context, clerkUserID string) (User, error) {
	if s == nil || s.reader == nil {
		return User{}, errors.New("current user service is not initialized")
	}

	user, found, err := s.reader.GetCurrentUserByClerkUserID(ctx, clerkUserID)
	if err != nil {
		return User{}, err
	}
	if !found {
		return User{}, ErrIdentityNotReady
	}

	switch user.Status {
	case "active":
		user.CreatedAt = user.CreatedAt.UTC()
		user.UpdatedAt = user.UpdatedAt.UTC()
		return user, nil
	case "disabled":
		return User{}, ErrAccountDisabled
	case "deleted":
		return User{}, ErrAccountDeleted
	default:
		return User{}, errors.New("local account has an unsupported status")
	}
}
