## Private metrics and access logs

Organization Service starts a second operational HTTP listener configured by:

```dotenv
ORGANIZATION_METRICS_ADDR=:9090
```

The address must be a non-empty `host:port`, must not include a URL scheme, and must differ from `HTTP_ADDR`. Bind failure stops startup. The listener exposes only `GET /metrics`, is gracefully shut down, is private to the Docker network, and is not routed through APISIX. Prometheus absence or scrape failure does not affect `/health/ready`, webhook synchronization, or authenticated requests.

Organization metrics use a service-owned Prometheus registry and bounded labels:

```text
http_requests_total{service,route,method,status_class}
http_request_duration_seconds{service,route,method}
http_requests_in_flight{service}
clerk_webhook_events_total{service,aggregate,outcome}
database_pool_*{service,pool="runtime"}
```

Webhook aggregate values are `organization` and `membership`. Outcomes are `processed`, `duplicate`, `stale`, `rejected`, and `retryable_failure`. Processed, duplicate, and stale outcomes come from the application-owned Unit of Work after its commit decision; the HTTP layer does not infer persistence semantics from status codes.

Every completed application request emits one structured access record containing `service`, `request_id`, `method`, matched chi `route`, `status`, `duration_ms`, and `response_bytes`. Unmatched routes use `unknown`; raw URL paths and query strings are never fallback labels or log fields. Health completion records use debug level.

Access logs and metrics exclude Authorization, Cookie, JWTs, webhook bodies, Svix headers, email addresses, Clerk user, organization, or membership IDs, local UUIDs, database URLs, request or response bodies, request IDs as metric labels, and raw dependency errors.

Pool defaults remain unchanged until measured load tests establish throughput, latency, replica count, connection wait, and the total Organization PostgreSQL connection budget. OpenTelemetry tracing, APISIX rate limiting, caching, and inbox retention remain separate work.
