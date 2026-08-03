package clerkwebhook

import (
	"net/http"
	"strings"

	"github.com/DoMinhHHung/bridgeworks/service/identity-service/internal/platform/safeerr"
	svix "github.com/svix/svix-webhooks/go"
)

const (
	headerSvixID        = "svix-id"
	headerSvixTimestamp = "svix-timestamp"
	headerSvixSignature = "svix-signature"
)

type Verifier struct {
	webhook *svix.Webhook
}

func NewVerifier(secret string) (*Verifier, error) {
	webhook, err := svix.NewWebhook(secret)
	if err != nil {
		return nil, safeerr.Wrap("initialize Clerk webhook verifier", err)
	}

	return &Verifier{webhook: webhook}, nil
}

func (v *Verifier) VerifyAndParse(payload []byte, headers http.Header) (Event, error) {
	if v == nil || v.webhook == nil {
		return Event{}, ErrInvalidWebhook
	}

	eventID := strings.TrimSpace(headers.Get(headerSvixID))
	if eventID == "" ||
		strings.TrimSpace(headers.Get(headerSvixTimestamp)) == "" ||
		strings.TrimSpace(headers.Get(headerSvixSignature)) == "" {
		return Event{}, ErrInvalidWebhook
	}

	if err := v.webhook.Verify(payload, headers); err != nil {
		return Event{}, ErrInvalidWebhook
	}

	return parseEvent(eventID, payload)
}
