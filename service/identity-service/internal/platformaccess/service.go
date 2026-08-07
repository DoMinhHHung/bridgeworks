package platformaccess

import (
	"context"
	"errors"

	"github.com/google/uuid"
)

const (
	RolePlatformAdmin                       = "platform_admin"
	PermissionOrganizationVerificationReview = "organization.verification.review"
)

var (
	ErrIdentityNotReady = errors.New("identity synchronization is not complete")
	ErrAccountDisabled  = errors.New("account is disabled")
	ErrAccountDeleted   = errors.New("account is deleted")
)

type User struct {
	ID     uuid.UUID
	Status string
}

type Access struct {
	Roles       []string
	Permissions []string
}

type Reader interface {
	GetPlatformAccessUserByClerkUserID(context.Context, string) (User, bool, error)
	ListActivePlatformRolesByUserID(context.Context, uuid.UUID) ([]string, error)
}

type Service struct {
	reader Reader
}

func New(reader Reader) *Service {
	return &Service{reader: reader}
}

func (s *Service) Resolve(ctx context.Context, clerkUserID string) (Access, error) {
	if s == nil || s.reader == nil {
		return Access{}, errors.New("platform access service is not initialized")
	}

	user, found, err := s.reader.GetPlatformAccessUserByClerkUserID(ctx, clerkUserID)
	if err != nil {
		return Access{}, err
	}
	if !found {
		return Access{}, ErrIdentityNotReady
	}

	switch user.Status {
	case "active":
	case "disabled":
		return Access{}, ErrAccountDisabled
	case "deleted":
		return Access{}, ErrAccountDeleted
	default:
		return Access{}, errors.New("local account has an unsupported status")
	}

	roles, err := s.reader.ListActivePlatformRolesByUserID(ctx, user.ID)
	if err != nil {
		return Access{}, err
	}

	access := Access{
		Roles:       make([]string, 0, len(roles)),
		Permissions: make([]string, 0, len(roles)),
	}
	seen := make(map[string]struct{}, len(roles))
	for _, role := range roles {
		if _, duplicate := seen[role]; duplicate {
			return Access{}, errors.New("duplicate active platform role")
		}
		seen[role] = struct{}{}

		switch role {
		case RolePlatformAdmin:
			access.Roles = append(access.Roles, RolePlatformAdmin)
			access.Permissions = append(access.Permissions, PermissionOrganizationVerificationReview)
		default:
			return Access{}, errors.New("unrecognized active platform role")
		}
	}

	return access, nil
}
