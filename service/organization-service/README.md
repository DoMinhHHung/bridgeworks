# Organization Service

Organization Service owns the BridgeWorks organization product profile and local authorization boundary while projecting Clerk organizations and memberships.

Clerk remains authoritative for provider organization/session/membership identity. Organization Service is authoritative for BridgeWorks-specific product fields, verification/trust state, local application roles, local permissions, authorization policy, and the Clerk organization/membership webhook inbox.

It does not own user identity, passwords, sessions, talent, jobs, applications, billing, or Identity Service data.

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
GET   /api/v1/organizations/health/live
GET   /api/v1/organizations/health/ready
POST  /api/v1/organizations/webhooks/clerk
GET   /api/v1/organizations/current
PATCH /api/v1/organizations/current
POST  /api/v1/organizations/current/verification
GET   /api/v1/organizations/current/membership
```

The service port is private. APISIX is the public edge. The production Cloud Run route keeps Google platform authentication in `X-Serverless-Authorization` and preserves the caller's Clerk Bearer token in `Authorization`.

There is intentionally no `POST /api/v1/organizations` endpoint.

## Creation and command ownership

Organization creation is provider-owned by Clerk; Organization Service projects the verified organization through Clerk webhooks and owns subsequent BridgeWorks onboarding state.

The product flow is:

```text
Frontend / Clerk
    -> create Clerk organization
    -> Clerk emits signed organization + membership events
    -> Organization Service transactionally projects provider state
    -> authenticated BridgeWorks onboarding commands mutate local product fields
```

This keeps one source of truth for provider organization identity. Organization Service does not call Clerk Backend API to duplicate creation, stores no Clerk Backend secret, and never opens a PostgreSQL transaction around a provider network call.

Clerk webhooks are asynchronous. A valid session can therefore contain an active Clerk organization before its local projection arrives. In that state authenticated APIs return:

```http
409 Conflict
Retry-After: 2
```

with code `organization_not_ready`. The service never creates a local organization from unverified client input to hide webhook latency.

## Ownership boundary

Clerk owns:

- provider organization identity and Clerk organization ID;
- provider organization name and slug;
- organization creator identity exposed by the verified provider organization resource;
- active organization session context;
- provider memberships and Clerk membership role.

Organization Service owns:

- Clerk mapping/projection and provider/application lifecycle;
- BridgeWorks product profile: `legal_name`, `website`, `country`, `company_type`;
- BridgeWorks `verification_status`;
- BridgeWorks `trust_status`;
- local `application_role` and permissions;
- tenant-scoped authorization policy;
- private initial-owner bootstrap eligibility/completion state;
- webhook inbox, ordering, and reconciliation.

Identity Service remains authoritative for Clerk-user mapping, local Identity UUID, public `id_user`, verified primary-email projection, local account lifecycle, and `/api/v1/me`. Organization Service never queries Identity PostgreSQL and creates no cross-service foreign key.

## Initial owner bootstrap

The role catalog includes `owner`, but Clerk role claims are not used as BridgeWorks owner authority.

Migration v3 distinguishes organizations that existed before this owner-bootstrap feature from organizations projected afterwards:

```text
legacy row existing at v3 migration:
owner_bootstrap_eligible = false

row inserted after v3:
owner_bootstrap_eligible = true
```

The migration first adds `owner_bootstrap_eligible boolean not null default false`, so every pre-existing row is materialized as ineligible, and only then changes the column default to `true` for future inserts. The real PostgreSQL migration regression verifies both sides of that default transition.

For an eligible newly projected provider organization, Organization Service consumes `created_by` only from the **verified Clerk organization webhook payload** and stores it as private provider metadata. Owner bootstrap completes only when every gate is true:

```text
owner_bootstrap_eligible = true
owner_bootstrapped = false
organization lifecycle = active
authoritative created_by exists
exact creator has an active local membership projection
```

Then and only then:

```text
local application_role = owner
owner_bootstrapped = true
```

Organization and membership webhook delivery can race in either order. The existing organization-scoped advisory lock serializes both paths. If the organization event arrives first, the creator signal waits for the membership. If the membership arrives first, it creates an eligible pending local organization and initially uses the compatibility role mapping; the later verified organization projection activates the organization and promotes only the exact creator.

An inactive/deleted creator membership does not complete owner bootstrap. Disabled or deleted organizations do not complete owner bootstrap either.

The bootstrap marker is a one-time fence after a successful bootstrap. If a future explicit BridgeWorks role change moves that membership away from `owner`, later provider organization events do not re-grant owner.

Legacy organizations are permanently ineligible for this automatic bootstrap path. A later verified Clerk organization payload may safely project its historical `created_by` metadata, but it **must not** promote that historical creator from local `viewer` or `admin` to `owner`. Existing legacy `admin` and `viewer` application roles remain unchanged. `admin` keeps its compatibility administration permissions so legacy organizations are not locked out, but `admin` is not semantically treated as `owner`.

Explicit ownership assignment for legacy organizations is deferred to a future reviewed authorization flow; PR 2 does not guess it from provider history.

Clients cannot supply `role=owner`, `created_by`, `owner_bootstrap_eligible`, or the bootstrap marker.

## Clerk webhook contract

Supported event names remain exact:

```text
organization.created
organization.updated
organization.deleted
organizationMembership.created
organizationMembership.updated
organizationMembership.deleted
```

Signature verification occurs on the raw request body before decoding. Supported events enter the existing transactional inbox, duplicate/stale ordering, transaction advisory lock, UUIDv7 projection, and membership savepoint/conflict logic.

Provider organization events mutate only provider-owned projection fields:

```text
name
slug
provider/application lifecycle status
```

The verified provider creator signal is private bootstrap metadata. Webhook updates do **not** mutate:

```text
legal_name
website
country
company_type
verification_status
trust_status
```

and do not generally derive `application_role` from Clerk role claims. The only application-role mutation performed by synchronization is the one-time exact-creator bootstrap for eligible post-v3 organizations described above.

The compatibility initialization remains:

```text
org:admin -> admin
all other Clerk roles -> viewer
```

After initialization, local `application_role` remains the authorization authority. Clerk JWT organization role/permission claims do not override it.

## Product profile onboarding command

`PATCH /api/v1/organizations/current` requires local `organization.manage` and may include only:

```json
{
  "legal_name": "BridgeWorks Company Limited",
  "website": "https://example.com",
  "country": "VN",
  "company_type": "software-agency"
}
```

Fields are partial: omitted means preserve; explicit `null` clears the optional value.

The command cannot mutate local/provider IDs, `name`, `slug`, provider lifecycle status, verification/trust state, application role, or permissions. Tenant authority always comes from the verified Clerk active-organization session plus local Identity/membership/permission resolution; request bodies contain no organization identifier used for authorization.

Validation policy:

- `legal_name`: trim whitespace, non-null values must be nonblank, maximum 200 Unicode code points;
- `website`: maximum 2048 bytes, absolute HTTP/HTTPS URL, credentials rejected, fragments rejected, scheme/host normalized to lowercase, default `:80`/`:443` removed;
- `country`: ISO 3166-1 alpha-2, normalized uppercase;
- `company_type`: taxonomy-neutral bounded identifier, maximum 64 Unicode code points, normalized lowercase, ASCII alphanumeric plus internal `_` or `-` only.

`company_type` is intentionally not a large closed enum because no authoritative product taxonomy exists yet. The bounded identifier prevents unlimited free text without freezing an invented taxonomy.

Unknown JSON fields are rejected. Command bodies are bounded to 16 KiB. Equivalent normalized retries are idempotent and do not perform another row update.

## Verification request boundary

`POST /api/v1/organizations/current/verification` is the customer-side request boundary only. It requires local `organization.verify.request` and accepts no target state.

Allowed behavior:

```text
unverified -> pending
rejected   -> pending
pending    -> pending   # idempotent success
```

A verified organization cannot request another transition under the current policy and receives a stable conflict. This endpoint never performs:

```text
pending -> verified
pending -> rejected
```

The same endpoint can return `409 organization_not_ready` during Clerk/local projection lag. That conflict includes `Retry-After: 2`; `verification_transition_not_allowed` does not emit `Retry-After`.

Manual approval/rejection remains blocked on a separate platform-admin authorization prerequisite.

## Concurrency and transaction boundary

Authenticated command flow is deliberately split:

1. verify Clerk JWT and active organization context;
2. call private Identity `/me` and resolve active local membership/permissions;
3. only then begin a short Organization PostgreSQL transaction;
4. `SELECT ... FOR UPDATE` the local organization row;
5. re-check provider lifecycle and current BridgeWorks state;
6. apply the local mutation;
7. commit.

No Identity or Clerk network call occurs inside the PostgreSQL transaction.

The row lock prevents lost product-profile updates and makes verification transition checks atomic. PATCH merges omitted fields against the freshly locked row. Verification never performs an unlocked read followed by a later state update.

Webhook reconciliation and command mutation can run concurrently. PostgreSQL row locking serializes writes to the same organization row, while webhook projection SQL remains restricted to provider-owned columns. Therefore a later `organization.updated` event can change provider name/slug without overwriting BridgeWorks product fields, verification state, trust state, or local application role.

## Stable onboarding read model

No extra `onboarding_status` column is introduced. The existing authenticated read model is sufficient and avoids a second derived state machine:

- `GET /api/v1/organizations/current` returns product fields plus `verification_status` and `trust_status`;
- `GET /api/v1/organizations/current/membership` returns local application role and permissions;
- `organization_not_ready` represents provider/local synchronization lag.

UI onboarding progress can be derived from these authoritative fields without persisting another independently mutable status.

## Organization product domain

Provider/application lifecycle remains:

```text
pending | active | disabled | deleted
```

BridgeWorks verification remains separate:

```text
unverified | pending | verified | rejected
```

The complete domain policy still permits `pending -> verified|rejected`, but PR 2 exposes only the customer-side request transitions. Operator transitions need a reviewed platform-admin boundary.

`trust_status` remains independent and currently supports only:

```text
unassessed
```

PR 2 exposes no trust mutation.

## Local roles and permissions

Roles:

```text
owner
admin
recruiter
delivery_manager
viewer
```

| Permission | owner | admin | recruiter | delivery_manager | viewer |
| --- | --- | --- | --- | --- | --- |
| `organization.read` | yes | yes | yes | yes | yes |
| `organization.manage` | yes | yes | no | no | no |
| `organization.verify.request` | yes | yes | no | no | no |
| `membership.read` | yes | yes | no | no | no |
| `membership.invite` | yes | yes | no | no | no |
| `membership.manage` | yes | yes | no | no | no |
| `membership.role.manage` | yes | yes | no | no | no |

PR 2 does not implement general role mutation, last-owner/self-demotion/leave rules, or invitations.

## Synchronization transaction and ordering model

`organizationsync.Service` preserves the established use case:

1. begin transaction;
2. insert transactional inbox row;
3. commit duplicates before advisory locking;
4. acquire organization-scoped transaction advisory lock;
5. load latest competing event for the exact aggregate;
6. classify stale events by timestamp, delete > update > create, then lexical event ID;
7. apply provider projection and, for eligible post-v3 organizations only, one-time exact-creator owner bootstrap;
8. commit only after all projection work succeeds.

Membership inserts retain the `SAVEPOINT membership_insert` boundary. Only `memberships_clerk_membership_id_uq` and `memberships_active_organization_user_uq` have explicit conflict recovery; unrelated unique violations still roll back the transaction and inbox insertion.

## Authorization flow

For authenticated APIs:

1. verify Bearer token with Clerk SDK;
2. require verified active organization claim;
3. call Identity `/me` over the private service network;
4. require active local Identity account;
5. resolve local organization lifecycle;
6. resolve active membership using local organization ID + verified Clerk user ID;
7. load permissions from local `application_role`;
8. construct immutable `ActorContext`;
9. authorize the endpoint.

The Identity call uses the Clerk token in `Authorization`. In Cloud Run, Organization separately obtains a Google ID token and sends it through `X-Serverless-Authorization`. No service-account JSON key is used.

Authenticated responses retain:

```http
Cache-Control: no-store
Vary: Authorization
```

Gateway CORS may append `Origin`. Request IDs are generated/preserved through `X-Request-Id`.

## Platform-admin prerequisite

Manual company verification still requires platform-level administrator authority. No reusable platform-admin authentication/authorization contract exists in current `main`.

PR 2 therefore implements only customer verification requests. A later PR must first establish/reuse a reviewed platform-admin boundary and must not use an environment email allowlist, hidden header, client-supplied role, or unauthenticated operator endpoint.

Business-email verification also remains deferred until an authoritative verified-email signal is available through an approved Identity/Clerk public contract. Organization Service must not query Identity tables directly.

## Local commands

From repository root:

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

Organization CI also runs real PostgreSQL migration upgrades, including v2 legacy admin/viewer rows and the post-v3 eligibility default; a signed Clerk regression against that same migrated legacy row; both organization-first and membership-first owner-bootstrap orderings; lifecycle/inactive-membership/one-time-fence checks; APISIX onboarding ownership regression; duplicate/stale handling; membership conflict/concurrency/savepoint regressions; projection-ownership checks; unrelated-unique rollback; dependency outage/recovery; and private-port isolation.

## PR 2 non-goals

Not implemented here:

- Organization-to-Clerk Backend API creation;
- platform-admin authentication;
- manual company approval/rejection;
- business-email verification;
- invitation lifecycle;
- general member role mutation;
- last-owner mutation rules;
- audit framework;
- trust-status mutation;
- Talent/Profile/Passport/Hiring Intent;
- frontend changes;
- production deployment.

## Production operations

Cross-service production work remains tracked in the [production-readiness roadmap](../../docs/production-readiness-roadmap.md). Rotate Clerk verification keys with the [Clerk JWT key-rotation runbook](../../docs/runbooks/clerk-jwt-key-rotation.md).

Organization Service starts a private metrics listener configured by `ORGANIZATION_METRICS_ADDR`. It exposes only `GET /metrics`, is not routed through APISIX, and is independent of readiness.

Bounded metrics remain:

```text
http_requests_total{service,route,method,status_class}
http_request_duration_seconds{service,route,method}
http_requests_in_flight{service}
clerk_webhook_events_total{service,aggregate,outcome}
database_pool_*{service,pool="runtime"}
```

HTTP method labels are bounded to `GET|POST|PUT|PATCH|DELETE|HEAD|OPTIONS|CONNECT|TRACE|OTHER`; arbitrary methods map to `OTHER`. Access logs keep the actual method but exclude raw paths/query strings and sensitive values.

Access logs and metrics exclude Authorization, cookies, JWTs, webhook bodies, Svix headers, email addresses, Clerk user/organization/membership IDs, local UUIDs, database URLs, request/response bodies, secrets, and raw dependency errors. Metrics stay low-cardinality.
