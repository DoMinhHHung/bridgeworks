## Private metrics and access logs

Identity Service starts a second operational HTTP listener configured by:

```dotenv
METRICS_ADDR=:9090
```

The address must be a non-empty `host:port`, must not include a URL scheme, and must differ from `HTTP_ADDR`. Bind failure stops startup. The listener exposes only `GET /metrics`, is gracefully shut down, is private to the Docker network, and is not routed through APISIX. Prometheus absence or scrape failure does not affect `/health/ready` or request handling.

Identity metrics use a service-owned Prometheus registry and bounded labels:

```text
http_requests_total{service,route,method,status_class}
http_request_duration_seconds{service,route,method}
http_requests_in_flight{service}
clerk_webhook_events_total{service,aggregate="user",outcome}
database_pool_*{service,pool="runtime"}
```

Webhook outcomes are `processed`, `duplicate`, `stale`, `rejected`, and `retryable_failure`. Processed, duplicate, and stale outcomes are returned by the application transaction flow after the corresponding commit decision.

Every completed application request emits one structured access record containing `service`, `request_id`, `method`, matched chi `route`, `status`, `duration_ms`, and `response_bytes`. Unmatched routes use `unknown`; raw URL paths and query strings are never fallback labels or log fields. Health completion records use debug level.

Access logs and metrics exclude Authorization, Cookie, JWTs, webhook bodies, Svix headers, email addresses, Clerk user IDs, local UUIDs, database URLs, request or response bodies, request IDs as metric labels, and raw dependency errors.

Pool defaults remain unchanged until measured load tests establish throughput, latency, replica count, connection wait, and the total Supabase PostgreSQL connection budget. A later Identity cache-aside PR will use Upstash Redis with PostgreSQL fallback and Redis excluded from readiness; no Redis code exists in this observability PR.
