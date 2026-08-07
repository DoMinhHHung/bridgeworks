# organization-service

Organization Service owns the BridgeWorks-local organization profile, local
organization lifecycle, tenant authorization, business-email proof, membership
administration intents, and webhook projections. Clerk remains provider authority
for organization identity/session context, provider memberships, and invitations.

## Authority boundary

Clerk is authoritative for:

- `clerk_organization_id`;
- provider organization membership identity and lifecycle;
- provider organization invitation lifecycle;
- active Clerk organization context on authenticated sessions.

Organization Service is authoritative for:

- BridgeWorks-local Organization UUIDs;
- organization product profile fields;
- local organization lifecycle/status;
- tenant `application_role` and permission policy;
- owner invariants and ownership transfer;
- point-in-time business-email proof;
- local invitation role intent and removal intent;
- local authorization decisions.

Identity Service is authoritative for:

- active local BridgeWorks user state;
- minimal verified primary-email projection used for explicit business-email
  verification;
- GLOBAL BridgeWorks platform access such as `platform_admin`.

No Organization code reads Identity PostgreSQL. Cross-service Identity data is
consumed through authenticated private HTTP contracts.

## Tenant roles are not platform roles

Tenant-local roles remain exactly:

```text
owner
admin
recruiter
delivery_manager
viewer
```

These roles are scoped to one Organization and are not a source of BridgeWorks
global authority. A tenant `owner` or `admin` is **not** a `platform_admin`, and
Clerk `org:admin` is also not a BridgeWorks platform admin.

PR3.5 prepares a separate Organization-side platform authorization resolver that
consumes Identity Service's private current-user platform-access contract:

```text
Clerk authenticated user
        ↓
Identity local active user
        ↓
Identity local platform access
        ↓
organization.verification.review
```

The platform resolver does not require active Clerk organization context,
Organization membership, tenant owner/admin role, or current-organization
resolution. That separation is required because a future BridgeWorks platform
admin must review organizations across tenants.

For Organization → private Identity calls the existing trusted transport remains
unchanged:

```text
Authorization: Bearer <same Clerk session JWT>
X-Serverless-Authorization: Bearer <Google platform identity token>  # Cloud Run
X-Request-Id: <preserved request id>
```

Local Docker/CI may use the configured private transport without Google platform
token; Cloud Run keeps the existing Google service-to-service identity boundary.
Organization never queries Identity PostgreSQL and does not duplicate the
platform-access table.

Identity platform-access responses are validated strictly. Unknown role,
unknown permission, malformed response, timeout, 5xx, or platform-token failure
all fail closed as platform authorization unavailable. They never fall back to
tenant `owner`, tenant `admin`, Clerk organization role, a forged header, JWT
custom metadata, email, or email domain.

PR4 may later use the exact global permission:

```text
organization.verification.review
```

PR3.5 does **not** add a company approve/reject endpoint or trust mutation.

## Webhook projection

Public Clerk webhook endpoint:

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

Webhook signature verification uses the configured Organization-specific Svix
secret on the raw request body. Payload is bounded, verified before decode,
processed through a transactional inbox, and never logged.

Provider lifecycle events update only provider-owned projection fields. They do
not overwrite BridgeWorks-owned product fields such as `legal_name`, `website`,
`country`, `company_type`, `verification_status`, `trust_status`,
`business_email_domain`, or local `application_role`.

## Current organization

Authenticated tenant route:

```http
GET /api/v1/organizations/current
```

Authorization chain remains tenant-specific:

```text
Clerk session JWT
→ Identity /api/v1/me active-account check
→ Clerk active organization context
→ Organization local membership lookup
→ local application_role / permissions
```

This chain is intentionally distinct from the global platform authorization
resolver described above.

Public response uses BridgeWorks-local identifiers. Provider IDs are not public
API authority. Current response includes the local business-email proof domain
and timestamp when present, but does not expose the external Identity UUID that
performed the proof.

## Product profile

Authenticated product-profile mutation remains:

```http
PATCH /api/v1/organizations/current
```

Only tenant-local authorization governs this route. PR3.5 does not change its
semantics or privilege matrix.

## Verification request

Existing tenant verification-request route remains:

```http
POST /api/v1/organizations/current/verification
```

This request does not perform manual company approval/rejection. PR3.5 leaves
its current tenant authorization and status semantics unchanged.

## Business-email proof

Authenticated route:

```http
POST /api/v1/organizations/current/business-email-verification
```

An active local `owner` or `admin` may explicitly prove that, at that request
time, their Identity projection had a verified primary email on a configured
non-personal domain.

The proof means only:

```text
an authorized active local owner/admin
had a verified Identity primary email
on the stored normalized business domain
at business_email_verified_at
```

It does not prove legal ownership of the organization or domain and does not set
`verification_status=verified`.

Organization stores only:

- normalized domain;
- verification timestamp;
- external Identity local UUID that performed verification.

It does not store raw email and there is no cross-service FK.

Identity email updates never silently rewrite historical Organization proof.
Explicit re-verification replaces the stored point-in-time proof with the
current verified business domain. If the current Identity primary email becomes
unverified or absent, a new verification request fails safely and the previous
historical proof is retained.

## Invitation authority

Authenticated invitation endpoint:

```http
POST /api/v1/organizations/current/invitations
```

BridgeWorks does not trust Clerk metadata as application-role authority. The
flow is:

1. create local UUIDv7 `membership_invitation_intents` row containing the
   requested local `application_role`;
2. call Clerk OrganizationInvitation with provider role always `org:member`;
3. send only opaque public metadata:
   `bridgeworks_invitation_id=<local intent UUIDv7>`;
4. wait for a verified signed Clerk membership webhook;
5. resolve the pending intent scoped to the same local organization;
6. project the membership and consume the intent only after successful local
   role reconciliation.

Missing, malformed, non-UUIDv7, unknown, already-consumed, or cross-organization
markers cannot grant the intended privileged local role. Those cases use the
safe compatibility projection path instead.

The local intended role itself is never stored in or trusted from Clerk
metadata.

## Membership role administration

Authenticated role mutation:

```http
PATCH /api/v1/organizations/current/members/{membershipID}/role
```

Targets are local BridgeWorks membership UUIDs. Client/provider organization IDs
are never accepted as authorization authority.

Role rules:

- `owner` may assign/revoke owner subject to last-owner invariant;
- `admin` may manage non-owner roles only;
- `admin` cannot promote itself/others to owner;
- `admin` cannot mutate an owner;
- recruiter, delivery manager, and viewer cannot administer roles;
- cross-tenant target IDs use the same not-found path and do not create an
  existence oracle.

## Owner invariant and transfer

Multiple owners are allowed. Effective owner means:

```text
status = active
AND application_role = owner
AND no pending membership removal intent
```

Organization-scoped PostgreSQL advisory locking serializes owner counting,
owner role mutation, removal reservation, and ownership transfer.

Last effective owner cannot:

- leave;
- be removed;
- demote itself;
- be demoted by another actor.

Ownership transfer endpoint:

```http
POST /api/v1/organizations/current/ownership-transfer
```

Transfer is a single local PostgreSQL transaction: target becomes owner and
actor becomes admin without a zero-owner intermediate state.

## Removal and leave

Authenticated routes:

```http
DELETE /api/v1/organizations/current/members/{membershipID}
DELETE /api/v1/organizations/current/membership
```

Removal is deliberately two-phase:

1. inside a local transaction, acquire organization advisory lock and create a
   removal reservation;
2. the reserved membership is immediately excluded from current-membership
   authorization and effective-owner counting;
3. commit;
4. call Clerk membership deletion outside PostgreSQL transaction;
5. verified Clerk `organizationMembership.deleted` webhook finalizes the local
   deleted projection and removes the reservation.

A removal-pending actor cannot continue to use business verification,
invitations, role mutation, ownership transfer, or membership removal routes.

Provider timeout, 429, 5xx, or ambiguous network failure is returned as a
sanitized retryable service failure. The reservation remains because provider
side effects may already have occurred. Provider 404 is treated as already
absent/pending webhook reconciliation. Deterministic provider conflict/rejection
cleans the local reservation.

## Creator owner-bootstrap fence

Initial creator owner bootstrap remains a one-time local projection behavior.
Once `owner_bootstrap_eligible=false`, BridgeWorks never restores it and does not
reset `owner_bootstrapped`.

Supported BridgeWorks remove/invite/rejoin flow therefore cannot let a historical
creator regain automatic owner merely because a fresh Clerk membership appears.
A BridgeWorks invitation for that creator is reconciled using the local
invitation role intent instead.

## Provider administration boundary

Organization membership administration uses a narrow Clerk Backend API adapter
with bounded timeout. Provider calls happen only after local PostgreSQL
transactions commit; no network I/O is held inside a transaction.

`CLERK_BACKEND_API_URL` is an origin-only config value. Production default is:

```text
https://api.clerk.com
```

The client appends `/v1`. Config rejects path, query, fragment, userinfo, or
cleartext HTTP outside trusted local/private destinations.

Invitation creation is not blindly retried after timeout/429/ambiguous failure,
because Clerk may have accepted the original request. Local pending invitation
intent is retained for later operational reconciliation.

## Configuration

Important Organization variables:

```dotenv
IDENTITY_SERVICE_URL=http://identity-service:8080
IDENTITY_SERVICE_AUTH_MODE=none
IDENTITY_SERVICE_AUDIENCE=
IDENTITY_SERVICE_REQUEST_TIMEOUT=2s
CLERK_SECRET_KEY=<secret-managed Clerk Backend API key>
CLERK_BACKEND_API_URL=https://api.clerk.com
CLERK_BACKEND_API_TIMEOUT=3s
```

`IDENTITY_SERVICE_AUTH_MODE=google_id_token` in Cloud Run uses Google platform
identity for the private Identity service. The same user Clerk bearer token is
preserved on the downstream request. PR3.5 reuses this exact transport for
private `/internal/v1/platform-access/me` lookups; it does not create a parallel
trust channel.

Production secret values are not committed. `CLERK_SECRET_KEY` and future
operator credentials belong in managed secret infrastructure.

## Public HTTP contracts

PR3 tenant API remains:

```text
GET    /api/v1/organizations/current
PATCH  /api/v1/organizations/current
POST   /api/v1/organizations/current/verification
POST   /api/v1/organizations/current/business-email-verification
POST   /api/v1/organizations/current/invitations
PATCH  /api/v1/organizations/current/members/{membershipID}/role
DELETE /api/v1/organizations/current/members/{membershipID}
DELETE /api/v1/organizations/current/membership
POST   /api/v1/organizations/current/ownership-transfer
POST   /api/v1/organizations/webhooks/clerk
GET    /api/v1/organizations/health/live
GET    /api/v1/organizations/health/ready
```

PR3.5 adds **no** public Organization platform-admin or manual-verification
route. Identity `/internal/v1/platform-access/me` is private and is not routed by
APISIX.

## Manual company verification blocker

Manual company verification mutation is still outside this PR:

```text
manual_company_verification_blocked=true
```

PR3.5 establishes the secure platform authority foundation only. While this PR
is unmerged, the prerequisite is still pending. Once PR3.5 is merged and its
production credential/transport prerequisites are satisfied, the specific
platform-admin authority prerequisite can be considered provided for a later
PR4. PR4 itself is not part of this change.

No email allowlist, hidden header, hardcoded user, Clerk org-role shortcut,
static operator bearer token, or tenant-owner/admin fallback is introduced.

## Verification

Repository validation keeps the exact current Go toolchain and clean generated
SQL:

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

PR3.5 security regressions cover:

- ordinary user without local platform assignment has no review permission;
- tenant owner/admin and Clerk `org:admin` do not imply platform access;
- forged platform header and role-like JWT claims do not grant authority;
- explicit local `platform_admin` maps only to
  `organization.verification.review`;
- disabled/deleted Identity account is denied even with an assignment;
- Identity timeout/5xx/malformed/unknown role or permission fails closed;
- platform authorization does not require Organization context;
- Organization→Identity request preserves Clerk bearer token, request ID, and
  existing Google serverless auth when configured;
- APISIX does not expose the private Identity platform-access path;
- existing Organization PR3 migration, onboarding, business-email, invitation,
  owner concurrency, projection, unique-violation, sensitive-log, and private
  port regressions remain authoritative.

## Private metrics and access logs

Organization Service starts a second operational HTTP listener configured by:

```dotenv
ORGANIZATION_METRICS_ADDR=:9090
```

The metrics listener is private to the Docker/Cloud Run service network and is
not routed through APISIX. It exposes bounded service-owned Prometheus metrics
and does not affect application readiness.

Access logs exclude Authorization, Cookie, JWTs, webhook bodies, Svix headers,
email addresses, Clerk user IDs, local UUIDs, database URLs, request/response
bodies, platform-access payloads, and raw dependency errors.

## Production operations

Production Organization→Identity calls must retain private Identity ingress and
Google service identity token enforcement. PR3.5 does not weaken or replace that
platform-auth boundary. Identity runtime and future platform-access operator
credentials must remain separately managed; Organization receives neither.

Cross-service capacity, observability, retention, and deployment work remains in
the repository production-readiness roadmap. No production deployment is part
of PR3.5.
