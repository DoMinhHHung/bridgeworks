## Production Observability Foundation

Identity Service and Organization Service expose low-cardinality Prometheus metrics on private operational listeners only:

```text
identity-service:9090/metrics
organization-service:9090/metrics
```

The ports are available only inside the Docker `bridgeworks` network. APISIX does not route `/metrics`, and the metrics ports are not published to the host.

Every completed application request emits a structured access record with `service`, `request_id`, `method`, bounded chi `route`, `status`, `duration_ms`, and `response_bytes`. Health completion records use debug level. Raw paths, query strings, request/response bodies, authorization material, webhook signatures, email addresses, provider identifiers, local UUIDs, database URLs, and raw dependency errors are excluded.

Implemented metrics:

```text
http_requests_total{service,route,method,status_class}
http_request_duration_seconds{service,route,method}
http_requests_in_flight{service}
clerk_webhook_events_total{service,aggregate,outcome}
database_pool_acquired_connections{service,pool}
database_pool_idle_connections{service,pool}
database_pool_total_connections{service,pool}
database_pool_max_connections{service,pool}
database_pool_acquire_count_total{service,pool}
database_pool_acquire_duration_seconds_total{service,pool}
database_pool_empty_acquire_count_total{service,pool}
database_pool_canceled_acquire_count_total{service,pool}
```

Route labels use matched chi templates or the bounded fallback `unknown`. Webhook aggregates and outcomes use explicit bounded enums. PostgreSQL pool metrics are collected from `pgxpool.Stat()` at scrape time without queries or ticker goroutines.

Metrics and scraping do not participate in application readiness. PostgreSQL pool defaults remain unchanged until load tests provide evidence for a safe per-replica connection budget. Redis cache-aside, using Upstash when implemented, rate limiting, OpenTelemetry tracing, and inbox retention remain separate roadmap work.
