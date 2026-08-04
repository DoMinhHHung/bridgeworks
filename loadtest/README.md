# BridgeWorks Load-Test Harness

This directory contains a Dockerized, version-pinned k6 harness and deterministic database-capacity calculator. It is operational tooling only: no code here is linked into Identity Service or Organization Service production binaries.

Entrypoints:

```bash
make loadtest-unit
make loadtest-smoke
bash loadtest/scripts/run.sh baseline identity_me none
python3 loadtest/capacity.py --help
```

The harness creates RSA keys, Clerk session tokens, Svix signing secrets, provider fixtures, and Compose environment files under a temporary directory with restrictive permissions. Cleanup deletes those fixtures and tears down the isolated Compose project. Only sanitized `result.json`, `result.md`, and capacity-budget artifacts are retained.

See [`docs/load-test-capacity-runbook.md`](../docs/load-test-capacity-runbook.md) for profile selection, interpretation, stop conditions, and comparison workflow.
