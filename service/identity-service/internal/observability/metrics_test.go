package observability

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

type fakePoolStat struct{}

func (fakePoolStat) AcquiredConns() int32           { return 2 }
func (fakePoolStat) IdleConns() int32               { return 3 }
func (fakePoolStat) TotalConns() int32              { return 5 }
func (fakePoolStat) MaxConns() int32                { return 7 }
func (fakePoolStat) AcquireCount() int64            { return 11 }
func (fakePoolStat) AcquireDuration() time.Duration { return 1500 * time.Millisecond }
func (fakePoolStat) EmptyAcquireCount() int64       { return 4 }
func (fakePoolStat) CanceledAcquireCount() int64    { return 1 }

func TestNormalizeHTTPMethod(t *testing.T) {
	standard := []string{
		http.MethodGet,
		http.MethodPost,
		http.MethodPut,
		http.MethodPatch,
		http.MethodDelete,
		http.MethodHead,
		http.MethodOptions,
		http.MethodConnect,
		http.MethodTrace,
	}
	for _, method := range standard {
		if got := normalizeHTTPMethod(method); got != method {
			t.Fatalf("normalizeHTTPMethod(%q) = %q", method, got)
		}
	}
	for _, method := range []string{"", "get", "X-CUSTOM-123", "BREW", "POST "} {
		if got := normalizeHTTPMethod(method); got != methodOther {
			t.Fatalf("normalizeHTTPMethod(%q) = %q, want %q", method, got, methodOther)
		}
	}
}

func TestMetricsExposeBoundedHTTPWebhookAndPoolValues(t *testing.T) {
	metrics, err := New("identity-service", func() PoolStat { return fakePoolStat{} })
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	metrics.HTTPRequestStarted()
	metrics.ObserveHTTPRequest("/me", http.MethodGet, 204, 25*time.Millisecond)
	metrics.HTTPRequestCompleted()
	for _, outcome := range []string{OutcomeProcessed, OutcomeDuplicate, OutcomeStale, OutcomeRejected, OutcomeRetryableFailure} {
		metrics.ObserveWebhook(AggregateUser, outcome)
	}
	metrics.ObserveWebhook("organization", OutcomeProcessed)
	metrics.ObserveWebhook("user_provider_123", OutcomeProcessed)

	body := scrapeMetrics(t, metrics)
	for _, expected := range []string{
		`http_requests_total{method="GET",route="/me",service="identity-service",status_class="2xx"} 1`,
		`http_request_duration_seconds_count{method="GET",route="/me",service="identity-service"} 1`,
		`http_requests_in_flight{service="identity-service"} 0`,
		`clerk_webhook_events_total{aggregate="user",outcome="processed",service="identity-service"} 1`,
		`database_pool_acquired_connections{pool="runtime",service="identity-service"} 2`,
		`database_pool_idle_connections{pool="runtime",service="identity-service"} 3`,
		`database_pool_total_connections{pool="runtime",service="identity-service"} 5`,
		`database_pool_max_connections{pool="runtime",service="identity-service"} 7`,
		`database_pool_acquire_count_total{pool="runtime",service="identity-service"} 11`,
		`database_pool_acquire_duration_seconds_total{pool="runtime",service="identity-service"} 1.5`,
		`database_pool_empty_acquire_count_total{pool="runtime",service="identity-service"} 4`,
		`database_pool_canceled_acquire_count_total{pool="runtime",service="identity-service"} 1`,
	} {
		if !strings.Contains(body, expected) {
			t.Fatalf("metrics missing %q\n%s", expected, body)
		}
	}
	for _, forbidden := range []string{"organization", "user_provider_123"} {
		if strings.Contains(body, `aggregate="`+forbidden+`"`) {
			t.Fatalf("rejected aggregate label leaked %q: %s", forbidden, body)
		}
	}
}

func TestArbitraryHTTPMethodsCollapseToOther(t *testing.T) {
	metrics, err := New("identity-service", nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	arbitrary := []string{"X-CUSTOM-123", "PURGE", "BREW-456", "ATTACKER-METHOD"}
	for _, method := range arbitrary {
		metrics.ObserveHTTPRequest("unknown", method, 404, time.Millisecond)
	}

	body := scrapeMetrics(t, metrics)
	if !strings.Contains(body, `http_requests_total{method="OTHER",route="unknown",service="identity-service",status_class="4xx"} 4`) {
		t.Fatalf("OTHER series missing: %s", body)
	}
	for _, method := range arbitrary {
		if strings.Contains(body, method) {
			t.Fatalf("raw method %q leaked: %s", method, body)
		}
	}
}

func TestMetricsRegistryConstructionAndUnavailablePoolAreSafe(t *testing.T) {
	for range 2 {
		metrics, err := New("identity-service", nil)
		if err != nil {
			t.Fatalf("New() error = %v", err)
		}
		if body := scrapeMetrics(t, metrics); strings.Contains(body, "database_pool_") {
			t.Fatalf("nil stat source emitted pool values: %s", body)
		}
	}

	metrics, err := New("identity-service", func() PoolStat { return nil })
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if body := scrapeMetrics(t, metrics); strings.Contains(body, "database_pool_") {
		t.Fatalf("nil pool stat emitted values: %s", body)
	}
}

func TestPoolCollectorDoesNotSuppressProgrammingPanics(t *testing.T) {
	collector := newPoolCollector("identity-service", "runtime", func() PoolStat {
		panic("programming error")
	})
	defer func() {
		if recovered := recover(); recovered == nil {
			t.Fatal("collector suppressed panic")
		}
	}()
	collector.Collect(make(chan prometheus.Metric))
}

func scrapeMetrics(t *testing.T, metrics *Metrics) string {
	t.Helper()
	recorder := httptest.NewRecorder()
	metrics.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("metrics status = %d", recorder.Code)
	}
	return recorder.Body.String()
}
