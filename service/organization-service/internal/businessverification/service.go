package businessverification

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode"

	"github.com/DoMinhHHung/bridgeworks/service/organization-service/internal/authorization"
	"github.com/DoMinhHHung/bridgeworks/service/organization-service/internal/platform/safeerr"
	"github.com/google/uuid"
)

var (
	ErrVerifiedPrimaryEmailRequired = errors.New("verified primary email required")
	ErrInvalidBusinessEmail         = errors.New("invalid business email")
	ErrPersonalEmailDomain          = errors.New("personal email domain not allowed")
	ErrPermissionDenied             = errors.New("permission denied")
	ErrMembershipNotActive          = errors.New("membership not active")
)

type Membership struct {
	ID              uuid.UUID
	OrganizationID  uuid.UUID
	ApplicationRole string
	Status          string
}

type UnitOfWork interface {
	AcquireOrganizationLock(context.Context, uuid.UUID) error
	LockMembership(context.Context, uuid.UUID, uuid.UUID) (Membership, bool, error)
	UpdateBusinessEmailVerification(context.Context, uuid.UUID, string, time.Time, uuid.UUID) error
	Commit(context.Context) error
	Rollback(context.Context) error
}

type UnitOfWorkFactory interface {
	BeginBusinessVerification(context.Context) (UnitOfWork, error)
}

type Result struct {
	Domain     string
	VerifiedAt time.Time
}

type Service struct {
	factory        UnitOfWorkFactory
	personalDomain map[string]struct{}
	now            func() time.Time
}

func New(factory UnitOfWorkFactory, personalDomains []string) (*Service, error) {
	policy := make(map[string]struct{}, len(personalDomains))
	for _, raw := range personalDomains {
		domain, err := normalizeDomain(raw)
		if err != nil {
			return nil, errors.New("invalid personal email domain policy")
		}
		policy[domain] = struct{}{}
	}
	if len(policy) == 0 {
		return nil, errors.New("personal email domain policy must not be empty")
	}
	return &Service{factory: factory, personalDomain: policy, now: time.Now}, nil
}

func (s *Service) Verify(
	ctx context.Context,
	actor authorization.ActorContext,
	verifiedPrimaryEmail *string,
) (result Result, err error) {
	if verifiedPrimaryEmail == nil {
		return Result{}, ErrVerifiedPrimaryEmailRequired
	}
	domain, err := domainFromEmail(*verifiedPrimaryEmail)
	if err != nil {
		return Result{}, err
	}
	if s.isPersonalDomain(domain) {
		return Result{}, ErrPersonalEmailDomain
	}

	uow, err := s.factory.BeginBusinessVerification(ctx)
	if err != nil {
		return Result{}, safeerr.Wrap("begin business email verification transaction", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = uow.Rollback(ctx)
		}
	}()

	if err := uow.AcquireOrganizationLock(ctx, actor.OrganizationID); err != nil {
		return Result{}, safeerr.Wrap("lock organization for business email verification", err)
	}
	membership, found, err := uow.LockMembership(ctx, actor.OrganizationID, actor.MembershipID)
	if err != nil {
		return Result{}, safeerr.Wrap("lock actor membership for business email verification", err)
	}
	if !found || membership.Status != "active" {
		return Result{}, ErrMembershipNotActive
	}
	if membership.ApplicationRole != "owner" && membership.ApplicationRole != "admin" {
		return Result{}, ErrPermissionDenied
	}

	verifiedAt := s.now().UTC()
	if err := uow.UpdateBusinessEmailVerification(
		ctx,
		actor.OrganizationID,
		domain,
		verifiedAt,
		actor.IdentityUserID,
	); err != nil {
		return Result{}, safeerr.Wrap("persist business email verification", err)
	}
	if err := uow.Commit(ctx); err != nil {
		return Result{}, safeerr.Wrap("commit business email verification", err)
	}
	committed = true
	return Result{Domain: domain, VerifiedAt: verifiedAt}, nil
}

func (s *Service) isPersonalDomain(domain string) bool {
	for blocked := range s.personalDomain {
		if domain == blocked || strings.HasSuffix(domain, "."+blocked) {
			return true
		}
	}
	return false
}

func domainFromEmail(raw string) (string, error) {
	email := strings.TrimSpace(raw)
	if len(email) == 0 || len(email) > 320 || strings.Count(email, "@") != 1 {
		return "", ErrInvalidBusinessEmail
	}
	local, domain, ok := strings.Cut(email, "@")
	if !ok || local == "" || domain == "" || len(local) > 64 {
		return "", ErrInvalidBusinessEmail
	}
	for _, r := range local {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return "", ErrInvalidBusinessEmail
		}
	}
	normalized, err := normalizeDomain(domain)
	if err != nil {
		return "", ErrInvalidBusinessEmail
	}
	return normalized, nil
}

func normalizeDomain(raw string) (string, error) {
	domain := strings.ToLower(strings.TrimSpace(raw))
	if domain == "" || len(domain) > 253 || strings.HasPrefix(domain, "[") || strings.HasSuffix(domain, ".") {
		return "", ErrInvalidBusinessEmail
	}
	if !strings.Contains(domain, ".") {
		return "", ErrInvalidBusinessEmail
	}
	labels := strings.Split(domain, ".")
	for _, label := range labels {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return "", ErrInvalidBusinessEmail
		}
		for _, r := range label {
			if r > unicode.MaxASCII || !((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-') {
				return "", ErrInvalidBusinessEmail
			}
		}
	}
	return domain, nil
}
