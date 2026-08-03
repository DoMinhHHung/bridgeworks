# identity-service

Identity boundary của BridgeWorks. Runtime foundation này chỉ cung cấp health
endpoints; chưa có Clerk, PostgreSQL, migration execution hoặc `/api/v1/me`.

## Boundary

Service sở hữu Clerk user mapping, local account lifecycle, public `id_user`,
minimal primary email projection và Clerk webhook inbox. Service không sở hữu
password, session, magic link, email verification, organization hoặc talent.

Migration `migrations/000001_create_app_users.sql` là locked contract và không
được runtime bootstrap tự động apply.

## Requirements

- Go 1.26.5
- Docker Engine với Docker Compose plugin
- `golangci-lint` v2.11 khi chạy lint local

## Configuration

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

Duration phải dùng Go duration syntax và phải lớn hơn zero. `LOG_LEVEL` chỉ
nhận `debug`, `info`, `warn`, `error`. Config sai làm process fail ngay lúc
startup.

## Run service locally

```bash
cd services/identity
cp .env.example .env
set -a
source .env
set +a
go run ./cmd/identity-service
```

Direct service verification:

```bash
curl -i http://127.0.0.1:8080/health/live
curl -i http://127.0.0.1:8080/health/ready
```

## Run through APISIX

From repository root:

```bash
cp -n .env.example .env
docker compose up -d --build
make gateway-smoke
```

Public gateway routes:

```bash
curl -i http://127.0.0.1:9080/api/v1/identity/health/live
curl -i http://127.0.0.1:9080/api/v1/identity/health/ready
```

`identity-service` chỉ dùng Docker `expose`; host không publish port 8080.
APISIX gọi `identity-service:8080` qua private `bridgeworks` network.

## Verify

Repository root:

```bash
make repo-check
docker compose config --quiet
```

Service module:

```bash
cd services/identity
unformatted="$(find . -name '*.go' -type f -print0 | xargs -0 -r gofmt -l)"
test -z "${unformatted}" || { printf '%s\n' "${unformatted}"; exit 1; }
go mod tidy -diff
go vet ./...
go test -race ./...
golangci-lint run ./...
```

Full container verification:

```bash
docker compose up -d --build
make gateway-smoke
curl -i http://127.0.0.1:9080/api/v1/identity/health/live
curl -i http://127.0.0.1:9080/api/v1/identity/health/ready
docker compose down --remove-orphans
```

## Health semantics

- `/health/live`: process đang sống và HTTP stack phản hồi.
- `/health/ready`: process đã load config, dựng router và sẵn sàng nhận traffic.

Slice này chưa có external dependency, nên readiness chưa probe PostgreSQL.
Khi thêm DB, readiness phải phản ánh dependency cần thiết thay vì luôn trả 200.

Mọi response có `X-Request-Id`. Service reuse request ID hợp lệ do APISIX gửi;
nếu thiếu hoặc không hợp lệ thì generate UUIDv4 mới. Error response dùng shape:

```json
{
  "code": "not_found",
  "message": "route not found",
  "request_id": "7d77b7a8-37a6-4f27-98f1-b5dd80753a87",
  "details": null
}
```
