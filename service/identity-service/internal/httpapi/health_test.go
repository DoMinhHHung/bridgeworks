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
	t.Parallel()

	router := NewRouter("identity-service", slog.New(slog.NewTextHandler(io.Discard, nil)))

	for _, path := range []string{"/health/live", "/health/ready"} {
		t.Run(path, func(t *testing.T) {
			t.Parallel()

			request := httptest.NewRequest(http.MethodGet, path, nil)
			response := httptest.NewRecorder()

			router.ServeHTTP(response, request)

			if response.Code != http.StatusOK {
				t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
			}
			if response.Header().Get("Content-Type") != "application/json" {
				t.Fatalf("Content-Type = %q", response.Header().Get("Content-Type"))
			}
			if response.Header().Get(requestIDHeader) == "" {
				t.Fatal("missing X-Request-Id response header")
			}

			var body healthResponse
			if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if body.Status != "ok" || body.Service != "identity-service" {
				t.Fatalf("unexpected body: %+v", body)
			}
		})
	}
}

func TestUnknownRouteUsesErrorEnvelope(t *testing.T) {
	t.Parallel()

	router := NewRouter("identity-service", slog.New(slog.NewTextHandler(io.Discard, nil)))
	request := httptest.NewRequest(http.MethodGet, "/missing", nil)
	request.Header.Set(requestIDHeader, "gateway-request-id")
	response := httptest.NewRecorder()

	router.ServeHTTP(response, request)

	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d", response.Code)
	}

	var body errorEnvelope
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Code != "not_found" || body.RequestID != "gateway-request-id" || body.Details != nil {
		t.Fatalf("unexpected error envelope: %+v", body)
	}
}
