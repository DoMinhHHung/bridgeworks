package observability

import (
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

const (
	AggregateUser         = "user"
	AggregateOrganization = "organization"
	AggregateMembership   = "membership"

	OutcomeProcessed        = "processed"
	OutcomeDuplicate        = "duplicate"
	OutcomeStale            = "stale"
	OutcomeRejected         = "rejected"
	OutcomeRetryableFailure = "retryable_failure"
)

var httpLatencyBuckets = []float64{
	0.005, 0.010, 0.025, 0.050, 0.100, 0.250,
	0.500, 1, 2.5, 5, 10,
}

type PoolStat interface {
	AcquiredConns() int32
	IdleConns() int32
	TotalConns() int32
	MaxConns() int32
	AcquireCount() int64
	AcquireDuration() time.Duration
	EmptyAcquireCount() int64
	CanceledAcquireCount() int64
}

type Metrics struct {
	service       string
	registry      *prometheus.Registry
	httpRequests  *prometheus.CounterVec
	httpDuration  *prometheus.HistogramVec
	httpInFlight  *prometheus.GaugeVec
	webhookEvents *prometheus.CounterVec
}

func New(service string, poolStat func() PoolStat) (*Metrics, error) {
	if service == "" {
		return nil, fmt.Errorf("observability service name must not be empty")
	}

	metrics := &Metrics{
		service:  service,
		registry: prometheus.NewRegistry(),
		httpRequests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "http_requests_total",
			Help: "Completed HTTP requests grouped by bounded route, method, and status class.",
		}, []string{"service", "route", "method", "status_class"}),
		httpDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "http_request_duration_seconds",
			Help:    "HTTP request duration grouped by bounded route and method.",
			Buckets: httpLatencyBuckets,
		}, []string{"service", "route", "method"}),
		httpInFlight: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "http_requests_in_flight",
			Help: "HTTP requests currently executing.",
		}, []string{"service"}),
		webhookEvents: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "clerk_webhook_events_total",
			Help: "Clerk webhook events grouped by bounded aggregate and outcome.",
		}, []string{"service", "aggregate", "outcome"}),
	}

	collectors := []prometheus.Collector{
		metrics.httpRequests,
		metrics.httpDuration,
		metrics.httpInFlight,
		metrics.webhookEvents,
		newPoolCollector(service, "runtime", poolStat),
	}
	for _, collector := range collectors {
		if err := metrics.registry.Register(collector); err != nil {
			return nil, fmt.Errorf("register observability collector: %w", err)
		}
	}
	return metrics, nil
}

func (m *Metrics) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("GET /metrics", promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{}))
	return mux
}

func (m *Metrics) HTTPRequestStarted() {
	if m == nil {
		return
	}
	m.httpInFlight.WithLabelValues(m.service).Inc()
}

func (m *Metrics) HTTPRequestCompleted() {
	if m == nil {
		return
	}
	m.httpInFlight.WithLabelValues(m.service).Dec()
}

func (m *Metrics) ObserveHTTPRequest(route, method string, status int, duration time.Duration) {
	if m == nil {
		return
	}
	if route == "" {
		route = "unknown"
	}
	statusClass := strconv.Itoa(status/100) + "xx"
	if status < 100 || status > 599 {
		statusClass = "5xx"
	}
	m.httpRequests.WithLabelValues(m.service, route, method, statusClass).Inc()
	m.httpDuration.WithLabelValues(m.service, route, method).Observe(duration.Seconds())
}

func (m *Metrics) ObserveWebhook(aggregate, outcome string) {
	if m == nil || !boundedAggregate(aggregate) || !boundedOutcome(outcome) {
		return
	}
	m.webhookEvents.WithLabelValues(m.service, aggregate, outcome).Inc()
}

func boundedAggregate(value string) bool {
	switch value {
	case AggregateUser, AggregateOrganization, AggregateMembership:
		return true
	default:
		return false
	}
}

func boundedOutcome(value string) bool {
	switch value {
	case OutcomeProcessed, OutcomeDuplicate, OutcomeStale, OutcomeRejected, OutcomeRetryableFailure:
		return true
	default:
		return false
	}
}

type poolCollector struct {
	service              string
	pool                 string
	stat                 func() PoolStat
	acquired             *prometheus.Desc
	idle                 *prometheus.Desc
	total                *prometheus.Desc
	max                  *prometheus.Desc
	acquireCount         *prometheus.Desc
	acquireDuration      *prometheus.Desc
	emptyAcquireCount    *prometheus.Desc
	canceledAcquireCount *prometheus.Desc
}

func newPoolCollector(service, pool string, stat func() PoolStat) *poolCollector {
	labels := []string{"service", "pool"}
	return &poolCollector{
		service:              service,
		pool:                 pool,
		stat:                 stat,
		acquired:             prometheus.NewDesc("database_pool_acquired_connections", "Currently acquired PostgreSQL pool connections.", labels, nil),
		idle:                 prometheus.NewDesc("database_pool_idle_connections", "Currently idle PostgreSQL pool connections.", labels, nil),
		total:                prometheus.NewDesc("database_pool_total_connections", "Current total PostgreSQL pool connections.", labels, nil),
		max:                  prometheus.NewDesc("database_pool_max_connections", "Configured PostgreSQL pool maximum connections.", labels, nil),
		acquireCount:         prometheus.NewDesc("database_pool_acquire_count_total", "Total successful PostgreSQL pool acquisitions.", labels, nil),
		acquireDuration:      prometheus.NewDesc("database_pool_acquire_duration_seconds_total", "Cumulative PostgreSQL pool acquisition wait duration.", labels, nil),
		emptyAcquireCount:    prometheus.NewDesc("database_pool_empty_acquire_count_total", "Total PostgreSQL acquisitions that waited for an empty pool.", labels, nil),
		canceledAcquireCount: prometheus.NewDesc("database_pool_canceled_acquire_count_total", "Total canceled PostgreSQL pool acquisitions.", labels, nil),
	}
}

func (c *poolCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.acquired
	ch <- c.idle
	ch <- c.total
	ch <- c.max
	ch <- c.acquireCount
	ch <- c.acquireDuration
	ch <- c.emptyAcquireCount
	ch <- c.canceledAcquireCount
}

func (c *poolCollector) Collect(ch chan<- prometheus.Metric) {
	if c == nil || c.stat == nil {
		return
	}
	defer func() { _ = recover() }()
	stat := c.stat()
	if stat == nil {
		return
	}
	labels := []string{c.service, c.pool}
	ch <- prometheus.MustNewConstMetric(c.acquired, prometheus.GaugeValue, float64(stat.AcquiredConns()), labels...)
	ch <- prometheus.MustNewConstMetric(c.idle, prometheus.GaugeValue, float64(stat.IdleConns()), labels...)
	ch <- prometheus.MustNewConstMetric(c.total, prometheus.GaugeValue, float64(stat.TotalConns()), labels...)
	ch <- prometheus.MustNewConstMetric(c.max, prometheus.GaugeValue, float64(stat.MaxConns()), labels...)
	ch <- prometheus.MustNewConstMetric(c.acquireCount, prometheus.CounterValue, float64(stat.AcquireCount()), labels...)
	ch <- prometheus.MustNewConstMetric(c.acquireDuration, prometheus.CounterValue, stat.AcquireDuration().Seconds(), labels...)
	ch <- prometheus.MustNewConstMetric(c.emptyAcquireCount, prometheus.CounterValue, float64(stat.EmptyAcquireCount()), labels...)
	ch <- prometheus.MustNewConstMetric(c.canceledAcquireCount, prometheus.CounterValue, float64(stat.CanceledAcquireCount()), labels...)
}
