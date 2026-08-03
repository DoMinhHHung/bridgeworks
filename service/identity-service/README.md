# identity-service

Identity boundary của BridgeWorks. PR foundation này thêm PostgreSQL runtime,
database-backed readiness và standalone migration binary; chưa có Clerk,
business logic, repository hoặc `/api/v1/me`.

## Boundary and ownership

Service sở hữu schema `app`, Clerk user mapping, local account lifecycle, public
`id_user`, minimal primary email projection và Clerk webhook inbox. Service
không sở hữu password, session, magic link, email verification, organization
hoặc talent. Service khác không được đọc database identity trực tiếp.

`migrations/000001_create_app_users.sql` vẫn là locked schema contract.
`identity-service` không tự chạy migration lúc startup. DDL chỉ được apply qua
standalone `identity-migrate`.

## Requirements

- Go 1.26.5
- Docker Engine với Docker Compose plugin
- `golangci-lint` v2.11 khi chạy lint local

## Database credentials

- `DATABASE_URL`: runtime role cho persistent identity-service. Hosted
  staging/production nên là least-privileged application role.
- `MIGRATION_DATABASE_URL`: migration/owner role có quyền DDL cần thiết.
- Local development dùng cùng một role cho đơn giản.
- Hosted Supabase/staging/production phải tách hai credentials khi provisioning
  hoàn tất.
- Không commit credentials hoặc database URLs thật.

Với hosted Supabase:

- Persistent backend ưu tiên direct connection.
- Deployment IPv4-only dùng Supavisor session mode.
- Không dùng transaction pooler cho persistent identity-service.
- Migration URL ưu tiên direct connection.

## Runtime configuration

| Variable | Default |
| --- | --- |
| `SERVICE_NAME` | `identity-service` |
| `HTTP_ADDR` | `:8080` |
| `HTTP_READ_HEADER_TIMEOUT` | `5s` |
| `HTTP_READ_TIMEOUT` | `15s` |
| `HTTP_WRITE_TIMEOUT` | `15s` |
| `HTTP_IDLE_TIMEOUT` | `60s` |
| `SHUTDOWN_TIMEOUT` | `10s` |
| `LOG_LEVEL` | `info` |
| `DATABASE_URL` | required |
| `DATABASE_CONNECT_TIMEOUT` | `5s` |
| `DATABASE_READINESS_TIMEOUT` | `2s` |
| `DATABASE_MAX_CONNS` | `5` |
| `DATABASE_MIN_CONNS` | `0` |
| `DATABASE_MAX_CONN_LIFETIME` | `30m` |
| `DATABASE_MAX_CONN_IDLE_TIME` | `5m` |
| `DATABASE_HEALTH_CHECK_PERIOD` | `1m` |

All durations phải lớn hơn zero. `DATABASE_MAX_CONNS` phải lớn hơn zero;
`DATABASE_MIN_CONNS` phải từ zero đến max. Config sai làm process fail startup.
Errors không echo database URL.

## Migration configuration

| Variable | Default |
| --- | --- |
| `MIGRATION_DATABASE_URL` | required |
| `MIGRATION_TIMEOUT` | `1m` |
| `LOG_LEVEL` | `info` |

Migration binary chỉ hỗ trợ:

```text
up
status
version
```

Không expose `down`, `reset`, `redo` hoặc destructive command. Goose version
table là `app.goose_db_version`. Binary tạo schema `app` trước khi Goose
initialize version table; business tables chỉ được tạo từ SQL migrations.

## Local Compose workflow

From repository root:

```bash
cp -n .env.example .env
make stack-up
docker compose ps
make gateway-smoke
```

`stack-up` start PostgreSQL, chạy migration one-shot, rồi start identity-service
và APISIX. PostgreSQL và identity-service không publish host ports.

Migration commands:

```bash
make identity-migrate-up
make identity-migrate-status
make identity-migrate-version
```

Database operations:

```bash
make identity-db-logs
make identity-db-shell
```

Không có destructive reset target.

## Health semantics

- `/health/live`: luôn 200 khi process và HTTP stack còn sống; không ping DB.
- `/health/ready`: ping PostgreSQL với `DATABASE_READINESS_TIMEOUT`.
- Database unavailable/timeout trả 503 với redacted envelope:

```json
{
  "code": "service_unavailable",
  "message": "service is not ready",
  "request_id": "database-down-ready",
  "details": null
}
```

## Verification

```bash
make repo-check

cd service/identity-service
unformatted="$(find . -name '*.go' -type f -print0 | xargs -0 -r gofmt -l)"
test -z "${unformatted}" || { printf '%s\n' "${unformatted}"; exit 1; }
go mod tidy -diff
go vet ./...
go test -race -coverprofile=coverage.out ./...
golangci-lint run ./...
cd ../..

docker compose config --quiet
docker compose down --remove-orphans --volumes

make gateway-up
curl -i http://127.0.0.1:9080/healthz

docker compose up -d identity-postgres
docker compose run --rm identity-migrate up
docker compose run --rm identity-migrate up
docker compose run --rm identity-migrate version
docker compose run --rm identity-migrate status

make stack-up
docker compose ps
make gateway-smoke

docker compose stop identity-postgres
curl -i http://127.0.0.1:9080/api/v1/identity/health/live
curl -i http://127.0.0.1:9080/api/v1/identity/health/ready
docker compose start identity-postgres
make gateway-smoke
```

APISIX không có `depends_on`; `/healthz` phải hoạt động ngay cả khi PostgreSQL,
migration hoặc identity-service chưa start.
