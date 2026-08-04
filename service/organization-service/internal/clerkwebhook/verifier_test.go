package clerkwebhook

import (
	"bytes"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	svix "github.com/svix/svix-webhooks/go"
)

const testSigningSecret = "whsec_Y2xlcmstd2ViaG9vay10ZXN0LXNlY3JldA=="

func TestVerifierAcceptsSignedRawOrganizationPayload(t *testing.T) {
	verifier, payload, headers := signedEvent(t, `{"type":"organization.created","timestamp":1785744000000,"data":{"id":"org_sensitive","name":" Example ","slug":" example "}}`)
	event, supported, err := verifier.VerifyAndParse(payload, headers)
	if err != nil || !supported {
		t.Fatalf("supported=%v err=%v", supported, err)
	}
	if event.EventID != "msg_test" || event.ClerkOrganizationID != "org_sensitive" || event.Organization.Name == nil || *event.Organization.Name != "Example" {
		t.Fatalf("event=%+v", event)
	}
}

func TestVerifierRejectsMissingHeaders(t *testing.T) {
	for _, missing := range []string{"svix-id", "svix-timestamp", "svix-signature"} {
		t.Run(missing, func(t *testing.T) {
			verifier, payload, headers := signedEvent(t, `{"type":"organization.created","timestamp":1785744000000,"data":{"id":"org_sensitive"}}`)
			headers.Del(missing)
			if _, _, err := verifier.VerifyAndParse(payload, headers); err != ErrInvalidWebhook {
				t.Fatalf("err=%v", err)
			}
		})
	}
}

func TestVerifierRejectsInvalidSignatureModifiedBodyAndMalformedVerifiedJSON(t *testing.T) {
	verifier, payload, headers := signedEvent(t, `{"type":"organization.created","timestamp":1785744000000,"data":{"id":"org_sensitive"}}`)
	invalid := headers.Clone()
	invalid.Set("svix-signature", "v1,invalid")
	if _, _, err := verifier.VerifyAndParse(payload, invalid); err != ErrInvalidWebhook {
		t.Fatalf("invalid signature err=%v", err)
	}
	modified := append([]byte(nil), payload...)
	modified[len(modified)-2] = ' '
	if _, _, err := verifier.VerifyAndParse(modified, headers); err != ErrInvalidWebhook {
		t.Fatalf("modified body err=%v", err)
	}

	malformed := []byte(`{"type":`)
	malformedHeaders := signHeaders(t, malformed)
	if _, _, err := verifier.VerifyAndParse(malformed, malformedHeaders); err != ErrInvalidWebhook {
		t.Fatalf("malformed err=%v", err)
	}
}

func TestVerifierReturnsVerifiedUnsupportedWithoutEvent(t *testing.T) {
	verifier, payload, headers := signedEvent(t, `{"type":"session.created","timestamp":1785744000000,"data":{"id":"session_sensitive"}}`)
	event, supported, err := verifier.VerifyAndParse(payload, headers)
	if err != nil || supported || event.EventID != "" {
		t.Fatalf("event=%+v supported=%v err=%v", event, supported, err)
	}
}

func TestVerifierSetupErrorDoesNotLeakSecret(t *testing.T) {
	const secret = "whsec_not-valid-secret-sentinel"
	_, err := NewVerifier(secret)
	if err == nil {
		t.Fatal("expected setup error")
	}
	var logs bytes.Buffer
	slog.New(slog.NewJSONHandler(&logs, nil)).Error("setup failed", "error", err)
	if strings.Contains(err.Error()+logs.String(), secret) {
		t.Fatal("secret leaked")
	}
}

func signedEvent(t *testing.T, raw string) (*Verifier, []byte, http.Header) {
	t.Helper()
	verifier, err := NewVerifier(testSigningSecret)
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte(raw)
	return verifier, payload, signHeaders(t, payload)
}

func signHeaders(t *testing.T, payload []byte) http.Header {
	t.Helper()
	webhook, err := svix.NewWebhook(testSigningSecret)
	if err != nil {
		t.Fatal(err)
	}
	timestamp := time.Now().UTC()
	signature, err := webhook.Sign("msg_test", timestamp, payload)
	if err != nil {
		t.Fatal(err)
	}
	headers := make(http.Header)
	headers.Set("svix-id", "msg_test")
	headers.Set("svix-timestamp", strconv.FormatInt(timestamp.Unix(), 10))
	headers.Set("svix-signature", signature)
	return headers
}
