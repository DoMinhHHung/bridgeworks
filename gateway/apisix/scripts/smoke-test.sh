#!/usr/bin/env bash
set -euo pipefail

base_url="${APISIX_BASE_URL:-http://127.0.0.1:${APISIX_HTTP_PORT:-9080}}"
max_attempts="${APISIX_SMOKE_MAX_ATTEMPTS:-30}"
sleep_seconds="${APISIX_SMOKE_SLEEP_SECONDS:-2}"
request_id="gateway-smoke-request-id"
headers_file="$(mktemp)"
body_file="$(mktemp)"
trap 'rm -f "${headers_file}" "${body_file}"' EXIT

check_endpoint() {
  local path="$1"
  local expected_fragment="$2"

  : > "${headers_file}"
  : > "${body_file}"

  if ! curl --silent --show-error --fail \
    --connect-timeout 2 \
    --max-time 5 \
    --header "X-Request-Id: ${request_id}" \
    --dump-header "${headers_file}" \
    --output "${body_file}" \
    "${base_url}${path}"; then
    return 1
  fi

  grep --quiet --ignore-case "^X-Request-Id: ${request_id}" "${headers_file}" &&
    grep --quiet '"status":"ok"' "${body_file}" &&
    grep --quiet "${expected_fragment}" "${body_file}"
}

for attempt in $(seq 1 "${max_attempts}"); do
  if check_endpoint "/healthz" '"component":"apisix"' &&
    check_endpoint "/api/v1/identity/health/live" '"service":"identity-service"' &&
    check_endpoint "/api/v1/identity/health/ready" '"service":"identity-service"'; then
    echo "APISIX and identity smoke tests passed."
    exit 0
  fi

  echo "Waiting for APISIX, identity-service, and PostgreSQL readiness (${attempt}/${max_attempts})..."
  sleep "${sleep_seconds}"
done

echo "APISIX or identity-service smoke test failed." >&2
docker compose logs --no-color apisix identity-service identity-postgres >&2 || true
exit 1
