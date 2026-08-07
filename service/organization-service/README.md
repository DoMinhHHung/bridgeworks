# Organization Service

Organization Service owns the BridgeWorks organization product profile and local authorization boundary while projecting Clerk organizations and memberships.

Clerk remains authoritative for provider organization/session/membership identity. Organization Service is authoritative for BridgeWorks-specific organization product fields, verification/trust state, local application roles, local permissions, authorization policy, and the Clerk organization/membership webhook inbox.

It does not own user identity, passwords, sessions, talent, jobs, applications, billing, or Identity Service data. This PR does not introduce organization-create, invitation, role-mutation, verification-command, or platform-admin endpoints.

## Runtime

- Go 1.26.5
- `net/http` and `chi`
- `pgx/v5`
- `sqlc`
- Goose
- `log/slog`
- Clerk Go SDK
- Svix webhook verification
- PostgreSQL schema `organization`

## Public APISIX paths

```text
GET  /api/v1/organizations/health/live
GET  /api/v1/organizations/health/ready
POST /api/v1/organizations/webhooks/clerk
GET  /api/v1/organizations/current
GET  /api/v1/organizations/current/membership
```

The service port is private to the Docker network. APISIX is the only host-published HTTP entry point. PR 1 adds no public route.

## Ownership boundary

Organization Service owns:

- Clerk organization mapping and provider/application lifecycle projection;
- Clerk membership projection;
- BridgeWorks organization product profile: `legal_name`, `website`, `country`, and `company_type`;
- BridgeWorks company `verification_status`;
- BridgeWorks `trust_status`;
- BridgeWorks application role assigned to each membership;
- local role-to-permission mapping;
- tenant-scoped `ActorContext` construction;
- organization authorization decisions;
- Clerk organization/membership webhook inbox.

Identity Service remains authoritative for:

- Clerk user mapping;
- local Identity UUID;
- public `id_user`;
- verified primary-email projection;
- local account status;
- authenticated `/api/v1/me`.

Organization Service never queries the Identity database, imports no Identity `internal` package, and creates no cross-service foreign key.

### Command ownership

The current repository has no Organization-to-Clerk Backend API command adapter. Provider organization/session/membership identity is synchronized into Organization Service through verified Clerk webhooks, and the local service must not create a second uncontrolled source of truth.

PR 1 therefore does not add a backend `create organization` or `invite member` endpoint. The onboarding-command PR must re-inspect current `main` and explicitly choose between:

- Clerk-owned frontend/provider commands followed by webhook reconciliation; or
- a reviewed Organization-owned Clerk Backend API adapter with explicit timeout, idempotency, failure, secret-management, and reconciliation semantics.

No PostgreSQL transaction may be held open around a future Clerk network call.

## Clerk webhook contract

Supported event names are exact:

```text
organization.created
organization.updated
organization.deleted
organizationMembership.created
organizationMembership.updated
organizationMembership.deleted
```

The organization decoder projects only Clerk-owned/provider fields such as organization ID, name, slug, and provider lifecycle. Webhook queries do not write BridgeWorks product fields, verification state, trust state, application roles, or local permissions.

The membership decoder reads only:

- membership ID;
- nested organization ID;
- `public_user_data.user_id`;
- Clerk role.

It does not persist email, name, username, avatar, phone, or metadata.

The initial application-role mapping remains intentionally narrow:

```text
org:admin -> admin
all other Clerk roles -> viewer
```

`owner`, `recruiter`, and `delivery_manager` are local BridgeWorks roles and are never inferred from Clerk role or permission claims. After initialization, local `application_role` is authoritative. Clerk webhook updates and JWT role/permission claims do not overwrite it.

## Organization product domain

The provider/application lifecycle remains separate and unchanged:

```text
pending | active | disabled | deleted
```

BridgeWorks company verification uses a separate lifecycle:

```text
unverified | pending | verified | rejected
```

Allowed verification transitions in the current domain policy are:

```text
unverified -> pending
pending    -> verified
pending    -> rejected
rejected   -> pending
```

Idempotent same-state writes are valid. `verified` has no outgoing transition in PR 1 because the current product contract does not yet define revocation or re-verification semantics. A later command implementation must not invent such a transition silently.

`trust_status` is also separate from provider lifecycle and company verification. The only currently defined trust state is:

```text
unassessed
```

Additional values such as trusted, restricted, or suspended are intentionally not guessed. A reviewed trust/fraud policy must define their meaning and transitions before persistence accepts them.

Existing production rows migrate deterministically to:

```text
verification_status = unverified
trust_status        = unassessed
```

`legal_name`, `website`, `country`, and `company_type` remain nullable for existing rows and reject non-null blank values. PR 1 does not guess URL normalization, ISO country-code policy, or a closed `company_type` taxonomy because those are command-validation/product-contract decisions.

No new indexes are added for these fields because PR 1 introduces no filtering or lookup query that would use them.

## Local roles and permissions

The application role catalog is:

```text
owner
admin
recruiter
delivery_manager
viewer
```

The least-privilege matrix for the currently known capabilities is:

| Permission | owner | admin | recruiter | delivery_manager | viewer |
| --- | --- | --- | --- | --- | --- |
| `organization.read` | yes | yes | yes | yes | yes |
| `organization.manage` | yes | yes | no | no | no |
| `organization.verify.request` | yes | yes | no | no | no |
| `membership.read` | yes | yes | no | no | no |
| `membership.invite` | yes | yes | no | no | no |
| `membership.manage` | yes | yes | no | no | no |
| `membership.role.manage` | yes | yes | no | no | no |

The conservative recruiter and delivery-manager grants are intentional. Their future mutation capabilities should be added only with concrete product use cases and authorization tests rather than inferred from role names.

Existing `admin` and `viewer` memberships are not rewritten. `admin` retains its original permissions and gains the explicit administration permissions required by the expanded catalog; `viewer` retains read-only access. Existing organizations are not backfilled with an `owner`, because selecting an owner from existing memberships would be an unsupported authorization guess.

Owner invariants such as “at least one owner”, self-demotion, last-owner leave/delete, and concurrent owner changes belong to the later role-mutation command boundary and are not pretended to be enforced by the role catalog alone.

## Transaction and ordering model

`organizationsync.Service` owns the complete synchronization use case:

1. begin the unit of work;
2. insert the transactional inbox row;
3. commit duplicates before advisory locking;
4. acquire an organization-scoped transaction advisory lock;
5. load the latest competing event for the exact aggregate;
6. classify stale events by timestamp, delete > update > create, then lexical event ID;
7. decide organization or membership transitions;
8. generate UUIDv7 identifiers;
9. commit only after the projection mutation succeeds.

The PostgreSQL adapter exposes only narrow sqlc-backed persistence operations and transaction control. It does not decide status transitions, event precedence, role initialization, deleted-entity reactivation, or UUID generation.

Membership inserts run inside the transaction savepoint:

```text
SAVEPOINT membership_insert
```

A failed insert is followed by `ROLLBACK TO SAVEPOINT membership_insert` and `RELEASE SAVEPOINT membership_insert` before any follow-up read. Only these exact unique constraints have recoverable conflict logic:

```text
memberships_clerk_membership_id_uq
memberships_active_organization_user_uq
```

Any other `23505` remains an operational error and rolls back the outer transaction and inbox row.

## Provider lifecycle semantics

Organizations use:

```text
pending | active | disabled | deleted
```

- Membership-first delivery creates a `pending` organization placeholder.
- Organization create/update promotes `pending` to `active`.
- Provider updates preserve locally `disabled` organizations.
- Deleted organizations never restore from later create/update events.
- Delete events create tombstones when the projection is absent.

Memberships use:

```text
active | deleted
```

- Deleted membership rows never reactivate.
- Rejoin requires a new Clerk membership ID.
- Only one active membership may exist for an organization/user pair.
- A competing active membership ID is retryable; the losing transaction and inbox insertion roll back.

## Authorization flow

For authenticated current-organization requests:

1. verify the Bearer token with the Clerk SDK;
2. require the verified active organization claim;
3. call Identity `/me` over the private service network;
4. require an active local Identity account;
5. resolve the local organization projection and provider lifecycle status;
6. load the active membership by local organization ID and verified Clerk user ID;
7. load local effective permissions from `application_role`;
8. construct an immutable `ActorContext`;
9. authorize the endpoint.

The Identity call occurs outside Organization PostgreSQL transactions. Identity outage does not fail Organization readiness and does not stop webhook processing.

Authenticated responses use:

```http
Cache-Control: no-store
Vary: Authorization
```

Unauthorized responses use:

```http
WWW-Authenticate: Bearer realm="bridgeworks"
```

## Platform-admin prerequisite

Manual company verification requires platform-level administrator authority. No reusable platform-admin authentication/authorization contract was identified in the current files and public contracts inspected for this work.

PR 1 therefore persists only the verification domain model and exposes no operator mutation endpoint. A future manual-verification PR must first establish or reuse a reviewed platform-admin boundary; it must not use an environment email allowlist, hidden header, unauthenticated endpoint, or client-supplied role.

## Local commands

From the repository root:

```bash
cp .env.example .env
make organization-sqlc-check
make stack-up
make organization-smoke
```

Module validation:

```bash
cd service/organization-service
unformatted="$(find . -name '*.go' -type f -print0 | xargs -0 -r gofmt -l)"
test -z "${unformatted}"
go mod tidy -diff
go vet ./...
go test -race -coverprofile=coverage.out ./...
golangci-lint run ./...
go tool cover -func=coverage.out
```

The CI integration suite runs signed Clerk webhook synchronization, transactional conflict/concurrency regressions, the real PostgreSQL v1-to-v2 domain migration regression, current-organization authorization, CORS/cache/security contracts, dependency outage/recovery, migration idempotency, and private-port isolation.

## Explicit non-goals for PR 1

This PR does not add Redis, cache implementation, OpenTelemetry, `pg_cron`, a retention goroutine, an APISIX rate-limit plugin, organization-create commands, business-email verification commands, manual-verification commands, invitation commands, role-mutation APIs, trust mutation, a platform-admin endpoint, RabbitMQ, an outbox, an audit framework, or new public endpoints.

## Production operations

Cross-service capacity, traffic protection, observability, cache, and retention work is tracked in the [production-readiness roadmap](../../docs/production-readiness-roadmap.md). Rotate the configured Clerk verification key with the [Clerk JWT key-rotation runbook](../../docs/runbooks/clerk-jwt-key-rotation.md).

## Private metrics and access logs

Organization Service starts a second operational HTTP listener configured by:

```dotenv
ORGANIZATION_METRICS_ADDR=:9090
```

The address must be a non-empty `host:port`, must not include a URL scheme, and must differ from `HTTP_ADDR`. Bind failure stops startup. The listener exposes only `GET /metrics`, is gracefully shut down, is private to the service network, and is not routed through APISIX. Prometheus absence or scrape failure does not affect `/health/ready`, webhook synchronization, or authenticated requests.

Organization metrics use a service-owned Prometheus registry and bounded labels:

```text
http_requests_total{service,route,method,status_class}
http_request_duration_seconds{service,route,method}
http_requests_in_flight{service}
clerk_webhook_events_total{service,aggregate,outcome}
database_pool_*{service,pool="runtime"}
```

Webhook aggregate values are `organization` and `membership`. Outcomes are `processed`, `duplicate`, `stale`, `rejected`, and `retryable_failure`. Processed, duplicate, and stale outcomes come from the application-owned Unit of Work after its commit decision; the HTTP layer does not infer persistence semantics from status codes.

Every completed application request emits one structured access record containing `service`, `request_id`, `method`, matched chi `route`, `status`, `duration_ms`, and `response_bytes`. Unmatched routes use `unknown`; raw URL paths and query strings are never fallback labels or log fields. Health completion records are written only at debug level; with the normal info threshold they are intentionally absent.

Access logs and metrics exclude Authorization, Cookie, JWTs, webhook bodies, Svix headers, email addresses, Clerk user, organization, or membership IDs, local UUIDs, database URLs, request or response bodies, request IDs as metric labels, and raw dependency errors.

Pool defaults remain unchanged until measured load tests establish throughput, latency, replica count, connection wait, and the total Organization PostgreSQL connection budget. OpenTelemetry tracing, APISIX rate limiting, caching, and inbox retention remain separate work.

### Telemetry guarantees

The production Clerk webhook route depends on `ProcessWithResult(context.Context, organizationsync.Event) (organizationsync.Result, error)` at compile time. There is no runtime type assertion, fallback to `Process`, or default `processed` outcome. Organization webhook metrics accept only `aggregate=organization|membership`.

HTTP metric method labels use the bounded values `GET|POST|PUT|PATCH|DELETE|HEAD|OPTIONS|CONNECT|TRACE|OTHER`; arbitrary methods map to `OTHER`. Access logs retain the actual request method. Pool metrics are collected from `pgxpool.Stat()` at scrape time without database queries or silent panic recovery. Shutdown stops metrics serving before closing PostgreSQL. Health completion records are debug-only and are absent at the normal info log threshold.
