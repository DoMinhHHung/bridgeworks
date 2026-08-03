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

func TestVerifierValidRawPayload(t *testing.T) {
	t.Parallel()

	verifier, payload, headers := signedTestEvent(t)
	event, err := verifier.VerifyAndParse(payload, headers)
	if err != nil {
		t.Fatalf("verify and parse: %v", err)
	}
	if event.EventID != headers.Get(headerSvixID) || !event.Supported {
		t.Fatalf("unexpected event: %+v", event)
	}
}

func TestVerifierRejectsMissingHeaders(t *testing.T) {
	t.Parallel()

	for _, missing := range []string{headerSvixID, headerSvixTimestamp, headerSvixSignature} {
		missing := missing
		t.Run(missing, func(t *testing.T) {
			t.Parallel()
			verifier, payload, headers := signedTestEvent(t)
			headers.Del(missing)
			if _, err := verifier.VerifyAndParse(payload, headers); err != ErrInvalidWebhook {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestVerifierRejectsInvalidSignatureAndModifiedBody(t *testing.T) {
	t.Parallel()

	verifier, payload, headers := signedTestEvent(t)
	invalidHeaders := headers.Clone()
	invalidHeaders.Set(headerSvixSignature, "v1,invalid")
	if _, err := verifier.VerifyAndParse(payload, invalidHeaders); err != ErrInvalidWebhook {
		t.Fatalf("invalid signature error = %v", err)
	}

	modified := append([]byte(nil), payload...)
	modified[len(modified)-2] = ' '
	if _, err := verifier.VerifyAndParse(modified, headers); err != ErrInvalidWebhook {
		t.Fatalf("modified body error = %v", err)
	}
}

func TestVerifierRejectsMalformedVerifiedJSON(t *testing.T) {
	t.Parallel()

	verifier, err := NewVerifier(testSigningSecret)
	if err != nil {
		t.Fatalf("new verifier: %v", err)
	}
	payload := []byte(`{"type":`)
	headers := signHeaders(t, payload)
	if _, err := verifier.VerifyAndParse(payload, headers); err != ErrInvalidWebhook {
		t.Fatalf("error = %v", err)
	}
}

func TestVerifierErrorsAndLogsDoNotLeakSecrets(t *testing.T) {
	t.Parallel()

	const secret = "whsec_not-valid-secret-sentinel"
	_, err := NewVerifier(secret)
	if err == nil {
		t.Fatal("expected invalid secret error")
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("error leaked secret: %q", err)
	}

	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	logger.Error("verifier setup failed", "error", err)
	if strings.Contains(logs.String(), secret) {
		t.Fatalf("logs leaked secret: %s", logs.String())
	}
}

func signedTestEvent(t *testing.T) (*Verifier, []byte, http.Header) {
	t.Helper()
	verifier, err := NewVerifier(testSigningSecret)
	if err != nil {
		t.Fatalf("new verifier: %v", err)
	}
	payload := []byte(`{"type":"user.created","timestamp":1785744000000,"data":{"id":"user_test","primary_email_address_id":null,"email_addresses":[]}}`)
	return verifier, payload, signHeaders(t, payload)
}

func signHeaders(t *testing.T, payload []byte) http.Header {
	t.Helper()
	webhook, err := svix.NewWebhook(testSigningSecret)
	if err != nil {
		t.Fatalf("new Svix webhook: %v", err)
	}
	timestamp := time.Now().UTC()
	signature, err := webhook.Sign("msg_test", timestamp, payload)
	if err != nil {
		t.Fatalf("sign webhook: %v", err)
	}
	headers := make(http.Header)
	headers.Set(headerSvixID, "msg_test")
	headers.Set(headerSvixTimestamp, strconv.FormatInt(timestamp.Unix(), 10))
	headers.Set(headerSvixSignature, signature)
	return headers
}
