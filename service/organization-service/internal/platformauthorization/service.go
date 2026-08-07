package platformauthorization

import (
	"context"
	"errors"
)

const (
	RolePlatformAdmin                        = "platform_admin"
	PermissionOrganizationVerificationReview = "organization.verification.review"
)

var (
	ErrUnauthorized     = errors.New("platform identity is unauthorized")
	ErrAccountInactive  = errors.New("platform identity account is inactive")
	ErrIdentityNotReady = errors.New("platform identity is not ready")
	ErrUnavailable      = errors.New("platform authorization is unavailable")
	ErrPermissionDenied = errors.New("platform permission denied")
)

type Access struct {
	Roles       []string
	Permissions []string
}

type Reader interface {
	ResolvePlatformAccess(context.Context, string, string) (Access, error)
}

type Service struct {
	reader Reader
}

func New(reader Reader) *Service {
	return &Service{reader: reader}
}

func (s *Service) Resolve(
	ctx context.Context,
	authorizationHeader string,
	requestID string,
) (Access, error) {
	if s == nil || s.reader == nil {
		return Access{}, ErrUnavailable
	}
	access, err := s.reader.ResolvePlatformAccess(ctx, authorizationHeader, requestID)
	if err != nil {
		switch {
		case errors.Is(err, ErrUnauthorized):
			return Access{}, ErrUnauthorized
		case errors.Is(err, ErrAccountInactive):
			return Access{}, ErrAccountInactive
		case errors.Is(err, ErrIdentityNotReady):
			return Access{}, ErrIdentityNotReady
		default:
			return Access{}, ErrUnavailable
		}
	}
	if !validAccess(access) {
		return Access{}, ErrUnavailable
	}
	return access, nil
}

func RequirePermission(access Access, permission string) error {
	for _, candidate := range access.Permissions {
		if candidate == permission {
			return nil
		}
	}
	return ErrPermissionDenied
}

func validAccess(access Access) bool {
	if len(access.Roles) == 0 && len(access.Permissions) == 0 {
		return true
	}
	if len(access.Roles) != 1 || len(access.Permissions) != 1 {
		return false
	}
	return access.Roles[0] == RolePlatformAdmin &&
		access.Permissions[0] == PermissionOrganizationVerificationReview
}
