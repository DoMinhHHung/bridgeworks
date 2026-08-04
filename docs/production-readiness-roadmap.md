# Production Readiness Roadmap

This document records operational work identified during the Organization Service production-readiness review. These items are intentionally not implemented in PR #7 because they require focused design, capacity measurements, rollout plans, and independent failure-mode validation.

The roadmap applies to the Identity and Organization services unless a section says otherwise.

## Group A — Traffic protection and capacity

### Load testing before pool changes

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

### APISIX rate limiting

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

### Structured success access logs

Add bounded structured logs for successful requests, not only errors. Required fields should include:

- service name;
- request ID;
- method;
- route template, never an unbounded raw path when avoidable;
- status code;
- duration;
- response size where practical.

Do not log Authorization headers, JWTs, webhook bodies, signing headers, email addresses, Clerk IDs, database URLs, or raw dependency errors.

### Metrics

Add low-cardinality HTTP metrics:

- request count by service, route template, method, and status class;
- request duration histogram by route template and method;
- in-flight request gauge;
- webhook outcome counters for accepted, duplicate, stale, rejected, and retryable failure classes.

Add PostgreSQL pool metrics:

- acquired and idle connections;
- maximum configured connections;
- acquisition count and wait duration;
- canceled acquisitions;
- query or transaction timeout counts where available.

Do not put provider IDs, user IDs, organization IDs, request IDs, raw paths, or error strings into metric labels.

### Tracing

Adopt W3C Trace Context propagation across APISIX, Identity, and Organization. A focused OpenTelemetry PR must define:

- inbound `traceparent` and `tracestate` handling;
- propagation on the private Organization-to-Identity call;
- exporter endpoint and credentials through configuration or secret management;
- sampling policy;
- bounded attributes and redaction rules;
- exporter timeout and backpressure behavior;
- behavior when the collector is unavailable.

Metrics and tracing exporters are operational dependencies only. They must not make service readiness fail.

No unauthenticated public metrics endpoint may be exposed through APISIX. Metrics should be scraped or exported only through a private operational network or collector path.

## Group C — Lifecycle operations

### Identity `/me` Redis cache-aside

A future Identity PR may introduce Redis cache-aside for the local account projection returned by `/me`.

Required properties:

- PostgreSQL remains the source of truth;
- a cache miss or Redis outage falls back to PostgreSQL;
- Redis is excluded from readiness;
- cache entries are bounded by TTL and schema version;
- cache invalidation occurs only after a successful user-sync transaction commits;
- disabled and deleted status changes must invalidate or replace cached active projections;
- cache keys and values must not expose secrets;
- stampede behavior and negative caching require explicit design;
- the Organization-to-Identity request timeout must still bound cache and database fallback work.

Do not copy a generic Redis snippet into Identity. The cache contract must be tested against webhook commit, rollback, outage, and stale-entry scenarios.

### Webhook inbox retention

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

### Clerk JWT verification-key rotation

Identity and Organization currently use static configured public verification key material. Operators must follow the checked-in rotation runbook:

- [`runbooks/clerk-jwt-key-rotation.md`](runbooks/clerk-jwt-key-rotation.md)

The static-key design has limited overlap support. A future design should evaluate multi-key verification or Clerk JWKS retrieval with bounded caching, issuer validation, failure fallback, and rotation observability before implementation.
