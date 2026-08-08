# CI/CD Artifact Pipeline

This document defines the source-level artifact foundation for BridgeWorks. It does not deploy staging or production resources.

## Invariant: build once, promote by digest

The development flow is:

```text
feature/*
  -> pull request to dev
  -> required validation
  -> squash/merge to protected dev
  -> resolve or build immutable component artifacts once
  -> Artifact Registry
  -> future staging deployment by exact IMAGE@sha256:... reference
  -> future pull request dev -> main
  -> future production promotion of the same digests
```

`main` is promotion territory. The artifact workflow never publishes on a `main` push and never publishes from a feature branch or pull request.

The three deployable images are:

- `identity-service`
- `organization-service`
- `apisix-gateway`

The existing Dockerfiles and build contexts remain authoritative:

| Component | Dockerfile/build context | Image name |
| --- | --- | --- |
| Identity | `service/identity-service` | `identity-service` |
| Organization | `service/organization-service` | `organization-service` |
| APISIX | `gateway/apisix` | `apisix-gateway` |

Identity remains one image containing `identity-service`, `identity-migrate`, and `identity-platform-access`. Organization remains one image containing `organization-service`, `organization-migrate`, and `organization-maintenance`.

## Source identity versus artifact identity

A trusted `dev` publication records four different identities and they must not be conflated:

- `source_commit_sha`: `git rev-parse HEAD`
- `source_tree_sha`: `git rev-parse HEAD^{tree}`
- per-component `component_tree_sha`: `git rev-parse HEAD:<build-context>`
- OCI image `digest`: the remote `sha256:...` resolved from Artifact Registry

The Git tree SHAs identify source content. The OCI image digest is the authoritative runtime artifact identity.

The full repository tree SHA is required because a future squash merge from `dev` to `main` creates a different commit SHA even when the repository tree is identical. Production promotion must compare the `main` tree with the tested release `source_tree_sha`; it must not assume the `dev` and `main` commit SHAs are equal.

## Component detection and content-addressed reuse

PR validation maps changed paths to build contexts:

- `service/identity-service/**` -> Identity
- `service/organization-service/**` -> Organization
- `gateway/apisix/**` -> APISIX
- artifact-pipeline workflow/helper changes -> all three components (`force_all=true`)
- unrelated docs-only changes -> no runtime image build

The trusted `dev` publication resolves all three components on every successful run so that the release manifest is always complete.

Each component uses the content-addressed tag:

```text
tree-<full-component-tree-sha>
```

For each component the trusted publication does this:

1. Compute the current component tree SHA.
2. Query the exact tree tag in Artifact Registry.
3. If the tag resolves to one strict `sha256:<64 lowercase hex>` digest, reuse it and do not rebuild.
4. If the tag is absent, build the component once, re-check that the tag is still absent, push it, then resolve the remote digest.
5. If lookup is ambiguous, malformed, or fails for a reason other than a clear not-found result, fail closed.
6. Verify the remote digest reference exists before accepting the component result.

The dev publication uses one non-cancelling concurrency group. Individual successfully pushed content-addressed artifacts may remain after a later component fails. Cleanup is deliberately non-destructive: a rerun reuses already-resolved tree tags.

For defense in depth, configure the Artifact Registry repository with immutable tags. That prevents any actor from moving an existing `tree-...` tag to a different digest outside this workflow.

## Pull-request security boundary

`pull_request` execution is validation-only:

- no Artifact Registry write;
- no Google OIDC publisher job;
- no deployment credentials;
- no Cloud Run mutation;
- no Secret Manager mutation;
- no staging or production deployment.

Relevant components are built locally with `docker build` and are never pushed. The stable required gate `Artifact build required` exists on every pull request to `dev` or `main`, including docs-only changes.

The workflow does not use `pull_request_target` for trusted execution.

## Trusted dev publication

Publication is reachable only when both are true:

```text
github.event_name == 'push'
github.ref == 'refs/heads/dev'
```

The job requests only:

```yaml
permissions:
  contents: read
  id-token: write
```

`id-token: write` is scoped to the trusted publish job. Pull-request jobs do not receive it.

The registry host is derived from `GCP_REGION` as `<region>-docker.pkg.dev`. Project, region, and repository identifiers are not duplicated in source.

## GitHub repository variables

Configure these non-secret repository or protected-environment variables before the first trusted `dev` publication:

- `GCP_PROJECT_ID`
- `GCP_REGION`
- `GCP_ARTIFACT_REGISTRY_REPOSITORY`
- `GCP_WORKLOAD_IDENTITY_PROVIDER`
- `GCP_ARTIFACT_WRITER_SERVICE_ACCOUNT`

No service-account JSON key, base64 key, long-lived GCP credential, database URL, Clerk secret, or access token belongs in repository source or the release manifest.

## External Google Cloud prerequisites

These are operator/infrastructure prerequisites. PR1 does not create or mutate them.

1. Create or select the Artifact Registry Docker repository identified by the variables above.
2. Create a dedicated artifact-publisher Google service account. Do not give it Cloud Run Admin, Secret Manager Admin, Owner, or Editor merely for convenience.
3. Grant that service account `roles/artifactregistry.writer` at the specific Artifact Registry repository, not broadly at project level when repository-level binding is sufficient. The workflow needs read/list/tag resolution and upload authority, but no deployment authority.
4. Configure a GitHub OIDC Workload Identity Pool/Provider and map repository/ref claims needed for authorization.
5. Restrict provider trust to `DoMinhHHung/bridgeworks` (repository ID `1321399390` can be used as an additional stable claim where supported). Do not allow arbitrary repositories or fork pull requests to mint the publisher identity.
6. Restrict service-account impersonation so publisher authority is obtainable only by the trusted BridgeWorks `dev` workflow/ref. The cloud-side condition should include the repository identity and `refs/heads/dev`; constraining `job_workflow_ref` to the artifact workflow is recommended as an additional boundary.
7. Grant the restricted federated principal `roles/iam.workloadIdentityUser` on only the dedicated artifact-publisher service account.
8. Enable immutable Artifact Registry tags for the release repository as defense in depth for `tree-...` content-addressed tags.

Do not create service-account JSON keys for this pipeline.

## Release manifest

A trusted `dev` publication emits a complete machine-readable manifest only after all three components have exact verified digests:

```json
{
  "schema_version": 1,
  "repository": "DoMinhHHung/bridgeworks",
  "source_branch": "dev",
  "source_commit_sha": "<40 lowercase hex>",
  "source_tree_sha": "<40 lowercase hex>",
  "components": {
    "identity": {
      "component_tree_sha": "<40 lowercase hex>",
      "image": "<region>-docker.pkg.dev/<project>/<repository>/identity-service",
      "digest": "sha256:<64 lowercase hex>",
      "reference": "<region>-docker.pkg.dev/<project>/<repository>/identity-service@sha256:<64 lowercase hex>",
      "built": true
    },
    "organization": {
      "component_tree_sha": "<40 lowercase hex>",
      "image": "<region>-docker.pkg.dev/<project>/<repository>/organization-service",
      "digest": "sha256:<64 lowercase hex>",
      "reference": "<region>-docker.pkg.dev/<project>/<repository>/organization-service@sha256:<64 lowercase hex>",
      "built": false
    },
    "apisix": {
      "component_tree_sha": "<40 lowercase hex>",
      "image": "<region>-docker.pkg.dev/<project>/<repository>/apisix-gateway",
      "digest": "sha256:<64 lowercase hex>",
      "reference": "<region>-docker.pkg.dev/<project>/<repository>/apisix-gateway@sha256:<64 lowercase hex>",
      "built": false
    }
  }
}
```

`built` means the image was built and pushed during this workflow run. `false` means the existing content-addressed artifact was safely reused.

The manifest is uploaded as a GitHub Actions artifact named `bridgeworks-release-<source-commit-sha>`. That Actions artifact is metadata only; it is not the OCI image and is never the deployment authority.

The job summary prints only source/tree SHAs, digest-pinned image references, and built/reused state. It must not print OIDC tokens, Google access tokens, credentials, Clerk secrets, webhook bodies, or database URLs.

## Partial failure and reruns

A successful release manifest is all-or-nothing. Example:

```text
Identity push succeeds
Organization resolves successfully
APISIX push fails
```

The workflow fails and emits no successful release manifest. The already-pushed Identity artifact is left intact. On rerun, the same `tree-<sha>` tag resolves to its existing digest and Identity is reused instead of rebuilt.

An existing tree tag with an invalid or ambiguous digest is a hard failure. The pipeline never rebuilds over ambiguity.

## Base-aware pull-request validation

Migration and locked-scope guards use immutable GitHub event data to find the actual PR base SHA. Therefore:

```text
feature/* -> dev  compares against the dev PR base SHA
dev -> main       compares against the main PR base SHA
```

They no longer treat `main` as the universal development baseline. Existing migration files remain protected; this PR adds or edits no migration.

For push/manual fallback paths, `.github/scripts/ci-base-sha.sh` uses deterministic event/history data rather than a client-provided ref.

## Stable required gates

Two additive gates are introduced:

- `Artifact build required`
- `APISIX Cloud Run required`

They are designed to exist on every relevant pull request. Expensive APISIX validation may be skipped when irrelevant, but the final required gate still reports success only after detection succeeds and any required validation succeeds.

The existing 12 required job names are unchanged.

## Future staging and production

PR1 intentionally stops at artifact publication metadata.

A future staging workflow must read a successful dev release manifest and deploy exact `IMAGE@sha256:...` references. It must not rebuild.

A future production promotion workflow must first prove the `main` repository tree equals the tested `source_tree_sha`, then promote the exact same component digests. It must not infer artifact identity from a `main` commit SHA and must not rebuild for production.

No staging deployment, production deployment, hosted database migration, Cloud Run mutation, or Secret Manager mutation is implemented by this PR.
