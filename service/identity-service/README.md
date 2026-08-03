# identity-service

Runtime foundation cho BridgeWorks identity boundary. Service hiện chỉ expose
liveness và readiness; chưa có Clerk, PostgreSQL, migration runtime, Redis,
RabbitMQ hoặc `/api/v1/me`.

## Requirements

- Go 1.26.5
- Docker Engine với Docker Compose plugin
- `golangci-lint` v2.11 khi chạy lint local

## Run local

```bash
cd service/identity-service
go mod download
go run ./cmd/identity-service
```

Defaults:

```text
SERVICE_NAME=identity-service
HTTP_ADDR=:8080
HTTP_READ_HEADER_TIMEOUT=5s
HTTP_READ_TIMEOUT=15s
HTTP_WRITE_TIMEOUT=15s
HTTP_IDLE_TIMEOUT=60s
SHUTDOWN_TIMEOUT=10s
LOG_LEVEL=info
```

Override bằng environment, ví dụ:

```bash
HTTP_ADDR=:8081 LOG_LEVEL=debug go run ./cmd/identity-service
```

Invalid duration hoặc invalid log level làm startup fail ngay.

## Verify service directly

```bash
curl -i http://127.0.0.1:8080/health/live
curl -i http://127.0.0.1:8080/health/ready
curl -i -H 'X-Request-Id: local-check' http://127.0.0.1:8080/health/live
```

Expected JSON:

```json
{"status":"ok","service":"identity-service"}
```

Mọi response có `X-Request-Id`. Incoming header từ APISIX được tái sử dụng.

## Quality checks

```bash
gofmt -w ./cmd ./internal
go mod tidy -diff
go vet ./...
go test -race ./...
golangci-lint run ./...
```

## Verify through Docker Compose and APISIX

Từ repository root:

```bash
make repo-check
docker compose up -d --build
make gateway-smoke
curl -i http://127.0.0.1:9080/api/v1/identity/health/live
curl -i http://127.0.0.1:9080/api/v1/identity/health/ready
docker compose down --remove-orphans
```

`identity-service` chỉ dùng Docker `expose`; port 8080 không được publish ra host.
APISIX gọi service qua private network `bridgeworks`.
