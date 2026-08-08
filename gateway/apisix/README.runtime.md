# APISIX runtime portability

`gateway/apisix/Dockerfile` builds one immutable BridgeWorks gateway image. The same image is selected at startup for either Render staging or Cloud Run production with `RUNTIME_TARGET`.

Local Docker Compose remains separate from the deployable image contract and continues to mount `conf/config.yaml` and `conf/apisix.yaml`.

## Shared artifact contract

Supported runtime targets are exactly:

```text
RUNTIME_TARGET=render
RUNTIME_TARGET=cloud-run
```

Missing or unknown targets fail startup. Both platforms must configure the APISIX container port as `9080`; a different `PORT` fails startup.

The public route surface is maintained once in `conf/routes.runtime.yaml`. Runtime generation combines that shared route file with a small target-specific upstream profile. Cloud Run generation injects `google-cloud-run-auth` on private-service routes; Render generation never injects or registers that plugin.

## Render staging

Required environment variables:

- `RUNTIME_TARGET=render`
- `PORT=9080`
- `IDENTITY_SERVICE_HOST`: private Render hostname only, without scheme, path, query, or fragment.
- `IDENTITY_SERVICE_PORT`: numeric TCP port in `1..65535`.
- `IDENTITY_SERVICE_SCHEME`: `http` or `https`.
- `ORGANIZATION_SERVICE_HOST`: private Render hostname only, without scheme, path, query, or fragment.
- `ORGANIZATION_SERVICE_PORT`: numeric TCP port in `1..65535`.
- `ORGANIZATION_SERVICE_SCHEME`: `http` or `https`.
- `CLERK_AUTHORIZED_PARTIES`: comma-separated browser origins used by gateway CORS routes.

Render mode does not require Cloud Run audiences, Google metadata identity, or `X-Serverless-Authorization`.

## Cloud Run production

Required environment variables:

- `RUNTIME_TARGET=cloud-run`
- `PORT=9080`
- `IDENTITY_SERVICE_HOST`: private Identity `*.run.app` hostname without scheme or path.
- `IDENTITY_SERVICE_AUDIENCE`: canonical private Identity `https://*.run.app` origin without a path.
- `ORGANIZATION_SERVICE_HOST`: private Organization `*.run.app` hostname without scheme or path.
- `ORGANIZATION_SERVICE_AUDIENCE`: canonical private Organization `https://*.run.app` origin without a path.
- `CLERK_AUTHORIZED_PARTIES`: comma-separated browser origins used by gateway CORS routes.

Cloud Run upstreams remain HTTPS on port `443`. The custom `google-cloud-run-auth` plugin obtains a Google-signed ID token from the metadata server and sets `X-Serverless-Authorization`. Metadata/token acquisition failure remains fail closed.

The APISIX runtime service account should have `roles/run.invoker` only on the private Identity and Organization services. Do not create or mount a service-account key.

## Gateway security invariants

Both targets keep:

- public health at `GET /health/live`;
- Admin API disabled;
- Control API disabled;
- request ID generation/preservation;
- the same public route IDs, methods, external URIs, CORS configuration, and proxy rewrites;
- `/internal/v1/platform-access/me` absent from the gateway route surface;
- access logs that do not include Authorization or Google identity-token headers.

Private upstream retries remain disabled to avoid replaying POST webhooks after ambiguous upstream responses.

## Validation

Run:

```text
bash gateway/apisix/scripts/validate-runtime-image.sh
```

The validation builds the APISIX image once, starts that exact image in both runtime modes, validates strict runtime input handling, checks generated route-surface parity, verifies `/health/live` and `X-Request-Id`, confirms the private Identity route is not exposed, preserves Cloud Run metadata failure behavior, and scans logs for credential leakage.
