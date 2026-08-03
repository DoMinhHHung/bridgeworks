package httpapi

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHealthEndpoints(t *testing.T) {
	handler := NewRouter("identity-service", slog.New(slog.NewTextHandler(io.Discard, nil)))

	for _, path := range []string{"/health/live", "/health/ready"} {
		t.Run(path, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, path, nil)
			response := httptest.NewRecorder()

			handler.ServeHTTP(response, request)

			if response.Code != http.StatusOK {
				t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
			}
			if response.Header().Get("Content-Type") != "application/json; charset=utf-8" {
				t.Fatalf("Content-Type = %q", response.Header().Get("Content-Type"))
			}
			if response.Header().Get(RequestIDHeader) == "" {
				t.Fatal("missing X-Request-Id response header")
			}

			var body healthResponse
			if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if body.Status != "ok" || body.Service != "identity-service" {
				t.Fatalf("body = %+v", body)
			}
		})
	}
}

func TestRouterErrorResponses(t *testing.T) {
	handler := NewRouter("identity-service", slog.New(slog.NewTextHandler(io.Discard, nil)))

	tests := []struct {
		name      string
		method    string
		path      string
		status    int
		code      string
		message   string
		requestID string
	}{
		{
			name:      "not found",
			method:    http.MethodGet,
			path:      "/missing",
			status:    http.StatusNotFound,
			code:      "not_found",
			message:   "resource not found",
			requestID: "not-found-request",
		},
		{
			name:      "method not allowed",
			method:    http.MethodPost,
			path:      "/health/live",
			status:    http.StatusMethodNotAllowed,
			code:      "method_not_allowed",
			message:   "method not allowed",
			requestID: "method-not-allowed-request",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(test.method, test.path, nil)
			request.Header.Set(RequestIDHeader, test.requestID)
			response := httptest.NewRecorder()

			handler.ServeHTTP(response, request)

			assertErrorResponse(t, response, test.status, test.code, test.message, test.requestID)
		})
	}
}

func assertErrorResponse(
	t *testing.T,
	response *httptest.ResponseRecorder,
	status int,
	code string,
	message string,
	requestID string,
) {
	t.Helper()

	if response.Code != status {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if response.Header().Get("Content-Type") != "application/json; charset=utf-8" {
		t.Fatalf("Content-Type = %q", response.Header().Get("Content-Type"))
	}
	if response.Header().Get(RequestIDHeader) != requestID {
		t.Fatalf("X-Request-Id = %q", response.Header().Get(RequestIDHeader))
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(response.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode raw response: %v", err)
	}
	if len(raw) != 4 {
		t.Fatalf("top-level field count = %d, body = %s", len(raw), response.Body.String())
	}
	if _, exists := raw["error"]; exists {
		t.Fatalf("unexpected nested error property: %s", response.Body.String())
	}

	var body errorEnvelope
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Code != code || body.Message != message || body.RequestID != requestID {
		t.Fatalf("body = %+v", body)
	}
	if body.Details == nil || len(body.Details) != 0 {
		t.Fatalf("details = %#v", body.Details)
	}
}
