package organizationaudit

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/DoMinhHHung/bridgeworks/service/organization-service/internal/authorization"
	"github.com/DoMinhHHung/bridgeworks/service/organization-service/internal/currentorganization"
	"github.com/DoMinhHHung/bridgeworks/service/organization-service/internal/platform/safeerr"
	"github.com/google/uuid"
)

const (
	DefaultPageSize = 20
	MaxPageSize     = 50
)

var ErrInvalidCursor = errors.New("invalid audit cursor")

type Record struct {
	ID                  uuid.UUID
	EventType           string
	ActorKind           string
	SubjectMembershipID *uuid.UUID
	FromValue           *string
	ToValue             *string
	OccurredAt          time.Time
}

type Reader interface {
	ListOrganizationAuditEvents(context.Context, uuid.UUID, time.Time, uuid.UUID, int32) ([]Record, error)
}

type Page struct {
	Items      []Record
	NextCursor *string
}

type Service struct {
	reader Reader
}

func New(reader Reader) *Service {
	return &Service{reader: reader}
}

func (s *Service) List(
	ctx context.Context,
	actor authorization.ActorContext,
	limit int,
	cursor string,
) (Page, error) {
	if !actor.HasPermission(authorization.PermissionOrganizationAuditRead) {
		return Page{}, currentorganization.ErrPermissionDenied
	}
	if s == nil || s.reader == nil {
		return Page{}, errors.New("organization audit service is not initialized")
	}
	if limit <= 0 {
		limit = DefaultPageSize
	}
	if limit > MaxPageSize {
		return Page{}, ErrInvalidCursor
	}
	beforeTime, beforeID, err := decodeCursor(cursor)
	if err != nil {
		return Page{}, err
	}
	rows, err := s.reader.ListOrganizationAuditEvents(ctx, actor.OrganizationID, beforeTime, beforeID, int32(limit+1))
	if err != nil {
		return Page{}, safeerr.Wrap("list organization audit events", err)
	}
	page := Page{Items: rows}
	if len(rows) > limit {
		page.Items = rows[:limit]
		last := page.Items[len(page.Items)-1]
		next, err := encodeCursor(last.OccurredAt, last.ID)
		if err != nil {
			return Page{}, safeerr.Wrap("encode audit cursor", err)
		}
		page.NextCursor = &next
	}
	return page, nil
}

type cursorValue struct {
	OccurredAt string `json:"occurred_at"`
	ID         string `json:"id"`
}

func decodeCursor(raw string) (time.Time, uuid.UUID, error) {
	if strings.TrimSpace(raw) == "" {
		return time.Date(9999, 12, 31, 23, 59, 59, 0, time.UTC), uuid.MustParse("ffffffff-ffff-ffff-ffff-ffffffffffff"), nil
	}
	if len(raw) > 512 {
		return time.Time{}, uuid.Nil, ErrInvalidCursor
	}
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return time.Time{}, uuid.Nil, ErrInvalidCursor
	}
	var cursor cursorValue
	decoder := json.NewDecoder(strings.NewReader(string(decoded)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cursor); err != nil {
		return time.Time{}, uuid.Nil, ErrInvalidCursor
	}
	occurredAt, err := time.Parse(time.RFC3339Nano, cursor.OccurredAt)
	if err != nil {
		return time.Time{}, uuid.Nil, ErrInvalidCursor
	}
	id, err := uuid.Parse(cursor.ID)
	if err != nil {
		return time.Time{}, uuid.Nil, ErrInvalidCursor
	}
	return occurredAt.UTC(), id, nil
}

func encodeCursor(occurredAt time.Time, id uuid.UUID) (string, error) {
	payload, err := json.Marshal(cursorValue{
		OccurredAt: occurredAt.UTC().Format(time.RFC3339Nano),
		ID:         id.String(),
	})
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(payload), nil
}
