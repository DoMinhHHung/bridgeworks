# Load Test and Database Capacity Runbook

## Purpose and non-goals

This runbook defines the repeatable BridgeWorks load-test harness for Identity and Organization and the deterministic PostgreSQL/Supabase connection-budget methodology. The harness consumes private telemetry introduced in PR #8. It does not change PostgreSQL pool defaults and GitHub-hosted results are not production capacity claims.

Production binaries have no k6 dependency. The harness pins:

```text
grafana/k6:2.0.0
python:3.13.5-alpine3.22   # delayed dependency proxy used only by the harness
```

Generated RSA keys, JWTs, webhook secrets, provider fixture IDs, webhook bodies, emails, and database URLs remain in a restrictive temporary directory and are deleted during cleanup. Retained artifacts contain sanitized measurements only.

## Scenarios

Authenticated reads and webhook traffic remain isolated:

| Scenario | Path under test |
|---|---|
| `identity_me` | APISIX → Identity `/me` |
| `organization_current` | APISIX → Organization current organization |
| `organization_membership` | APISIX → Organization current membership |
| `organization_dependency` | APISIX → Organization → private Identity dependency |
| `identity_user_unique` / `identity_user_retry` | Clerk user webhook unique events and retries |
| `organization_unique` / `organization_retry` | Clerk organization webhook unique events and retries |
| `membership_unique` / `membership_retry` | Clerk membership webhook unique events and retries |

Do not merge authenticated-read and webhook results into one latency or throughput number.

## Profiles

### Smoke

Runs on every PR. It validates fixture creation, routing, telemetry scraping, report generation, sanitization, and cleanup with loose correctness thresholds.

```bash
make loadtest-smoke
```

Smoke is not a production benchmark.

### Baseline

Manual steady-state constant-arrival test:

```bash
bash loadtest/scripts/run.sh baseline identity_me none
bash loadtest/scripts/run.sh baseline organization_current none
```

Keep load shape, replica count, pool configuration, deployment shape, and environment stable when comparing commits.

### Burst

Manual sudden-concurrency increase. Run reads and webhooks separately:

```bash
bash loadtest/scripts/run.sh burst organization_current none
bash loadtest/scripts/run.sh burst identity_user_unique none
```

### Saturation

Manual only. Arrival rate increases gradually and k6 aborts on explicit check, error, or p99 thresholds:

```bash
bash loadtest/scripts/run.sh saturation identity_me none
```

Never schedule saturation on every commit.

### Dependency degradation

Supported combinations are validated before credentials, image builds, Compose, or k6:

```bash
# Whole Identity dependency unavailable to Organization.
bash loadtest/scripts/run.sh \
  dependency-degradation organization_dependency identity-unavailable

# Identity dependency delayed through a test-only proxy.
bash loadtest/scripts/run.sh \
  dependency-degradation organization_dependency identity-delayed

# Experimental max=1 pools in the generated environment only.
bash loadtest/scripts/run.sh \
  dependency-degradation organization_current constrained-pool

# Restart exactly one Identity replica while another stays available.
IDENTITY_REPLICAS=2 ORGANIZATION_REPLICAS=1 \
  bash loadtest/scripts/run.sh \
  dependency-degradation identity_me replica-restart
```

Rules:

- `identity-unavailable` and `identity-delayed` require `organization_dependency`;
- `replica-restart` requires at least two replicas for the scenario’s target service;
- any non-degradation profile requires `degradation_mode=none`;
- replica counts must be integers from 1 through 5;
- dependency degradation requires an explicit supported mode.

Identity scenarios restart one Identity replica. Organization scenarios restart one Organization replica.

## Single-replica restart semantics

`replica-restart` is distinct from `identity-unavailable`:

- `identity-unavailable` intentionally stops the complete Identity service dependency;
- `replica-restart` selects and restarts exactly one concrete container while the other replicas must remain running.

For `replica-restart`, the harness:

1. resolves all running container IDs for the target service;
2. sorts the IDs lexicographically;
3. selects the first ID as the deterministic target;
4. derives a bounded 12-hex-character replica key;
5. captures the scenario's exact `http_requests_total` counter on each non-target replica;
6. starts a bounded monitor that tolerates individual failed scrapes but fails if a non-target replica stops;
7. calls `docker restart` only with the target container ID;
8. keeps polling until that same target becomes healthy and requires a positive non-target request delta over the restart window;
9. captures the recovered target's request counter, allows a short bounded traffic interval, and requires a positive post-recovery delta;
10. verifies non-target identities remained running and the expected replica count is restored.

The retained report records bounded target/non-target replica keys, `non_target_request_delta_during_restart`, `target_request_delta_after_recovery`, and their corresponding booleans. It never includes Docker inspect output, environment variables, secrets, database URLs, raw metrics responses, or provider identifiers.

These are four distinct facts:

- the k6 container is running;
- requests reach the target application service;
- the surviving replica serves requests while the target is restarting;
- the restarted replica serves requests after becoming healthy.

Only the last two measured counter increases establish restart continuity and recovery. k6 process state alone is never used as evidence.

The PR CI regression runs two Identity replicas, short authenticated `/me` traffic, and one single-container restart. It proves the harness measures those facts correctly; it is not a production high-availability or resilience claim.

## Stable replica identity in snapshots

Snapshot filenames retain phase, sample, service, and replica identity:

```text
metrics-before-0000-identity-a1b2c3d4e5f6.prom
metrics-during-0007-identity-a1b2c3d4e5f6.prom
metrics-after-0000-identity-a1b2c3d4e5f6.prom
```

A Docker restart keeps the same container ID and therefore the same series. A replaced container receives a different key and begins a new series. The reporter does not use `docker compose ps -q` ordering as identity.

## Per-replica counter accounting

Monotonic counters are never summed across replicas before reset handling.

For each service and replica key, the reporter orders snapshots chronologically and calculates the increase independently:

- a replica with a `before` sample uses that first value as its baseline;
- a replica first seen during or after the run starts from zero;
- when `current < previous`, `current` is added as the post-reset increase;
- a temporarily missing replica has no observation and is not treated as zero;
- a replacement container starts a separate series;
- after each replica’s increase is complete, increases are summed across replicas.

This applies to:

```text
http_requests_total
http_request_duration_seconds_bucket
http_request_duration_seconds_count
database_pool_acquire_count_total
database_pool_acquire_duration_seconds_total
database_pool_empty_acquire_count_total
database_pool_canceled_acquire_count_total
```

Example:

```text
Replica A: 100 -> 110       increase 10
Replica B: 100 -> 0 -> 5    increase 5
Total increase: 15
```

Aggregating first would incorrectly treat the combined series as one reset and may recount the surviving replica.

## Gauge aggregation

Gauges are not monotonic counters. For each sample, the reporter sums available replica values for:

```text
database_pool_acquired_connections
database_pool_idle_connections
database_pool_total_connections
database_pool_max_connections
http_requests_in_flight
```

The before and after phases report their final sample. The during phase reports the maximum summed value observed across samples. A missing replica contributes no observation for that scrape; it is not manufactured as a zero-valued counter reset.

## Client/service telemetry reconciliation

Artifacts contain explicit fields:

```text
client_request_count
service_request_count
service_histogram_count
gateway_or_transport_only_failures
request_count_reconciliation_complete
```

Normal profiles use a strict tolerance of exactly zero requests:

```text
client_request_count == service_request_count
service_histogram_count == service_request_count
```

Any unexplained gateway-only gap fails a normal report.

Explicit dependency-degradation profiles may have requests fail at APISIX or transport before entering the application. Those requests are recorded as:

```text
gateway_or_transport_only_failures
  = max(client_request_count - service_request_count, 0)
```

The reporter does not manufacture service metrics for such requests. Even during degradation, the required invariant is:

```text
0 < service_request_count <= client_request_count
service_histogram_count == service_request_count
```

Required finite histogram buckets and the `+Inf` bucket must be present, and the `+Inf` increase must equal the histogram count. A complete APISIX/transport outage with `client_request_count > 0` but zero application requests is an incomplete report and cannot pass.

For `replica-restart`, pass additionally requires positive measured request deltas on the surviving replica during restart and on the recovered target after health restoration, together with all existing running/health/replica-count checks.

## Result artifacts

Each scenario produces:

```text
loadtest-results/<profile>/<scenario>/result.json
loadtest-results/<profile>/<scenario>/result.md
```

Fields include:

- git SHA;
- scenario and profile;
- replica counts;
- effective min/max pool configuration and experimental marker;
- duration, client throughput, p50/p95/p99/max, error rate, and status distribution;
- service request and histogram deltas;
- client/service reconciliation;
- per-replica HTTP request increases;
- gauge measurements before, during, and after;
- per-replica-reset-safe PostgreSQL counter deltas;
- disruption verification metadata;
- thresholds and environment limitations.

Raw Prometheus snapshots and raw k6 summaries are removed after report generation.

## Interpreting pool telemetry

Use metrics together:

- acquired near summed max with low idle indicates little remaining pool headroom;
- increasing acquire duration means callers wait longer for a connection;
- increasing empty acquire count confirms acquisitions encountered no immediately idle connection;
- canceled acquire count means callers gave up while waiting;
- HTTP in-flight combined with p95/p99 distinguishes queueing from isolated slow requests;
- compare per-run counter deltas, not raw process-lifetime values.

A pool candidate is not justified merely because all configured connections were used. Review database CPU, query latency, locks, Supabase limits, replica count, and operational reserves.

## Deterministic connection budget

Run with the real PostgreSQL/Supabase limit and explicit reservations:

```bash
python3 loadtest/capacity.py \
  --postgres-max-connections 100 \
  --reserved-admin-connections 5 \
  --reserved-migration-connections 5 \
  --operational-headroom-connections 20 \
  --identity-allocation 30 \
  --identity-max-replicas 3 \
  --organization-allocation 30 \
  --organization-max-replicas 3 \
  --output-json loadtest-results/capacity/capacity-budget.json \
  --output-markdown loadtest-results/capacity/capacity-budget.md
```

For each service:

```text
per-replica max connections
  <= floor(service connection budget / maximum service replica count)
```

Identity and Organization allocations remain separate. A calculated value is a planning ceiling, not a production default.

## Comparing commits

1. Use the same profile, scenario, replica counts, pool values, deployment shape, k6 variables, and environment.
2. Download both workflow artifacts.
3. Compare client latency/throughput/error fields.
4. Compare service request and histogram counts and reconciliation fields.
5. Compare per-replica-safe pool counter deltas and during gauge maxima.
6. Repeat material changes in a representative deployment environment.

## Stop conditions

Stop when:

- saturation aborts;
- sustained error rate exceeds the environment policy;
- p99 exceeds the environment safety threshold;
- acquire wait or canceled acquisitions rise without recovery;
- database CPU, storage, lock pressure, or connection reserves become unsafe;
- unrelated workloads show impact;
- cleanup cannot restore the environment.

## Rollback criteria for future pool changes

A future pool-default PR must preserve a known-good value and roll back for:

- worse p95/p99 without throughput benefit;
- database CPU, query, or lock regression;
- reduced admin/migration/headroom reserve;
- connection exhaustion at maximum replica count;
- increased empty or canceled acquisition deltas;
- readiness or restart amplification;
- worse degradation behavior than the accepted baseline.

Actual production pool sizing remains pending until representative deployment measurements are reviewed. CI smoke and restart regressions alone must never change committed pool defaults.
