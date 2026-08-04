# BridgeWorks Load-Test Harness

This directory contains the Dockerized, version-pinned k6 harness and deterministic database-capacity calculator used by PR #9. It is operational/test tooling only; Identity and Organization production binaries do not import it.

Entrypoints:

```bash
make loadtest-unit
make loadtest-smoke
bash loadtest/scripts/run.sh baseline identity_me none
IDENTITY_REPLICAS=2 bash loadtest/scripts/run.sh \
  dependency-degradation identity_me replica-restart
python3 loadtest/capacity.py --help
```

## Replica-aware telemetry

Metric snapshots use a stable bounded replica key derived from the first 12 hexadecimal characters of the concrete Docker container ID:

```text
metrics-<phase>-<sample>-<service>-<replica_key>.prom
```

A restart keeps the same container ID and therefore continues the same counter series. A replacement container receives a different key and starts a new series. Reporter counter deltas are calculated independently for every replica before being summed. A missing scrape is not treated as zero.

Gauges are different: acquired, idle, total, maximum connections, and HTTP in-flight values are summed across replicas present in each sample, then the relevant phase value or during-phase maximum is reported.

## Single-replica restart

`replica-restart` is not a whole-service outage. The harness:

1. requires at least two replicas for the target service;
2. sorts concrete running container IDs;
3. restarts exactly the first container ID with `docker restart <container-id>`;
4. continuously checks every non-target replica remains running;
5. waits for the target to become healthy;
6. verifies the expected replica count and original identities are restored;
7. records only bounded replica keys and boolean verification results.

Identity scenarios target one Identity replica. Organization scenarios target one Organization replica. A one-replica configuration fails during pure configuration validation, before credentials, image builds, Compose startup, or k6.

## Retained data

RSA keys, Clerk session tokens, Svix signing secrets, provider fixtures, webhook bodies, emails, and Compose environment files stay under a restricted temporary directory. Raw Prometheus snapshots and k6 summaries are deleted after sanitized `result.json` and `result.md` are generated.

Normal profiles require exact client/service request-count reconciliation with zero-request tolerance. Explicit degradation profiles may record gateway or transport-only failures, but the service histogram count must still equal the number of requests completed inside the service.

See [`docs/load-test-capacity-runbook.md`](../docs/load-test-capacity-runbook.md) for profile selection, interpretation, stop conditions, commit comparison, and capacity budgeting.
