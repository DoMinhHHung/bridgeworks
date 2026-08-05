#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "${ROOT}"

for command in docker curl go python3 grep stat; do
  command -v "${command}" >/dev/null 2>&1 || {
    printf 'identity cache integration failed: missing command %s\n' "${command}" >&2
    exit 1
  }
done

umask 077
test -f .env.example
cp .env.example .env
env_mode="$(stat -c '%a' .env)"
if (( (8#${env_mode} & 8#077) != 0 )); then
  printf 'identity cache integration failed: generated .env permissions are too broad\n' >&2
  exit 1
fi
work="$(mktemp -d "${RUNNER_TEMP:-/tmp}/bridgeworks-identity-cache.XXXXXX")"
auth_dir="${work}/auth"
responses="${work}/responses"
metrics_dir="${work}/metrics"
mkdir -p "${auth_dir}" "${responses}" "${metrics_dir}"

set_env() {
  local key="$1" value="$2"
  python3 - .env "${key}" "${value}" <<'PY'
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

random_hex() {
  python3 - <<'PY'
import secrets
print(secrets.token_hex(24))
PY
}

random_webhook_secret() {
  python3 - <<'PY'
import base64
import secrets
print("whsec_" + base64.b64encode(secrets.token_bytes(32)).decode("ascii"))
PY
}

project="bridgeworks-cache-$(python3 - <<'PY'
import secrets
print(secrets.token_hex(5))
PY
)"
redis_password="$(random_hex)"
webhook_secret="$(random_webhook_secret)"
set_env COMPOSE_PROJECT_NAME "${project}"
set_env REDIS_PASSWORD "${redis_password}"
set_env CLERK_WEBHOOK_SIGNING_SECRET "${webhook_secret}"

(
  cd service/identity-service
  go run ../../.github/scripts/clerk-session-token.go \
    -action generate \
    -private-key "${auth_dir}/private.pem" \
    -public-key "${auth_dir}/public.key" >/dev/null
)
public_key="$(cat "${auth_dir}/public.key")"
test -n "${public_key}"
set_env CLERK_JWT_KEY "${public_key}"

set -a
source .env
set +a

authorized_party="${CLERK_AUTHORIZED_PARTIES%%,*}"

declare -A tokens=()
declare -A sessions=()
sign_token() {
  local alias="$1" user_id="$2"
  local session_id="sess_cache_${alias}_$(random_hex)"
  local output="${auth_dir}/${alias}.jwt"
  (
    cd service/identity-service
    go run ../../.github/scripts/clerk-session-token.go \
      -action sign \
      -private-key "${auth_dir}/private.pem" \
      -output "${output}" \
      -issuer "${CLERK_ISSUER}" \
      -subject "${user_id}" \
      -session-id "${session_id}" \
      -authorized-party "${authorized_party}" \
      -ttl 30m >/dev/null
  )
  tokens["${alias}"]="$(cat "${output}")"
  sessions["${alias}"]="${session_id}"
}

user_a="user_cache_a_$(random_hex)"
user_b="user_cache_b_$(random_hex)"
user_c="user_cache_c_$(random_hex)"
user_d="user_cache_d_$(random_hex)"
user_e="user_cache_e_$(random_hex)"
sign_token a "${user_a}"
sign_token b "${user_b}"
sign_token c "${user_c}"
sign_token d "${user_d}"
sign_token e "${user_e}"

cleanup_complete=false
cleanup() {
  set +e
  docker compose --env-file .env down --remove-orphans --volumes >/dev/null 2>&1 || true
  set -e
}
trap 'status=$?; if [[ "${cleanup_complete}" != true ]]; then cleanup; fi; rm -rf "${work}" .env; exit "${status}"' EXIT

cleanup
docker compose --env-file .env config --quiet
docker compose --env-file .env up -d --build --scale identity-service=2

retry_status() {
  local expected="$1" url="$2" deadline=$((SECONDS + 120)) status
  while (( SECONDS < deadline )); do
    status="$(curl --show-error --silent --output /dev/null --write-out '%{http_code}' "${url}" || true)"
    if [[ "${status}" == "${expected}" ]]; then
      return 0
    fi
    sleep 2
  done
  printf 'identity cache integration failed: expected status %s from bounded endpoint\n' "${expected}" >&2
  return 1
}

wait_container_healthy() {
  local label="$1" id="$2" deadline=$((SECONDS + 120)) status
  while (( SECONDS < deadline )); do
    status="$(docker inspect --format '{{if .State.Health}}{{.State.Health.Status}}{{else}}{{.State.Status}}{{end}}' "${id}" 2>/dev/null || true)"
    if [[ "${status}" == healthy || "${status}" == running ]]; then
      return 0
    fi
    sleep 1
  done
  if [[ "${label}" == identity-redis ]]; then
    if docker exec "${id}" sh -c 'test -n "$REDIS_PASSWORD"'; then
      printf 'identity cache integration diagnostic: redis_password=present\n' >&2
    else
      printf 'identity cache integration diagnostic: redis_password=missing\n' >&2
    fi
    if docker exec "${id}" sh -c 'redis-cli --no-auth-warning -h 127.0.0.1 -p 6379 -a "$REDIS_PASSWORD" ping 2>/dev/null | grep -qx PONG'; then
      printf 'identity cache integration diagnostic: redis_auth_ping=ok\n' >&2
    else
      printf 'identity cache integration diagnostic: redis_auth_ping=failed\n' >&2
    fi
  fi
  printf 'identity cache integration failed: %s did not become healthy (status=%s)\n' "${label}" "${status}" >&2
  return 1
}

retry_status 200 http://127.0.0.1:9080/healthz
retry_status 200 http://127.0.0.1:9080/api/v1/identity/health/ready

redis_id="$(docker compose --env-file .env ps -q identity-redis)"
test -n "${redis_id}"
wait_container_healthy identity-redis "${redis_id}"

mapfile -t identity_ids < <(docker compose --env-file .env ps -q identity-service | sort)
test "${#identity_ids[@]}" = 2
identity_index=0
for id in "${identity_ids[@]}"; do
  identity_index=$((identity_index + 1))
  wait_container_healthy "identity-service-${identity_index}" "${id}"
done

network="$(docker inspect --format '{{range $name, $_ := .NetworkSettings.Networks}}{{println $name}}{{end}}' "${identity_ids[0]}" | head -n 1)"
test -n "${network}"

base_ms="$(python3 - <<'PY'
import time
print(int(time.time() * 1000))
PY
)"
next_timestamp=0

write_user_event() {
  local path="$1" event_type="$2" user_id="$3" email="$4"
  next_timestamp=$((next_timestamp + 1))
  python3 - "${path}" "${event_type}" "${user_id}" "${email}" "$((base_ms + next_timestamp))" <<'PY'
from pathlib import Path
import json
import sys
path = Path(sys.argv[1])
event_type, user_id, email, timestamp = sys.argv[2], sys.argv[3], sys.argv[4], int(sys.argv[5])
data = {"id": user_id}
if event_type != "user.deleted":
    email_id = "email_cache_fixture"
    data.update({
        "primary_email_address_id": email_id,
        "email_addresses": [{
            "id": email_id,
            "email_address": email,
            "verification": {"status": "verified"},
        }],
    })
path.write_text(json.dumps({"type": event_type, "timestamp": timestamp, "data": data}), encoding="utf-8")
PY
}

send_signed() {
  local event_id="$1" payload="$2" request_id="$3"
  local result
  result="$(
    cd service/identity-service
    go run ../../.github/scripts/clerk-webhook-client.go \
      -url http://127.0.0.1:9080/api/v1/identity/webhooks/clerk \
      -secret "${CLERK_WEBHOOK_SIGNING_SECRET}" \
      -event-id "${event_id}" \
      -payload-file "${payload}" \
      -body-output "${responses}/${request_id}.body" \
      -request-id "${request_id}"
  )"
  test "${result}" = "204|${request_id}"
}

request_me() {
  local token="$1" request_id="$2" body="$3" headers="$4"
  curl --show-error --silent \
    --output "${body}" \
    --dump-header "${headers}" \
    --write-out '%{http_code}' \
    -H "Authorization: Bearer ${token}" \
    -H "X-Request-Id: ${request_id}" \
    http://127.0.0.1:9080/api/v1/me
}

assert_active_response() {
  local path="$1" email="$2"
  python3 - "${path}" "${email}" <<'PY'
from pathlib import Path
import json
import sys
value = json.loads(Path(sys.argv[1]).read_text(encoding="utf-8"))
assert set(value) == {"id", "id_user", "primary_email", "status", "created_at", "updated_at"}, value
assert value["primary_email"] == sys.argv[2], value
assert value["status"] == "active", value
assert isinstance(value["id"], str) and value["id"], value
assert isinstance(value["id_user"], str) and value["id_user"].startswith("bw"), value
assert isinstance(value["created_at"], str) and value["created_at"].endswith("Z"), value
assert isinstance(value["updated_at"], str) and value["updated_at"].endswith("Z"), value
PY
}

assert_error_code() {
  local path="$1" code="$2"
  python3 - "${path}" "${code}" <<'PY'
from pathlib import Path
import json
import sys
value = json.loads(Path(sys.argv[1]).read_text(encoding="utf-8"))
assert set(value) == {"code", "message", "request_id", "details"}, value
assert value["code"] == sys.argv[2], value
assert value["details"] is None, value
PY
}

scrape_all_metrics() {
  local label="$1" output
  output="${metrics_dir}/${label}.prom"
  : > "${output}"
  mapfile -t identity_ids < <(docker compose --env-file .env ps -q identity-service | sort)
  for id in "${identity_ids[@]}"; do
    docker exec "${id}" wget --quiet --output-document=- http://127.0.0.1:9090/metrics >> "${output}"
  done
  printf '%s\n' "${output}"
}

cache_metric_total() {
  local operation="$1" outcome="$2" label="$3" file
  file="$(scrape_all_metrics "${label}")"
  python3 - "${file}" "${operation}" "${outcome}" <<'PY'
from pathlib import Path
import re
import sys
path, operation, outcome = Path(sys.argv[1]), sys.argv[2], sys.argv[3]
metric = re.compile(r'^current_user_cache_operations_total\{([^}]*)\}\s+([0-9.eE+-]+)$')
labels = re.compile(r'([a-zA-Z_][a-zA-Z0-9_]*)="([^"]*)"')
total = 0.0
for line in path.read_text(encoding="utf-8").splitlines():
    match = metric.match(line.strip())
    if not match:
        continue
    values = dict(labels.findall(match.group(1)))
    if values.get("operation") == operation and values.get("outcome") == outcome:
        total += float(match.group(2))
print(int(round(total)))
PY
}

cache_key() {
  python3 - "$1" <<'PY'
import hashlib
import sys
print("bridgeworks:identity:current-user:v1:" + hashlib.sha256(sys.argv[1].encode()).hexdigest())
PY
}

cache_generation_key() {
  python3 - "$1" <<'PY'
import hashlib
import sys
print("bridgeworks:identity:current-user-generation:v1:" + hashlib.sha256(sys.argv[1].encode()).hexdigest())
PY
}

direct_me() {
  local container="$1" token="$2" request_id="$3" output="$4"
  docker exec "${container}" wget --quiet --output-document=- \
    --header="Authorization: Bearer ${token}" \
    --header="X-Request-Id: ${request_id}" \
    http://127.0.0.1:8080/me > "${output}"
}

# 1-4: signed create, first miss, second hit, unchanged response contract.
write_user_event "${work}/user-a-create.json" user.created "${user_a}" cache-a@example.test
send_signed "msg_cache_a_create_$(random_hex)" "${work}/user-a-create.json" cache-a-create
miss_before="$(cache_metric_total get miss before-first-me)"
hit_before="$(cache_metric_total get hit before-first-hit)"
status="$(request_me "${tokens[a]}" cache-a-first "${responses}/a-first.json" "${responses}/a-first.headers")"
test "${status}" = 200
assert_active_response "${responses}/a-first.json" cache-a@example.test
grep --quiet --ignore-case '^Cache-Control: no-store' "${responses}/a-first.headers"
grep --quiet --ignore-case '^Vary:.*Authorization' "${responses}/a-first.headers"
miss_after="$(cache_metric_total get miss after-first-me)"
test "$((miss_after - miss_before))" -ge 1
status="$(request_me "${tokens[a]}" cache-a-second "${responses}/a-second.json" "${responses}/a-second.headers")"
test "${status}" = 200
assert_active_response "${responses}/a-second.json" cache-a@example.test
hit_after="$(cache_metric_total get hit after-second-me)"
test "$((hit_after - hit_before))" -ge 1
cmp --silent "${responses}/a-first.json" "${responses}/a-second.json"

# 5-6: Redis outage is not readiness; PostgreSQL fallback works; Redis recovers without Identity restart.
docker compose --env-file .env stop identity-redis >/dev/null
retry_status 200 http://127.0.0.1:9080/api/v1/identity/health/ready
status="$(request_me "${tokens[a]}" cache-a-redis-down "${responses}/a-redis-down.json" "${responses}/a-redis-down.headers")"
test "${status}" = 200
assert_active_response "${responses}/a-redis-down.json" cache-a@example.test
docker compose --env-file .env start identity-redis >/dev/null
redis_id="$(docker compose --env-file .env ps -q identity-redis)"
deadline=$((SECONDS + 60))
while (( SECONDS < deadline )); do
  [[ "$(docker inspect --format '{{.State.Health.Status}}' "${redis_id}" 2>/dev/null || true)" == healthy ]] && break
  sleep 1
done
test "$(docker inspect --format '{{.State.Health.Status}}' "${redis_id}")" = healthy
hit_recovery_before="$(cache_metric_total get hit before-redis-recovery)"
status="$(request_me "${tokens[a]}" cache-a-recovery-fill "${responses}/a-recovery-fill.json" "${responses}/a-recovery-fill.headers")"
test "${status}" = 200
status="$(request_me "${tokens[a]}" cache-a-recovery-hit "${responses}/a-recovery-hit.json" "${responses}/a-recovery-hit.headers")"
test "${status}" = 200
hit_recovery_after="$(cache_metric_total get hit after-redis-recovery)"
test "$((hit_recovery_after - hit_recovery_before))" -ge 1

# 7: committed update invalidates the shared key and returns the new projection.
write_user_event "${work}/user-a-update.json" user.updated "${user_a}" cache-a-updated@example.test
delete_before="$(cache_metric_total delete success before-update-delete)"
send_signed "msg_cache_a_update_$(random_hex)" "${work}/user-a-update.json" cache-a-update
delete_after="$(cache_metric_total delete success after-update-delete)"
test "$((delete_after - delete_before))" -ge 1
status="$(request_me "${tokens[a]}" cache-a-after-update "${responses}/a-after-update.json" "${responses}/a-after-update.headers")"
test "${status}" = 200
assert_active_response "${responses}/a-after-update.json" cache-a-updated@example.test

# 8: committed delete invalidates an active cache entry; next /me preserves account_deleted.
write_user_event "${work}/user-b-create.json" user.created "${user_b}" cache-b@example.test
send_signed "msg_cache_b_create_$(random_hex)" "${work}/user-b-create.json" cache-b-create
status="$(request_me "${tokens[b]}" cache-b-prime "${responses}/b-prime.json" "${responses}/b-prime.headers")"
test "${status}" = 200
write_user_event "${work}/user-b-delete.json" user.deleted "${user_b}" ""
send_signed "msg_cache_b_delete_$(random_hex)" "${work}/user-b-delete.json" cache-b-delete
status="$(request_me "${tokens[b]}" cache-b-after-delete "${responses}/b-after-delete.json" "${responses}/b-after-delete.headers")"
test "${status}" = 403
assert_error_code "${responses}/b-after-delete.json" account_deleted

# 9-11: invalid webhook does not invalidate; warm cache serves while PostgreSQL is down; cold cache preserves 503.
write_user_event "${work}/user-c-create.json" user.created "${user_c}" cache-c@example.test
send_signed "msg_cache_c_create_$(random_hex)" "${work}/user-c-create.json" cache-c-create
status="$(request_me "${tokens[c]}" cache-c-prime "${responses}/c-prime.json" "${responses}/c-prime.headers")"
test "${status}" = 200
write_user_event "${work}/user-d-create.json" user.created "${user_d}" cache-d@example.test
send_signed "msg_cache_d_create_$(random_hex)" "${work}/user-d-create.json" cache-d-create
invalid_delete_before="$(cache_metric_total delete success before-invalid-webhook)"
invalid_status="$(curl --show-error --silent \
  --output "${responses}/invalid-webhook.json" \
  --write-out '%{http_code}' \
  -H 'Content-Type: application/json' \
  -H 'X-Request-Id: cache-invalid-webhook' \
  -H 'svix-id: msg_cache_invalid' \
  -H "svix-timestamp: $(date +%s)" \
  -H 'svix-signature: v1,invalid-signature' \
  --data-binary "@${work}/user-c-create.json" \
  http://127.0.0.1:9080/api/v1/identity/webhooks/clerk)"
test "${invalid_status}" = 400
invalid_delete_after="$(cache_metric_total delete success after-invalid-webhook)"
test "${invalid_delete_after}" = "${invalid_delete_before}"

docker compose --env-file .env stop identity-postgres >/dev/null
retry_status 503 http://127.0.0.1:9080/api/v1/identity/health/ready
status="$(request_me "${tokens[c]}" cache-c-warm-db-down "${responses}/c-warm-db-down.json" "${responses}/c-warm-db-down.headers")"
test "${status}" = 200
assert_active_response "${responses}/c-warm-db-down.json" cache-c@example.test
status="$(request_me "${tokens[d]}" cache-d-cold-db-down "${responses}/d-cold-db-down.json" "${responses}/d-cold-db-down.headers")"
test "${status}" = 503
assert_error_code "${responses}/d-cold-db-down.json" service_unavailable

docker compose --env-file .env start identity-postgres >/dev/null
postgres_id="$(docker compose --env-file .env ps -q identity-postgres)"
deadline=$((SECONDS + 90))
while (( SECONDS < deadline )); do
  [[ "$(docker inspect --format '{{.State.Health.Status}}' "${postgres_id}" 2>/dev/null || true)" == healthy ]] && break
  sleep 1
done
test "$(docker inspect --format '{{.State.Health.Status}}' "${postgres_id}")" = healthy
retry_status 200 http://127.0.0.1:9080/api/v1/identity/health/ready

# 12: two Identity replicas share Redis and observe post-commit invalidation.
write_user_event "${work}/user-e-create.json" user.created "${user_e}" cache-e@example.test
send_signed "msg_cache_e_create_$(random_hex)" "${work}/user-e-create.json" cache-e-create
mapfile -t identity_ids < <(docker compose --env-file .env ps -q identity-service | sort)
test "${#identity_ids[@]}" = 2
direct_me "${identity_ids[0]}" "${tokens[e]}" cache-e-replica-a "${responses}/e-replica-a.json"
assert_active_response "${responses}/e-replica-a.json" cache-e@example.test
direct_me "${identity_ids[1]}" "${tokens[e]}" cache-e-replica-b "${responses}/e-replica-b.json"
assert_active_response "${responses}/e-replica-b.json" cache-e@example.test
write_user_event "${work}/user-e-update.json" user.updated "${user_e}" cache-e-shared-updated@example.test
send_signed "msg_cache_e_update_$(random_hex)" "${work}/user-e-update.json" cache-e-update
direct_me "${identity_ids[0]}" "${tokens[e]}" cache-e-updated-a "${responses}/e-updated-a.json"
assert_active_response "${responses}/e-updated-a.json" cache-e-shared-updated@example.test
direct_me "${identity_ids[1]}" "${tokens[e]}" cache-e-updated-b "${responses}/e-updated-b.json"
assert_active_response "${responses}/e-updated-b.json" cache-e-shared-updated@example.test

# 13-14: private Redis port and no APISIX Redis route/dependency.
redis_id="$(docker compose --env-file .env ps -q identity-redis)"
port_bindings="$(docker inspect --format '{{json .HostConfig.PortBindings}}' "${redis_id}")"
case "${port_bindings}" in
  null|'{}') ;;
  *) printf 'identity cache integration failed: Redis published host ports\n' >&2; exit 1 ;;
esac
if grep -R --line-number --ignore-case 'redis' gateway/apisix; then
  printf 'identity cache integration failed: APISIX references Redis\n' >&2
  exit 1
fi
apisix_id="$(docker compose --env-file .env ps -q apisix)"
inspect_apisix="$(docker inspect "${apisix_id}")"
if grep --quiet --fixed-strings 'identity-redis' <<< "${inspect_apisix}"; then
  printf 'identity cache integration failed: APISIX depends on identity-redis\n' >&2
  exit 1
fi

# 15: retained responses, private metrics, and Identity logs are sanitized.
all_metrics="$(scrape_all_metrics final)"
identity_logs="${work}/identity.log"
docker compose --env-file .env logs --no-color identity-service > "${identity_logs}"
for forbidden in \
  "${redis_password}" \
  "${REDIS_ADDR:-}" \
  "${tokens[a]}" "${tokens[b]}" "${tokens[c]}" "${tokens[d]}" "${tokens[e]}" \
  "${sessions[a]}" "${sessions[b]}" "${sessions[c]}" "${sessions[d]}" "${sessions[e]}" \
  "${user_a}" "${user_b}" "${user_c}" "${user_d}" "${user_e}" \
  "$(cache_key "${user_a}")" "$(cache_key "${user_b}")" "$(cache_key "${user_c}")" \
  "$(cache_key "${user_d}")" "$(cache_key "${user_e}")" \
  "$(cache_generation_key "${user_a}")" "$(cache_generation_key "${user_b}")" \
  "$(cache_generation_key "${user_c}")" "$(cache_generation_key "${user_d}")" \
  "$(cache_generation_key "${user_e}")"; do
  [[ -n "${forbidden}" ]] || continue
  if grep --quiet --fixed-strings -- "${forbidden}" "${identity_logs}" "${all_metrics}"; then
    printf 'identity cache integration failed: sensitive runtime value retained\n' >&2
    exit 1
  fi
done
if grep --quiet --extended-regexp '[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}' "${identity_logs}" "${all_metrics}"; then
  printf 'identity cache integration failed: email retained in logs or metrics\n' >&2
  exit 1
fi
if grep --quiet --extended-regexp 'connection refused|dial tcp|i/o timeout|redis: nil|WRONGPASS|NOAUTH' "${identity_logs}"; then
  printf 'identity cache integration failed: raw Redis error retained in logs\n' >&2
  exit 1
fi
grep --quiet 'current_user_cache_operations_total' "${all_metrics}"
grep --quiet 'operation="get"' "${all_metrics}"
grep --quiet 'operation="set"' "${all_metrics}"
grep --quiet 'operation="delete"' "${all_metrics}"

# Cleanup must remove all project containers, networks, and volumes deterministically.
cleanup
cleanup_complete=true
remaining_containers="$(docker ps -aq --filter "label=com.docker.compose.project=${project}")"
remaining_projects="$(docker compose ls --format json | python3 -c 'import json,sys; print("\n".join(item["Name"] for item in json.load(sys.stdin) if item["Name"] == sys.argv[1]))' "${project}")"
remaining_networks="$(docker network ls --format '{{.Name}}' | grep "^${project}_" || true)"
remaining_volumes="$(docker volume ls --format '{{.Name}}' | grep "^${project}_" || true)"
test -z "${remaining_containers}"
test -z "${remaining_projects}"
test -z "${remaining_networks}"
test -z "${remaining_volumes}"

printf 'Identity current-user cache integration passed.\n'
