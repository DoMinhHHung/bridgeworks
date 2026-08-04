# Production Readiness Roadmap

This document records focused operational work for Identity Service and Organization Service. PR #8 implements the bounded metrics and access-log foundation only; capacity, traffic protection, tracing, caching, and retention remain separate work that requires measured rollout criteria.

## Group A — Traffic protection and capacity

### Load testing before pool changes — pending

Do not raise PostgreSQL pool defaults from intuition or copy values from another deployment. Establish a repeatable load-test target first:

- representative authenticated read traffic;
- Clerk webhook bursts and retries;
- expected p50, p95, and p99 latency targets;
- acceptable PostgreSQL connection wait time;
- expected service replica count;
- failure tests with one database connection unavailable or one replica restarting.

Pool sizing must be derived from measured concurrency and the database connection budget. A starting budget formula is:

```text
per-replica max connections
  <= floor((database connection limit - reserved admin/migration/headroom connections)
           / maximum service replica count)
```

Identity and Organization need separate budgets. Migration jobs must use separately budgeted credentials and connections. Any pool increase requires evidence that connection wait, query latency, and database CPU support the change.

PR #8 exposes the pool telemetry required for this decision but deliberately leaves all pool defaults unchanged.

### APISIX rate limiting — pending

Rate limiting requires an endpoint-specific policy rather than one broad global quota. A focused gateway PR must define:

- quota and burst allowance for each public endpoint class;
- key strategy, such as verified user, organization, webhook endpoint, or trusted client IP;
- trusted proxy boundaries and deterministic client-IP extraction;
- protection against spoofed forwarding headers;
- single-node local policy for development and a multi-node Redis-backed policy for production;
- Clerk webhook retry safety so temporary throttling does not convert valid provider retries into data loss;
- a deterministic JSON `429` contract using the existing error envelope and request ID;
- `Retry-After` behavior;
- rollout monitoring and rollback thresholds.

Webhook quotas must account for legitimate Clerk retry bursts and should not share a bucket with authenticated read APIs.

## Group B — Observability

### Structured HTTP access logs — completed in PR #8

Every completed application request emits one structured `slog` record with:

- `service`;
- `request_id`;
- `method`;
- bounded chi `route` pattern;
- `status`;
- `duration_ms`;
- `response_bytes`.

Unmatched requests use `route=unknown`; raw paths and query strings are never used as fallback values. Middleware ordering ensures the request ID exists first and recovered panics produce a final `500` completion record. Health request completion logs use debug level to avoid routine probe noise.

Access logs do not include Authorization, Cookie, JWTs, webhook bodies, Svix headers, email addresses, Clerk identifiers, membership identifiers, local UUIDs, database URLs, request/response bodies, or raw dependency errors.

### Low-cardinality HTTP and webhook metrics — completed in PR #8

Each service owns an independent Prometheus registry. Implemented HTTP metrics are:

```text
http_requests_total{service,route,method,status_class}
http_request_duration_seconds{service,route,method}
http_requests_in_flight{service}
```

`route` is the matched chi route pattern or the bounded value `unknown`. `status_class` is restricted to `1xx|2xx|3xx|4xx|5xx`. No provider/user identifier, request ID, raw path, error string, host, database value, or authorization party is a label.

Implemented Clerk webhook metric:

```text
clerk_webhook_events_total{service,aggregate,outcome}
```

Bounded aggregates are `user`, `organization`, and `membership`. Bounded outcomes are `processed`, `duplicate`, `stale`, `rejected`, and `retryable_failure`. Processed/duplicate/stale values come from application-layer transaction results rather than inference from HTTP status.

### PostgreSQL pool metrics — completed in PR #8

A scrape-time collector reads `pgxpool.Stat()` without running a database query or ticker goroutine. It exposes:

```text
database_pool_acquired_connections
database_pool_idle_connections
database_pool_total_connections
database_pool_max_connections
database_pool_acquire_count_total
database_pool_acquire_duration_seconds_total
database_pool_empty_acquire_count_total
database_pool_canceled_acquire_count_total
```

Labels are restricted to `service` and `pool=runtime`. Nil or closed pool handling is defensive and cannot panic the request path. Collector execution does not participate in readiness.

### Private metrics listeners — completed in PR #8

Identity uses `METRICS_ADDR` and Organization uses `ORGANIZATION_METRICS_ADDR`, both defaulting to `:9090` inside their own containers. Each listener exposes only `GET /metrics`, has HTTP timeouts and graceful shutdown, and fails startup if it cannot bind.

Metrics ports are exposed only on the private Docker network. They are not host-published and no APISIX route points to them. Prometheus absence or scrape failure does not affect application requests or `/health/ready`.

### OpenTelemetry tracing — pending

Adopt W3C Trace Context propagation across APISIX, Identity, and Organization in a focused later PR. That work must define:

- inbound `traceparent` and `tracestate` handling;
- propagation on the private Organization-to-Identity call;
- exporter endpoint and credentials through configuration or secret management;
- sampling policy;
- bounded attributes and redaction rules;
- exporter timeout and backpressure behavior;
- behavior when the collector is unavailable.

Tracing exporters are operational dependencies only. They must not make service readiness fail. PR #8 intentionally adds no OpenTelemetry SDK, exporter, or collector.

## Group C — Lifecycle operations

### Identity `/me` Upstash Redis cache-aside — pending

A future Identity PR may introduce Upstash Redis cache-aside for the local account projection returned by `/me`.

Required properties:

- PostgreSQL remains the source of truth;
- a cache miss or Upstash outage falls back to PostgreSQL;
- Redis is excluded from readiness;
- cache entries are bounded by TTL and schema version;
- cache invalidation occurs only after a successful user-sync transaction commits;
- disabled and deleted status changes must invalidate or replace cached active projections;
- cache keys and values must not expose secrets;
- stampede behavior and negative caching require explicit design;
- network/TLS timeout to Upstash must be short and bounded;
- the Organization-to-Identity request timeout must still bound cache and database fallback work.

Do not copy a generic Redis snippet into Identity. The cache contract must be tested against webhook commit, rollback, outage, stale-entry, and multi-instance invalidation scenarios. PR #8 adds telemetry needed to measure cache impact but contains no Redis code.

### Webhook inbox retention — pending

Both Identity and Organization webhook inbox tables need bounded retention. The retention duration must be selected from actual replay, incident investigation, compliance, and audit requirements.

A focused retention design must define:

- minimum retained replay window;
- whether processed event metadata is required for incident analysis;
- batch size and deletion cadence;
- indexes used by retention queries;
- lock and vacuum impact;
- behavior during database load or maintenance;
- metrics and alerting for backlog age;
- backup and restore expectations.

Do not add `pg_cron`, an application goroutine, or a scheduler implicitly. Choose the operational owner and execution mechanism explicitly.

### Clerk JWT verification-key rotation — runbook exists; design follow-up pending

Identity and Organization currently use static configured public verification key material. Operators must follow the checked-in rotation runbook:

- [`runbooks/clerk-jwt-key-rotation.md`](runbooks/clerk-jwt-key-rotation.md)

The static-key design has limited overlap support. A future design should evaluate multi-key verification or Clerk JWKS retrieval with bounded caching, issuer validation, failure fallback, and rotation observability before implementation.

### Observability correction status

The completed observability foundation enforces webhook outcomes through compile-time application interfaces, bounds arbitrary HTTP methods to the Prometheus label `OTHER`, validates service-specific webhook aggregate allowlists, and does not silently recover pool-collector programming panics. Private-listener isolation and shutdown ordering are covered by regression tests. Health access records remain debug-only and are normally absent when services run at info level.

This does not complete load testing, pool sizing, rate limiting, Upstash Redis cache-aside, OpenTelemetry, or inbox retention.
