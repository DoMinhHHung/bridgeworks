# organization-service

Organization Service owns BridgeWorks organization product state and tenant authorization. Clerk remains provider authority for authentication/session, provider organizations, provider memberships, provider invitations, and active Clerk organization context. Identity Service remains authority for local BridgeWorks user lifecycle, verified primary-email projection, and global BridgeWorks platform access.

PR4 completes the Organization MVP source boundary for manual company verification, immutable product/security audit, invitation-intent retention, and stuck removal reconciliation. It does **not** deploy production or create production credentials.

## Authority model

Clerk owns:

- authentication and session validity;
- provider organization identity/lifecycle;
- provider membership identity/lifecycle;
- provider invitations;
- active Clerk organization context.

Identity Service owns:

- local BridgeWorks Identity UUID and user lifecycle (`active|disabled|deleted`);
- verified primary-email projection;
- global `platform_admin` assignment;
- global permission `organization.verification.review`;
- private `/internal/v1/platform-access/me` contract.

Organization Service owns:

- local public Organization UUID;
- product profile (`legal_name`, `website`, `country`, `company_type`);
- local organization lifecycle projection;
- `verification_status` and `trust_status`;
- point-in-time business-email proof;
- local membership `application_role` and tenant permissions;
- owner invariants and ownership transfer;
- invitation/removal intents;
- Organization-owned append-only audit history.

Organization never reads Identity PostgreSQL and never copies Identity platform-access rows locally. Cross-service Identity data is consumed only through private HTTP contracts.

## Tenant roles are not platform roles

Tenant roles are exactly:

```text
owner
admin
recruiter
delivery_manager
viewer
```

They are scoped to one Organization. `owner`, `admin`, Clerk `org:admin`, Clerk permissions, email/domain, JWT custom claims, metadata, frontend state, and headers such as `X-Platform-Admin` never imply `platform_admin` or `organization.verification.review`.

## Private Organization -> Identity transport

Organization reuses the existing private Identity dependency:

```text
Authorization: Bearer <same Clerk session JWT>
X-Serverless-Authorization: Bearer <Google Cloud Run service identity token>
X-Request-Id: <propagated request ID>
```

Local Docker/CI can use private network transport without Google identity. Cloud Run production must preserve service-to-service authentication and private Identity ingress.

Identity/platform-access timeout, 5xx, malformed response, invalid local user state, unknown role/permission, or platform-token failure fails closed. No tenant-role fallback exists.

## Manual company verification

Customer request route:

```http
POST /api/v1/organizations/current/verification
```

Customer-side lifecycle remains:

```text
unverified -> pending
rejected   -> pending
pending    -> pending   # idempotent
verified   -> terminal for MVP
```

The local tenant permission remains `organization.verify.request`.

Platform review routes:

```http
GET  /api/v1/platform/organizations/verification-queue
POST /api/v1/platform/organizations/{organizationID}/verification-decisions
```

Platform authorization is resolved before target Organization lookup:

```text
verify Clerk session JWT
-> resolve active Identity user through /api/v1/me
-> resolve private Identity platform access
-> require organization.verification.review
-> only then load/lock the target Organization
```

An active Clerk Organization context is **not** required for platform review. A platform reviewer may have no tenant membership at all.

Authorization behavior:

```text
unauthenticated                       -> 401
authenticated without platform grant -> 403
Identity/platform-access unavailable -> 503
authorized + unknown organization    -> 404
```

### Review queue

`GET /api/v1/platform/organizations/verification-queue` returns only reviewable `verification_status=pending` organizations whose local lifecycle is active. It uses deterministic oldest-request-first ordering and bounded keyset pagination (`limit`, opaque `cursor`).

The response may include local Organization ID, name, legal name, website, country, company type, verification status, point-in-time business-email domain/timestamp, request timestamp, and update timestamp. It never exposes Clerk organization/membership/user IDs, platform-access rows, reviewer Identity UUIDs, tokens, or provider payloads.

### Review decision

Request body is intentionally narrow and rejects unknown fields:

```json
{"decision":"verified"}
```

or:

```json
{"decision":"rejected"}
```

The Organization ID is path authority; it is not accepted from the body.

Decision semantics are deterministic:

```text
pending  + verified -> verified + one review audit
pending  + rejected -> rejected + one review audit
verified + verified -> idempotent success, no new audit
rejected + rejected -> idempotent success, no new audit
verified + rejected -> 409 verification_decision_conflict
rejected + verified -> 409 until customer resubmits to pending
unverified + either -> 409 verification_not_pending
non-active org      -> 409 organization_not_reviewable
```

The transaction locks the target Organization row, evaluates fresh lifecycle/verification state, updates `verification_status`, appends `organization.verification.reviewed`, and commits. Identity/Clerk network calls happen before the local transaction and never while the row lock is held.

Concurrent opposite decisions serialize on the Organization row: one terminal transition wins and one review event is committed. Concurrent identical terminal decisions can both return success, but only the transition winner inserts the review event.

`verified` remains terminal for the MVP. Manual verification does not mutate `trust_status`.

## Reviewer attribution

Manual review audit stores the authenticated local BridgeWorks Identity UUID as an external UUID reference only. There is no cross-service FK.

Organization audit never stores reviewer email, Clerk user/session ID, JWT, provider metadata, or platform-access rows. Tenant audit responses expose only `actor_kind=platform_admin`; the reviewer Identity UUID is intentionally omitted.

## Business-email proof remains separate

Authenticated route:

```http
POST /api/v1/organizations/current/business-email-verification
```

An active local owner/admin may record that their **current verified Identity primary email** belongs to a configured non-personal domain at the explicit verification time.

This means only:

```text
an authorized active tenant owner/admin
had a verified Identity primary email
on the recorded normalized domain
at business_email_verified_at
```

It does not prove legal organization ownership or legal domain ownership and is not a hard precondition for manual company verification. Identity email changes do not silently rewrite historical proof; explicit re-verification replaces it.

## Product/security audit

Migration `000005_add_organization_audit.sql` adds `organization.audit_events` and tenant permission `organization.audit.read`.

The append-only audit row contains the minimum bounded state needed for product/security history:

- UUIDv7 audit `id`;
- local `organization_id` FK;
- bounded `event_type`;
- bounded `actor_kind` (`tenant_user|platform_admin|system`);
- optional external `actor_identity_user_id` UUID with no Identity FK;
- optional historical actor/subject local membership UUIDs without destructive membership FKs;
- optional bounded `from_value` / `to_value`;
- `occurred_at`.

It does **not** store raw email, Clerk user/organization/membership/invitation IDs, JWTs, Authorization headers, Svix signatures, secrets, database URLs, request bodies, provider responses, or arbitrary metadata blobs. Profile update events do not duplicate the profile body.

Recognized audit events are:

```text
organization.profile.updated
organization.verification.requested
organization.business_email.verified
membership.invitation.requested
membership.role.changed
membership.removal.requested
membership.leave.requested
organization.ownership.transferred
organization.verification.reviewed
membership.activated
membership.removal.completed
```

Application runtime has no update/delete path for audit events.

### Audit atomicity

For local transactional commands, product mutation and audit insert share the same PostgreSQL transaction:

- profile update;
- customer verification request;
- business-email proof;
- invitation intent creation;
- membership role mutation;
- removal reservation;
- leave reservation;
- ownership transfer;
- platform verification decision;
- maintenance removal finalization.

If audit insertion fails, the corresponding local mutation rolls back. Provider network I/O remains outside local database transactions.

Provider reconciliation emits system completion events only when local authorization state actually changes; duplicate/stale reconciliation does not duplicate completion audit.

## Tenant audit API

Authenticated route:

```http
GET /api/v1/organizations/current/audit-events
```

Permission:

```text
owner            -> organization.audit.read
admin            -> organization.audit.read
viewer           -> denied
recruiter        -> denied
delivery_manager -> denied
```

The route is current-Organization scoped and uses bounded keyset pagination. Tenant response fields are limited to safe local audit data such as event ID, event type, actor kind, safe subject membership UUID, bounded from/to values, and occurrence time.

Platform reviewer Identity UUID, provider IDs, emails, and internal dependency data are never returned.

## Provider webhook projection

Public signed endpoint:

```http
POST /api/v1/organizations/webhooks/clerk
```

Supported provider events:

- `organization.created`
- `organization.updated`
- `organization.deleted`
- `organizationMembership.created`
- `organizationMembership.updated`
- `organizationMembership.deleted`

Signature verification uses the Organization-specific Svix secret against the raw bounded body before decoding. Provider lifecycle events mutate only provider-owned projection fields. They do not overwrite `verification_status`, `trust_status`, product profile, business-email proof, or local `application_role`.

A later signed `organization.updated` may update provider name/slug while a prior manual `verification_status=verified`, business-email proof, and review audit remain unchanged.

## Invitations and intent retention

Authenticated route:

```http
POST /api/v1/organizations/current/invitations
```

Flow:

1. transactionally create a local UUIDv7 invitation intent with requested local role and audit `membership.invitation.requested`;
2. commit;
3. call Clerk invitation API with provider role fixed to `org:member`;
4. send only opaque `bridgeworks_invitation_id` public metadata;
5. signed Clerk membership webhook resolves the pending same-Organization UUIDv7 intent, applies local role, consumes it, and may record `membership.activated`.

Missing, malformed, non-UUIDv7, unknown, consumed, or cross-Organization markers cannot grant the intended privileged role.

Retention policy:

- consumed/terminal invitation intents may be pruned only after configured retention;
- pending intents are not deleted merely because they are old;
- ambiguous provider-timeout intents remain reconcilable;
- intent UUIDs are never recycled.

## Removal/leave and reconciliation

Authenticated routes:

```http
DELETE /api/v1/organizations/current/members/{membershipID}
DELETE /api/v1/organizations/current/membership
```

Removal remains fail closed:

```text
local removal reservation transaction + audit
-> membership immediately loses local authorization/effective-owner status
-> commit
-> provider delete outside transaction
-> signed provider webhook normally finalizes local deleted projection
```

Provider timeout/429/5xx/ambiguous result never restores authorization and never implies absence.

Maintenance reconciliation handles old stuck removal intents conservatively:

1. query authoritative Clerk membership presence outside the DB transaction;
2. provider reports membership present -> leave removal pending;
3. provider timeout/429/5xx -> leave state unchanged and fail safely;
4. authoritative provider absence -> open local transaction, acquire Organization lock, lock intent + membership, re-check local state, then finalize deletion, clear intent, append one `membership.removal.completed` system audit, commit;
5. if the intent disappeared or membership was already locally finalized before the transaction acquired locks, do not fabricate a second deletion/audit event.

A removal-pending membership stays unauthorized throughout reconciliation.

## Maintenance CLI

Operator-only binary:

```text
/usr/local/bin/organization-maintenance
```

Commands:

```text
prune-consumed-invitations
reconcile-removals
status
```

There is no maintenance HTTP listener and no APISIX maintenance route.

Maintenance configuration:

```dotenv
ORGANIZATION_MAINTENANCE_DATABASE_URL=<operator-only DB credential>
ORGANIZATION_MAINTENANCE_DATABASE_CONNECT_TIMEOUT=5s
ORGANIZATION_MAINTENANCE_COMMAND_TIMEOUT=30s
ORGANIZATION_INVITATION_INTENT_RETENTION=720h
ORGANIZATION_REMOVAL_RECONCILE_AFTER=15m
ORGANIZATION_MAINTENANCE_BATCH_SIZE=100
```

Removal reconciliation additionally requires the existing Clerk Backend API configuration.

The normal `organization-service` runtime container must **not** receive `ORGANIZATION_MAINTENANCE_DATABASE_URL`.

## Database credential boundaries

Production must provision distinct authority classes; PR4 only documents the contract and does not create credentials.

Identity:

```text
DATABASE_URL                   normal Identity runtime
PLATFORM_ACCESS_DATABASE_URL   platform-access operator only
MIGRATION_DATABASE_URL         migration/DDL
```

Organization:

```text
DATABASE_URL                            normal Organization runtime
MIGRATION_DATABASE_URL                  migration/DDL
ORGANIZATION_MAINTENANCE_DATABASE_URL  maintenance operator only
```

Normal runtime containers must not receive operator-only DB credentials. No production credential values or service-account JSON keys belong in the repository.

## Public API surface

Public Organization routes through APISIX are:

```text
GET    /api/v1/organizations/health/live
GET    /api/v1/organizations/health/ready
POST   /api/v1/organizations/webhooks/clerk
GET    /api/v1/organizations/current
PATCH  /api/v1/organizations/current
POST   /api/v1/organizations/current/verification
POST   /api/v1/organizations/current/business-email-verification
GET    /api/v1/organizations/current/audit-events
POST   /api/v1/organizations/current/invitations
GET    /api/v1/organizations/current/membership
DELETE /api/v1/organizations/current/membership
PATCH  /api/v1/organizations/current/members/{membershipID}/role
DELETE /api/v1/organizations/current/members/{membershipID}
POST   /api/v1/organizations/current/ownership-transfer
GET    /api/v1/platform/organizations/verification-queue
POST   /api/v1/platform/organizations/{organizationID}/verification-decisions
```

Identity `/internal/v1/platform-access/me` remains private and is intentionally absent from APISIX/public Organization OpenAPI.

Authenticated responses use `Cache-Control: no-store`, `Vary: Authorization`, and propagated `X-Request-Id`; browser preflight uses the configured authorized frontend origins and never wildcard credentialed CORS.

## Verification and CI

Authoritative module validation:

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
go tool cover -func=coverage.out
```

Organization CI additionally verifies real PostgreSQL v1->v5 migration, preserved PR1-PR3.5 invariants, owner concurrency, manual platform review E2E, same-session platform grant revocation, Identity outage fail-closed behavior, real concurrent review decisions, audit authorization/atomicity, invitation retention, removal reconciliation, signed Clerk projection ownership, sensitive-log leakage, APISIX private-route policy, and private-port isolation.

## Production prerequisites

PR4 is source-level completion only. Production still requires deployment work outside this PR, including:

- provisioned least-privilege runtime/migration/operator DB roles;
- Secret Manager wiring for DB URLs, Clerk secrets, and service identity configuration;
- Cloud Run service-to-service Identity authentication and private ingress verification;
- production migration execution/rollback procedure;
- operator scheduling/runbook for maintenance CLI (for example a separately authorized Cloud Run Job);
- production observability/alert thresholds and operational ownership;
- deployment/cutover validation.

No production migration, Cloud Run deployment, Vercel deployment, Secret Manager mutation, database-role creation, or CI/CD cutover is performed by PR4.
