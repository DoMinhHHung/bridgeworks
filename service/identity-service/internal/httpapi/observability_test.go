package httpapi

import (
	"bufio"
	"bytes"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
)

type observedRequest struct {
	route    string
	method   string
	status   int
	duration time.Duration
}

type fakeMetrics struct {
	inFlight int
	observed []observedRequest
	webhooks [][2]string
}

func (m *fakeMetrics) HTTPRequestStarted()   { m.inFlight++ }
func (m *fakeMetrics) HTTPRequestCompleted() { m.inFlight-- }
func (m *fakeMetrics) ObserveHTTPRequest(route, method string, status int, duration time.Duration) {
	m.observed = append(m.observed, observedRequest{route: route, method: method, status: status, duration: duration})
}
func (m *fakeMetrics) ObserveWebhook(aggregate, outcome string) {
	m.webhooks = append(m.webhooks, [2]string{aggregate, outcome})
}

func TestHTTPObservabilityCapturesBoundedCompletionRecords(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil)).With("service", "identity-service")
	metrics := &fakeMetrics{}

	router := chi.NewRouter()
	router.Use(RequestID)
	router.Use(HTTPObservability(logger, metrics))
	router.Use(Recoverer(logger))
	router.Get("/items/{id}", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	router.Get("/default/{id}", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("hello"))
	})
	router.Get("/panic/{id}", func(http.ResponseWriter, *http.Request) { panic("boom") })

	tests := []struct {
		path   string
		status int
		bytes  int64
		route  string
	}{
		{path: "/items/provider-secret?token=jwt-secret", status: 204, route: "/items/{id}"},
		{path: "/default/provider-secret?token=jwt-secret", status: 200, bytes: 5, route: "/default/{id}"},
		{path: "/panic/provider-secret?token=jwt-secret", status: 500, route: "/panic/{id}"},
		{path: "/missing/provider-secret?token=jwt-secret", status: 404, route: "unknown"},
	}
	for index, tt := range tests {
		request := httptest.NewRequest(http.MethodGet, tt.path, strings.NewReader("webhook-body-secret"))
		request.Header.Set("Authorization", "Bearer jwt-secret")
		request.Header.Set("X-Request-Id", "request-"+string(rune('a'+index)))
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, request)
		if recorder.Code != tt.status {
			t.Fatalf("%s status = %d, want %d", tt.path, recorder.Code, tt.status)
		}
		if int64(recorder.Body.Len()) != tt.bytes && tt.status != 500 && tt.status != 404 {
			t.Fatalf("%s bytes = %d, want %d", tt.path, recorder.Body.Len(), tt.bytes)
		}
		got := metrics.observed[len(metrics.observed)-1]
		if got.route != tt.route || got.status != tt.status || got.method != http.MethodGet {
			t.Fatalf("observed = %+v", got)
		}
	}
	if metrics.inFlight != 0 {
		t.Fatalf("in-flight = %d", metrics.inFlight)
	}

	output := logs.String()
	for _, expected := range []string{"identity-service", "request-a", "http request completed", "/items/{id}", "response_bytes"} {
		if !strings.Contains(output, expected) {
			t.Fatalf("access logs missing %q: %s", expected, output)
		}
	}
	for _, forbidden := range []string{"provider-secret", "jwt-secret", "webhook-body-secret", "token="} {
		if strings.Contains(output, forbidden) {
			t.Fatalf("access logs leaked %q: %s", forbidden, output)
		}
	}
}

type optionalWriter struct{ *httptest.ResponseRecorder }

func (w optionalWriter) Flush() {}
func (w optionalWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return nil, nil, http.ErrNotSupported
}
func (w optionalWriter) Push(string, *http.PushOptions) error { return http.ErrNotSupported }
func (w optionalWriter) ReadFrom(reader io.Reader) (int64, error) {
	return io.Copy(w.ResponseRecorder, reader)
}

func TestHTTPObservabilityPreservesOptionalResponseWriterInterfaces(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil)).With("service", "identity-service")
	handler := HTTPObservability(logger, &fakeMetrics{})(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if _, ok := w.(http.Flusher); !ok {
			t.Fatal("http.Flusher lost")
		}
		if _, ok := w.(http.Hijacker); !ok {
			t.Fatal("http.Hijacker lost")
		}
		if _, ok := w.(http.Pusher); !ok {
			t.Fatal("http.Pusher lost")
		}
		if _, ok := w.(io.ReaderFrom); !ok {
			t.Fatal("io.ReaderFrom lost")
		}
	}))
	handler.ServeHTTP(optionalWriter{httptest.NewRecorder()}, httptest.NewRequest(http.MethodGet, "/", nil))
}
