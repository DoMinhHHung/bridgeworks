#!/usr/bin/env bash
set -euo pipefail

set -a
source .env
set +a

auth_work="${RUNNER_TEMP:?RUNNER_TEMP is required}/clerk-auth"
response_work="${auth_work}/browser-responses"
mkdir -p "${response_work}"

me_url='http://127.0.0.1:9080/api/v1/me'
allowed_origin="$(printf '%s' "${CLERK_AUTHORIZED_PARTIES%%,*}" | xargs)"
test -n "${allowed_origin}"

for required_file in \
  recovery.token \
  missing-user.token \
  invalid-signature.token \
  wrong-issuer.token \
  wrong-party.token; do
  test -s "${auth_work}/${required_file}"
done
printf 'not-a-jwt' > "${auth_work}/malformed.token"

postgres_id="$(docker compose ps -q identity-postgres)"
postgres_user="$(docker inspect --format '{{range .Config.Env}}{{println .}}{{end}}' "${postgres_id}" | sed -n 's/^POSTGRES_USER=//p')"
postgres_db="$(docker inspect --format '{{range .Config.Env}}{{println .}}{{end}}' "${postgres_id}" | sed -n 's/^POSTGRES_DB=//p')"

psql_value() {
  docker compose exec -T identity-postgres psql \
    --username "${postgres_user}" \
    --dbname "${postgres_db}" \
    --set ON_ERROR_STOP=1 \
    --tuples-only \
    --no-align \
    --command "$1" | sed '/^[[:space:]]*$/d'
}

request_me() {
  local token_file="$1"
  local request_id="$2"
  local origin="$3"
  local body_file="$4"
  local header_file="$5"
  local args=(
    --show-error
    --silent
    --output "${body_file}"
    --dump-header "${header_file}"
    --write-out '%{http_code}'
    -H "X-Request-Id: ${request_id}"
  )
  if [[ -n "${token_file}" ]]; then
    args+=(-H "Authorization: Bearer $(cat "${token_file}")")
  fi
  if [[ -n "${origin}" ]]; then
    args+=(-H "Origin: ${origin}")
  fi
  curl "${args[@]}" "${me_url}"
}

assert_header_exact() {
  local header_file="$1"
  local name="$2"
  local expected="$3"
  python3 - "${header_file}" "${name}" "${expected}" <<'PY'
import pathlib
import sys

path, name, expected = sys.argv[1:]
values = []
for line in pathlib.Path(path).read_text(encoding="utf-8").replace("\r", "").splitlines():
    if ":" not in line:
        continue
    key, value = line.split(":", 1)
    if key.strip().lower() == name.lower():
        values.append(value.strip())
assert expected in values, f"{name}={values!r}, expected {expected!r}"
PY
}

assert_header_token() {
  local header_file="$1"
  local name="$2"
  local expected="$3"
  python3 - "${header_file}" "${name}" "${expected}" <<'PY'
import pathlib
import sys

path, name, expected = sys.argv[1:]
tokens = []
for line in pathlib.Path(path).read_text(encoding="utf-8").replace("\r", "").splitlines():
    if ":" not in line:
        continue
    key, value = line.split(":", 1)
    if key.strip().lower() == name.lower():
        tokens.extend(part.strip().lower() for part in value.split(",") if part.strip())
assert expected.lower() in tokens, f"{name} tokens={tokens!r}, missing {expected!r}"
PY
}

assert_header_absent() {
  local header_file="$1"
  local name="$2"
  python3 - "${header_file}" "${name}" <<'PY'
import pathlib
import sys

path, name = sys.argv[1:]
values = []
for line in pathlib.Path(path).read_text(encoding="utf-8").replace("\r", "").splitlines():
    if ":" not in line:
        continue
    key, value = line.split(":", 1)
    if key.strip().lower() == name.lower():
        values.append(value.strip())
assert not values, f"{name} must be absent, got {values!r}"
PY
}

assert_no_store() {
  local header_file="$1"
  assert_header_exact "${header_file}" Cache-Control no-store
  assert_header_token "${header_file}" Vary Authorization
}

assert_bearer_challenge() {
  assert_header_exact "$1" WWW-Authenticate 'Bearer realm="bridgeworks"'
}

allowed_preflight_headers="${response_work}/allowed-preflight-headers"
allowed_preflight_body="${response_work}/allowed-preflight-body"
allowed_preflight_status="$(curl --show-error --silent \
  --request OPTIONS \
  --output "${allowed_preflight_body}" \
  --dump-header "${allowed_preflight_headers}" \
  --write-out '%{http_code}' \
  -H "Origin: ${allowed_origin}" \
  -H 'Access-Control-Request-Method: GET' \
  -H 'Access-Control-Request-Headers: Authorization' \
  "${me_url}")"
case "${allowed_preflight_status}" in
  2??) ;;
  *)
    echo "allowed preflight status=${allowed_preflight_status}" >&2
    exit 1
    ;;
esac
assert_header_exact "${allowed_preflight_headers}" Access-Control-Allow-Origin "${allowed_origin}"
assert_header_token "${allowed_preflight_headers}" Access-Control-Allow-Methods GET
assert_header_token "${allowed_preflight_headers}" Access-Control-Allow-Headers Authorization
assert_header_absent "${allowed_preflight_headers}" Access-Control-Allow-Credentials

blocked_preflight_headers="${response_work}/blocked-preflight-headers"
blocked_preflight_body="${response_work}/blocked-preflight-body"
blocked_preflight_status="$(curl --show-error --silent \
  --request OPTIONS \
  --output "${blocked_preflight_body}" \
  --dump-header "${blocked_preflight_headers}" \
  --write-out '%{http_code}' \
  -H 'Origin: https://attacker.example' \
  -H 'Access-Control-Request-Method: GET' \
  -H 'Access-Control-Request-Headers: Authorization' \
  "${me_url}")"
case "${blocked_preflight_status}" in
  2??) ;;
  *)
    echo "blocked preflight status=${blocked_preflight_status}" >&2
    exit 1
    ;;
esac
assert_header_absent "${blocked_preflight_headers}" Access-Control-Allow-Origin

actual_status="$(request_me \
  "${auth_work}/recovery.token" \
  me-cors-actual \
  "${allowed_origin}" \
  "${response_work}/actual-body.json" \
  "${response_work}/actual-headers")"
test "${actual_status}" = "200"
assert_header_exact "${response_work}/actual-headers" Access-Control-Allow-Origin "${allowed_origin}"
assert_header_token "${response_work}/actual-headers" Access-Control-Expose-Headers X-Request-Id
assert_header_token "${response_work}/actual-headers" Access-Control-Expose-Headers Retry-After
assert_header_token "${response_work}/actual-headers" Vary Origin
assert_header_absent "${response_work}/actual-headers" Access-Control-Allow-Credentials
assert_no_store "${response_work}/actual-headers"

for scenario in \
  'missing||me-cache-401-missing' \
  'malformed|malformed.token|me-cache-401-malformed' \
  'invalid-signature|invalid-signature.token|me-cache-401-signature' \
  'wrong-issuer|wrong-issuer.token|me-cache-401-issuer' \
  'wrong-party|wrong-party.token|me-cache-401-party'; do
  IFS='|' read -r name token_name request_id <<< "${scenario}"
  token_file=''
  if [[ -n "${token_name}" ]]; then
    token_file="${auth_work}/${token_name}"
  fi
  status="$(request_me \
    "${token_file}" \
    "${request_id}" \
    '' \
    "${response_work}/${name}-body.json" \
    "${response_work}/${name}-headers")"
  test "${status}" = "401"
  assert_no_store "${response_work}/${name}-headers"
  assert_bearer_challenge "${response_work}/${name}-headers"
done

test "$(psql_value "select count(*) from app.app_users where clerk_user_id = 'user_ci_me_missing'")" = "0"
not_ready_status="$(request_me \
  "${auth_work}/missing-user.token" \
  me-cache-409 \
  '' \
  "${response_work}/not-ready-body.json" \
  "${response_work}/not-ready-headers")"
test "${not_ready_status}" = "409"
assert_no_store "${response_work}/not-ready-headers"
assert_header_exact "${response_work}/not-ready-headers" Retry-After 2
test "$(psql_value "select count(*) from app.app_users where clerk_user_id = 'user_ci_me_missing'")" = "0"

psql_value "update app.app_users set status = 'disabled' where clerk_user_id = 'user_ci_outage' returning status" | grep --quiet '^disabled$'
disabled_status="$(request_me \
  "${auth_work}/recovery.token" \
  me-cache-403-disabled \
  '' \
  "${response_work}/disabled-body.json" \
  "${response_work}/disabled-headers")"
test "${disabled_status}" = "403"
assert_no_store "${response_work}/disabled-headers"

psql_value "update app.app_users set status = 'deleted', primary_email = null where clerk_user_id = 'user_ci_outage' returning status" | grep --quiet '^deleted$'
deleted_status="$(request_me \
  "${auth_work}/recovery.token" \
  me-cache-403-deleted \
  '' \
  "${response_work}/deleted-body.json" \
  "${response_work}/deleted-headers")"
test "${deleted_status}" = "403"
assert_no_store "${response_work}/deleted-headers"

identity_before="$(docker compose ps -q identity-service)"
docker compose stop identity-postgres
outage_status="$(request_me \
  "${auth_work}/recovery.token" \
  me-cache-503 \
  '' \
  "${response_work}/outage-body.json" \
  "${response_work}/outage-headers")"
test "${outage_status}" = "503"
assert_no_store "${response_work}/outage-headers"

docker compose start identity-postgres
make gateway-smoke
test "$(docker compose ps -q identity-service)" = "${identity_before}"
