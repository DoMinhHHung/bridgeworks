package httpapi

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/DoMinhHHung/bridgeworks/service/identity-service/internal/clerkwebhook"
	svix "github.com/svix/svix-webhooks/go"
)

const handlerTestSecret = "whsec_Y2xlcmstd2ViaG9vay10ZXN0LXNlY3JldA=="

func TestClerkWebhookHandlerAcceptsValidRawPayload(t *testing.T) {
	t.Parallel()

	processor := &recordingProcessor{}
	response, logs := executeSignedWebhook(t, supportedPayload(), processor, 1<<20, nil)
	if response.Code != http.StatusNoContent || response.Body.Len() != 0 {
		t.Fatalf("response = %d %q", response.Code, response.Body.String())
	}
	if processor.calls != 1 || processor.event.EventID != "msg_handler_test" {
		t.Fatalf("processor state: %+v", processor)
	}
	assertNoSensitiveWebhookData(t, logs, supportedPayload(), "verified@example.test")
}

func TestClerkWebhookHandlerRejectsMissingEachSvixHeader(t *testing.T) {
	t.Parallel()

	for _, header := range []string{"svix-id", "svix-timestamp", "svix-signature"} {
		header := header
		t.Run(header, func(t *testing.T) {
			t.Parallel()
			processor := &recordingProcessor{}
			response, _ := executeSignedWebhook(t, supportedPayload(), processor, 1<<20, func(request *http.Request) {
				request.Header.Del(header)
			})
			assertErrorEnvelope(t, response, http.StatusBadRequest, "invalid_webhook", "invalid webhook request")
			if processor.calls != 0 {
				t.Fatal("processor called for unsigned request")
			}
		})
	}
}

func TestClerkWebhookHandlerRejectsInvalidSignatureAndModifiedBody(t *testing.T) {
	t.Parallel()

	t.Run("invalid signature", func(t *testing.T) {
		t.Parallel()
		response, _ := executeSignedWebhook(t, supportedPayload(), &recordingProcessor{}, 1<<20, func(request *http.Request) {
			request.Header.Set("svix-signature", "v1,invalid-signature-sentinel")
		})
		assertErrorEnvelope(t, response, http.StatusBadRequest, "invalid_webhook", "invalid webhook request")
	})

	t.Run("body modified after signing", func(t *testing.T) {
		t.Parallel()
		payload := supportedPayload()
		response, _ := executeSignedWebhook(t, payload, &recordingProcessor{}, 1<<20, func(request *http.Request) {
			modified := append([]byte(nil), payload...)
			modified = append(modified, ' ')
			request.Body = ioNopCloser(bytes.NewReader(modified))
		})
		assertErrorEnvelope(t, response, http.StatusBadRequest, "invalid_webhook", "invalid webhook request")
	})
}

func TestClerkWebhookHandlerRejectsMalformedVerifiedJSONAndOversizedBody(t *testing.T) {
	t.Parallel()

	response, _ := executeSignedWebhook(t, []byte(`{"type":`), &recordingProcessor{}, 1<<20, nil)
	assertErrorEnvelope(t, response, http.StatusBadRequest, "invalid_webhook", "invalid webhook request")

	oversized := []byte(strings.Repeat("x", 33))
	response, _ = executeSignedWebhook(t, oversized, &recordingProcessor{}, 32, nil)
	assertErrorEnvelope(t, response, http.StatusRequestEntityTooLarge, "request_too_large", "request body too large")
}

func TestClerkWebhookHandlerAcknowledgesUnsupportedVerifiedEvent(t *testing.T) {
	t.Parallel()

	processor := &recordingProcessor{}
	response, _ := executeSignedWebhook(t, []byte(`{"type":"session.created"}`), processor, 1<<20, nil)
	if response.Code != http.StatusNoContent || processor.calls != 0 {
		t.Fatalf("response=%d processor calls=%d", response.Code, processor.calls)
	}
}

func TestClerkWebhookHandlerReturns503WithoutLeakingProcessorError(t *testing.T) {
	t.Parallel()

	const secretError = "postgres://runtime:password@identity-postgres:5432/db pgx connection refused"
	processor := &recordingProcessor{err: errors.New(secretError)}
	response, logs := executeSignedWebhook(t, supportedPayload(), processor, 1<<20, nil)
	assertErrorEnvelope(t, response, http.StatusServiceUnavailable, "service_unavailable", "service temporarily unavailable")
	if strings.Contains(response.Body.String(), secretError) || strings.Contains(logs, secretError) {
		t.Fatalf("database details leaked: response=%s logs=%s", response.Body.String(), logs)
	}
}

func TestClerkWebhookHandlerAppliesProcessingTimeout(t *testing.T) {
	t.Parallel()

	processor := &recordingProcessor{waitForCancellation: true}
	response, _ := executeSignedWebhookWithTimeout(t, supportedPayload(), processor, 1<<20, time.Millisecond, nil)
	assertErrorEnvelope(t, response, http.StatusServiceUnavailable, "service_unavailable", "service temporarily unavailable")
	if !errors.Is(processor.errSeen, context.DeadlineExceeded) {
		t.Fatalf("processor context error = %v", processor.errSeen)
	}
}

func executeSignedWebhook(
	t *testing.T,
	payload []byte,
	processor *recordingProcessor,
	maxBytes int64,
	mutate func(*http.Request),
) (*httptest.ResponseRecorder, string) {
	t.Helper()
	return executeSignedWebhookWithTimeout(t, payload, processor, maxBytes, time.Second, mutate)
}

func executeSignedWebhookWithTimeout(
	t *testing.T,
	payload []byte,
	processor *recordingProcessor,
	maxBytes int64,
	timeout time.Duration,
	mutate func(*http.Request),
) (*httptest.ResponseRecorder, string) {
	t.Helper()

	verifier, err := clerkwebhook.NewVerifier(handlerTestSecret)
	if err != nil {
		t.Fatalf("new verifier: %v", err)
	}
	webhook, err := svix.NewWebhook(handlerTestSecret)
	if err != nil {
		t.Fatalf("new signer: %v", err)
	}
	timestamp := time.Now().UTC()
	signature, err := webhook.Sign("msg_handler_test", timestamp, payload)
	if err != nil {
		t.Fatalf("sign payload: %v", err)
	}

	request := httptest.NewRequest(http.MethodPost, "/webhooks/clerk", bytes.NewReader(payload))
	request.Header.Set("svix-id", "msg_handler_test")
	request.Header.Set("svix-timestamp", strconv.FormatInt(timestamp.Unix(), 10))
	request.Header.Set("svix-signature", signature)
	request.Header.Set("X-Request-Id", "webhook-request-id")
	if mutate != nil {
		mutate(request)
	}

	var logBuffer bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logBuffer, nil))
	handler := RequestID(clerkWebhookHandler(logger, verifier, processor, maxBytes, timeout))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response, logBuffer.String()
}

func assertErrorEnvelope(t *testing.T, response *httptest.ResponseRecorder, status int, code, message string) {
	t.Helper()
	if response.Code != status {
		t.Fatalf("status = %d, body=%s", response.Code, response.Body.String())
	}
	for _, expected := range []string{
		fmt.Sprintf(`"code":"%s"`, code),
		fmt.Sprintf(`"message":"%s"`, message),
		`"request_id":"webhook-request-id"`,
		`"details":null`,
	} {
		if !strings.Contains(response.Body.String(), expected) {
			t.Fatalf("response missing %q: %s", expected, response.Body.String())
		}
	}
}

func assertNoSensitiveWebhookData(t *testing.T, logs string, payload []byte, secrets ...string) {
	t.Helper()
	for _, value := range append(secrets, handlerTestSecret, string(payload), "svix-signature") {
		if strings.Contains(logs, value) {
			t.Fatalf("logs leaked %q: %s", value, logs)
		}
	}
}

func supportedPayload() []byte {
	return []byte(`{"type":"user.created","timestamp":1785744000000,"data":{"id":"user_handler_test","primary_email_address_id":"email_primary","email_addresses":[{"id":"email_primary","email_address":"verified@example.test","verification":{"status":"verified"}}]}}`)
}

type recordingProcessor struct {
	calls               int
	event               clerkwebhook.Event
	err                 error
	waitForCancellation bool
	errSeen             error
}

func (p *recordingProcessor) Process(ctx context.Context, event clerkwebhook.Event) error {
	p.calls++
	p.event = event
	if p.waitForCancellation {
		<-ctx.Done()
		p.errSeen = ctx.Err()
		return ctx.Err()
	}
	return p.err
}

type readCloser struct{ *bytes.Reader }

func (readCloser) Close() error { return nil }

func ioNopCloser(reader *bytes.Reader) readCloser { return readCloser{Reader: reader} }
