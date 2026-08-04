#!/usr/bin/env bash
set -euo pipefail

LOADTEST_SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
LOADTEST_ROOT="$(cd "${LOADTEST_SCRIPT_DIR}/.." && pwd)"
REPOSITORY_ROOT="$(cd "${LOADTEST_ROOT}/.." && pwd)"
K6_IMAGE="${K6_IMAGE:-grafana/k6:2.0.0}"
DELAY_PROXY_IMAGE="${DELAY_PROXY_IMAGE:-python:3.13.5-alpine3.22}"

loadtest_die() {
  printf 'load-test harness failed: %s\n' "$1" >&2
  return 1
}

loadtest_require_command() {
  command -v "$1" >/dev/null 2>&1 || loadtest_die "required command is unavailable: $1"
}

loadtest_set_env() {
  local file="$1" key="$2" value="$3"
  python3 - "$file" "$key" "$value" <<'PY'
from pathlib import Path
import sys

path = Path(sys.argv[1])
key = sys.argv[2]
value = sys.argv[3]
lines = path.read_text(encoding="utf-8").splitlines()
result = []
replaced = False
for line in lines:
    if line.startswith(key + "="):
        result.append(f"{key}={value}")
        replaced = True
    else:
        result.append(line)
if not replaced:
    result.append(f"{key}={value}")
path.write_text("\n".join(result) + "\n", encoding="utf-8")
PY
}

loadtest_random_secret() {
  python3 - <<'PY'
import base64
import secrets
print("whsec_" + base64.b64encode(secrets.token_bytes(32)).decode("ascii"))
PY
}

loadtest_random_suffix() {
  python3 - <<'PY'
import secrets
print(secrets.token_hex(8))
PY
}

loadtest_compose() {
  docker compose --env-file "${LOADTEST_ENV_FILE}" "$@"
}

loadtest_cleanup() {
  local exit_code="${1:-0}"
  set +e
  if [[ -n "${LOADTEST_K6_CONTAINER:-}" ]]; then
    docker rm --force "${LOADTEST_K6_CONTAINER}" >/dev/null 2>&1 || true
  fi
  if [[ -n "${LOADTEST_DELAY_CONTAINER:-}" ]]; then
    docker rm --force "${LOADTEST_DELAY_CONTAINER}" >/dev/null 2>&1 || true
  fi
  if [[ -n "${LOADTEST_ENV_FILE:-}" && -f "${LOADTEST_ENV_FILE}" ]]; then
    loadtest_compose down --remove-orphans --volumes >/dev/null 2>&1 || true
  fi
  if [[ -n "${LOADTEST_TEMP_ROOT:-}" && -d "${LOADTEST_TEMP_ROOT}" ]]; then
    rm -rf "${LOADTEST_TEMP_ROOT}"
  fi
  set -e
  return "${exit_code}"
}

loadtest_wait_service_healthy() {
  local service="$1"
  local deadline=$((SECONDS + 120))
  while (( SECONDS < deadline )); do
    local ids status all_healthy=true
    ids="$(loadtest_compose ps -q "${service}")"
    if [[ -n "${ids}" ]]; then
      while IFS= read -r id; do
        [[ -n "${id}" ]] || continue
        status="$(docker inspect --format '{{if .State.Health}}{{.State.Health.Status}}{{else}}{{.State.Status}}{{end}}' "${id}" 2>/dev/null || true)"
        if [[ "${status}" != "healthy" && "${status}" != "running" ]]; then
          all_healthy=false
          break
        fi
      done <<< "${ids}"
      if [[ "${all_healthy}" == "true" ]]; then
        return 0
      fi
    fi
    sleep 2
  done
  loadtest_compose ps >&2 || true
  loadtest_die "service did not become healthy: ${service}"
}

loadtest_wait_gateway() {
  local deadline=$((SECONDS + 90))
  while (( SECONDS < deadline )); do
    if curl --fail --show-error --silent --output /dev/null "${LOADTEST_GATEWAY_URL}/healthz"; then
      return 0
    fi
    sleep 2
  done
  loadtest_die "APISIX did not become ready"
}

loadtest_initialize() {
  local profile="$1"
  local degradation_mode="${2:-none}"
  loadtest_require_command docker
  loadtest_require_command curl
  loadtest_require_command go
  loadtest_require_command python3

  case "${profile}" in
    smoke|baseline|burst|saturation|dependency-degradation) ;;
    *) loadtest_die "unsupported profile: ${profile}" ;;
  esac
  case "${degradation_mode}" in
    none|identity-unavailable|identity-delayed|constrained-pool|replica-restart) ;;
    *) loadtest_die "unsupported degradation mode: ${degradation_mode}" ;;
  esac
  if [[ "${profile}" != "dependency-degradation" && "${degradation_mode}" != "none" ]]; then
    loadtest_die "degradation mode requires dependency-degradation profile"
  fi
  if [[ "${profile}" == "dependency-degradation" && "${degradation_mode}" == "none" ]]; then
    loadtest_die "dependency-degradation profile requires an explicit degradation mode"
  fi

  export LOADTEST_PROFILE="${profile}"
  export LOADTEST_DEGRADATION_MODE="${degradation_mode}"
  export LOADTEST_RUN_SUFFIX="$(loadtest_random_suffix)"
  export LOADTEST_TEMP_ROOT="$(mktemp -d "${RUNNER_TEMP:-/tmp}/bridgeworks-loadtest.XXXXXX")"
  export LOADTEST_ENV_FILE="${LOADTEST_TEMP_ROOT}/loadtest.env"
  export LOADTEST_AUTH_DIR="${LOADTEST_TEMP_ROOT}/auth"
  export LOADTEST_FIXTURE_DIR="${LOADTEST_TEMP_ROOT}/fixtures"
  export LOADTEST_RESULTS_ROOT="${LOADTEST_RESULTS_DIR:-${REPOSITORY_ROOT}/loadtest-results}"
  export LOADTEST_IDENTITY_REPLICAS="${IDENTITY_REPLICAS:-1}"
  export LOADTEST_ORGANIZATION_REPLICAS="${ORGANIZATION_REPLICAS:-1}"
  export LOADTEST_EXPERIMENTAL_POOL_OVERRIDE=false
  mkdir -p "${LOADTEST_AUTH_DIR}" "${LOADTEST_FIXTURE_DIR}" "${LOADTEST_RESULTS_ROOT}"
  cp "${REPOSITORY_ROOT}/.env.example" "${LOADTEST_ENV_FILE}"
  chmod 600 "${LOADTEST_ENV_FILE}"

  loadtest_set_env "${LOADTEST_ENV_FILE}" COMPOSE_PROJECT_NAME "bridgeworks-loadtest-${LOADTEST_RUN_SUFFIX}"
  loadtest_set_env "${LOADTEST_ENV_FILE}" APISIX_HTTP_PORT 0
  loadtest_set_env "${LOADTEST_ENV_FILE}" CLERK_WEBHOOK_SIGNING_SECRET "$(loadtest_random_secret)"
  loadtest_set_env "${LOADTEST_ENV_FILE}" CLERK_ORGANIZATION_WEBHOOK_SIGNING_SECRET "$(loadtest_random_secret)"

  if [[ "${degradation_mode}" == "constrained-pool" ]]; then
    loadtest_set_env "${LOADTEST_ENV_FILE}" DATABASE_MAX_CONNS 1
    loadtest_set_env "${LOADTEST_ENV_FILE}" DATABASE_MIN_CONNS 0
    loadtest_set_env "${LOADTEST_ENV_FILE}" ORGANIZATION_DATABASE_MAX_CONNS 1
    loadtest_set_env "${LOADTEST_ENV_FILE}" ORGANIZATION_DATABASE_MIN_CONNS 0
    export LOADTEST_EXPERIMENTAL_POOL_OVERRIDE=true
  fi
  if [[ "${degradation_mode}" == "identity-delayed" ]]; then
    loadtest_set_env "${LOADTEST_ENV_FILE}" IDENTITY_SERVICE_URL "http://bridgeworks-loadtest-identity-delay-${LOADTEST_RUN_SUFFIX}:8080"
    loadtest_set_env "${LOADTEST_ENV_FILE}" IDENTITY_SERVICE_REQUEST_TIMEOUT 3s
  fi

  (
    cd "${REPOSITORY_ROOT}/service/identity-service"
    go run ../../.github/scripts/clerk-session-token.go \
      -action generate \
      -private-key "${LOADTEST_AUTH_DIR}/private.pem" \
      -public-key "${LOADTEST_AUTH_DIR}/public.key"
  )
  local public_key
  public_key="$(cat "${LOADTEST_AUTH_DIR}/public.key")"
  [[ -n "${public_key}" ]] || loadtest_die "generated Clerk public key is empty"
  loadtest_set_env "${LOADTEST_ENV_FILE}" CLERK_JWT_KEY "${public_key}"

  export LOADTEST_CLERK_USER_ID="user_load_${LOADTEST_RUN_SUFFIX}"
  export LOADTEST_CLERK_ORGANIZATION_ID="org_load_${LOADTEST_RUN_SUFFIX}"
  export LOADTEST_CLERK_MEMBERSHIP_ID="mem_load_${LOADTEST_RUN_SUFFIX}"
  (
    cd "${REPOSITORY_ROOT}/service/identity-service"
    go run ../../.github/scripts/clerk-session-token.go \
      -action sign \
      -private-key "${LOADTEST_AUTH_DIR}/private.pem" \
      -output "${LOADTEST_AUTH_DIR}/session.jwt" \
      -issuer "https://clerk.bridgeworks.test" \
      -subject "${LOADTEST_CLERK_USER_ID}" \
      -session-id "sess_load_${LOADTEST_RUN_SUFFIX}" \
      -authorized-party "http://localhost:3000" \
      -organization-id "${LOADTEST_CLERK_ORGANIZATION_ID}" \
      -organization-role "org:admin" \
      -ttl 30m
  )

  loadtest_compose up -d --build \
    --scale identity-service="${LOADTEST_IDENTITY_REPLICAS}" \
    --scale organization-service="${LOADTEST_ORGANIZATION_REPLICAS}"
  loadtest_wait_service_healthy identity-service
  loadtest_wait_service_healthy organization-service

  local gateway_port
  gateway_port="$(loadtest_compose port apisix 9080 | awk -F: 'END {print $NF}')"
  [[ "${gateway_port}" =~ ^[0-9]+$ ]] || loadtest_die "cannot resolve APISIX host port"
  export LOADTEST_GATEWAY_URL="http://127.0.0.1:${gateway_port}"
  loadtest_wait_gateway

  local identity_id
  identity_id="$(loadtest_compose ps -q identity-service | head -n 1)"
  [[ -n "${identity_id}" ]] || loadtest_die "identity container is unavailable"
  export LOADTEST_NETWORK="$(docker inspect --format '{{range $name, $_ := .NetworkSettings.Networks}}{{$name}}{{end}}' "${identity_id}")"
  [[ -n "${LOADTEST_NETWORK}" ]] || loadtest_die "cannot resolve private Docker network"

  if [[ "${degradation_mode}" == "identity-delayed" ]]; then
    export LOADTEST_DELAY_CONTAINER="bridgeworks-loadtest-identity-delay-${LOADTEST_RUN_SUFFIX}"
    docker run --detach --name "${LOADTEST_DELAY_CONTAINER}" \
      --label bridgeworks.loadtest=true \
      --network "${LOADTEST_NETWORK}" \
      --env UPSTREAM_URL=http://identity-service:8080 \
      --env DELAY_SECONDS="${IDENTITY_DELAY_SECONDS:-1.5}" \
      --volume "${LOADTEST_SCRIPT_DIR}/delay_proxy.py:/delay_proxy.py:ro" \
      "${DELAY_PROXY_IMAGE}" python /delay_proxy.py >/dev/null
  fi

  loadtest_seed_fixtures
}

loadtest_read_env_value() {
  local key="$1"
  sed -n "s/^${key}=//p" "${LOADTEST_ENV_FILE}" | tail -n 1
}

loadtest_send_webhook() {
  local url="$1" secret="$2" event_id="$3" payload="$4" output="$5"
  local result
  result="$(
    cd "${REPOSITORY_ROOT}/service/identity-service"
    go run ../../.github/scripts/clerk-webhook-client.go \
      -url "${url}" \
      -secret "${secret}" \
      -event-id "${event_id}" \
      -payload-file "${payload}" \
      -body-output "${output}" \
      -request-id "loadtest-fixture"
  )" || return 1
  [[ "${result}" == "204|loadtest-fixture" ]] || loadtest_die "fixture webhook was not accepted"
}

loadtest_seed_fixtures() {
  local now_ms identity_secret organization_secret
  now_ms="$(python3 - <<'PY'
import time
print(int(time.time() * 1000))
PY
)"
  identity_secret="$(loadtest_read_env_value CLERK_WEBHOOK_SIGNING_SECRET)"
  organization_secret="$(loadtest_read_env_value CLERK_ORGANIZATION_WEBHOOK_SIGNING_SECRET)"

  python3 - "${LOADTEST_FIXTURE_DIR}" "${LOADTEST_CLERK_USER_ID}" "${LOADTEST_CLERK_ORGANIZATION_ID}" "${LOADTEST_CLERK_MEMBERSHIP_ID}" "${now_ms}" <<'PY'
from pathlib import Path
import json
import sys

root = Path(sys.argv[1])
user_id, org_id, membership_id, now_ms = sys.argv[2], sys.argv[3], sys.argv[4], int(sys.argv[5])
(root / "identity-user.json").write_text(json.dumps({
    "type": "user.created",
    "timestamp": now_ms,
    "data": {
        "id": user_id,
        "primary_email_address_id": "email_load_fixture",
        "email_addresses": [{
            "id": "email_load_fixture",
            "email_address": "load-fixture@example.test",
            "verification": {"status": "verified"},
        }],
    },
}), encoding="utf-8")
(root / "organization.json").write_text(json.dumps({
    "type": "organization.created",
    "timestamp": now_ms + 1,
    "data": {"id": org_id, "name": "Load Fixture", "slug": "load-fixture"},
}), encoding="utf-8")
(root / "membership.json").write_text(json.dumps({
    "type": "organizationMembership.created",
    "timestamp": now_ms + 2,
    "data": {
        "id": membership_id,
        "organization": {"id": org_id},
        "public_user_data": {"user_id": user_id},
        "role": "org:admin",
    },
}), encoding="utf-8")
PY

  loadtest_send_webhook \
    "${LOADTEST_GATEWAY_URL}/api/v1/identity/webhooks/clerk" "${identity_secret}" \
    "msg_load_seed_user_${LOADTEST_RUN_SUFFIX}" "${LOADTEST_FIXTURE_DIR}/identity-user.json" "${LOADTEST_FIXTURE_DIR}/identity-response"
  loadtest_send_webhook \
    "${LOADTEST_GATEWAY_URL}/api/v1/organizations/webhooks/clerk" "${organization_secret}" \
    "msg_load_seed_org_${LOADTEST_RUN_SUFFIX}" "${LOADTEST_FIXTURE_DIR}/organization.json" "${LOADTEST_FIXTURE_DIR}/organization-response"
  loadtest_send_webhook \
    "${LOADTEST_GATEWAY_URL}/api/v1/organizations/webhooks/clerk" "${organization_secret}" \
    "msg_load_seed_membership_${LOADTEST_RUN_SUFFIX}" "${LOADTEST_FIXTURE_DIR}/membership.json" "${LOADTEST_FIXTURE_DIR}/membership-response"
}

loadtest_scrape_service() {
  local phase="$1" sample="$2" service="$3" suffix="$4" output_dir="$5"
  local index=0 id
  while IFS= read -r id; do
    [[ -n "${id}" ]] || continue
    local output="${output_dir}/metrics-${phase}-${sample}-$(printf '%02d' "${index}")-${suffix}.prom"
    if docker exec "${id}" wget --quiet --output-document=- http://127.0.0.1:9090/metrics > "${output}"; then
      index=$((index + 1))
    else
      rm -f "${output}"
      [[ "${phase}" == "during" ]] || loadtest_die "metrics scrape failed for ${service}"
    fi
  done < <(loadtest_compose ps -q "${service}")
  if (( index == 0 )) && [[ "${phase}" != "during" ]]; then
    loadtest_die "no metrics target found for ${service}"
  fi
}

loadtest_scrape() {
  local phase="$1" sample="$2" output_dir="$3"
  loadtest_scrape_service "${phase}" "${sample}" identity-service identity "${output_dir}"
  loadtest_scrape_service "${phase}" "${sample}" organization-service organization "${output_dir}"
}

loadtest_k6_script() {
  case "$1" in
    identity_me|organization_current|organization_membership|organization_dependency)
      printf '%s\n' authenticated-read.js
      ;;
    identity_user_unique|identity_user_retry|organization_unique|organization_retry|membership_unique|membership_retry)
      printf '%s\n' webhook.js
      ;;
    *) loadtest_die "unsupported scenario: $1" ;;
  esac
}

loadtest_prepare_k6_env() {
  local scenario="$1" env_file="$2"
  umask 077
  {
    printf 'LOAD_PROFILE=%s\n' "${LOADTEST_PROFILE}"
    printf 'LOAD_SCENARIO=%s\n' "${scenario}"
    printf 'BASE_URL=http://apisix:9080\n'
    printf 'RUN_NONCE=%s\n' "${LOADTEST_RUN_SUFFIX}"
    printf 'WEBHOOK_EVENT_TIMESTAMP_MS=%s\n' "$(python3 - <<'PY'
import time
print(int(time.time() * 1000))
PY
)"
    case "${scenario}" in
      identity_me|organization_current|organization_membership|organization_dependency)
        printf 'AUTH_TOKEN=%s\n' "$(cat "${LOADTEST_AUTH_DIR}/session.jwt")"
        ;;
      identity_user_*)
        printf 'WEBHOOK_SECRET=%s\n' "$(loadtest_read_env_value CLERK_WEBHOOK_SIGNING_SECRET)"
        ;;
      organization_*|membership_*)
        printf 'WEBHOOK_SECRET=%s\n' "$(loadtest_read_env_value CLERK_ORGANIZATION_WEBHOOK_SIGNING_SECRET)"
        printf 'WEBHOOK_ORGANIZATION_ID=%s\n' "${LOADTEST_CLERK_ORGANIZATION_ID}"
        ;;
    esac
  } > "${env_file}"
}

loadtest_start_disruption() {
  local scenario="$1"
  case "${LOADTEST_DEGRADATION_MODE}" in
    identity-unavailable|identity-delayed)
      [[ "${scenario}" == "organization_dependency" ]] || loadtest_die "${LOADTEST_DEGRADATION_MODE} requires organization_dependency scenario"
      if [[ "${LOADTEST_DEGRADATION_MODE}" == "identity-delayed" ]]; then
        LOADTEST_DISRUPTION_PID=''
        return 0
      fi
      (
        sleep "${DISRUPTION_DELAY_SECONDS:-8}"
        loadtest_compose stop identity-service >/dev/null
        sleep "${DISRUPTION_HOLD_SECONDS:-10}"
        loadtest_compose start identity-service >/dev/null
        loadtest_wait_service_healthy identity-service
      ) &
      LOADTEST_DISRUPTION_PID=$!
      ;;
    replica-restart)
      (
        sleep "${DISRUPTION_DELAY_SECONDS:-8}"
        if [[ "${scenario}" == identity_* ]]; then
          loadtest_compose restart identity-service >/dev/null
          loadtest_wait_service_healthy identity-service
        else
          loadtest_compose restart organization-service >/dev/null
          loadtest_wait_service_healthy organization-service
        fi
      ) &
      LOADTEST_DISRUPTION_PID=$!
      ;;
    constrained-pool|none)
      LOADTEST_DISRUPTION_PID=''
      ;;
  esac
}

loadtest_run_scenario() {
  local scenario="$1"
  local script result_dir metrics_dir k6_env sample=0 k6_exit=0
  script="$(loadtest_k6_script "${scenario}")"
  result_dir="${LOADTEST_RESULTS_ROOT}/${LOADTEST_PROFILE}/${scenario}"
  metrics_dir="${result_dir}/metrics"
  rm -rf "${result_dir}"
  mkdir -p "${metrics_dir}"
  k6_env="${LOADTEST_TEMP_ROOT}/k6-${scenario}.env"
  loadtest_prepare_k6_env "${scenario}" "${k6_env}"
  chmod 600 "${k6_env}"

  loadtest_scrape before 0000 "${metrics_dir}"
  export LOADTEST_K6_CONTAINER="bridgeworks-k6-${LOADTEST_RUN_SUFFIX}-${scenario//_/-}"
  docker run --detach --name "${LOADTEST_K6_CONTAINER}" \
    --user "$(id -u):$(id -g)" \
    --label bridgeworks.loadtest=true \
    --network "${LOADTEST_NETWORK}" \
    --env-file "${k6_env}" \
    --volume "${LOADTEST_ROOT}/k6:/scripts:ro" \
    --volume "${result_dir}:/results" \
    "${K6_IMAGE}" run "/scripts/${script}" >/dev/null

  loadtest_start_disruption "${scenario}"
  while true; do
    sample=$((sample + 1))
    loadtest_scrape during "$(printf '%04d' "${sample}")" "${metrics_dir}"
    if [[ "$(docker inspect --format '{{.State.Running}}' "${LOADTEST_K6_CONTAINER}" 2>/dev/null || printf false)" != "true" ]]; then
      break
    fi
    sleep "${METRICS_SAMPLE_INTERVAL_SECONDS:-1}"
  done
  k6_exit="$(docker inspect --format '{{.State.ExitCode}}' "${LOADTEST_K6_CONTAINER}")"
  docker logs "${LOADTEST_K6_CONTAINER}" > "${LOADTEST_TEMP_ROOT}/k6-${scenario}.log" 2>&1 || true
  if [[ -n "${LOADTEST_DISRUPTION_PID:-}" ]]; then
    wait "${LOADTEST_DISRUPTION_PID}" || loadtest_die "degradation disruption failed"
  fi
  loadtest_scrape after 0000 "${metrics_dir}"

  local report_args=(
    --k6-summary "${result_dir}/k6-summary.json"
    --metrics-dir "${metrics_dir}"
    --output-json "${result_dir}/result.json"
    --output-markdown "${result_dir}/result.md"
    --git-sha "${LOADTEST_GIT_SHA:-${GITHUB_SHA:-$(git -C "${REPOSITORY_ROOT}" rev-parse HEAD 2>/dev/null || printf unknown)}}"
    --profile "${LOADTEST_PROFILE}"
    --scenario "${scenario}"
    --identity-replicas "${LOADTEST_IDENTITY_REPLICAS}"
    --organization-replicas "${LOADTEST_ORGANIZATION_REPLICAS}"
    --identity-max-conns "$(loadtest_read_env_value DATABASE_MAX_CONNS)"
    --identity-min-conns "$(loadtest_read_env_value DATABASE_MIN_CONNS)"
    --organization-max-conns "$(loadtest_read_env_value ORGANIZATION_DATABASE_MAX_CONNS)"
    --organization-min-conns "$(loadtest_read_env_value ORGANIZATION_DATABASE_MIN_CONNS)"
    --limitation "GitHub-hosted and local Docker measurements validate the harness and enable relative comparisons; they are not production capacity claims."
    --limitation "Shared runner CPU, storage, and network scheduling can vary between runs."
    --limitation "Production pool sizing remains pending representative deployment measurements."
  )
  if [[ "${LOADTEST_EXPERIMENTAL_POOL_OVERRIDE}" == "true" ]]; then
    report_args+=(--experimental-pool-override)
  fi
  [[ -f "${result_dir}/k6-summary.json" ]] || loadtest_die "k6 did not produce a summary for ${scenario}"
  python3 "${LOADTEST_ROOT}/report.py" "${report_args[@]}"

  rm -rf "${metrics_dir}" "${result_dir}/k6-summary.json"
  docker rm --force "${LOADTEST_K6_CONTAINER}" >/dev/null 2>&1 || true
  LOADTEST_K6_CONTAINER=''
  [[ "${k6_exit}" == "0" ]] || loadtest_die "k6 thresholds failed for ${scenario}"
}

loadtest_write_capacity_example() {
  local output_dir="${LOADTEST_RESULTS_ROOT}/capacity"
  mkdir -p "${output_dir}"
  python3 "${LOADTEST_ROOT}/capacity.py" \
    --postgres-max-connections "${CAPACITY_POSTGRES_MAX_CONNECTIONS:-100}" \
    --reserved-admin-connections "${CAPACITY_RESERVED_ADMIN_CONNECTIONS:-5}" \
    --reserved-migration-connections "${CAPACITY_RESERVED_MIGRATION_CONNECTIONS:-5}" \
    --operational-headroom-connections "${CAPACITY_OPERATIONAL_HEADROOM_CONNECTIONS:-20}" \
    --identity-allocation "${CAPACITY_IDENTITY_ALLOCATION:-30}" \
    --identity-max-replicas "${CAPACITY_IDENTITY_MAX_REPLICAS:-3}" \
    --organization-allocation "${CAPACITY_ORGANIZATION_ALLOCATION:-30}" \
    --organization-max-replicas "${CAPACITY_ORGANIZATION_MAX_REPLICAS:-3}" \
    --output-json "${output_dir}/capacity-budget.json" \
    --output-markdown "${output_dir}/capacity-budget.md"
}
