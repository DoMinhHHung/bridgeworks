package platformaccessoperator

import (
	"context"
	"errors"
	"time"

	"github.com/DoMinhHHung/bridgeworks/service/identity-service/internal/platformaccess"
	"github.com/google/uuid"
)

var (
	ErrUserNotFound = errors.New("identity user was not found")
	ErrUserInactive = errors.New("platform access may only be granted to an active identity user")
)

type User struct {
	ID     uuid.UUID
	Status string
}

type Assignment struct {
	GrantedAt time.Time
	RevokedAt *time.Time
	UpdatedAt time.Time
}

type Status struct {
	Role      string
	Assigned  bool
	Active    bool
	GrantedAt time.Time
	RevokedAt *time.Time
	UpdatedAt time.Time
}

type Repository interface {
	GetPlatformAccessUserByIDUser(context.Context, string) (User, bool, error)
	GrantPlatformAccess(context.Context, uuid.UUID, string) error
	RevokePlatformAccess(context.Context, uuid.UUID, string) error
	GetPlatformAccessAssignment(context.Context, uuid.UUID, string) (Assignment, bool, error)
}

type Service struct {
	repository Repository
}

func New(repository Repository) *Service {
	return &Service{repository: repository}
}

func (s *Service) Grant(ctx context.Context, idUser string) (Status, error) {
	user, err := s.user(ctx, idUser)
	if err != nil {
		return Status{}, err
	}
	if user.Status != "active" {
		return Status{}, ErrUserInactive
	}
	if err := s.repository.GrantPlatformAccess(ctx, user.ID, platformaccess.RolePlatformAdmin); err != nil {
		return Status{}, err
	}
	return s.statusForUser(ctx, user)
}

func (s *Service) Revoke(ctx context.Context, idUser string) (Status, error) {
	user, err := s.user(ctx, idUser)
	if err != nil {
		return Status{}, err
	}
	if err := s.repository.RevokePlatformAccess(ctx, user.ID, platformaccess.RolePlatformAdmin); err != nil {
		return Status{}, err
	}
	return s.statusForUser(ctx, user)
}

func (s *Service) Status(ctx context.Context, idUser string) (Status, error) {
	user, err := s.user(ctx, idUser)
	if err != nil {
		return Status{}, err
	}
	return s.statusForUser(ctx, user)
}

func (s *Service) user(ctx context.Context, idUser string) (User, error) {
	if s == nil || s.repository == nil {
		return User{}, errors.New("platform access operator service is not initialized")
	}
	user, found, err := s.repository.GetPlatformAccessUserByIDUser(ctx, idUser)
	if err != nil {
		return User{}, err
	}
	if !found {
		return User{}, ErrUserNotFound
	}
	return user, nil
}

func (s *Service) statusForUser(ctx context.Context, user User) (Status, error) {
	assignment, found, err := s.repository.GetPlatformAccessAssignment(ctx, user.ID, platformaccess.RolePlatformAdmin)
	if err != nil {
		return Status{}, err
	}
	status := Status{Role: platformaccess.RolePlatformAdmin, Assigned: found}
	if !found {
		return status, nil
	}
	status.Active = assignment.RevokedAt == nil
	status.GrantedAt = assignment.GrantedAt.UTC()
	status.RevokedAt = assignment.RevokedAt
	status.UpdatedAt = assignment.UpdatedAt.UTC()
	return status, nil
}
