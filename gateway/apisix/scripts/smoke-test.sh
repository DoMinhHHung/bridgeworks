#!/usr/bin/env bash
set -euo pipefail

mode="${1:-gateway}"
base_url="${APISIX_BASE_URL:-http://127.0.0.1:${APISIX_HTTP_PORT:-9080}}"
max_attempts="${APISIX_SMOKE_MAX_ATTEMPTS:-30}"
sleep_seconds="${APISIX_SMOKE_SLEEP_SECONDS:-2}"
tmp_dir="$(mktemp -d)"
trap 'rm -rf "${tmp_dir}"' EXIT

check_endpoint() {
  local path="$1"
  local expected_marker="$2"
  local name="$3"
  local request_id="$4"
  local headers_file="${tmp_dir}/${name}.headers"
  local body_file="${tmp_dir}/${name}.body"

  if ! curl --silent --show-error --fail \
    --connect-timeout 2 \
    --max-time 5 \
    --header "X-Request-Id: ${request_id}" \
    --dump-header "${headers_file}" \
    --output "${body_file}" \
    "${base_url}${path}"; then
    return 1
  fi

  tr -d '\r' < "${headers_file}" | grep --quiet --ignore-case --fixed-strings "X-Request-Id: ${request_id}" &&
    grep --quiet --fixed-strings '"status":"ok"' "${body_file}" &&
    grep --quiet --fixed-strings "${expected_marker}" "${body_file}"
}

run_gateway_smoke() {
  local request_id="bridgeworks-gateway-smoke"

  for attempt in $(seq 1 "${max_attempts}"); do
    if check_endpoint "/healthz" '"component":"apisix"' "gateway" "${request_id}"; then
      echo "Gateway smoke test passed."
      return 0
    fi

    echo "Waiting for APISIX (${attempt}/${max_attempts})..."
    sleep "${sleep_seconds}"
  done

  echo "Gateway smoke test failed." >&2
  docker compose logs --no-color apisix >&2 || true
  return 1
}

run_identity_smoke() {
  local request_id="bridgeworks-identity-smoke"

  for attempt in $(seq 1 "${max_attempts}"); do
    if check_endpoint "/api/v1/identity/health/live" '"service":"identity-service"' "identity-live" "${request_id}" &&
      check_endpoint "/api/v1/identity/health/ready" '"service":"identity-service"' "identity-ready" "${request_id}"; then
      echo "Identity smoke test passed."
      return 0
    fi

    echo "Waiting for identity-service through APISIX (${attempt}/${max_attempts})..."
    sleep "${sleep_seconds}"
  done

  echo "Identity smoke test failed." >&2
  docker compose logs --no-color apisix identity-service >&2 || true
  return 1
}

case "${mode}" in
  gateway)
    run_gateway_smoke
    ;;
  identity)
    run_identity_smoke
    ;;
  *)
    echo "usage: $0 {gateway|identity}" >&2
    exit 2
    ;;
esac
