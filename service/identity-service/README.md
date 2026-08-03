# identity-service

Runtime foundation for the BridgeWorks identity boundary. The current service
only exposes liveness and readiness endpoints. It does not implement Clerk
integration, database connectivity, migration execution, messaging, or
`/api/v1/me`.

## Intentional module layout

The repository directory and Go module path intentionally use different names:

```text
Repository directory: service/identity-service
Go module path:       github.com/DoMinhHHung/bridgeworks/services/identity
```

Do not change imports to match the physical directory. Verify the authoritative
module path with:

```bash
cd service/identity-service
go list -m
```

Expected output:

```text
github.com/DoMinhHHung/bridgeworks/services/identity
```

## Identity boundary

Identity service owns:

- Clerk user to BridgeWorks user mapping.
- Local account lifecycle.
- Public `id_user`.
- Minimal verified primary email projection.
- Clerk webhook transactional inbox.

Identity service does not own:

- Passwords.
- Sessions.
- Magic links.
- MFA.
- Email verification.
- Organization membership.
- Talent profiles.
- Hiring intents.

Architectural rules:

- Clerk is the source of truth for authentication.
- Identity service stores only a local identity projection.
- Other services must not query identity-owned tables directly.
- Cross-service access must use an API or a versioned event.
- `migrations/000001_create_app_users.sql` is the locked authoritative identity
  database contract.
- Do not modify that migration without an explicit schema decision.

## Requirements

- Go 1.26.5.
- Docker Engine with the Docker Compose plugin.
- `golangci-lint` v2.11 for local linting.

## Run locally

```bash
cd service/identity-service
go mod download
go run ./cmd/identity-service
```

Default configuration:

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

Override configuration with environment variables, for example:

```bash
HTTP_ADDR=:8081 LOG_LEVEL=debug go run ./cmd/identity-service
```

An invalid duration or log level causes startup to fail immediately.

## Health endpoints

Service-internal paths:

```text
GET /health/live
GET /health/ready
```

Public paths through APISIX:

```text
GET /api/v1/identity/health/live
GET /api/v1/identity/health/ready
```

Successful response:

```json
{"status":"ok","service":"identity-service"}
```

Every response includes `X-Request-Id`. An incoming value from APISIX is
preserved. Routine successful health probes are excluded from the normal HTTP
access log, while panic and internal-error logs are still emitted.

## Error contract

Errors use the locked top-level envelope:

```json
{
  "code": "stable_machine_code",
  "message": "human-readable message",
  "request_id": "request identifier",
  "details": {}
}
```

`request_id` and `details` are always present. Internal errors, panic values,
and stack traces are never returned in HTTP responses.

## Direct verification

With the service running locally:

```bash
curl -i http://127.0.0.1:8080/health/live
curl -i http://127.0.0.1:8080/health/ready
curl -i \
  -H 'X-Request-Id: local-check' \
  http://127.0.0.1:8080/health/live
```

## Go quality checks

```bash
cd service/identity-service
test -z "$(gofmt -l .)"
go mod tidy -diff
go vet ./...
go test -race ./...
golangci-lint run ./...
go list -m
```

## Docker Compose and APISIX verification

From the repository root:

```bash
make repo-check
make stack-up
make gateway-smoke
make identity-smoke
make stack-down
```

Target responsibilities:

- `gateway-up` starts APISIX only.
- `stack-up` builds and starts the complete local stack.
- `gateway-smoke` tests only APISIX `/healthz`.
- `identity-smoke` tests identity live and ready routes through APISIX.
- `stack-down` stops containers and removes orphans.

`identity-service` uses Docker `expose`; port 8080 is not published to the
host. APISIX calls the service over the private `bridgeworks` network. APISIX
has no Compose lifecycle dependency on identity-service and can start while an
upstream is unavailable.
