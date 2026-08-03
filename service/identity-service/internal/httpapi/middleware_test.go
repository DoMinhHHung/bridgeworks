package httpapi

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAccessLogBehavior(t *testing.T) {
	tests := []struct {
		name    string
		method  string
		path    string
		wantLog bool
	}{
		{
			name:    "ordinary request is logged",
			method:  http.MethodGet,
			path:    "/missing",
			wantLog: true,
		},
		{
			name:    "successful liveness probe is not logged",
			method:  http.MethodGet,
			path:    "/health/live",
			wantLog: false,
		},
		{
			name:    "successful readiness probe is not logged",
			method:  http.MethodGet,
			path:    "/health/ready",
			wantLog: false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var logs bytes.Buffer
			logger := slog.New(slog.NewJSONHandler(&logs, nil))
			handler := NewRouter("identity-service", logger)
			request := httptest.NewRequest(test.method, test.path, nil)
			response := httptest.NewRecorder()

			handler.ServeHTTP(response, request)

			gotLog := strings.Contains(logs.String(), `"msg":"http request"`)
			if gotLog != test.wantLog {
				t.Fatalf("access log present = %t, logs = %s", gotLog, logs.String())
			}
		})
	}
}

func TestRecoverReturnsSafeInternalError(t *testing.T) {
	const (
		requestID  = "panic-request"
		panicValue = "sensitive-panic-value"
	)

	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	handler := RequestID(Recover(logger)(AccessLog(logger)(http.HandlerFunc(
		func(http.ResponseWriter, *http.Request) {
			panic(panicValue)
		},
	))))
	request := httptest.NewRequest(http.MethodGet, "/health/live", nil)
	request.Header.Set(RequestIDHeader, requestID)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	assertErrorResponse(
		t,
		response,
		http.StatusInternalServerError,
		"internal_error",
		"internal server error",
		requestID,
	)

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(response.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	responseBody := response.Body.String()
	for _, forbidden := range []string{panicValue, "goroutine", "runtime/debug", "stack"} {
		if strings.Contains(responseBody, forbidden) {
			t.Fatalf("response leaks %q: %s", forbidden, responseBody)
		}
	}
	if !strings.Contains(logs.String(), `"msg":"request panic"`) {
		t.Fatalf("panic was not logged: %s", logs.String())
	}
}
