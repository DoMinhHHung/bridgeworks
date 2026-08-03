# BridgeWorks repository bootstrap — identity first

## 1. Current target

Bootstrap only:

- APISIX standalone gateway.
- GitHub Actions CI.
- Repository conventions.
- Locked first identity schema.
- Identity-specific ChatGPT Project Instructions.

No identity HTTP handler or Go module is created yet.

## 2. Apply files

From the repository root:

```bash
unzip ~/Downloads/bridgeworks-repo-setup-v2.zip -d /tmp
cp -a /tmp/bridgeworks-repo-setup-v2/. .
chmod +x gateway/apisix/scripts/smoke-test.sh
cp -n .env.example .env
```

Review before running:

```bash
git status
git diff -- . ':!.env'
```

## 3. Validate

```bash
make repo-check
```

## 4. Start APISIX

```bash
make gateway-up
make gateway-smoke
```

Inspect when needed:

```bash
make gateway-logs
curl -i http://127.0.0.1:9080/healthz
```

Stop:

```bash
make gateway-down
```

## 5. Expected health response

```json
{"status":"ok","component":"apisix","project":"bridgeworks"}
```

The response must include `X-Request-Id`.

## 6. Commit

```bash
git add .
git status
git commit -m "chore: bootstrap gateway, CI, and identity contract"
git push origin main
```

Verify the three GitHub Actions jobs:

- Repository checks.
- APISIX smoke test.
- Go services (currently skips because no `go.mod` exists).

## 7. Locked identity decisions

- Clerk handles magic links and email verification.
- `status` is only the BridgeWorks lifecycle.
- UUIDv7 is generated in Go, not PostgreSQL.
- `id_user` format is fixed and generated in `Asia/Ho_Chi_Minh`.
- `app` schema is owned exclusively by identity-service.
- `app.clerk_webhook_events` is the transactional inbox for webhook deduplication.
- Other services may not read `app.app_users` directly.
- The migration is a contract, not yet applied to Supabase during bootstrap.

## 8. Next task

After CI is green, implement only the first identity vertical slice. Do not add
RabbitMQ, Redis, organization-service or talent-service during this step.
