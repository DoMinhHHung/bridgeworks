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

loadtest_is_supported_scenario() {
  case "$1" in
    identity_me|organization_current|organization_membership|organization_dependency|identity_user_unique|identity_user_retry|organization_unique|organization_retry|membership_unique|membership_retry)
      return 0
      ;;
    *)
      return 1
      ;;
  esac
}

loadtest_target_service_for_scenario() {
  case "$1" in
    identity_*) printf '%s\n' identity-service ;;
    *) printf '%s\n' organization-service ;;
  esac
}

loadtest_http_target_for_scenario() {
  case "$1" in
    identity_me)
      printf 'identity-service\t/me\tGET\n'
      ;;
    organization_current|organization_dependency)
      printf 'organization-service\t/organizations/current\tGET\n'
      ;;
    organization_membership)
      printf 'organization-service\t/organizations/current/membership\tGET\n'
      ;;
    identity_user_unique|identity_user_retry)
      printf 'identity-service\t/webhooks/clerk\tPOST\n'
      ;;
    organization_unique|organization_retry|membership_unique|membership_retry)
      printf 'organization-service\t/webhooks/clerk\tPOST\n'
      ;;
    *)
      loadtest_die "unsupported scenario for HTTP telemetry"
      ;;
  esac
}

loadtest_validate_replica_count() {
  local name="$1" value="$2"
  [[ "${value}" =~ ^[0-9]+$ ]] || loadtest_die "${name} must be an integer from 1 through 5"
  (( value >= 1 && value <= 5 )) || loadtest_die "${name} must be between 1 and 5"
}

loadtest_validate_configuration() {
  local profile="$1" scenario="$2" degradation_mode="$3"
  local identity_replicas="$4" organization_replicas="$5"

  case "${profile}" in
    smoke|baseline|burst|saturation|dependency-degradation) ;;
    *) loadtest_die "unsupported profile" ;;
  esac
  loadtest_is_supported_scenario "${scenario}" || loadtest_die "unsupported scenario"
  case "${degradation_mode}" in
    none|identity-unavailable|identity-delayed|constrained-pool|replica-restart) ;;
    *) loadtest_die "unsupported degradation mode" ;;
  esac

  loadtest_validate_replica_count IDENTITY_REPLICAS "${identity_replicas}"
  loadtest_validate_replica_count ORGANIZATION_REPLICAS "${organization_replicas}"

  if [[ "${profile}" != "dependency-degradation" ]]; then
    [[ "${degradation_mode}" == "none" ]] || loadtest_die "non-degradation profiles require degradation_mode=none"
    return 0
  fi

  [[ "${degradation_mode}" != "none" ]] || loadtest_die "dependency-degradation requires an explicit degradation mode"

  case "${degradation_mode}" in
    identity-unavailable|identity-delayed)
      [[ "${scenario}" == "organization_dependency" ]] || loadtest_die "identity dependency degradation requires scenario=organization_dependency"
      ;;
    replica-restart)
      local target_service target_replicas
      target_service="$(loadtest_target_service_for_scenario "${scenario}")"
      if [[ "${target_service}" == "identity-service" ]]; then
        target_replicas="${identity_replicas}"
      else
        target_replicas="${organization_replicas}"
      fi
      (( target_replicas >= 2 )) || loadtest_die "replica-restart requires at least two target service replicas"
      ;;
    constrained-pool) ;;
  esac
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
  if [[ -n "${LOADTEST_DISRUPTION_PID:-}" ]]; then
    kill "${LOADTEST_DISRUPTION_PID}" >/dev/null 2>&1 || true
    wait "${LOADTEST_DISRUPTION_PID}" >/dev/null 2>&1 || true
  fi
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

loadtest_service_container_ids() {
  local service="$1"
  loadtest_compose ps -q "${service}" | sed '/^[[:space:]]*$/d' | sort -u
}

loadtest_container_replica_key() {
  local id="$1" key
  key="$(printf '%s' "${id}" | tr '[:upper:]' '[:lower:]' | tr -cd 'a-f0-9' | cut -c1-12)"
  [[ "${key}" =~ ^[a-f0-9]{12}$ ]] || loadtest_die "container identity cannot be converted to a bounded replica key"
  printf '%s\n' "${key}"
}

loadtest_expected_replicas_for_service() {
  case "$1" in
    identity-service) printf '%s\n' "${LOADTEST_IDENTITY_REPLICAS}" ;;
    organization-service) printf '%s\n' "${LOADTEST_ORGANIZATION_REPLICAS}" ;;
    *) loadtest_die "unsupported service for replica accounting" ;;
  esac
}

loadtest_container_status() {
  docker inspect --format '{{if .State.Health}}{{.State.Health.Status}}{{else}}{{.State.Status}}{{end}}' "$1" 2>/dev/null || true
}

loadtest_scrape_http_request_counter() {
  local id="$1" service="$2" route="$3" method="$4"
  docker exec "${id}" wget --quiet --output-document=- http://127.0.0.1:9090/metrics \
    | python3 -c '
import re
import sys

service, route, method = sys.argv[1:]
metric = re.compile(r"^http_requests_total(?:\{([^}]*)\})?\s+([-+a-zA-Z0-9.eE]+)$")
label = re.compile(r"([a-zA-Z_][a-zA-Z0-9_]*)=\"([^\"]*)\"")
total = 0.0
for raw_line in sys.stdin:
    match = metric.match(raw_line.strip())
    if match is None:
        continue
    labels = dict(label.findall(match.group(1) or ""))
    if (
        labels.get("service") == service
        and labels.get("route") == route
        and labels.get("method") == method
    ):
        total += float(match.group(2))
print(int(round(total)))
' "${service}" "${route}" "${method}"
}

loadtest_capture_request_counter() {
  local id="$1" service="$2" route="$3" method="$4"
  local deadline=$((SECONDS + ${RESTART_COUNTER_SCRAPE_TIMEOUT_SECONDS:-10})) value
  while (( SECONDS < deadline )); do
    [[ "$(docker inspect --format '{{.State.Running}}' "${id}" 2>/dev/null || true)" == "true" ]] \
      || loadtest_die "service replica stopped while capturing request telemetry"
    if value="$(loadtest_scrape_http_request_counter "${id}" "${service}" "${route}" "${method}" 2>/dev/null)"; then
      [[ "${value}" =~ ^[0-9]+$ ]] || loadtest_die "request telemetry returned an invalid counter"
      printf '%s\n' "${value}"
      return 0
    fi
    sleep "${RESTART_COUNTER_POLL_SECONDS:-0.2}"
  done
  loadtest_die "request telemetry was unavailable before the bounded deadline"
}

loadtest_monitor_non_target_request_progress() {
  local stop_file="$1" ready_file="$2" failure_file="$3" delta_file="$4"
  local service="$5" route="$6" method="$7"
  shift 7
  local ids=("$@") id value total_delta deadline baseline_value maximum_value
  declare -A baseline=() maximum=()

  deadline=$((SECONDS + ${RESTART_COUNTER_SCRAPE_TIMEOUT_SECONDS:-10}))
  for id in "${ids[@]}"; do
    while (( SECONDS < deadline )); do
      if [[ "$(docker inspect --format '{{.State.Running}}' "${id}" 2>/dev/null || true)" != "true" ]]; then
        printf 'non-target replica stopped\n' > "${failure_file}"
        return 1
      fi
      if value="$(loadtest_scrape_http_request_counter "${id}" "${service}" "${route}" "${method}" 2>/dev/null)" \
        && [[ "${value}" =~ ^[0-9]+$ ]]; then
        baseline["${id}"]="${value}"
        maximum["${id}"]="${value}"
        break
      fi
      sleep "${RESTART_COUNTER_POLL_SECONDS:-0.2}"
    done
    if [[ -z "${baseline[$id]+present}" ]]; then
      printf 'non-target request telemetry unavailable\n' > "${failure_file}"
      return 1
    fi
  done

  printf '0\n' > "${delta_file}"
  touch "${ready_file}"
  deadline=$((SECONDS + ${RESTART_MONITOR_TIMEOUT_SECONDS:-120}))
  while [[ ! -f "${stop_file}" ]]; do
    if (( SECONDS >= deadline )); then
      printf 'restart monitor exceeded bounded timeout\n' > "${failure_file}"
      return 1
    fi
    total_delta=0
    for id in "${ids[@]}"; do
      if [[ "$(docker inspect --format '{{.State.Running}}' "${id}" 2>/dev/null || true)" != "true" ]]; then
        printf 'non-target replica stopped\n' > "${failure_file}"
        return 1
      fi
      baseline_value="${baseline[$id]}"
      maximum_value="${maximum[$id]}"
      if value="$(loadtest_scrape_http_request_counter "${id}" "${service}" "${route}" "${method}" 2>/dev/null)" \
        && [[ "${value}" =~ ^[0-9]+$ ]]; then
        if (( value < baseline_value )); then
          printf 'non-target request counter reset unexpectedly\n' > "${failure_file}"
          return 1
        fi
        if (( value > maximum_value )); then
          maximum["${id}"]="${value}"
          maximum_value="${value}"
        fi
      fi
      total_delta=$((total_delta + maximum_value - baseline_value))
    done
    printf '%s\n' "${total_delta}" > "${delta_file}.tmp"
    mv "${delta_file}.tmp" "${delta_file}"
    sleep "${RESTART_COUNTER_POLL_SECONDS:-0.2}"
  done

  total_delta=0
  for id in "${ids[@]}"; do
    baseline_value="${baseline[$id]}"
    maximum_value="${maximum[$id]}"
    if value="$(loadtest_scrape_http_request_counter "${id}" "${service}" "${route}" "${method}" 2>/dev/null)" \
      && [[ "${value}" =~ ^[0-9]+$ ]] && (( value >= baseline_value )); then
      if (( value > maximum_value )); then
        maximum["${id}"]="${value}"
        maximum_value="${value}"
      fi
    fi
    total_delta=$((total_delta + maximum_value - baseline_value))
  done
  printf '%s\n' "${total_delta}" > "${delta_file}.tmp"
  mv "${delta_file}.tmp" "${delta_file}"
  (( total_delta > 0 )) || {
    printf 'non-target replica served no requests during restart\n' > "${failure_file}"
    return 1
  }
}

loadtest_wait_for_target_request_progress() {
  local id="$1" service="$2" route="$3" method="$4"
  local baseline current deadline
  baseline="$(loadtest_capture_request_counter "${id}" "${service}" "${route}" "${method}")"
  deadline=$((SECONDS + ${POST_RECOVERY_REQUEST_TIMEOUT_SECONDS:-8}))
  while (( SECONDS < deadline )); do
    [[ "$(docker inspect --format '{{.State.Running}}' "${id}" 2>/dev/null || true)" == "true" ]] \
      || loadtest_die "restarted target replica stopped after recovery"
    [[ "$(docker inspect --format '{{.State.Running}}' "${LOADTEST_K6_CONTAINER}" 2>/dev/null || true)" == "true" ]] \
      || loadtest_die "traffic generator stopped before target post-recovery progress"
    if current="$(loadtest_scrape_http_request_counter "${id}" "${service}" "${route}" "${method}" 2>/dev/null)" \
      && [[ "${current}" =~ ^[0-9]+$ ]]; then
      (( current >= baseline )) || loadtest_die "target request counter reset after recovery baseline"
      if (( current > baseline )); then
        printf '%s\n' "$((current - baseline))"
        return 0
      fi
    fi
    sleep "${RESTART_COUNTER_POLL_SECONDS:-0.2}"
  done
  loadtest_die "restarted target replica served no requests after recovery"
}

loadtest_wait_container_healthy() {
  local id="$1" deadline=$((SECONDS + 120)) status
  while (( SECONDS < deadline )); do
    status="$(loadtest_container_status "${id}")"
    if [[ "${status}" == "healthy" || "${status}" == "running" ]]; then
      return 0
    fi
    sleep 1
  done
  loadtest_die "restarted container did not become healthy"
}

loadtest_wait_service_healthy() {
  local service="$1" expected deadline=$((SECONDS + 120))
  expected="$(loadtest_expected_replicas_for_service "${service}")"
  while (( SECONDS < deadline )); do
    local ids=() all_healthy=true status
    mapfile -t ids < <(loadtest_service_container_ids "${service}")
    if (( ${#ids[@]} == expected )); then
      for id in "${ids[@]}"; do
        status="$(loadtest_container_status "${id}")"
        if [[ "${status}" != "healthy" && "${status}" != "running" ]]; then
          all_healthy=false
          break
        fi
      done
      [[ "${all_healthy}" == "true" ]] && return 0
    fi
    sleep 2
  done
  loadtest_compose ps >&2 || true
  loadtest_die "service did not restore expected healthy replica count"
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

loadtest_initialize_disruption_metadata() {
  printf '{"mode":"%s"}\n' "${LOADTEST_DEGRADATION_MODE}" > "${LOADTEST_DISRUPTION_METADATA}"
}

loadtest_initialize() {
  local profile="$1" scenario="$2" degradation_mode="${3:-none}"
  local identity_replicas="${IDENTITY_REPLICAS:-1}"
  local organization_replicas="${ORGANIZATION_REPLICAS:-1}"

  loadtest_validate_configuration \
    "${profile}" "${scenario}" "${degradation_mode}" \
    "${identity_replicas}" "${organization_replicas}"

  loadtest_require_command docker
  loadtest_require_command curl
  loadtest_require_command go
  loadtest_require_command python3

  export LOADTEST_PROFILE="${profile}"
  export LOADTEST_DEGRADATION_MODE="${degradation_mode}"
  export LOADTEST_RUN_SUFFIX="$(loadtest_random_suffix)"
  export LOADTEST_TEMP_ROOT="$(mktemp -d "${RUNNER_TEMP:-/tmp}/bridgeworks-loadtest.XXXXXX")"
  export LOADTEST_ENV_FILE="${LOADTEST_TEMP_ROOT}/loadtest.env"
  export LOADTEST_AUTH_DIR="${LOADTEST_TEMP_ROOT}/auth"
  export LOADTEST_FIXTURE_DIR="${LOADTEST_TEMP_ROOT}/fixtures"
  export LOADTEST_DISRUPTION_METADATA="${LOADTEST_TEMP_ROOT}/disruption.json"
  export LOADTEST_RESULTS_ROOT="${LOADTEST_RESULTS_DIR:-${REPOSITORY_ROOT}/loadtest-results}"
  export LOADTEST_IDENTITY_REPLICAS="${identity_replicas}"
  export LOADTEST_ORGANIZATION_REPLICAS="${organization_replicas}"
  export LOADTEST_EXPERIMENTAL_POOL_OVERRIDE=false
  mkdir -p "${LOADTEST_AUTH_DIR}" "${LOADTEST_FIXTURE_DIR}" "${LOADTEST_RESULTS_ROOT}"
  loadtest_initialize_disruption_metadata
  cp "${REPOSITORY_ROOT}/.env.example" "${LOADTEST_ENV_FILE}"
  chmod 600 "${LOADTEST_ENV_FILE}" "${LOADTEST_DISRUPTION_METADATA}"

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

  local identity_ids=()
  mapfile -t identity_ids < <(loadtest_service_container_ids identity-service)
  (( ${#identity_ids[@]} > 0 )) || loadtest_die "identity container is unavailable"
  export LOADTEST_NETWORK="$(docker inspect --format '{{range $name, $_ := .NetworkSettings.Networks}}{{$name}}{{end}}' "${identity_ids[0]}")"
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
  local phase="$1" sample="$2" service="$3" file_service="$4" output_dir="$5"
  local expected success=0 ids=() id replica_key output
  expected="$(loadtest_expected_replicas_for_service "${service}")"
  mapfile -t ids < <(loadtest_service_container_ids "${service}")
  for id in "${ids[@]}"; do
    replica_key="$(loadtest_container_replica_key "${id}")"
    output="${output_dir}/metrics-${phase}-${sample}-${file_service}-${replica_key}.prom"
    if docker exec "${id}" wget --quiet --output-document=- http://127.0.0.1:9090/metrics > "${output}"; then
      success=$((success + 1))
    else
      rm -f "${output}"
      [[ "${phase}" == "during" ]] || loadtest_die "metrics scrape failed for service replica"
    fi
  done
  if [[ "${phase}" != "during" ]]; then
    (( success == expected )) || loadtest_die "metrics scrape did not cover the expected replica count"
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
    *) loadtest_die "unsupported scenario" ;;
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

loadtest_validate_restart_evidence() {
  local non_target_delta="$1" target_delta="$2"
  [[ "${non_target_delta}" =~ ^[0-9]+$ ]] \
    && (( non_target_delta > 0 && non_target_delta <= 1000000000 )) \
    || loadtest_die "replica-restart requires measured non-target request progress"
  [[ "${target_delta}" =~ ^[0-9]+$ ]] \
    && (( target_delta > 0 && target_delta <= 1000000000 )) \
    || loadtest_die "replica-restart requires measured target post-recovery request progress"
}

loadtest_write_restart_metadata() {
  local target_service="$1" target_key="$2" non_targets_csv="$3"
  local non_target_delta="$4" target_delta="$5"
  loadtest_validate_restart_evidence "${non_target_delta}" "${target_delta}"
  python3 - \
    "${LOADTEST_DISRUPTION_METADATA}" \
    "${target_service}" \
    "${target_key}" \
    "${non_targets_csv}" \
    "${non_target_delta}" \
    "${target_delta}" <<'PY'
from pathlib import Path
import json
import sys

path = Path(sys.argv[1])
non_targets = [value for value in sys.argv[4].split(",") if value]
non_target_delta = int(sys.argv[5])
target_delta = int(sys.argv[6])
payload = {
    "mode": "replica-restart",
    "target_service": sys.argv[2],
    "restarted_replica_key": sys.argv[3],
    "non_target_replica_keys": non_targets,
    "non_target_remained_running": True,
    "target_healthy_after_restart": True,
    "expected_replica_count_restored": True,
    "non_target_request_delta_during_restart": non_target_delta,
    "target_request_delta_after_recovery": target_delta,
    "traffic_continued_during_restart": non_target_delta > 0,
    "target_served_after_recovery": target_delta > 0,
}
path.write_text(json.dumps(payload, sort_keys=True) + "\n", encoding="utf-8")
PY
}

loadtest_restart_one_replica() {
  local scenario="$1" service route method measured_service expected ids=()
  local target target_key non_targets=() non_target_keys=() current_ids=() id
  local monitor_stop monitor_ready monitor_failure monitor_delta monitor_pid
  local non_target_delta target_delta

  service="$(loadtest_target_service_for_scenario "${scenario}")"
  IFS=$'\t' read -r measured_service route method < <(loadtest_http_target_for_scenario "${scenario}")
  [[ "${measured_service}" == "${service}" ]] \
    || loadtest_die "scenario HTTP telemetry target does not match restart service"
  expected="$(loadtest_expected_replicas_for_service "${service}")"
  (( expected >= 2 )) || loadtest_die "replica-restart requires at least two target service replicas"

  mapfile -t ids < <(loadtest_service_container_ids "${service}")
  (( ${#ids[@]} == expected )) || loadtest_die "target service replica count is not ready for restart"
  target="${ids[0]}"
  target_key="$(loadtest_container_replica_key "${target}")"
  non_targets=("${ids[@]:1}")
  for id in "${non_targets[@]}"; do
    non_target_keys+=("$(loadtest_container_replica_key "${id}")")
  done

  monitor_stop="${LOADTEST_TEMP_ROOT}/restart-monitor.stop"
  monitor_ready="${LOADTEST_TEMP_ROOT}/restart-monitor.ready"
  monitor_failure="${LOADTEST_TEMP_ROOT}/restart-monitor.failure"
  monitor_delta="${LOADTEST_TEMP_ROOT}/restart-monitor.delta"
  rm -f "${monitor_stop}" "${monitor_ready}" "${monitor_failure}" "${monitor_delta}"
  loadtest_monitor_non_target_request_progress \
    "${monitor_stop}" "${monitor_ready}" "${monitor_failure}" "${monitor_delta}" \
    "${service}" "${route}" "${method}" "${non_targets[@]}" &
  monitor_pid=$!

  local ready_deadline=$((SECONDS + ${RESTART_COUNTER_SCRAPE_TIMEOUT_SECONDS:-10}))
  while [[ ! -f "${monitor_ready}" ]]; do
    if [[ -s "${monitor_failure}" ]] || ! kill -0 "${monitor_pid}" >/dev/null 2>&1; then
      wait "${monitor_pid}" >/dev/null 2>&1 || true
      loadtest_die "non-target request monitor failed before restart"
    fi
    (( SECONDS < ready_deadline )) || {
      touch "${monitor_stop}"
      wait "${monitor_pid}" >/dev/null 2>&1 || true
      loadtest_die "non-target request monitor did not become ready"
    }
    sleep 0.1
  done

  if ! docker restart "${target}" >/dev/null; then
    touch "${monitor_stop}"
    wait "${monitor_pid}" >/dev/null 2>&1 || true
    loadtest_die "single target replica restart failed"
  fi
  if ! loadtest_wait_container_healthy "${target}"; then
    touch "${monitor_stop}"
    wait "${monitor_pid}" >/dev/null 2>&1 || true
    loadtest_die "restarted target replica did not recover"
  fi

  touch "${monitor_stop}"
  wait "${monitor_pid}" >/dev/null 2>&1 \
    || loadtest_die "non-target request monitor failed during restart"
  [[ ! -s "${monitor_failure}" ]] || loadtest_die "non-target replica continuity verification failed"
  non_target_delta="$(cat "${monitor_delta}")"
  [[ "${non_target_delta}" =~ ^[0-9]+$ ]] && (( non_target_delta > 0 )) \
    || loadtest_die "non-target replica served no requests during restart"

  target_delta="$(loadtest_wait_for_target_request_progress \
    "${target}" "${service}" "${route}" "${method}")"
  [[ "${target_delta}" =~ ^[0-9]+$ ]] && (( target_delta > 0 )) \
    || loadtest_die "restarted target replica served no requests after recovery"

  mapfile -t current_ids < <(loadtest_service_container_ids "${service}")
  (( ${#current_ids[@]} == expected )) || loadtest_die "expected target service replica count was not restored"
  printf '%s\n' "${current_ids[@]}" | grep --fixed-strings --line-regexp --quiet "${target}" \
    || loadtest_die "restarted container identity changed unexpectedly"
  for id in "${non_targets[@]}"; do
    printf '%s\n' "${current_ids[@]}" | grep --fixed-strings --line-regexp --quiet "${id}" \
      || loadtest_die "non-target replica identity changed during restart"
    [[ "$(docker inspect --format '{{.State.Running}}' "${id}" 2>/dev/null || true)" == "true" ]] \
      || loadtest_die "non-target replica is not running after restart"
  done

  local non_targets_csv
  non_targets_csv="$(IFS=,; printf '%s' "${non_target_keys[*]}")"
  loadtest_write_restart_metadata \
    "${service}" "${target_key}" "${non_targets_csv}" \
    "${non_target_delta}" "${target_delta}"
}

loadtest_start_disruption() {
  local scenario="$1"
  case "${LOADTEST_DEGRADATION_MODE}" in
    identity-unavailable|identity-delayed)
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
        loadtest_restart_one_replica "${scenario}"
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
  loadtest_validate_configuration \
    "${LOADTEST_PROFILE}" "${scenario}" "${LOADTEST_DEGRADATION_MODE}" \
    "${LOADTEST_IDENTITY_REPLICAS}" "${LOADTEST_ORGANIZATION_REPLICAS}"

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
    LOADTEST_DISRUPTION_PID=''
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
    --disruption-metadata "${LOADTEST_DISRUPTION_METADATA}"
    --limitation "GitHub-hosted and local Docker measurements validate the harness and enable relative comparisons; they are not production capacity claims."
    --limitation "Shared runner CPU, storage, and network scheduling can vary between runs."
    --limitation "Production pool sizing remains pending representative deployment measurements."
  )
  if [[ "${LOADTEST_EXPERIMENTAL_POOL_OVERRIDE}" == "true" ]]; then
    report_args+=(--experimental-pool-override)
  fi
  [[ -f "${result_dir}/k6-summary.json" ]] || loadtest_die "k6 did not produce a summary"
  python3 "${LOADTEST_ROOT}/report.py" "${report_args[@]}"

  rm -rf "${metrics_dir}" "${result_dir}/k6-summary.json"
  docker rm --force "${LOADTEST_K6_CONTAINER}" >/dev/null 2>&1 || true
  LOADTEST_K6_CONTAINER=''
  [[ "${k6_exit}" == "0" ]] || loadtest_die "k6 thresholds failed"
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
