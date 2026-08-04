# organization-service

Organization Service owns BridgeWorks organization and membership authorization projections. It is independently deployable, uses its own PostgreSQL schema and never reads or mutates Identity Service tables.

## Ownership

Organization Service owns:

- immutable Clerk organization mapping;
- organization lifecycle projection `pending|active|disabled|deleted`;
- Clerk organization-membership projection;
- BridgeWorks-owned `application_role` on each membership;
- local roles, permissions and effective authorization decisions;
- organization-scoped `ActorContext`;
- transactional Clerk organization webhook inbox.

Identity Service continues to own Clerk user mapping, internal user UUID, public `id_user`, account lifecycle, primary-email projection, user webhooks and authenticated `/api/v1/me`.

Organization Service does not own passwords, sessions, email verification, profile/talent data, jobs, applications, billing or legal organization verification. There is no cross-service foreign key and no import from `service/identity-service/internal/...`.

## Database boundary

The service owns PostgreSQL schema `organization` and Goose table `organization.goose_db_version`.

Tables:

- `organization.organizations`
- `organization.roles`
- `organization.permissions`
- `organization.role_permissions`
- `organization.memberships`
- `organization.clerk_webhook_events`

The runtime role should have only runtime DML privileges. The one-shot migration runner uses `MIGRATION_DATABASE_URL`. Service startup never runs migrations.

Organization IDs and membership IDs are generated in Go with `github.com/google/uuid.NewV7`. Existing IDs never change on update or delete.

## Organization lifecycle

- `pending`: placeholder created when a membership event arrives before the organization event. It is never authorizable.
- `active`: Clerk organization projection has synchronized. This does not mean legally verified.
- `disabled`: BridgeWorks-owned suspension. Clerk updates may refresh name/slug but cannot re-enable it.
- `deleted`: Clerk deletion tombstone. Name/slug are cleared and later Clerk updates cannot restore it.

Legal/company verification is intentionally out of scope.

## Membership and role authority

Memberships are provider projections keyed by immutable Clerk membership ID and preserve tombstones. A deleted membership is never reactivated. Rejoin requires a new Clerk membership ID. A partial unique index allows only one active membership per `(organization_id, clerk_user_id)` while preserving historical tombstones.

`clerk_role` is informational projection only. `application_role` is BridgeWorks authorization authority:

- exact Clerk default admin role `org:admin` initializes local `admin`;
- every other role initializes local `viewer`;
- later membership updates may change `clerk_role` but never overwrite `application_role`.

JWT `org_role` and `org_permissions` claims are not trusted for final authorization.

Seeded local permissions:

```text
admin  → organization.read, organization.manage, membership.read, membership.manage
viewer → organization.read
```

## Clerk webhook synchronization

Public endpoint:

```text
POST /api/v1/organizations/webhooks/clerk
```

Exact supported current Clerk webhook event names:

```text
organization.created
organization.updated
organization.deleted
organizationMembership.created
organizationMembership.updated
organizationMembership.deleted
```

Raw request bytes are read once, bounded, verified by the official Svix library, and only then decoded into a narrow DTO. The service never persists raw payloads, webhook headers, signatures, email, names of users, images or full public-user data.

Each supported event executes in one PostgreSQL transaction:

1. insert inbox row with `ON CONFLICT DO NOTHING`;
2. duplicate delivery commits without taking an advisory lock or mutating data;
3. acquire a transaction-level advisory lock keyed by Clerk organization ID;
4. load the latest event for the incoming aggregate stream;
5. retain stale inbox rows but skip aggregate mutation;
6. apply projection mutation;
7. commit before returning `204`.

Ordering within one aggregate stream is deterministic:

1. newer `occurred_at` wins;
2. equal timestamp: delete > update > create;
3. equal timestamp and type: lexically greater event ID wins.

Organization and membership aggregates have separate ordering streams. A rollback also removes the inbox insert so Clerk retry can process it again.

## Authentication and ActorContext

Authenticated browser flow:

```text
Clerk Bearer token
→ official Clerk Go SDK verification
→ active organization claim selects the tenant projection
→ private Identity Service GET /me using the same Bearer token
→ active local organization
→ active membership scoped by organization ID + verified Clerk user ID
→ local application role and permissions
→ immutable ActorContext
```

The narrow principal contains only Clerk user ID, session ID and active Clerk organization ID. The resulting actor context contains local Identity UUID, local organization UUID, local membership UUID, role and a private permission set.

Organization Service calls `http://identity-service:8080/me` directly on the private Docker network and forwards only `Authorization` and `X-Request-Id`. It does not call APISIX internally, does not retry automatically and bounds response size and time.

## Public API

```text
GET  /api/v1/organizations/health/live
GET  /api/v1/organizations/health/ready
POST /api/v1/organizations/webhooks/clerk
GET  /api/v1/organizations/current
GET  /api/v1/organizations/current/membership
```

Current organization returns only local `id`, nullable `name`, nullable `slug` and active status. Current membership returns only local IDs, local role and lexicographically sorted local permissions. Clerk IDs, Identity UUID, Clerk role and raw claims are never public.

All authenticated responses, including errors, set:

```http
Cache-Control: no-store
Vary: Authorization
```

401 responses also set:

```http
WWW-Authenticate: Bearer realm="bridgeworks"
```

APISIX handles browser CORS using the same `CLERK_AUTHORIZED_PARTIES` allowlist, never wildcard origins and never credentialed CORS.

## Health semantics

Liveness is process-only. Readiness checks Organization PostgreSQL only and intentionally excludes Identity Service.

When Identity Service is unavailable:

- organization liveness remains `200`;
- organization readiness remains `200` while Organization PostgreSQL is healthy;
- organization webhooks remain processable;
- authenticated organization APIs return sanitized `503`.

When Organization PostgreSQL is unavailable:

- liveness remains `200`;
- readiness, webhooks and authenticated organization APIs return `503`;
- Identity Service remains independent.

## Clerk Dashboard setup

1. Create a separate organization webhook endpoint.
2. Set its public URL to `/api/v1/organizations/webhooks/clerk`.
3. Subscribe to the six exact event names listed above.
4. Copy the endpoint signing secret to `CLERK_ORGANIZATION_WEBHOOK_SIGNING_SECRET`.
5. Never commit the real secret.
6. Configure Clerk organization/session tokens to include the active organization context required by the current official SDK setup.
7. Do not place BridgeWorks application permissions exclusively in client-controlled or provider metadata.

Dummy JWT keys and webhook secrets in `.env.example` are local/CI-only and are not production-safe.

## Local workflow

```bash
cp -n .env.example .env
make organization-sqlc-check
make organization-migrate-up
make stack-up
make gateway-smoke
make organization-smoke
```

Migration CLI supports only:

```bash
organization-migrate up
organization-migrate status
organization-migrate version
```

There is no down/reset/redo command exposed by the binary or Makefile.

## Validation

```bash
make repo-check
make identity-sqlc-check
make organization-sqlc-check

cd service/organization-service
unformatted="$(find . -name '*.go' -type f -print0 | xargs -0 -r gofmt -l)"
test -z "${unformatted}"
go mod tidy -diff
go vet ./...
go test -race -coverprofile=coverage.out ./...
golangci-lint run ./...
```

CI preserves all Identity integration scenarios and adds real PostgreSQL, signed organization webhook, tenant-isolation, Identity-dependency, CORS, cache-header and port-isolation scenarios.
