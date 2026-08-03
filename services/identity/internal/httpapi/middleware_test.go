package httpapi

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"
)

func TestRequestIDReusesIncomingHeader(t *testing.T) {
	t.Parallel()

	const incomingRequestID = "apisix-request-id"

	handler := RequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := RequestIDFromContext(r.Context()); got != incomingRequestID {
			t.Fatalf("context request ID = %q", got)
		}
		w.WriteHeader(http.StatusNoContent)
	}))

	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set(requestIDHeader, incomingRequestID)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if got := response.Header().Get(requestIDHeader); got != incomingRequestID {
		t.Fatalf("response request ID = %q", got)
	}
}

func TestRequestIDGeneratesUUIDWhenMissing(t *testing.T) {
	t.Parallel()

	var contextRequestID string
	handler := RequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		contextRequestID = RequestIDFromContext(r.Context())
		w.WriteHeader(http.StatusNoContent)
	}))

	request := httptest.NewRequest(http.MethodGet, "/", nil)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	responseRequestID := response.Header().Get(requestIDHeader)
	if responseRequestID == "" || responseRequestID != contextRequestID {
		t.Fatalf("response request ID = %q, context request ID = %q", responseRequestID, contextRequestID)
	}

	uuidPattern := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	if !uuidPattern.MatchString(responseRequestID) {
		t.Fatalf("generated request ID is not UUIDv4: %q", responseRequestID)
	}
}

func TestRequestIDReplacesInvalidHeader(t *testing.T) {
	t.Parallel()

	handler := RequestID(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set(requestIDHeader, "invalid request id")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if got := response.Header().Get(requestIDHeader); got == "invalid request id" || got == "" {
		t.Fatalf("invalid request ID was not replaced: %q", got)
	}
}
