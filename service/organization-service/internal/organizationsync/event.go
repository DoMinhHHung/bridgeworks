package organizationsync

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

const (
	EventOrganizationCreated = "organization.created"
	EventOrganizationUpdated = "organization.updated"
	EventOrganizationDeleted = "organization.deleted"
	EventMembershipCreated   = "organizationMembership.created"
	EventMembershipUpdated   = "organizationMembership.updated"
	EventMembershipDeleted   = "organizationMembership.deleted"

	AggregateOrganization = "organization"
	AggregateMembership   = "membership"

	RoleAdmin      = "admin"
	RoleViewer     = "viewer"
	ClerkRoleAdmin = "org:admin"
)

var ErrInvalidEvent = errors.New("invalid organization webhook event")

type Event struct {
	EventID             string
	Type                string
	AggregateType       string
	AggregateID         string
	ClerkOrganizationID string
	OccurredAt          time.Time
	Organization        OrganizationProjection
	Membership          MembershipProjection
}
type OrganizationProjection struct {
	Name *string
	Slug *string
}
type MembershipProjection struct {
	ClerkMembershipID, ClerkUserID string
	ClerkRole                      *string
}

type envelope struct {
	Type      string          `json:"type"`
	Timestamp int64           `json:"timestamp"`
	Data      json.RawMessage `json:"data"`
}
type organizationData struct {
	ID   string  `json:"id"`
	Name *string `json:"name"`
	Slug *string `json:"slug"`
}
type membershipData struct {
	ID           string `json:"id"`
	Organization *struct {
		ID string `json:"id"`
	} `json:"organization"`
	PublicUserData *struct {
		UserID string `json:"user_id"`
	} `json:"public_user_data"`
	Role *string `json:"role"`
}

func Decode(eventID string, payload []byte) (Event, bool, error) {
	eventID = strings.TrimSpace(eventID)
	if eventID == "" {
		return Event{}, false, ErrInvalidEvent
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	var raw envelope
	if err := decoder.Decode(&raw); err != nil {
		return Event{}, false, ErrInvalidEvent
	}
	if decoder.Decode(&struct{}{}) == nil {
		return Event{}, false, ErrInvalidEvent
	}
	raw.Type = strings.TrimSpace(raw.Type)
	if !Supported(raw.Type) {
		return Event{}, false, nil
	}
	if raw.Timestamp <= 0 || len(raw.Data) == 0 {
		return Event{}, true, ErrInvalidEvent
	}
	occurredAt := time.UnixMilli(raw.Timestamp).UTC()
	if occurredAt.IsZero() || occurredAt.Year() < 1 || occurredAt.Year() > 9999 {
		return Event{}, true, ErrInvalidEvent
	}
	event := Event{EventID: eventID, Type: raw.Type, OccurredAt: occurredAt}
	if strings.HasPrefix(raw.Type, "organizationMembership.") {
		var data membershipData
		if err := json.Unmarshal(raw.Data, &data); err != nil {
			return Event{}, true, ErrInvalidEvent
		}
		membershipID := strings.TrimSpace(data.ID)
		if membershipID == "" || data.Organization == nil || data.PublicUserData == nil {
			return Event{}, true, ErrInvalidEvent
		}
		organizationID := strings.TrimSpace(data.Organization.ID)
		userID := strings.TrimSpace(data.PublicUserData.UserID)
		if organizationID == "" || userID == "" {
			return Event{}, true, ErrInvalidEvent
		}
		event.AggregateType = AggregateMembership
		event.AggregateID = membershipID
		event.ClerkOrganizationID = organizationID
		event.Membership = MembershipProjection{ClerkMembershipID: membershipID, ClerkUserID: userID, ClerkRole: normalizeOptional(data.Role)}
		return event, true, nil
	}
	var data organizationData
	if err := json.Unmarshal(raw.Data, &data); err != nil {
		return Event{}, true, ErrInvalidEvent
	}
	organizationID := strings.TrimSpace(data.ID)
	if organizationID == "" {
		return Event{}, true, ErrInvalidEvent
	}
	event.AggregateType = AggregateOrganization
	event.AggregateID = organizationID
	event.ClerkOrganizationID = organizationID
	event.Organization = OrganizationProjection{Name: normalizeOptional(data.Name), Slug: normalizeOptional(data.Slug)}
	return event, true, nil
}

func Supported(eventType string) bool {
	switch eventType {
	case EventOrganizationCreated, EventOrganizationUpdated, EventOrganizationDeleted,
		EventMembershipCreated, EventMembershipUpdated, EventMembershipDeleted:
		return true
	default:
		return false
	}
}
func EventRank(eventType string) int {
	switch eventType {
	case EventOrganizationDeleted, EventMembershipDeleted:
		return 3
	case EventOrganizationUpdated, EventMembershipUpdated:
		return 2
	case EventOrganizationCreated, EventMembershipCreated:
		return 1
	default:
		return 0
	}
}
func IsStale(incoming Event, latest Event) bool {
	if latest.EventID == "" {
		return false
	}
	if incoming.OccurredAt.Before(latest.OccurredAt) {
		return true
	}
	if incoming.OccurredAt.After(latest.OccurredAt) {
		return false
	}
	incomingRank, latestRank := EventRank(incoming.Type), EventRank(latest.Type)
	if incomingRank < latestRank {
		return true
	}
	if incomingRank > latestRank {
		return false
	}
	return incoming.EventID <= latest.EventID
}
func InitialApplicationRole(clerkRole *string) string {
	if clerkRole != nil && *clerkRole == ClerkRoleAdmin {
		return RoleAdmin
	}
	return RoleViewer
}
func normalizeOptional(value *string) *string {
	if value == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*value)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}
