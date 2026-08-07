# identity-service

Identity boundary của BridgeWorks. Service đồng bộ Clerk user events vào local
PostgreSQL projection qua public signed webhook, xác thực Clerk session token cho
authenticated `GET /api/v1/me`, và sở hữu local BridgeWorks global platform
access gắn với Identity user.

## Ownership

Clerk sở hữu authentication, sessions, password/social login, magic links và
email verification. Identity Service sở hữu:

- immutable Clerk user mapping;
- local BridgeWorks lifecycle `active|disabled|deleted`;
- public `id_user`;
- minimal verified primary-email projection;
- transactional Clerk webhook inbox;
- authorization decision dựa trên local account status;
- GLOBAL BridgeWorks platform access assignment cho local Identity user.

Organization Service vẫn sở hữu tenant-local organization roles và permissions.
`owner|admin|recruiter|delivery_manager|viewer` không phải platform roles và
không thể suy ra `platform_admin`.

`status=disabled` là BridgeWorks-owned và Clerk updates không được re-enable.
`status=deleted` là tombstone; service không hard delete. `primary_email` chỉ là
projection của Clerk primary address khi address đó có verification status
`verified`. Service khác không được đọc schema `app` trực tiếp.

Merged `migrations/000001_create_app_users.sql` là locked Identity foundation.
PR3.5 thêm forward-only `migrations/000002_add_platform_access.sql`; migration
v2 không sửa v1, không seed admin và không tự promote existing user.

## Authentication trust boundary

Request path:

```text
Client Authorization: Bearer <Clerk session token>
→ APISIX request-id + proxy rewrite
→ Identity Service Clerk SDK verification
→ narrow Principal{ClerkUserID, SessionID}
→ local app user lookup
→ local status authorization
→ public /me response
```

APISIX không verify JWT và không có auth plugin. Nó forward `Authorization` tới
Identity Service. Service dùng official stable
`github.com/clerk/clerk-sdk-go/v2 v2.7.0` middleware
`WithHeaderAuthorization` với:

- configured RSA JSON Web Key;
- exact issuer binding;
- allowed authorized parties;
- bounded clock-skew leeway;
- custom failure handler để giữ consistent JSON error envelope.

Chỉ verified claims mới được dùng. Boundary đọc `iss`, `sub`, `sid`, `azp`,
`exp`, và `nbf`; application layer chỉ nhận narrow principal. Raw token, full
claims, Clerk error, subject đầy đủ và session ID đầy đủ không được log.

JWT custom metadata, arbitrary role-like claims, Clerk organization role,
organization permissions và active organization context không phải platform
access authority. Local `app.platform_access_assignments` mới là source of truth.

Service không dùng `CLERK_SECRET_KEY`, không gọi Clerk Backend API, không fetch
Clerk user trong request path và không tự tạo local user từ `/me` hoặc private
platform-access lookup.

Mọi authentication failure đều trả cùng contract:

```json
{
  "code": "unauthorized",
  "message": "authentication required",
  "request_id": "...",
  "details": null
}
```

Mọi 401 authentication rejection đồng thời trả:

```http
WWW-Authenticate: Bearer realm="bridgeworks"
```

Header, body và logs không chứa raw token error, issuer, subject, session ID,
authorized party hoặc validation details. Client không nhận biết token bị thiếu,
malformed, expired, sai signature, issuer hay authorized party.

## Authenticated current user

Public gateway endpoint:

```http
GET /api/v1/me
Authorization: Bearer <Clerk session token>
```

Active local account trả:

```json
{
  "id": "0198f3be-bf6f-7b0a-8a25-f8433567e0c1",
  "id_user": "bw012303082645",
  "primary_email": "developer@example.com",
  "status": "active",
  "created_at": "2026-08-03T03:04:05Z",
  "updated_at": "2026-08-03T03:04:05Z"
}
```

`primary_email` có thể là `null`. Response không chứa `clerk_user_id`,
`session_id`, JWT, claims hoặc webhook data.

Mọi `/me` response, bao gồm 200, 401, 403, 409 và 503, đều trả:

```http
Cache-Control: no-store
Vary: Authorization
```

`Vary` được append thay vì overwrite, nên gateway có thể giữ thêm `Origin` cho
CORS mà không làm mất `Authorization`.

Local lifecycle quyết định authorization:

| Local state | HTTP | Code |
| --- | ---: | --- |
| `active` | 200 | public current-user projection |
| `disabled` | 403 | `account_disabled` |
| `deleted` | 403 | `account_deleted` |
| projection chưa có | 409 | `identity_not_ready`, `Retry-After: 2` |
| PostgreSQL error/timeout | 503 | `service_unavailable` |

Valid Clerk token nhưng local projection chưa có không tạo user và không gọi
Clerk API. Client retry sau khi webhook synchronization hoàn tất.

### Browser CORS

Route APISIX `bridgeworks-identity-me` chấp nhận `GET` và browser preflight
`OPTIONS`. Allowed origins được lấy trực tiếp từ cùng comma-separated
`CLERK_AUTHORIZED_PARTIES` value dùng cho JWT authorized-party validation.
Không dùng wildcard origin và không bật credentials/cookies.

CORS contract:

```text
allow methods:  GET,OPTIONS
allow headers:  Authorization,Content-Type,X-Request-Id
expose headers: X-Request-Id,Retry-After
max age:        600 seconds
credentials:    false
```

Allowed preflight nhận `Access-Control-Allow-Origin` bằng đúng requested allowed
origin. Origin ngoài allowlist không nhận header đó. Actual authenticated GET từ
allowed origin expose `X-Request-Id` và `Retry-After` cho browser code.

## Global platform authorization

Global platform access là BridgeWorks-owned state gắn với local Identity UUID,
không gắn với Organization membership. PR3.5 support đúng một role và một
permission mapping code-owned:

```text
platform_admin
    -> organization.verification.review
```

Tenant authority và platform authority là hai security domains độc lập:

```text
Clerk authenticated user
        ↓
Identity local active user
        ↓
Identity local platform access
        ↓
organization.verification.review
```

Không có bước current Organization, Organization membership, tenant `owner` hoặc
tenant `admin`. Email, email domain, allowlist, hardcoded user ID, environment
user list, hidden header, client role, frontend state, Clerk org role/permission,
JWT custom role claim và Clerk public/private metadata đều không cấp
`platform_admin`.

### Private current-user platform-access contract

Identity application listener có private route:

```http
GET /internal/v1/platform-access/me
Authorization: Bearer <Clerk session token>
```

Active user có assignment trả:

```json
{
  "roles": ["platform_admin"],
  "permissions": ["organization.verification.review"]
}
```

Ordinary active user trả:

```json
{
  "roles": [],
  "permissions": []
}
```

Response không chứa email, Clerk IDs, provider metadata hoặc profile fields và
luôn `Cache-Control: no-store`, `Vary: Authorization`. Request ID được preserve.
Disabled/deleted local accounts bị deny trước role lookup. Missing projection trả
`identity_not_ready`; database/contract failure trả sanitized
`service_unavailable`.

Route này **không được expose qua APISIX** và cố ý không nằm trong public
`api/openapi.yaml`. Organization Service gọi private Identity service trực tiếp;
Cloud Run giữ private ingress/platform identity boundary hiện hữu.

Platform-access lookup không dùng Redis cache. Mỗi request đọc active local
assignment tại request time, vì vậy revoke không cần refresh Clerk session:

```text
same Clerk session
grant local platform_admin -> permission present
revoke local platform_admin -> next lookup returns no permission
```

Unknown/duplicate platform role hoặc malformed projection fail closed; không có
fallback sang tenant admin.

## Platform-admin provisioning

Không có public HTTP endpoint grant/revoke platform access. Provisioning dùng
operator-only command binary trong Identity image:

```text
identity-platform-access grant  --id-user <bridgeworks-id-user>
identity-platform-access revoke --id-user <bridgeworks-id-user>
identity-platform-access status --id-user <bridgeworks-id-user>
```

Command chỉ target public BridgeWorks `id_user`; không hỗ trợ email hoặc Clerk
user ID. `grant` và `revoke` idempotent. Grant yêu cầu existing local user có
`status=active`; revoke vẫn được phép cho inactive user để operator luôn có thể
thu hồi quyền. Binary không mở network listener, dùng structured `slog`, bounded
DB connect/command timeout và exit non-zero khi operation thực sự fail.

Production execution sẽ đi qua controlled operator infrastructure/Cloud Run Job
hoặc equivalent secure execution context. PR3.5 chỉ cung cấp binary và source
contract; không deploy operator job và không commit production credentials.

## Database privilege boundary

Ba database authority classes phải được tách khi hosted credentials được
provision:

- `DATABASE_URL`: normal Identity runtime. Runtime cần Identity core privileges
  cho existing user/webhook flows và **chỉ SELECT** platform-access assignments.
  Runtime HTTP credential không được INSERT/UPDATE platform roles.
- `PLATFORM_ACCESS_DATABASE_URL`: operator-only credential. Nó cần lookup target
  `app.app_users` và SELECT/INSERT/UPDATE `app.platform_access_assignments` để
  grant/revoke/status; credential này không được inject vào `identity-service`.
- `MIGRATION_DATABASE_URL`: migration/owner authority cho DDL.

Local/CI có thể reuse local database owner vì đó là disposable private test
infrastructure. Hosted Supabase/staging/production phải provision credential
boundary ở trên trước operator execution. Việc tạo Cloud Run Job, Secret Manager
binding hoặc production DB role credentials là deployment work và bị defer khỏi
PR3.5; không có credential value production trong repo.

## Clerk webhook setup

Trong Clerk Dashboard:

1. Create một webhook endpoint.
2. Dùng public gateway URL kết thúc bằng
   `/api/v1/identity/webhooks/clerk`.
3. Subscribe đúng ba events:
   - `user.created`
   - `user.updated`
   - `user.deleted`
4. Copy endpoint signing secret vào `CLERK_WEBHOOK_SIGNING_SECRET`.
5. Không commit secret.

Dummy secret trong `.env.example` chỉ dành cho local development và CI. Không
reuse dummy secret ngoài local/CI.

Webhook verification dùng official maintained Svix Go verifier trên raw body.
Body được đọc đúng một lần, signature được verify trước JSON decode, và raw
payload không được persist hoặc log.

## Delivery and ordering semantics

Webhook synchronization là eventual consistency. Mỗi supported event chạy trong
một PostgreSQL transaction:

1. Insert inbox row bằng `ON CONFLICT (event_id) DO NOTHING`.
2. Duplicate delivery commit và trả 204 mà không acquire user lock hoặc mutate
   user.
3. Acquire transaction-level advisory lock theo `clerk_user_id`.
4. Nếu có superseding event cho cùng Clerk user, giữ inbox row nhưng bỏ qua user
   mutation, commit và trả 204.
5. Apply create/update/delete projection.
6. Commit rồi mới trả 204.

Nếu transaction rollback, inbox insert cũng rollback để Clerk retry được.
Equal timestamps dùng total order deterministic:

```text
user.deleted > user.updated > user.created > lexical event_id
```

Điều này đảm bảo delete thắng create/update ở cùng timestamp và kết quả không phụ
thuộc arrival order. Event ID lexical chỉ tie-break khi timestamp và event type
đều bằng nhau.

## User mutation rules

- Missing `user.created`/`user.updated`: create active local user.
- Existing update: update `primary_email`, giữ nguyên `id`, `id_user` và status.
- Existing disabled/deleted user không được re-enable.
- `user.deleted`: status deleted và clear email.
- Delete unknown user: create deleted tombstone.
- Existing users không regenerate UUID hoặc `id_user`.

UUID được generate trong Go bằng `github.com/google/uuid.NewV7`. `id_user` format:

```text
bw + 4 crypto-random digits + DDMMYY in Asia/Ho_Chi_Minh + 2 crypto-random digits
```

Random digits dùng `crypto/rand` với rejection sampling để tránh modulo bias.
Exact `app_users_id_user_uq` collision được retry tối đa 5 lần; error khác không
được retry.

## Configuration

Runtime Identity variables:

| Variable | Default |
| --- | --- |
| `DATABASE_URL` | required |
| `DATABASE_CONNECT_TIMEOUT` | `5s` |
| `DATABASE_READINESS_TIMEOUT` | `2s` |
| `DATABASE_MAX_CONNS` | `5` |
| `DATABASE_MIN_CONNS` | `0` |
| `DATABASE_MAX_CONN_LIFETIME` | `30m` |
| `DATABASE_MAX_CONN_IDLE_TIME` | `5m` |
| `DATABASE_HEALTH_CHECK_PERIOD` | `1m` |
| `CLERK_WEBHOOK_SIGNING_SECRET` | required |
| `CLERK_WEBHOOK_PROCESS_TIMEOUT` | `5s` |
| `CLERK_WEBHOOK_MAX_BODY_BYTES` | `1048576` |
| `CLERK_JWT_KEY` | required |
| `CLERK_ISSUER` | required |
| `CLERK_AUTHORIZED_PARTIES` | required |
| `CLERK_AUTH_LEEWAY` | `5s` |

Operator CLI-only variables:

| Variable | Default |
| --- | --- |
| `PLATFORM_ACCESS_DATABASE_URL` | required for operator command only |
| `PLATFORM_ACCESS_DATABASE_CONNECT_TIMEOUT` | `5s` |
| `PLATFORM_ACCESS_COMMAND_TIMEOUT` | `5s`, maximum `30s` |

`PLATFORM_ACCESS_DATABASE_URL` must not be present in the normal
`identity-service` runtime environment.

`CLERK_JWT_KEY` chứa public JWT verification key từ Clerk và được trim outer
whitespace. Không log hoặc echo key trong config errors. Dummy key trong
`.env.example` là test-only public key, không phải production credential.

`CLERK_ISSUER` được compare exact với verified `iss`. Config không normalize
trailing slash. Origin production phải dùng HTTPS. HTTP chỉ hợp lệ cho
`localhost` hoặc `127.0.0.1`.

`CLERK_AUTHORIZED_PARTIES` là comma-separated origin list. Mỗi item được trim;
blank item, duplicate hoặc non-local HTTP origin bị reject. Phải có ít nhất một
party. Compose truyền cùng value này vào APISIX để standalone YAML interpolate
CORS allowlist; không duy trì allowlist thứ hai.

`CLERK_AUTH_LEEWAY` phải lớn hơn 0 và không quá 30 giây.

`CLERK_WEBHOOK_PROCESS_TIMEOUT` phải lớn hơn 0, không quá 8 giây và nhỏ hơn
`HTTP_WRITE_TIMEOUT`. APISIX webhook `send`/`read` timeout được giữ cố định ở 10
giây, vì vậy webhook processing phải hoàn tất trước gateway timeout.

Webhook max body phải lớn hơn 0 và không quá 5 MiB. Signing secret không được
blank. Config errors không echo secret, JWT key hoặc database URL.

Persistent backend ưu tiên Supabase direct connection; IPv4-only deployment
dùng Supavisor session mode. Không dùng transaction pooler. Migration ưu tiên
direct connection.

## SQL generation

sqlc config version 2 dùng PostgreSQL + `pgx/v5`. Generated code được commit dưới
`internal/store/sqlcgen` và không sửa thủ công.

Current-user queries giữ narrow public projection. Platform-access queries tách
runtime read (`GetPlatformAccessUserByClerkUserID`,
`ListActivePlatformRolesByUserID`) khỏi operator grant/revoke/status operations.
Repository phân biệt `pgx.ErrNoRows` với operational database failure.

```bash
make identity-sqlc-generate
make identity-sqlc-check
```

`identity-sqlc-check` chạy pinned `sqlc/sqlc:1.31.1`, generate lại code và fail
nếu generated output khác Git.

## Local workflow

```bash
cp -n .env.example .env
make stack-up
make gateway-smoke
```

Thay dummy Clerk public key, issuer và authorized parties bằng values của Clerk
instance tương ứng trước khi dùng ngoài local/CI. Không thêm secret key.

APISIX không phụ thuộc Compose vào PostgreSQL/migration/identity-service.
Identity port 8080 và PostgreSQL port 5432 không publish ra host.

## HTTP contracts

Public APISIX contracts vẫn chỉ là:

```text
GET     /api/v1/me
OPTIONS /api/v1/me
GET     /api/v1/identity/health/live
GET     /api/v1/identity/health/ready
POST    /api/v1/identity/webhooks/clerk
```

Private service-to-service contract `/internal/v1/platform-access/me` không có
APISIX route và không phải public API.

Webhook success, duplicate, stale và verified unsupported events đều trả `204`
không body. Invalid signature/payload trả `400`, oversized body trả `413`, và
temporary PostgreSQL failure/timeout trả `503` để Clerk retry.

## Verification

```bash
make repo-check
make identity-sqlc-check

cd service/identity-service
unformatted="$(find . -name '*.go' -type f -print0 | xargs -0 -r gofmt -l)"
test -z "${unformatted}"
go mod tidy -diff
go vet ./...
go test -race -coverprofile=coverage.out ./...
golangci-lint run ./...
go tool cover -func=coverage.out
cd ../..

docker compose config --quiet
docker compose down --remove-orphans --volumes
make stack-up
make gateway-smoke
```

CI chạy real PostgreSQL Identity v1→v2 migration regression, chứng minh existing
users được preserve, không user nào auto-promote, schema constraints/PUBLIC
revocation/internal FK boundary đúng, grant/revoke/re-grant idempotent và
inactive account không được authorize. Full-stack integration dùng cùng một
Clerk session để chứng minh grant có hiệu lực và revoke làm permission biến mất
ở request kế tiếp mà không refresh session. Nó cũng assert forged platform
header bị ignore và internal route trả 404 qua APISIX.

CI generate ephemeral RSA keypairs ngoài Docker build context, inject chỉ public
verification key vào service và sign Clerk-shaped session tokens cho tests. CI
chạy webhook scenarios hiện hữu cùng `/me` scenarios: missing/malformed token,
invalid signature, wrong issuer, wrong authorized party, active, disabled,
deleted, identity not ready, PostgreSQL outage/recovery, no DB mutation, CORS
allowed/disallowed preflight, actual allowed-origin GET, `Cache-Control`, `Vary`,
`WWW-Authenticate`, port bindings và response/log redaction.

## Production operations

Trước khi production operator provisioning được bật, infrastructure phải tạo
một credential riêng cho `PLATFORM_ACCESS_DATABASE_URL` với đúng DML privileges
cần thiết trên platform-access assignment và không đưa credential đó vào normal
Identity runtime. Operator execution cần controlled infrastructure (ví dụ Cloud
Run Job) và secret-managed credential. PR3.5 không deploy job hoặc wire
production secret.

Cross-service capacity, traffic protection, observability, cache, and retention work is tracked in the [production-readiness roadmap](../../docs/production-readiness-roadmap.md). Rotate the configured Clerk verification key with the [Clerk JWT key-rotation runbook](../../docs/runbooks/clerk-jwt-key-rotation.md).

---

## Private metrics and access logs

Identity Service starts a second operational HTTP listener configured by:

```dotenv
METRICS_ADDR=:9090
```

The address must be a non-empty `host:port`, must not include a URL scheme, and must differ from `HTTP_ADDR`. Bind failure stops startup. The listener exposes only `GET /metrics`, is gracefully shut down, is private to the Docker network, and is not routed through APISIX. Prometheus absence or scrape failure does not affect `/health/ready` or request handling.

Identity metrics use a service-owned Prometheus registry and bounded labels:

```text
http_requests_total{service,route,method,status_class}
http_request_duration_seconds{service,route,method}
http_requests_in_flight{service}
clerk_webhook_events_total{service,aggregate="user",outcome}
current_user_cache_operations_total{service,operation,outcome}
database_pool_*{service,pool="runtime"}
```

Webhook outcomes are `processed`, `duplicate`, `stale`, `rejected`, and `retryable_failure`. Processed, duplicate, and stale outcomes are returned by the application transaction flow after the corresponding commit decision.

Every completed application request emits one structured access record containing `service`, `request_id`, `method`, matched chi `route`, `status`, `duration_ms`, and `response_bytes`. Unmatched routes use `unknown`; raw URL paths and query strings are never fallback labels or log fields. Health completion records are written only at debug level; with the normal info threshold they are intentionally absent.

Access logs and metrics exclude Authorization, Cookie, JWTs, webhook bodies, Svix headers, email addresses, Clerk user IDs, local UUIDs, database URLs, request or response bodies, request IDs as metric labels, and raw dependency errors.

Pool defaults remain unchanged until representative load tests establish throughput, latency, replica count, connection wait, and the total Supabase PostgreSQL connection budget. Identity cache-aside is implemented at the `currentuser.Reader` boundary with PostgreSQL fallback and Redis excluded from readiness; production pool sizing remains separate measured work.

### Correction-round telemetry guarantees

The production Clerk webhook route depends on `ProcessWithResult(context.Context, clerkwebhook.Event) (usersync.Result, error)` at compile time. There is no runtime type assertion, fallback to `Process`, or default `processed` outcome. Identity webhook metrics accept only `aggregate=user`.

HTTP metric method labels use the bounded values `GET|POST|PUT|PATCH|DELETE|HEAD|OPTIONS|CONNECT|TRACE|OTHER`; arbitrary methods map to `OTHER`. Access logs retain the actual request method. Pool metrics are collected from `pgxpool.Stat()` at scrape time without database queries or silent panic recovery. Shutdown stops metrics serving before closing PostgreSQL. Health completion records are debug-only and are absent at the normal info log threshold.

## Authenticated current-user cache

`GET /api/v1/me` uses Redis cache-aside at the `currentuser.Reader` boundary:

```text
currentuser.Service
  -> currentuser.CachedReader
  -> Redis GET
       hit: validate and return cached projection
       miss/error/corruption:
         -> read shared generation marker
         -> PostgreSQL Reader
         -> atomic generation-CAS SET
```

PostgreSQL `app.app_users` remains the source of truth. `currentuser.Service` still owns `active`, `disabled`, `deleted`, `identity_not_ready`, and unsupported-status decisions. Redis stores only a schema-versioned JSON projection and never stores Clerk user IDs, JWTs, sessions, webhook payloads, or HTTP response bytes.

Data keys use `bridgeworks:identity:current-user:v1:<sha256>` and generation keys use `bridgeworks:identity:current-user-generation:v1:<sha256>`, so raw provider identifiers are absent. The default data TTL is 30 seconds, with a five-minute configuration maximum. Missing projections and operational errors are not negative-cached. Malformed or unknown-version values are deleted best-effort and fall back to PostgreSQL.

Concurrent misses are coalesced per Identity process. Redis is shared across replicas; coalescing is not a distributed lock. A loader observes the shared generation before its PostgreSQL read and may populate Redis only through an atomic Lua compare-and-set when that generation is unchanged.

The user-sync application service is wrapped by a post-commit invalidation decorator. Successful `processed`, `duplicate`, and `stale` results atomically increment the generation, refresh its 10-minute expiry, and delete the shared data key. Errors and rollbacks do not invalidate. The generation marker outlives the bounded two-second shared PostgreSQL load, so an older loader cannot repopulate a pre-invalidation projection after a successful invalidation.

A Redis invalidation-operation failure never changes the committed webhook result; the data TTL bounds the residual stale window and a sanitized warning plus bounded metric records the failure. The cache does not claim zero-staleness while Redis rejects or times out the invalidation. A generation-CAS rejection is recorded only as bounded `set,stale` telemetry and does not write Redis.

Redis is not pinged at startup and is excluded from readiness. A Redis outage falls back to PostgreSQL. A valid warm cache entry may serve `/me` while PostgreSQL is unavailable, but `/health/ready` still fails because readiness continues to represent PostgreSQL. A cold cache with PostgreSQL unavailable preserves the existing sanitized `503`.

The Redis client uses `MaxRetries=-1` to disable automatic retries and preserve bounded operation timeouts. The external contract remains unchanged, including `Cache-Control: no-store` and `Vary: Authorization`. Production Upstash traffic requires TLS and secret-managed credentials. Local Compose Redis is plaintext private test/development infrastructure and explicitly defaults TLS off for that topology.

See [`../../docs/runbooks/identity-current-user-cache.md`](../../docs/runbooks/identity-current-user-cache.md) for configuration, failure semantics, fencing guarantees, stale-risk analysis, multi-instance behavior, metrics, and cursor-based rollback.
