# identity-service

Identity boundary của BridgeWorks. Service đồng bộ Clerk user events vào local
PostgreSQL projection qua public signed webhook; chưa implement session/JWT
middleware hoặc `GET /api/v1/me`.

## Ownership

Clerk sở hữu authentication, sessions, password/social login, magic links và
email verification. Identity service chỉ sở hữu:

- immutable Clerk user mapping;
- local BridgeWorks lifecycle `active|disabled|deleted`;
- public `id_user`;
- minimal verified primary-email projection;
- transactional Clerk webhook inbox.

`status=disabled` là BridgeWorks-owned và Clerk updates không được re-enable.
`status=deleted` là tombstone; service không hard delete. `primary_email` chỉ là
projection của Clerk primary address khi address đó có verification status
`verified`. Service khác không được đọc schema `app` trực tiếp.

`migrations/000001_create_app_users.sql` là locked schema contract. PR webhook
không sửa migration hoặc thêm migration mới.

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

1. Acquire transaction-level advisory lock theo `clerk_user_id`.
2. Insert inbox row bằng `ON CONFLICT (event_id) DO NOTHING`.
3. Duplicate delivery commit và trả 204 mà không mutate user.
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

Webhook max body phải lớn hơn 0 và không quá 5 MiB. Signing secret không được
blank. Config errors không echo secret hoặc database URL.

Runtime và migration database credentials vẫn tách biệt:

- `DATABASE_URL`: least-privileged persistent runtime role.
- `MIGRATION_DATABASE_URL`: migration/owner role.
- Local có thể dùng cùng role.
- Hosted Supabase/staging/production phải tách credentials khi provisioning hoàn
  tất.
- Persistent backend ưu tiên Supabase direct connection; IPv4-only deployment
  dùng Supavisor session mode. Không dùng transaction pooler. Migration ưu tiên
  direct connection.

## SQL generation

sqlc config version 2 dùng PostgreSQL + `pgx/v5`. Generated code được commit dưới
`internal/store/sqlcgen` và không sửa thủ công.

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

APISIX không phụ thuộc Compose vào PostgreSQL/migration/identity-service.
Identity port 8080 và PostgreSQL port 5432 không publish ra host.

## HTTP contracts

```text
GET  /api/v1/identity/health/live
GET  /api/v1/identity/health/ready
POST /api/v1/identity/webhooks/clerk
```

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
cd ../..

docker compose config --quiet
docker compose down --remove-orphans --volumes
make stack-up
make gateway-smoke
```

CI gửi signed fixtures qua APISIX và verify invalid signature, create, duplicate,
update, disabled ownership, delete, stale ordering, unknown-user tombstone,
database outage/retry, port bindings và response/log redaction.
