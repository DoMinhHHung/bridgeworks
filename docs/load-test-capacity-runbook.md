# Load Test and Database Capacity Runbook

## Purpose and non-goals

This runbook defines a repeatable harness for measuring BridgeWorks Identity and Organization traffic against the private telemetry added in PR #8. It produces comparable evidence for future PostgreSQL pool decisions; it does **not** claim that GitHub-hosted runner throughput equals production capacity and it does not change pool defaults.

Production binaries have no k6 dependency. The harness uses pinned container images:

```text
grafana/k6:2.0.0
python:3.13.5-alpine3.22   # CI-only delayed dependency proxy
```

Generated RSA keys, JWTs, Svix secrets, Clerk fixture identifiers, webhook bodies, emails, and database URLs remain in an ephemeral temporary directory. They are never uploaded. Retained artifacts are sanitized JSON and Markdown only.

## Traffic scenarios

Authenticated reads are isolated from webhook traffic:

| Scenario | Traffic path | What it exercises |
|---|---|---|
| `identity_me` | APISIX → Identity `/me` | Clerk JWT verification and Identity PostgreSQL projection read |
| `organization_current` | APISIX → Organization current organization | Organization auth, private Identity resolution, organization and membership reads |
| `organization_membership` | APISIX → Organization current membership | Same private dependency with membership response mapping |
| `organization_dependency` | APISIX → Organization → Identity | Explicit dependency-degradation target |
| `identity_user_unique` | Clerk user webhook | Unique transactional inbox and tombstone mutations |
| `identity_user_retry` | Clerk user webhook | Same event ID/payload retries and inbox deduplication |
| `organization_unique` / `organization_retry` | Clerk organization webhook | Organization projection mutation and duplicate handling |
| `membership_unique` / `membership_retry` | Clerk membership webhook | Membership tombstone mutation and duplicate handling |

Do not combine authenticated reads and webhook writes into one aggregate result. Each artifact names exactly one scenario and one profile.

## Profiles

### Smoke

Runs on every pull request through `.github/workflows/load-test-ci.yml`. It starts one isolated Compose stack, executes each scenario sequentially at two virtual users for a few seconds, verifies result sanitization, and uploads artifacts. Thresholds are intentionally loose and correctness-oriented:

- checks above 90%;
- request failure rate below 10%;
- p99 below 5 seconds.

A smoke result is proof that fixtures, routing, telemetry sampling, reporting, and cleanup work. It is not a performance gate for production.

```bash
make loadtest-smoke
```

### Baseline

Manual steady-state profile using constant arrival rate. Run it in a stable, representative environment when comparing commits.

```bash
bash loadtest/scripts/run.sh baseline identity_me none
bash loadtest/scripts/run.sh baseline organization_current none
```

Default duration is two minutes. Override load shape only through explicit environment variables such as `LOAD_RATE`, `LOAD_DURATION`, `LOAD_PREALLOCATED_VUS`, and `LOAD_MAX_VUS`, and record those values with the test evidence.

### Burst

Manual sudden-concurrency profile. Run authenticated-read and webhook scenarios separately:

```bash
bash loadtest/scripts/run.sh burst organization_current none
bash loadtest/scripts/run.sh burst identity_user_unique none
```

The default shape ramps from two to thirty VUs, holds, then recovers. Do not merge the two outputs into one latency number.

### Saturation

Manual only. It gradually increases arrival rate and aborts when checks, error rate, or p99 crosses the documented threshold:

```bash
bash loadtest/scripts/run.sh saturation identity_me none
```

Default abort conditions are checks below 95%, errors at or above 5%, or p99 at or above five seconds after the initial evaluation delay. Stop immediately if database health, runner stability, or a shared environment becomes unsafe. Never schedule saturation on every pull request.

### Dependency degradation

Manual only. Supported modes:

```bash
# Organization private Identity dependency unavailable, then recovered.
bash loadtest/scripts/run.sh dependency-degradation organization_dependency identity-unavailable

# Organization private Identity dependency delayed by a CI-only proxy.
bash loadtest/scripts/run.sh dependency-degradation organization_dependency identity-delayed

# Both service pools temporarily constrained to max=1; artifact marks experimental override.
bash loadtest/scripts/run.sh dependency-degradation organization_current constrained-pool

# Restart the service owning the selected scenario during traffic.
bash loadtest/scripts/run.sh dependency-degradation identity_me replica-restart
```

Temporary pool overrides apply only to the generated Compose environment file. They never modify `DATABASE_MAX_CONNS`, `DATABASE_MIN_CONNS`, `ORGANIZATION_DATABASE_MAX_CONNS`, or `ORGANIZATION_DATABASE_MIN_CONNS` defaults in the repository.

## Result artifacts

Each scenario produces:

```text
loadtest-results/<profile>/<scenario>/result.json
loadtest-results/<profile>/<scenario>/result.md
```

Fields include:

- tested git SHA;
- scenario and load profile;
- service replica counts;
- effective database pool min/max values and whether they are experimental;
- duration, request count, throughput, p50/p95/p99/max latency;
- error rate, checks rate, dropped iterations, and bounded status distribution;
- Identity and Organization pool/in-flight telemetry before, during, and after;
- threshold outcomes and environment limitations.

During-phase gauges use the maximum summed value across sampled replicas. Cumulative counters are the maximum observed cumulative values; calculate run deltas from before and after snapshots. Artifacts exclude JWTs, Clerk/user/organization/membership identifiers, email addresses, webhook bodies, Svix secrets, and database URLs.

## Interpreting PostgreSQL pool telemetry

Use the metrics together rather than reading `database_pool_total_connections` alone:

- high acquired with low idle indicates active pool use;
- acquired repeatedly reaching max indicates no remaining pool headroom;
- increasing acquire duration shows callers waiting for a connection;
- increasing empty acquire count confirms acquisitions encountered an empty pool;
- canceled acquire count shows callers gave up while waiting;
- HTTP in-flight plus p95/p99 distinguishes queue pressure from isolated slow requests;
- compare before/after counter deltas, not raw cumulative values across unrelated process lifetimes.

A pool candidate is not justified merely because all configured connections were used. Verify database CPU, query latency, lock behavior, Supabase limits, replica count, and operational reserves.

## Deterministic connection budget

Run the calculator with the actual PostgreSQL/Supabase limit and explicit reservations:

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

Identity and Organization allocations are separate. The total allocation must fit after admin, migration, and operational headroom reservations. A computed value is a hard planning ceiling, not an instruction to set the pool to that value.

## Comparing two commits

1. Use the same deployment shape, profile, scenario, replica count, pool configuration, load environment, and k6 variables.
2. Download the two workflow artifacts.
3. Compare `result.json` fields: throughput, error rate, p50/p95/p99, status distribution, before/after pool-counter deltas, and during maxima.
4. Treat shared-runner variance as noise; repeat a material change in a representative deployment environment.
5. Record the commit SHA and artifact IDs in the pool-change proposal.

A simple local comparison can use:

```bash
python3 -m json.tool before/result.json >/tmp/before.pretty.json
python3 -m json.tool after/result.json  >/tmp/after.pretty.json
diff -u /tmp/before.pretty.json /tmp/after.pretty.json
```

## Stop conditions

Stop or abort a run when any of these occurs:

- the saturation threshold aborts;
- sustained 5xx/error rate exceeds the profile policy;
- p99 exceeds the environment’s safety threshold;
- acquire wait/canceled acquisitions rise without recovery;
- PostgreSQL health, CPU, storage, lock pressure, or connection reserves become unsafe;
- unrelated tenants or workloads share the environment and show impact;
- cleanup cannot restore all service replicas and dependencies.

## Rollback criteria for future pool changes

A future pool-default PR must define a previous known-good value and roll back when representative measurements show any of:

- increased p95/p99 without throughput benefit;
- database CPU, lock, or query latency regression;
- reduced admin/migration/headroom reserve;
- connection exhaustion under maximum replicas;
- increased canceled or empty acquisition count;
- readiness instability or restart amplification;
- degradation behavior worse than the baseline artifact.

Production pool sizing remains pending until these tests run in a representative deployment environment. CI smoke results alone must never change committed defaults.
