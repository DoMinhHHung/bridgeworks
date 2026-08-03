package clerkwebhook

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"
)

const (
	EventUserCreated = "user.created"
	EventUserUpdated = "user.updated"
	EventUserDeleted = "user.deleted"
)

var ErrInvalidWebhook = errors.New("invalid webhook request")

type Event struct {
	EventID      string
	Type         string
	ClerkUserID  string
	OccurredAt   time.Time
	PrimaryEmail *string
	Supported    bool
}

type envelope struct {
	Type      string          `json:"type"`
	Timestamp int64           `json:"timestamp"`
	Data      json.RawMessage `json:"data"`
}

type userData struct {
	ID                    string         `json:"id"`
	PrimaryEmailAddressID string         `json:"primary_email_address_id"`
	EmailAddresses        []emailAddress `json:"email_addresses"`
}

type emailAddress struct {
	ID           string            `json:"id"`
	EmailAddress string            `json:"email_address"`
	Verification emailVerification `json:"verification"`
}

type emailVerification struct {
	Status string `json:"status"`
}

func parseEvent(eventID string, payload []byte) (Event, error) {
	var raw envelope
	if err := decodeSingleJSON(payload, &raw); err != nil {
		return Event{}, ErrInvalidWebhook
	}

	eventType := strings.TrimSpace(raw.Type)
	if eventType == "" {
		return Event{}, ErrInvalidWebhook
	}

	if !isSupportedEventType(eventType) {
		return Event{
			EventID:   eventID,
			Type:      eventType,
			Supported: false,
		}, nil
	}

	if raw.Timestamp <= 0 || raw.Timestamp > maxUnixMilli() {
		return Event{}, ErrInvalidWebhook
	}
	if len(raw.Data) == 0 || bytes.Equal(bytes.TrimSpace(raw.Data), []byte("null")) {
		return Event{}, ErrInvalidWebhook
	}

	var user userData
	if err := decodeSingleJSON(raw.Data, &user); err != nil {
		return Event{}, ErrInvalidWebhook
	}

	clerkUserID := strings.TrimSpace(user.ID)
	if clerkUserID == "" {
		return Event{}, ErrInvalidWebhook
	}

	occurredAt := time.UnixMilli(raw.Timestamp).UTC()
	if occurredAt.UnixMilli() != raw.Timestamp {
		return Event{}, ErrInvalidWebhook
	}

	var primaryEmail *string
	if eventType != EventUserDeleted {
		primaryEmail = verifiedPrimaryEmail(user)
	}

	return Event{
		EventID:      eventID,
		Type:         eventType,
		ClerkUserID:  clerkUserID,
		OccurredAt:   occurredAt,
		PrimaryEmail: primaryEmail,
		Supported:    true,
	}, nil
}

func verifiedPrimaryEmail(user userData) *string {
	primaryID := strings.TrimSpace(user.PrimaryEmailAddressID)
	if primaryID == "" {
		return nil
	}

	for _, item := range user.EmailAddresses {
		if strings.TrimSpace(item.ID) != primaryID {
			continue
		}
		if item.Verification.Status != "verified" {
			return nil
		}

		email := strings.TrimSpace(item.EmailAddress)
		if email == "" {
			return nil
		}

		return &email
	}

	return nil
}

func isSupportedEventType(eventType string) bool {
	switch eventType {
	case EventUserCreated, EventUserUpdated, EventUserDeleted:
		return true
	default:
		return false
	}
}

func decodeSingleJSON(payload []byte, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	if err := decoder.Decode(destination); err != nil {
		return err
	}

	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple JSON values")
		}
		return err
	}

	return nil
}

func maxUnixMilli() int64 {
	return time.Date(9999, time.December, 31, 23, 59, 59, int(time.Second-time.Millisecond), time.UTC).UnixMilli()
}
