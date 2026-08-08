package platformreview

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/DoMinhHHung/bridgeworks/service/organization-service/internal/currentorganization"
	"github.com/DoMinhHHung/bridgeworks/service/organization-service/internal/organizationaudit"
	"github.com/DoMinhHHung/bridgeworks/service/organization-service/internal/organizationdomain"
	"github.com/DoMinhHHung/bridgeworks/service/organization-service/internal/platform/safeerr"
	"github.com/DoMinhHHung/bridgeworks/service/organization-service/internal/platformauthorization"
	"github.com/google/uuid"
)

const (
	DecisionVerified = organizationdomain.VerificationStatusVerified
	DecisionRejected = organizationdomain.VerificationStatusRejected
	MaxPageSize      = 50
	DefaultPageSize  = 20
)

var (
	ErrInvalidDecision           = errors.New("invalid verification decision")
	ErrInvalidCursor             = errors.New("invalid verification queue cursor")
	ErrOrganizationNotFound      = errors.New("organization not found")
	ErrOrganizationNotReviewable = errors.New("organization is not reviewable")
	ErrVerificationNotPending    = errors.New("verification is not pending")
	ErrDecisionConflict          = errors.New("verification decision conflict")
)

type IdentityReader interface {
	Resolve(context.Context, string, string) (currentorganization.Identity, error)
}

type PlatformAccessResolver interface {
	Resolve(context.Context, string, string) (platformauthorization.Access, error)
}

type QueueItem struct {
	ID                      uuid.UUID
	Name                    *string
	LegalName               *string
	Website                 *string
	Country                 *string
	CompanyType             *string
	VerificationStatus      string
	BusinessEmailDomain     *string
	BusinessEmailVerifiedAt *time.Time
	UpdatedAt               time.Time
	RequestedAt             time.Time
}

type QueueReader interface {
	ListPendingVerificationQueue(context.Context, time.Time, uuid.UUID, int32) ([]QueueItem, error)
}

type ReviewUnitOfWork interface {
	LockOrganization(context.Context, uuid.UUID) (currentorganization.Organization, bool, error)
	UpdateVerificationStatus(context.Context, uuid.UUID, string) (currentorganization.Organization, error)
	InsertAuditEvent(context.Context, organizationaudit.Event) error
	Commit(context.Context) error
	Rollback(context.Context) error
}

type ReviewUnitOfWorkFactory interface {
	BeginPlatformReview(context.Context) (ReviewUnitOfWork, error)
}

type Reviewer struct {
	IdentityUserID uuid.UUID
}

type QueuePage struct {
	Items      []QueueItem
	NextCursor *string
}

type Service struct {
	identity IdentityReader
	access   PlatformAccessResolver
	queue    QueueReader
	factory  ReviewUnitOfWorkFactory
}

func New(identity IdentityReader, access PlatformAccessResolver, queue QueueReader, factory ReviewUnitOfWorkFactory) *Service {
	return &Service{identity: identity, access: access, queue: queue, factory: factory}
}

func (s *Service) AuthorizeReviewer(
	ctx context.Context,
	authorizationHeader string,
	requestID string,
) (Reviewer, error) {
	if s == nil || s.identity == nil || s.access == nil {
		return Reviewer{}, platformauthorization.ErrUnavailable
	}
	identity, err := s.identity.Resolve(ctx, authorizationHeader, requestID)
	if err != nil {
		return Reviewer{}, err
	}
	access, err := s.access.Resolve(ctx, authorizationHeader, requestID)
	if err != nil {
		return Reviewer{}, err
	}
	if err := platformauthorization.RequirePermission(
		access,
		platformauthorization.PermissionOrganizationVerificationReview,
	); err != nil {
		return Reviewer{}, err
	}
	if identity.ID == uuid.Nil {
		return Reviewer{}, platformauthorization.ErrUnavailable
	}
	return Reviewer{IdentityUserID: identity.ID}, nil
}

func (s *Service) ListQueue(
	ctx context.Context,
	_ Reviewer,
	limit int,
	cursor string,
) (QueuePage, error) {
	if s == nil || s.queue == nil {
		return QueuePage{}, platformauthorization.ErrUnavailable
	}
	if limit <= 0 {
		limit = DefaultPageSize
	}
	if limit > MaxPageSize {
		return QueuePage{}, ErrInvalidCursor
	}
	afterTime, afterID, err := decodeQueueCursor(cursor)
	if err != nil {
		return QueuePage{}, err
	}
	rows, err := s.queue.ListPendingVerificationQueue(ctx, afterTime, afterID, int32(limit+1))
	if err != nil {
		return QueuePage{}, safeerr.Wrap("list verification queue", err)
	}
	page := QueuePage{Items: rows}
	if len(rows) > limit {
		page.Items = rows[:limit]
		last := page.Items[len(page.Items)-1]
		next, err := encodeQueueCursor(last.RequestedAt, last.ID)
		if err != nil {
			return QueuePage{}, safeerr.Wrap("encode verification queue cursor", err)
		}
		page.NextCursor = &next
	}
	return page, nil
}

func (s *Service) Decide(
	ctx context.Context,
	reviewer Reviewer,
	rawOrganizationID string,
	decision string,
) (organization currentorganization.Organization, err error) {
	if s == nil || s.factory == nil {
		return currentorganization.Organization{}, platformauthorization.ErrUnavailable
	}
	decision = strings.TrimSpace(decision)
	if decision != DecisionVerified && decision != DecisionRejected {
		return currentorganization.Organization{}, ErrInvalidDecision
	}
	organizationID, err := uuid.Parse(strings.TrimSpace(rawOrganizationID))
	if err != nil || organizationID == uuid.Nil {
		return currentorganization.Organization{}, ErrOrganizationNotFound
	}

	uow, err := s.factory.BeginPlatformReview(ctx)
	if err != nil {
		return currentorganization.Organization{}, safeerr.Wrap("begin platform verification review transaction", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = uow.Rollback(ctx)
		}
	}()

	organization, found, err := uow.LockOrganization(ctx, organizationID)
	if err != nil {
		return currentorganization.Organization{}, safeerr.Wrap("lock organization verification review row", err)
	}
	if !found {
		return currentorganization.Organization{}, ErrOrganizationNotFound
	}
	if organization.Status != "active" {
		return currentorganization.Organization{}, ErrOrganizationNotReviewable
	}

	current := organization.VerificationStatus
	if current == decision {
		if err := uow.Commit(ctx); err != nil {
			return currentorganization.Organization{}, safeerr.Wrap("commit idempotent verification decision", err)
		}
		committed = true
		return organization, nil
	}
	if current != organizationdomain.VerificationStatusPending {
		switch current {
		case organizationdomain.VerificationStatusVerified, organizationdomain.VerificationStatusRejected:
			return currentorganization.Organization{}, ErrDecisionConflict
		default:
			return currentorganization.Organization{}, ErrVerificationNotPending
		}
	}

	fromValue := current
	toValue := decision
	event, err := organizationaudit.PlatformEvent(
		organizationID,
		organizationaudit.EventOrganizationVerificationReviewed,
		reviewer.IdentityUserID,
		&fromValue,
		&toValue,
	)
	if err != nil {
		return currentorganization.Organization{}, safeerr.Wrap("create verification review audit event", err)
	}
	organization, err = uow.UpdateVerificationStatus(ctx, organizationID, decision)
	if err != nil {
		return currentorganization.Organization{}, safeerr.Wrap("update verification decision", err)
	}
	if err := uow.InsertAuditEvent(ctx, event); err != nil {
		return currentorganization.Organization{}, safeerr.Wrap("append verification review audit event", err)
	}
	if err := uow.Commit(ctx); err != nil {
		return currentorganization.Organization{}, safeerr.Wrap("commit verification decision", err)
	}
	committed = true
	return organization, nil
}

type queueCursor struct {
	RequestedAt string `json:"requested_at"`
	ID          string `json:"id"`
}

func decodeQueueCursor(raw string) (time.Time, uuid.UUID, error) {
	if strings.TrimSpace(raw) == "" {
		return time.Unix(0, 0).UTC(), uuid.Nil, nil
	}
	if len(raw) > 512 {
		return time.Time{}, uuid.Nil, ErrInvalidCursor
	}
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return time.Time{}, uuid.Nil, ErrInvalidCursor
	}
	var cursor queueCursor
	decoder := json.NewDecoder(strings.NewReader(string(decoded)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cursor); err != nil {
		return time.Time{}, uuid.Nil, ErrInvalidCursor
	}
	requestedAt, err := time.Parse(time.RFC3339Nano, cursor.RequestedAt)
	if err != nil {
		return time.Time{}, uuid.Nil, ErrInvalidCursor
	}
	id, err := uuid.Parse(cursor.ID)
	if err != nil {
		return time.Time{}, uuid.Nil, ErrInvalidCursor
	}
	return requestedAt.UTC(), id, nil
}

func encodeQueueCursor(requestedAt time.Time, id uuid.UUID) (string, error) {
	payload, err := json.Marshal(queueCursor{
		RequestedAt: requestedAt.UTC().Format(time.RFC3339Nano),
		ID:          id.String(),
	})
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(payload), nil
}
