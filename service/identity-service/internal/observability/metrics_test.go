package observability

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type fakePoolStat struct{}

func (fakePoolStat) AcquiredConns() int32          { return 2 }
func (fakePoolStat) IdleConns() int32              { return 3 }
func (fakePoolStat) TotalConns() int32             { return 5 }
func (fakePoolStat) MaxConns() int32               { return 7 }
func (fakePoolStat) AcquireCount() int64            { return 11 }
func (fakePoolStat) AcquireDuration() time.Duration { return 1500 * time.Millisecond }
func (fakePoolStat) EmptyAcquireCount() int64       { return 4 }
func (fakePoolStat) CanceledAcquireCount() int64    { return 1 }

func TestMetricsExposeBoundedHTTPWebhookAndPoolValues(t *testing.T) {
	metrics, err := New("identity-service", func() PoolStat { return fakePoolStat{} })
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	metrics.HTTPRequestStarted()
	metrics.ObserveHTTPRequest("/me", "GET", 204, 25*time.Millisecond)
	metrics.HTTPRequestCompleted()
	for _, outcome := range []string{OutcomeProcessed, OutcomeDuplicate, OutcomeStale, OutcomeRejected, OutcomeRetryableFailure} {
		metrics.ObserveWebhook(AggregateUser, outcome)
	}
	metrics.ObserveWebhook("user_provider_123", OutcomeProcessed)

	recorder := httptest.NewRecorder()
	metrics.Handler().ServeHTTP(recorder, httptest.NewRequest("GET", "/metrics", nil))
	body := recorder.Body.String()
	for _, expected := range []string{
		`http_requests_total{method="GET",route="/me",service="identity-service",status_class="2xx"} 1`,
		`http_request_duration_seconds_count{method="GET",route="/me",service="identity-service"} 1`,
		`http_requests_in_flight{service="identity-service"} 0`,
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
	if strings.Contains(body, "user_provider_123") {
		t.Fatalf("unbounded aggregate label leaked: %s", body)
	}
}

func TestMetricsRegistryConstructionAndNilPoolAreSafe(t *testing.T) {
	for range 2 {
		metrics, err := New("identity-service", nil)
		if err != nil {
			t.Fatalf("New() error = %v", err)
		}
		recorder := httptest.NewRecorder()
		metrics.Handler().ServeHTTP(recorder, httptest.NewRequest("GET", "/metrics", nil))
		if recorder.Code != 200 {
			t.Fatalf("status = %d", recorder.Code)
		}
	}
}
