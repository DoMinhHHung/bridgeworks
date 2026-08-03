#!/usr/bin/env bash
set -euo pipefail

base_url="${APISIX_BASE_URL:-http://127.0.0.1:${APISIX_HTTP_PORT:-9080}}"
max_attempts="${APISIX_SMOKE_MAX_ATTEMPTS:-30}"
sleep_seconds="${APISIX_SMOKE_SLEEP_SECONDS:-2}"

for attempt in $(seq 1 "${max_attempts}"); do
  response="$(curl --silent --show-error --fail     --connect-timeout 2     --max-time 5     "${base_url}/healthz" 2>/dev/null || true)"

  if [[ "${response}" == *'"status":"ok"'* ]] &&
     [[ "${response}" == *'"component":"apisix"'* ]]; then
    echo "APISIX smoke test passed: ${response}"
    exit 0
  fi

  echo "Waiting for APISIX (${attempt}/${max_attempts})..."
  sleep "${sleep_seconds}"
done

echo "APISIX smoke test failed." >&2
docker compose logs --no-color apisix >&2 || true
exit 1
