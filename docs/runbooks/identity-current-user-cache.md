# Identity Current-User Cache Runbook

## Scope

Identity uses Redis only as a cache-aside optimization for authenticated `GET /api/v1/me`. PostgreSQL `app.app_users` remains the source of truth. Redis does not own account lifecycle, authorization, Clerk identity, sessions, tokens, webhook ordering, or readiness.

Production uses an externally provisioned Upstash Redis database. The Compose `identity-redis` service is local development and CI infrastructure only; it is not a production topology.

## Read path

```text
Clerk-authenticated request
  -> currentuser.Service
  -> currentuser.CachedReader
  -> Redis GET
       hit: validate cached projection
       miss/error/corruption: PostgreSQL Reader
  -> currentuser.Service applies active|disabled|deleted policy
```

A valid warm cache entry can serve `/me` while PostgreSQL is unavailable. PostgreSQL readiness remains failed in that condition. A cold cache with PostgreSQL unavailable preserves the existing sanitized `503 service_unavailable` response.

Browser and proxy caching remain disabled:

```text
Cache-Control: no-store
Vary: Authorization
```

## Key and value privacy

Key format:

```text
bridgeworks:identity:current-user:v1:<64 lowercase SHA-256 hex characters>
```

The digest input is the Clerk user ID. The raw Clerk user ID, email, local UUID, token subject, session ID, and JWT never appear in the key.

Value format is strict deterministic JSON with `schema_version=1` and only:

```text
id
id_user
primary_email
status
created_at
updated_at
```

The value does not contain the Clerk user ID, JWT, session, webhook fields, Redis credentials, or provider payload. Decode validates UUIDv7, public ID format, lifecycle status, timestamps, schema version, unknown fields, trailing data, and size. Corrupt values are deleted best-effort and PostgreSQL is used.

Missing PostgreSQL projections and operational errors are never negative-cached.

## Configuration

| Variable | Default | Validation |
|---|---:|---|
| `REDIS_ADDR` | required | `host:port`, no scheme or credentials |
| `REDIS_USERNAME` | `default` | non-empty |
| `REDIS_PASSWORD` | required | non-empty; never logged |
| `REDIS_TLS_ENABLED` | `true` | boolean |
| `REDIS_DIAL_TIMEOUT` | `500ms` | greater than zero, maximum `5s` |
| `REDIS_OPERATION_TIMEOUT` | `150ms` | greater than zero, maximum `1s` |
| `CURRENT_USER_CACHE_TTL` | `30s` | greater than zero, maximum `5m` |

Upstash production traffic must use TLS. Keep `REDIS_TLS_ENABLED=true`, use the Upstash native Redis host and port, and provide credentials through the deployment secret manager. Do not commit an Upstash URL or password. Local Compose explicitly disables TLS and uses a non-production placeholder password on the private Docker network.

The service does not `PING` Redis during startup. Client creation performs no network I/O. Redis availability is not part of `/health/ready`.

## Failure matrix

| Redis | PostgreSQL | Cache | Behavior |
|---|---|---|---|
| up | up | hit | existing `/me` result |
| up | up | miss | PostgreSQL, then best-effort `SET` |
| down/timeout | up | any | PostgreSQL fallback |
| corrupt value | up | invalid | best-effort `DEL`, then PostgreSQL |
| up | down | valid warm hit | cached lifecycle projection |
| up | down | miss | existing sanitized `503` |
| down | down | unavailable | existing sanitized `503` |

A Redis `SET` failure never changes a successful PostgreSQL result. Redis errors, addresses, cache keys, and credentials are not returned or logged.

## Stampede behavior

Identical misses are coalesced per Identity process. One bounded shared PostgreSQL load runs for an in-flight key; waiting callers receive the same safe result. A canceled waiting caller does not cancel the shared load for other callers. Different keys are independent, and completed in-flight entries are removed.

This is not a distributed lock. Multiple Identity replicas may load the same cold key concurrently. Redis remains the shared cache across replicas.

## Post-commit invalidation

The production Clerk user-sync processor is decorated at the application boundary:

```text
usersync.Service transaction
  -> commit
  -> shared Redis DEL
```

Successful `processed`, `duplicate`, and `stale` outcomes all perform idempotent invalidation. Duplicate and stale deliveries intentionally self-heal a prior best-effort invalidation failure. Transaction errors and rollbacks do not invalidate.

Redis invalidation occurs outside the PostgreSQL transaction. If `DEL` fails after commit:

- the committed result remains successful;
- the webhook response is not converted into a retryable database error;
- one bounded `delete,error` metric and a sanitized warning are emitted;
- TTL bounds the residual stale window.

With the default TTL, an invalidation failure can leave an old projection for up to 30 seconds. The configured hard maximum is five minutes. During that interval an old active projection may be served, including during a concurrent PostgreSQL outage. Do not claim zero-staleness under partial Redis failure.

All lifecycle mutations must continue through the owned user-sync path. Manual database edits bypass post-commit invalidation and rely on TTL or an explicit operational cache flush.

## Multi-instance behavior

All Identity replicas use the same namespace and Upstash database. A cache fill by one replica is visible to another. A committed webhook processed by any replica deletes the same shared key. Per-process miss coalescing is local to each replica.

## Metrics and logs

Private Identity metrics expose:

```text
current_user_cache_operations_total{service,operation,outcome}
```

Allowed operations are `get`, `set`, and `delete`. Allowed outcomes are bounded to `hit`, `miss`, `success`, `error`, and `invalid` where applicable. No key, user ID, email, address, request ID, error string, or host is a label.

Invalidation warnings contain only bounded event type, committed outcome, and `reason=cache_delete_failed`. They never include the Redis error or Clerk user ID. Metrics remain private on the existing `:9090` listener; APISIX exposes no Redis or metrics route.

## Local operation

```bash
cp .env.example .env
docker compose up -d --build
make identity-cache-integration
```

`identity-redis`:

- is pinned to `redis:8.2.8-alpine3.22`;
- exposes port `6379` only on the private Compose network;
- has no host binding and no APISIX route;
- uses `64mb` maximum memory and `allkeys-lru`;
- disables persistence because it is a disposable cache;
- is not an Identity startup or readiness dependency.

## Rollback

1. Deploy the previous Identity image that reads PostgreSQL directly.
2. Keep PostgreSQL unchanged; there is no cache migration or database rollback.
3. Remove Redis environment variables from the Identity deployment only after the previous image is active.
4. Delete the `bridgeworks:identity:current-user:v1:*` namespace or let entries expire.
5. Confirm `/health/ready`, authenticated `/me`, Clerk webhook processing, Organization private Identity resolution, and private metrics.
6. Retain Upstash until rollback verification is complete, then deprovision through infrastructure ownership if no other consumer exists.

Rollback does not require APISIX, Clerk, migration, public API, or PostgreSQL pool changes.
