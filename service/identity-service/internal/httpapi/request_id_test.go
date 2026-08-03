package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRequestIDReusesIncomingHeader(t *testing.T) {
	const incomingRequestID = "apisix-request-id"

	handler := RequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := RequestIDFromContext(r.Context()); got != incomingRequestID {
			t.Fatalf("context request ID = %q", got)
		}
		if got := r.Header.Get(RequestIDHeader); got != incomingRequestID {
			t.Fatalf("request header = %q", got)
		}
		w.WriteHeader(http.StatusNoContent)
	}))

	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set(RequestIDHeader, incomingRequestID)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if got := response.Header().Get(RequestIDHeader); got != incomingRequestID {
		t.Fatalf("response header = %q", got)
	}
}

func TestRequestIDGeneratesMissingHeader(t *testing.T) {
	var generatedRequestID string
	handler := RequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		generatedRequestID = RequestIDFromContext(r.Context())
		if generatedRequestID == "" {
			t.Fatal("context request ID is empty")
		}
		if got := r.Header.Get(RequestIDHeader); got != generatedRequestID {
			t.Fatalf("request header = %q", got)
		}
		w.WriteHeader(http.StatusNoContent)
	}))

	request := httptest.NewRequest(http.MethodGet, "/", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if got := response.Header().Get(RequestIDHeader); got != generatedRequestID {
		t.Fatalf("response header = %q, generated = %q", got, generatedRequestID)
	}
}
