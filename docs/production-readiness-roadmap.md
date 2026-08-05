# Production Readiness Roadmap

This document records focused operational work for Identity Service and Organization Service. PR #8 implements bounded metrics and structured access logs. PR #9 adds the repeatable load-test harness and deterministic connection-budget methodology. Identity current-user cache-aside is complete with shared generation fencing that prevents a successful post-commit invalidation from being overwritten by an older in-flight cache fill. Actual production pool sizing, traffic protection, tracing, and retention remain separate work with measured rollout criteria.

## Group A — Traffic protection and capacity

### Load-test harness and capacity methodology — completed in PR #9

The checked-in load-test harness now provides isolated profiles for:

- Identity authenticated `/me`;
- Organization current organization and current membership;
- the private Organization-to-Identity dependency;
- Clerk user webhook bursts and retries;
- Clerk organization and membership webhook bursts and retries;
- dependency degradation through Identity unavailability/delay, experimental constrained pools, and service restarts.

The PR smoke suite validates harness correctness with loose thresholds. Baseline, burst, saturation, and degradation profiles are manually dispatched. Each scenario produces sanitized JSON and Markdown containing the tested SHA, load shape, throughput, p50/p95/p99, error/status distribution, replica and pool configuration, thresholds, environment limitations, and before/during/after HTTP in-flight plus PostgreSQL pool telemetry.

The deterministic calculator requires the PostgreSQL/Supabase maximum, reserved admin and migration connections, operational headroom, per-service allocations, and maximum replicas. It calculates separate Identity and Organization ceilings:

```text
per-replica max connections
  <= floor(service connection budget / maximum service replica count)
```

Run and interpretation guidance lives in [`load-test-capacity-runbook.md`](load-test-capacity-runbook.md).

### Representative production pool sizing — pending

Do not raise PostgreSQL pool defaults from CI measurements, intuition, or values copied from another deployment. GitHub-hosted runners validate repeatability and relative behavior only; they are not production capacity claims.

Before changing pool defaults:

- run the same baseline and degradation scenarios in a representative deployment environment;
- use the real Supabase/PostgreSQL connection limit and reserved operational budget;
- test expected maximum replica counts;
- compare p50/p95/p99, throughput, error distribution, acquire wait, empty/canceled acquisitions, database CPU, query latency, and lock pressure;
- document the candidate value as experimental until evidence is reviewed;
- define rollback criteria and the previous known-good pool configuration.

`DATABASE_MAX_CONNS`, `DATABASE_MIN_CONNS`, `ORGANIZATION_DATABASE_MAX_CONNS`, and `ORGANIZATION_DATABASE_MIN_CONNS` remain unchanged by PR #9.

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

Labels are restricted to `service` and `pool=runtime`. Nil pool handling emits no fake metric and cannot affect the request path. Collector execution does not participate in readiness.

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

### Identity `/me` Upstash Redis cache-aside — completed

Identity applies cache-aside at the `currentuser.Reader` boundary. PostgreSQL remains authoritative; Redis miss, timeout, corruption, generation-read failure, CAS failure, or outage falls back to PostgreSQL. Redis is not pinged during startup and is excluded from readiness.

The cache uses SHA-256-derived versioned data and generation keys, strict schema-versioned JSON values, a 30-second default data TTL with a five-minute maximum, no negative caching, and per-process same-key miss coalescing. A cold loader reads the shared generation before PostgreSQL and performs an atomic generation-CAS `SET`. Successful user-sync `processed`, `duplicate`, and `stale` results run one post-commit Redis operation that increments the generation, refreshes its 10-minute expiry, and deletes the shared data key.

The generation marker lifetime exceeds the bounded two-second shared PostgreSQL load. Consequently, when invalidation succeeds, an older in-flight load from any Identity replica cannot repopulate the cache with a pre-invalidation projection. A rejected fill returns the PostgreSQL result to its existing caller but records a bounded `set,stale` metric and does not modify Redis.

If the entire Redis invalidation operation fails after PostgreSQL commit, the committed webhook result is preserved and the data TTL still bounds residual stale data. The service does not claim zero-staleness during an actual Redis timeout or rejection. This is distinct from the successful-invalidation cache-fill race, which the shared generation CAS prevents.

A valid warm cache hit may serve `/me` during a PostgreSQL outage while `/health/ready` remains failed. Cold misses preserve the existing sanitized `503`. Multi-replica Identity instances share both Redis namespaces and the same invalidation fence. Metrics use only bounded operation/outcome labels.

Production Upstash requires TLS and secret-managed credentials. Local Redis is plaintext private Compose infrastructure for development and CI only, and the Compose boundary defaults TLS off for that local topology. Configuration, failure matrix, fencing semantics, stale-risk analysis, verification, and cursor-based rollback are documented in [`runbooks/identity-current-user-cache.md`](runbooks/identity-current-user-cache.md).

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

### Current operational status

The observability foundation enforces compile-time webhook outcomes, bounded HTTP method and route labels, service-specific webhook aggregates, private metrics isolation, and deterministic shutdown ordering. PR #9 consumes that telemetry through a repeatable load-test and capacity-reporting harness.

PR #9 completes the repeatable load-test harness and deterministic capacity methodology. This focused PR completes Identity `/me` cache-aside, including shared generation fencing for successful post-commit invalidation, within the current service boundary. Representative production pool sizing, rate limiting, OpenTelemetry, inbox retention, and JWT/JWKS rotation remain separate work.
