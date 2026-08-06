# APISIX on Cloud Run

This directory contains the production APISIX image used as the public BridgeWorks edge.
Local Docker Compose continues to use `conf/config.yaml` and `conf/apisix.yaml`.

## Deployment contract

Cloud Run must be configured with container port `9080`. The image intentionally exits during startup when `PORT` is not `9080`.

Required environment variables:

- `IDENTITY_SERVICE_HOST`: private Identity Cloud Run `run.app` hostname without scheme or path.
- `IDENTITY_SERVICE_AUDIENCE`: canonical private Identity Cloud Run HTTPS origin without a path.
- `ORGANIZATION_SERVICE_HOST`: private Organization Cloud Run `run.app` hostname without scheme or path.
- `ORGANIZATION_SERVICE_AUDIENCE`: canonical private Organization Cloud Run HTTPS origin without a path.
- `CLERK_AUTHORIZED_PARTIES`: comma-separated browser origins accepted by gateway CORS routes.

Example deployment flags:

```text
--port=9080
--allow-unauthenticated
--service-account=apisix-runtime@PROJECT_ID.iam.gserviceaccount.com
```

The APISIX runtime service account must have `roles/run.invoker` only on the private Identity and Organization services. Do not create or mount a service-account key.

## Authentication headers

APISIX preserves the incoming Clerk session token in:

```text
Authorization: Bearer <Clerk JWT>
```

The custom `google-cloud-run-auth` plugin obtains a Google-signed ID token from the Cloud Run metadata server and overwrites:

```text
X-Serverless-Authorization: Bearer <Google ID token>
```

The token is cached per APISIX worker. Failed refresh attempts use a bounded retry backoff while a cached token remains valid. Token values must never be logged.

## Retry policy

Private upstream retries are disabled. This prevents the gateway from replaying POST webhooks after an ambiguous upstream response. Clerk remains responsible for webhook delivery retries, while both services retain event-ID deduplication as defense in depth.

## Validation

Run:

```text
bash gateway/apisix/scripts/validate-cloud-run-image.sh
```

The validation builds the image, verifies non-root execution and pinned dependencies, checks startup configuration, isolates the container from the metadata server, confirms fail-closed behavior, and scans logs for credential leakage.
