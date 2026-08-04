package clerkwebhook

import (
	"errors"
	"net/http"
	"strings"

	"github.com/DoMinhHHung/bridgeworks/service/organization-service/internal/organizationsync"
	"github.com/DoMinhHHung/bridgeworks/service/organization-service/internal/platform/safeerr"
	svix "github.com/svix/svix-webhooks/go"
)

var ErrInvalidWebhook = errors.New("invalid webhook")

type Verifier struct{ webhook *svix.Webhook }

func NewVerifier(secret string) (*Verifier, error) {
	webhook, err := svix.NewWebhook(strings.TrimSpace(secret))
	if err != nil {
		return nil, safeerr.Wrap("initialize Clerk organization webhook verifier", err)
	}
	return &Verifier{webhook: webhook}, nil
}
func (v *Verifier) VerifyAndParse(payload []byte, headers http.Header) (organizationsync.Event, bool, error) {
	if v == nil || v.webhook == nil {
		return organizationsync.Event{}, false, ErrInvalidWebhook
	}
	eventID := strings.TrimSpace(headers.Get("svix-id"))
	if eventID == "" || strings.TrimSpace(headers.Get("svix-timestamp")) == "" || strings.TrimSpace(headers.Get("svix-signature")) == "" {
		return organizationsync.Event{}, false, ErrInvalidWebhook
	}
	if err := v.webhook.Verify(payload, headers); err != nil {
		return organizationsync.Event{}, false, ErrInvalidWebhook
	}
	event, supported, err := organizationsync.Decode(eventID, payload)
	if err != nil {
		return organizationsync.Event{}, supported, ErrInvalidWebhook
	}
	return event, supported, nil
}
