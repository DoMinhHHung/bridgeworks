package observability

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type fakePoolStat struct{}
func (fakePoolStat) AcquiredConns() int32 { return 2 }
func (fakePoolStat) IdleConns() int32 { return 3 }
func (fakePoolStat) TotalConns() int32 { return 5 }
func (fakePoolStat) MaxConns() int32 { return 7 }
func (fakePoolStat) AcquireCount() int64 { return 11 }
func (fakePoolStat) AcquireDuration() time.Duration { return 1500 * time.Millisecond }
func (fakePoolStat) EmptyAcquireCount() int64 { return 4 }
func (fakePoolStat) CanceledAcquireCount() int64 { return 1 }

func TestMetricsExposeBoundedHTTPWebhookAndPoolValues(t *testing.T) {
	metrics, err := New("organization-service", func() PoolStat { return fakePoolStat{} })
	if err != nil { t.Fatalf("New() error = %v", err) }
	metrics.HTTPRequestStarted()
	metrics.ObserveHTTPRequest("/organizations/current", "GET", 503, 25*time.Millisecond)
	metrics.HTTPRequestCompleted()
	for _, aggregate := range []string{AggregateOrganization, AggregateMembership} {
		for _, outcome := range []string{OutcomeProcessed, OutcomeDuplicate, OutcomeStale, OutcomeRejected, OutcomeRetryableFailure} {
			metrics.ObserveWebhook(aggregate, outcome)
		}
	}
	metrics.ObserveWebhook("org_provider_123", OutcomeProcessed)

	recorder := httptest.NewRecorder()
	metrics.Handler().ServeHTTP(recorder, httptest.NewRequest("GET", "/metrics", nil))
	body := recorder.Body.String()
	for _, expected := range []string{
		`http_requests_total{method="GET",route="/organizations/current",service="organization-service",status_class="5xx"} 1`,
		`http_request_duration_seconds_count{method="GET",route="/organizations/current",service="organization-service"} 1`,
		`http_requests_in_flight{service="organization-service"} 0`,
		`clerk_webhook_events_total{aggregate="organization",outcome="processed",service="organization-service"} 1`,
		`clerk_webhook_events_total{aggregate="membership",outcome="retryable_failure",service="organization-service"} 1`,
		`database_pool_max_connections{pool="runtime",service="organization-service"} 7`,
		`database_pool_acquire_duration_seconds_total{pool="runtime",service="organization-service"} 1.5`,
	} {
		if !strings.Contains(body, expected) { t.Fatalf("metrics missing %q\n%s", expected, body) }
	}
	if strings.Contains(body, "org_provider_123") { t.Fatalf("unbounded aggregate label leaked: %s", body) }
}

func TestMetricsRegistryConstructionAndNilPoolAreSafe(t *testing.T) {
	for range 2 {
		metrics, err := New("organization-service", nil)
		if err != nil { t.Fatalf("New() error = %v", err) }
		recorder := httptest.NewRecorder()
		metrics.Handler().ServeHTTP(recorder, httptest.NewRequest("GET", "/metrics", nil))
		if recorder.Code != 200 { t.Fatalf("status = %d", recorder.Code) }
	}
}
