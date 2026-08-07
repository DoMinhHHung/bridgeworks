# Organization Service

Organization Service owns the BridgeWorks organization product profile and local authorization boundary while projecting Clerk organizations and memberships.

Clerk remains authoritative for provider organization/session/membership identity and provider invitation/membership lifecycle. Organization Service is authoritative for BridgeWorks product fields, verification/trust state, local `application_role`, permissions, membership-administration policy, owner invariants, business-email point-in-time proof, and the Clerk organization/membership webhook inbox.

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
GET    /api/v1/organizations/health/live
GET    /api/v1/organizations/health/ready
POST   /api/v1/organizations/webhooks/clerk
GET    /api/v1/organizations/current
PATCH  /api/v1/organizations/current
POST   /api/v1/organizations/current/verification
POST   /api/v1/organizations/current/business-email-verification
GET    /api/v1/organizations/current/membership
POST   /api/v1/organizations/current/invitations
PATCH  /api/v1/organizations/current/members/{membershipID}/role
DELETE /api/v1/organizations/current/members/{membershipID}
DELETE /api/v1/organizations/current/membership
POST   /api/v1/organizations/current/ownership-transfer
```

The service port is private. APISIX is the public edge. Cloud Run routing keeps Google platform authentication in `X-Serverless-Authorization` and preserves the caller's Clerk Bearer token in `Authorization`.

There is intentionally no `POST /api/v1/organizations` endpoint. Organization creation remains Clerk/frontend-owned.

## Authority model

Clerk owns:

- provider organization identity, ID, name, and slug;
- active organization session context;
- provider membership identity and lifecycle;
- Clerk organization invitation/provider invitation state;
- provider membership deletion after a BridgeWorks remove/leave command.

Organization Service owns:

- provider mapping/projection and local lifecycle;
- `legal_name`, `website`, `country`, `company_type`;
- `verification_status` and `trust_status`;
- local `application_role`, permissions, and authorization;
- local invitation role intent;
- owner invariants and membership-administration policy;
- business-email point-in-time proof;
- private initial-owner bootstrap state;
- webhook inbox, ordering, and reconciliation.

Identity Service owns Clerk-user mapping, local Identity UUID, public `id_user`, verified primary-email projection, local account lifecycle, and `/api/v1/me`. Organization Service never queries Identity PostgreSQL and creates no cross-service foreign key.

Clerk JWT organization role/permission claims are never BridgeWorks authorization authority. They are identity/session context only.

## Creation and eventual consistency

The creation flow is:

```text
Frontend / Clerk
    -> create Clerk organization
    -> Clerk emits signed organization + membership events
    -> Organization Service transactionally projects provider state
    -> BridgeWorks commands mutate local product/authorization state
```

Clerk webhooks are asynchronous. A valid Clerk session may therefore reference an organization before the local projection arrives. Authenticated APIs return:

```http
409 Conflict
Retry-After: 2
```

with code `organization_not_ready`. The service never creates local authorization state from unverified client input to hide provider lag.

## Initial owner bootstrap and creator rejoin fence

Migration v3 distinguishes historical organizations from organizations first projected after the bootstrap feature:

```text
legacy row existing at v3 migration:
owner_bootstrap_eligible = false

row inserted after v3:
owner_bootstrap_eligible = true
```

`owner_bootstrap_eligible=true` means only that the one-time automatic initial-owner grant is still permitted. It is not an ownership fact or reusable authorization credential. `owner_bootstrapped=true` means the automatic initial-owner grant completed.

Bootstrap succeeds only when all of these are true:

```text
owner_bootstrap_eligible = true
owner_bootstrapped = false
organization status = active
verified provider created_by exists
no deleted membership history for that exact creator
exact creator has an active local membership projection
```

Then the exact creator becomes local `owner` and `owner_bootstrapped=true`.

Deleted creator membership history before bootstrap permanently cancels automatic eligibility. BridgeWorks remove/leave reservations also cancel unfinished creator bootstrap eligibility before calling Clerk. Invitation acceptance or rejoin never resets `owner_bootstrap_eligible` or `owner_bootstrapped`.

A historical creator who later rejoins receives only the normal compatibility role or an explicitly authorized local invitation role. Historical `created_by` is not a perpetual owner credential. Later provider organization updates cannot re-grant owner after the one-time fence has completed.

Legacy organizations remain permanently ineligible for automatic owner bootstrap.

## Local roles and permissions

Roles are explicit:

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

Permission presence is necessary but not sufficient for owner-sensitive mutations. Domain policy is explicit; there is no string-ordered role hierarchy.

Policy:

- `viewer`, `recruiter`, and `delivery_manager` cannot invite, remove members, mutate roles, or transfer ownership;
- `admin` can invite and manage active non-owner memberships;
- `admin` cannot create, promote, demote, remove, or otherwise mutate an `owner` membership;
- `admin` cannot self-promote to `owner`;
- only a current local `owner` can grant or revoke `owner`;
- an owner may create another owner;
- multiple owners are allowed.

All administration commands re-read the current local actor membership inside the organization transaction. A stale JWT/ActorContext cannot override a newer local role or a removal reservation.

## Last-owner and effective-owner invariant

BridgeWorks allows multiple owners to reduce lockout risk.

An **effective owner** is exactly:

```text
membership.status = active
AND application_role = owner
AND no membership_removal_intent exists
```

The organization-scoped PostgreSQL advisory lock is acquired before owner count, role mutation, removal reservation, and ownership transfer. This serializes competing owner mutations across separate transactions.

Normal administration operations must never reduce an organization with explicit ownership to zero effective owners. The last effective owner cannot:

- demote themselves;
- be demoted by another actor;
- leave;
- be removed.

Real PostgreSQL concurrency regressions run separate goroutines/transactions for concurrent two-owner leave, concurrent owner demotion, ownership transfer versus competing owner mutation, and duplicate ownership transfer. The final state must retain at least one effective owner and contain no partial transfer.

## Atomic ownership transfer

`POST /api/v1/organizations/current/ownership-transfer` accepts only a local target membership UUID. Tenant identity always comes from `ActorContext`; no organization ID is accepted from the client.

Inside one transaction:

1. acquire the organization advisory lock;
2. re-read the actor and target membership scoped to the actor organization;
3. require the actor to be an active local owner and not removal-pending;
4. require the target to be active, same-organization, and not removal-pending;
5. promote the target to `owner`;
6. demote the transferring owner to `admin`;
7. commit.

There is no intermediate zero-owner state and no external network call inside this transaction.

## Invitation authority and local role intent

Clerk Organization Invitations remain provider authority. Organization Service does not create a second independent provider invitation lifecycle.

The BridgeWorks command flow is:

```text
POST /current/invitations
    -> authorize local membership.invite
    -> create local UUIDv7 invitation intent with requested local application_role
    -> commit short Organization transaction
    -> call Clerk Backend API outside the transaction
```

The local intent stores only BridgeWorks authorization state required for reconciliation. Raw invitation email is not persisted by Organization Service.

The only correlation value sent to Clerk metadata is the opaque local intent ID:

```json
{
  "bridgeworks_invitation_id": "<UUIDv7>"
}
```

The requested local `application_role` is **not** stored in Clerk metadata. After invitation acceptance, Clerk transfers invitation `publicMetadata` into the resulting Organization Membership. The verified signed membership webhook then:

1. parses only `bridgeworks_invitation_id`;
2. requires a valid UUIDv7 marker;
3. looks up a pending local intent scoped to the projected organization;
4. reads the local role from Organization PostgreSQL;
5. applies that role;
6. consumes the intent in the same webhook transaction.

Missing, malformed, non-UUIDv7, unknown, already-consumed, or cross-organization markers cannot grant the intended local role. They fall back only to the safe compatibility projection:

```text
Clerk org:admin -> local admin
all other Clerk roles -> local viewer
```

Provider invitation success does not create a local active membership. Active membership exists only after verified Clerk membership projection.

## Invitation/provider failure semantics

Clerk Backend API calls use a bounded timeout and sanitized error mapping. No Clerk secret, raw provider response, provider diagnostic ID, Authorization header, email payload, or response body is logged.

Invitation creation is intentionally not blindly retried inside the same command. A timeout, HTTP 429, provider 5xx, or ambiguous network result may occur after Clerk accepted the request. In those cases Organization Service:

```text
returns membership_provider_unavailable
preserves the local invitation intent
```

The caller may retry later according to product/provider guidance. The service does not fabricate provider success or prematurely create a local membership.

Definite provider conflict/rejection can clean up a known-failed local intent. Stale/unconsumed invitation-intent retention and operational cleanup remain a PR4/operations retention follow-up; PR3 does not pretend ambiguous outcomes are resolved.

## Remove member and leave organization

Remove and leave remain provider-backed commands because Clerk owns membership lifecycle.

The command transaction first:

1. acquires the organization advisory lock;
2. re-reads the current actor/target under local tenant scope;
3. enforces explicit owner/admin policy and last-owner invariant;
4. inserts a `membership_removal_intent`;
5. commits.

A removal intent immediately excludes that membership from authorization and effective-owner counting. Only after commit does the service call Clerk Backend API. No provider network I/O is held inside a database transaction.

The signed Clerk membership deletion webhook remains the authority that marks the local membership deleted and removes the local reservation.

Provider timeout/429/5xx preserves the removal reservation and returns a sanitized retryable error. A later retry may safely attempt deletion again. Provider not-found is treated as already absent and waits for reconciliation. Definite provider conflict/rejection clears the reservation so local authorization can recover.

## Cross-tenant safety

No membership-administration API accepts an organization ID as authorization authority.

Target membership queries are always scoped by:

```text
actor.organization_id + local membership UUID
```

A foreign and nonexistent membership use the same not-found behavior where practical. Cross-tenant role mutation, removal, and ownership transfer therefore do not expose foreign membership existence.

Invitation creation always uses the verified actor organization/provider reference; a client-supplied organization field is rejected as an unknown JSON field.

## Business-email point-in-time proof

`POST /api/v1/organizations/current/business-email-verification` consumes the current Identity `/api/v1/me` contract. A successful Identity `/me` response already represents an active local Identity account, and `primary_email` is the verified Clerk primary-email projection.

The command proves only this statement at that instant:

> an authorized active BridgeWorks owner/admin had a verified Identity primary email on a non-personal domain when the command ran.

It does **not** prove legal ownership or technical control of the organization's domain. No MX lookup, arbitrary external URL fetch, DNS ownership claim, or website scrape is used as ownership proof.

The local point-in-time proof stores:

```text
business_email_domain
business_email_verified_at
business_email_verified_by_user_id   # private external Identity UUID reference
```

It does not persist raw email and creates no Identity database foreign key. The private verifier UUID is not returned by the public Organization API.

Email policy:

- trim surrounding whitespace;
- local-part content does not affect the verified domain;
- normalize the domain to lowercase;
- reject malformed domains;
- reject Unicode/non-ASCII domains rather than inventing an implicit IDNA policy;
- reject configured personal/free-email domains and their subdomains;
- maintain the personal-domain denylist as configuration, not a large hardcoded Go list.

The current proof is historical point-in-time state. If Identity later projects a different verified primary email, Organization Service does not silently mutate the organization because there is no approved cross-service invalidation event contract. The old proof remains until an authorized explicit re-verification/revocation path changes it.

Calling the verification command again re-reads current Identity `/me`; a newly verified business email replaces the stored domain/timestamp/verifier. If current Identity has no verified primary email, the new request fails with `verified_primary_email_required` and the historical proof remains unchanged.

Business-email proof never automatically sets company `verification_status=verified`.

## Company verification lifecycle

Customer request endpoint:

```text
POST /api/v1/organizations/current/verification
```

requires local `organization.verify.request` and supports:

```text
unverified -> pending
rejected   -> pending
pending    -> pending   # idempotent
```

It never performs operator approval/rejection.

Current `main` has no reviewed platform-admin authority suitable for:

```text
pending -> verified
pending -> rejected
```

Therefore PR3 intentionally exposes no manual operator endpoint:

```text
manual_company_verification_blocked=true
platform_admin_prerequisite_required=true
```

A focused Platform Admin Authorization Foundation is required before manual company verification can be implemented. PR3 does not use email allowlists, hidden headers, static admin bearer secrets, hardcoded IDs, Clerk organization role claims, or unauthenticated operator endpoints as substitutes.

`trust_status` remains independent and PR3 exposes no trust mutation.

## Product profile onboarding

`PATCH /api/v1/organizations/current` requires local `organization.manage` and may mutate only:

```text
legal_name
website
country
company_type
```

Fields are partial: omitted means preserve; explicit `null` clears an optional value. Unknown JSON fields are rejected and command bodies are bounded.

`company_type` remains a bounded taxonomy-neutral identifier instead of an invented large closed taxonomy.

## Webhook contract and synchronization

Supported Clerk event names are exact:

```text
organization.created
organization.updated
organization.deleted
organizationMembership.created
organizationMembership.updated
organizationMembership.deleted
```

Signature verification occurs on the raw request body before decoding. Supported events use the transactional inbox, duplicate/stale ordering, organization advisory lock, UUIDv7 projections, and existing membership savepoint/conflict recovery.

Provider organization updates mutate only provider-owned projection fields such as name, slug, and provider/application lifecycle. They do not overwrite BridgeWorks product fields, business-email proof, company verification/trust state, or explicit local application roles.

After compatibility initialization or invitation reconciliation, local `application_role` remains authoritative.

## Transaction boundaries

Authenticated commands follow this pattern:

1. verify Clerk Bearer token and active organization context;
2. call private Identity `/me` and resolve local actor membership/permissions;
3. begin a short Organization PostgreSQL transaction;
4. acquire the relevant row/advisory lock and re-check current local state;
5. persist the local mutation/reservation;
6. commit;
7. only then perform any Clerk Backend API network operation when required.

No Identity/Clerk network call runs inside a PostgreSQL transaction.

Authenticated responses retain:

```http
X-Request-Id: <request correlation ID>
Cache-Control: no-store
Vary: Authorization
```

Gateway CORS may append `Origin` to `Vary`.

## Configuration

PR3 adds these Organization runtime settings:

```text
CLERK_SECRET_KEY
CLERK_BACKEND_API_URL=https://api.clerk.com
CLERK_BACKEND_API_TIMEOUT=3s
ORGANIZATION_PERSONAL_EMAIL_DOMAINS=<comma-separated domain policy>
```

`CLERK_BACKEND_API_URL` is an **origin**, not a versioned path. The Clerk adapter appends `/v1` through the SDK request paths. Unexpected configured URL paths are rejected fail-closed.

`CLERK_SECRET_KEY` is required only by the Organization runtime Clerk Backend API adapter; it is not used as authorization authority. Production must provide credentials for the same Clerk instance through secret management. Never commit a real secret, print it, include it in logs, or use static service-account JSON keys. Actual Secret Manager/Cloud Run production wiring belongs to a later controlled deployment campaign, not PR3.

## Local validation

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

Organization CI also verifies clean sqlc generation, immutable merged migrations `000001..000003`, real v1→v4 migration behavior, legacy owner safety, signed Clerk reconciliation, APISIX routes, provider/local invitation semantics, creator rejoin fencing, cross-tenant administration, removal-pending authorization, real PostgreSQL owner concurrency, dependency outages, security/log leakage, and private-port isolation.

## PR3 non-goals

Not implemented in PR3:

- generic audit framework;
- platform-admin authentication foundation;
- manual company approval/rejection;
- trust/fraud mutation;
- domain-ownership verification beyond the point-in-time verified-email proof;
- Talent/Profile/Passport/Hiring Intent;
- jobs/applications/messaging/billing/escrow/payroll;
- frontend;
- production deployment.

## Production operations

Cross-service production work remains tracked in the [production-readiness roadmap](../../docs/production-readiness-roadmap.md). Rotate Clerk verification keys with the [Clerk JWT key-rotation runbook](../../docs/runbooks/clerk-jwt-key-rotation.md).

Organization Service starts a private metrics listener configured by `ORGANIZATION_METRICS_ADDR`. It exposes only `GET /metrics`, is not routed through APISIX, and remains independent of readiness. Logs exclude JWTs, authorization headers, raw webhook bodies, emails, provider IDs, Clerk secrets, database URLs, and raw provider responses.
