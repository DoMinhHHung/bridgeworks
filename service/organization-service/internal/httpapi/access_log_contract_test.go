package httpapi

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

func TestAccessLogContractIncludesRequiredFields(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil)).With("service", "organization-service")
	router := chi.NewRouter()
	router.Use(RequestID)
	router.Use(HTTPObservability(logger, nil))
	router.Use(Recoverer(logger))
	router.Get("/contract/{id}", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte("ok"))
	})

	request := httptest.NewRequest(http.MethodGet, "/contract/provider-secret?token=jwt-secret", strings.NewReader("webhook-body-secret"))
	request.Header.Set("Authorization", "Bearer jwt-secret")
	request.Header.Set("X-Request-Id", "access-contract-request")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	record := completionRecord(t, logs.String())
	for _, field := range []string{"service", "request_id", "method", "route", "status", "duration_ms", "response_bytes"} {
		if _, ok := record[field]; !ok {
			t.Fatalf("access record missing %q: %#v", field, record)
		}
	}
	if record["service"] != "organization-service" || record["request_id"] != "access-contract-request" || record["method"] != http.MethodGet || record["route"] != "/contract/{id}" {
		t.Fatalf("access record identity fields = %#v", record)
	}
	if record["status"] != float64(http.StatusAccepted) || record["response_bytes"] != float64(2) {
		t.Fatalf("access record response fields = %#v", record)
	}
	for _, forbidden := range []string{"provider-secret", "jwt-secret", "webhook-body-secret", "token="} {
		if strings.Contains(logs.String(), forbidden) {
			t.Fatalf("access logs leaked %q: %s", forbidden, logs.String())
		}
	}
}

func TestHealthAccessCompletionUsesDebugLevel(t *testing.T) {
	var debugLogs bytes.Buffer
	debugLogger := slog.New(slog.NewJSONHandler(&debugLogs, &slog.HandlerOptions{Level: slog.LevelDebug})).With("service", "organization-service")
	healthHandler := healthAccessHandler(debugLogger)
	healthHandler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/health/live", nil))
	record := completionRecord(t, debugLogs.String())
	if record["level"] != "DEBUG" {
		t.Fatalf("health access level = %#v", record["level"])
	}

	var infoLogs bytes.Buffer
	infoLogger := slog.New(slog.NewJSONHandler(&infoLogs, nil)).With("service", "organization-service")
	healthAccessHandler(infoLogger).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/health/live", nil))
	if strings.Contains(infoLogs.String(), "http request completed") {
		t.Fatalf("debug health access record emitted at info level: %s", infoLogs.String())
	}
}

func healthAccessHandler(logger *slog.Logger) http.Handler {
	router := chi.NewRouter()
	router.Use(RequestID)
	router.Use(HTTPObservability(logger, nil))
	router.Get("/health/live", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	return router
}

func completionRecord(t *testing.T, output string) map[string]any {
	t.Helper()
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		if line == "" {
			continue
		}
		var record map[string]any
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatalf("decode log record: %v; line=%s", err, line)
		}
		if record["msg"] == "http request completed" {
			return record
		}
	}
	t.Fatalf("completion record not found: %s", output)
	return nil
}
