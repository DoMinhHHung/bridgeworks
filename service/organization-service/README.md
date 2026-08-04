# Organization Service

Organization Service owns the BridgeWorks projection and authorization boundary for Clerk organizations and memberships.

It does not own user identity, passwords, sessions, talent, jobs, applications, organization verification, billing, invitations, or profile data.

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

The service port is private to the Docker network. APISIX is the only host-published HTTP entry point.

## Ownership boundary

Organization Service owns:

- Clerk organization mapping and lifecycle projection;
- Clerk membership projection;
- BridgeWorks application role assigned to each membership;
- local role-to-permission mapping;
- tenant-scoped `ActorContext` construction;
- organization authorization decisions;
- Clerk organization/membership webhook inbox.

Identity Service remains authoritative for:

- Clerk user mapping;
- local Identity UUID;
- public `id_user`;
- local account status;
- authenticated `/api/v1/me`.

Organization Service never queries the Identity database, imports no Identity `internal` package, and creates no cross-service foreign key.

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

The membership decoder reads only:

- membership ID;
- nested organization ID;
- `public_user_data.user_id`;
- Clerk role.

It does not persist email, name, username, avatar, phone, or metadata.

The initial application-role mapping is:

```text
org:admin -> admin
all other Clerk roles -> viewer
```

After initialization, local `application_role` is authoritative. Clerk webhook updates and JWT role/permission claims do not overwrite it.

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

## Lifecycle semantics

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
3. call Identity `/me` over the private Docker network;
4. require an active local Identity account;
5. resolve the local organization projection and lifecycle status;
6. load the active membership by local organization ID and verified Clerk user ID;
7. load local effective permissions;
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

The CI integration suite runs signed Clerk webhook synchronization, transactional conflict/concurrency regressions, real PostgreSQL constraint behavior, current-organization authorization, CORS/cache/security contracts, dependency outage/recovery, migration idempotency, and private-port isolation.

## Production operations

Cross-service capacity, traffic protection, observability, cache, and retention work is tracked in the [production-readiness roadmap](../../docs/production-readiness-roadmap.md). Rotate the configured Clerk verification key with the [Clerk JWT key-rotation runbook](../../docs/runbooks/clerk-jwt-key-rotation.md).
