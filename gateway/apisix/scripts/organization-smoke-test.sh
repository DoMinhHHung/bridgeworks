#!/usr/bin/env bash
set -euo pipefail

base_url="http://127.0.0.1:${APISIX_HTTP_PORT:-9080}"

for attempt in $(seq 1 30); do
  if curl --fail --show-error --silent --output /dev/null "${base_url}/healthz" && \
     curl --fail --show-error --silent --output /dev/null "${base_url}/api/v1/organizations/health/live" && \
     curl --fail --show-error --silent --output /dev/null "${base_url}/api/v1/organizations/health/ready"; then
    echo "Organization gateway smoke tests passed."
    exit 0
  fi
  echo "Waiting for APISIX and organization-service readiness (${attempt}/30)..."
  sleep 2
done

echo "Organization gateway smoke tests failed." >&2
exit 1
