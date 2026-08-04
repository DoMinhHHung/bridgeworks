# Clerk JWT Verification-Key Rotation Runbook

## Scope

This runbook covers rotation of the configured Clerk public JWT verification key for:

- `identity-service`;
- `organization-service`.

It does not contain key material. Never paste private keys, public key bodies, JWTs, Clerk secrets, or screenshots containing secrets into this document, pull requests, issues, chat, or logs.

## Preconditions

Before starting:

1. Confirm the Clerk instance and issuer used by the target environment.
2. Confirm both services use the same intended Clerk instance.
3. Confirm access to the environment's secret manager or deployment configuration.
4. Confirm the current deployment is healthy and the existing valid-token and invalid-token smoke tests pass.
5. Identify the rollback deployment revision and the previous verification-key secret version.
6. Schedule the rotation during a period when authentication errors and rollback can be monitored.

## Obtain and validate the new public verification key

1. Obtain the new public verification key from the authoritative Clerk administration surface or approved secret-distribution process.
2. Verify the value is public verification material, not a private signing key or API secret.
3. Validate that the key parses using the same Clerk SDK configuration path used by the services.
4. Confirm the configured `CLERK_ISSUER` remains correct.
5. Confirm `CLERK_AUTHORIZED_PARTIES` remains unchanged unless a separate reviewed change requires it.
6. Store the new value as a new version in the environment's secret manager. Do not overwrite or delete the previous version yet.

A validation failure is a stop condition. Do not deploy malformed or ambiguously sourced key material.

## Current overlap limitation

The current design accepts one static configured verification key per service process. It does not provide a built-in old-and-new key overlap window.

Consequences:

- tokens signed only by the new key fail on replicas still using the old key;
- tokens signed only by the old key fail on replicas already using the new key;
- a long mixed-version rollout can produce intermittent `token_invalid` responses;
- changing Clerk signing behavior before enough application replicas use the new key can cause an authentication outage.

Coordinate the Clerk-side activation and application rollout tightly. A future design may add bounded multi-key or JWKS-based verification, but that is outside this runbook.

## Update configuration

1. Update the versioned secret or environment configuration consumed as `CLERK_JWT_KEY` by both services.
2. Confirm no key material appears in deployment diffs, command history, CI output, or rendered manifests accessible to unauthorized users.
3. Do not change webhook signing secrets as part of JWT verification-key rotation.
4. Do not change issuer or authorized-party configuration unless separately reviewed.

## Rolling deployment order

Use a controlled rolling deployment:

1. Deploy `identity-service` with the new verification-key secret version.
2. Wait for Identity rollout health checks to pass.
3. Run Identity valid-token and invalid-token smoke tests.
4. Deploy `organization-service` with the same new verification-key secret version.
5. Wait for Organization rollout health checks to pass.
6. Run Organization valid-token, missing-organization-context, and invalid-token smoke tests.
7. Confirm Organization can still call Identity `/me` for a valid authenticated request.

Do not update every environment simultaneously. Complete and observe one environment before proceeding to the next.

If the platform or Clerk rotation procedure requires the signing-key switch before application rollout, reduce the mixed-version window as much as possible and prepare immediate rollback.

## Smoke tests

Run tests without printing tokens or key material.

### Identity Service

Verify:

- a currently valid Clerk token receives the expected `/api/v1/me` response;
- an invalidly signed token receives `401` with the deterministic error envelope;
- a malformed Bearer credential receives `401`;
- a disabled or deleted local account still receives the expected authorization denial;
- health endpoints remain healthy.

### Organization Service

Verify:

- a valid token with active organization context resolves the current organization;
- the same valid token resolves the current membership;
- a valid token without active organization context receives the expected `409`;
- an invalidly signed token receives `401`;
- the private Identity dependency call succeeds for a valid token;
- health and Clerk webhook endpoints remain available.

Never log or echo the token values used by these tests.

## Monitoring

During and after rollout, monitor:

- sudden increases in `token_invalid` or generic `401` responses;
- differences between Identity and Organization authentication failure rates;
- rollout replica health;
- Organization-to-Identity dependency failures;
- user reports of intermittent authentication;
- Clerk-side signing or key-activation status.

A sharp increase in invalid-token responses immediately after rollout is a stop condition. Do not assume clients are at fault until key-version consistency is verified across replicas and services.

## Rollback

Rollback when valid tokens fail, authentication errors increase unexpectedly, or the two services use inconsistent verification-key versions.

1. Stop further rollout.
2. Restore the previous `CLERK_JWT_KEY` secret version for the affected service or services.
3. Roll back both services when their configured versions may differ.
4. If the Clerk-side signing key was already switched, coordinate reversal or reactivation according to Clerk's supported procedure.
5. Re-run valid-token and invalid-token smoke tests for Identity and Organization.
6. Confirm `token_invalid` rates return to baseline.
7. Preserve deployment and metric timestamps for incident review, but do not capture token or key material.

Do not delete the previous secret version until the rotation is complete, observed, and formally closed.

## Completion criteria

The rotation is complete only when:

- all Identity replicas use the intended verification-key version;
- all Organization replicas use the same intended version;
- valid-token and invalid-token smoke tests pass for both services;
- Organization-to-Identity authenticated calls pass;
- authentication error rates remain at baseline through the observation window;
- rollback material remains available for the agreed retention period;
- the operational change record identifies versions and timestamps without containing key material.
