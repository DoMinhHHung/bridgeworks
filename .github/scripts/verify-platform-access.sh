#!/usr/bin/env bash
set -Eeuo pipefail

phase=bootstrap
trap 'printf "Platform access integration failed phase=%s line=%s\n" "${phase}" "${LINENO}" >&2' ERR

set -a
source .env
set +a

work="${RUNNER_TEMP:?RUNNER_TEMP is required}/platform-access"
auth_work="${RUNNER_TEMP}/clerk-auth"
mkdir -p "${work}"

base_url='http://127.0.0.1:9080'
webhook_url="${base_url}/api/v1/identity/webhooks/clerk"
private_url='http://identity-service:8080/internal/v1/platform-access/me'
public_internal_url="${base_url}/internal/v1/platform-access/me"

postgres_id="$(docker compose ps -q identity-postgres)"
identity_id="$(docker compose ps -q identity-service)"
postgres_user="$(docker inspect --format '{{range .Config.Env}}{{println .}}{{end}}' "${postgres_id}" | sed -n 's/^POSTGRES_USER=//p')"
postgres_db="$(docker inspect --format '{{range .Config.Env}}{{println .}}{{end}}' "${postgres_id}" | sed -n 's/^POSTGRES_DB=//p')"
network="$(docker inspect --format '{{range $name, $_ := .NetworkSettings.Networks}}{{println $name}}{{end}}' "${identity_id}" | head -n 1)"

test -n "${postgres_user}"
test -n "${postgres_db}"
test -n "${network}"
test -n "${PLATFORM_ACCESS_DATABASE_URL}"
test -f "${auth_work}/private.pem"

if docker inspect --format '{{range .Config.Env}}{{println .}}{{end}}' "${identity_id}" | grep --quiet '^PLATFORM_ACCESS_DATABASE_URL='; then
  echo 'identity-service runtime must not receive the platform access operator database credential' >&2
  exit 1
fi

psql_value() {
  docker compose exec -T identity-postgres psql \
    --username "${postgres_user}" \
    --dbname "${postgres_db}" \
    --set ON_ERROR_STOP=1 \
    --tuples-only \
    --no-align \
    --field-separator '|' \
    --command "$1" | sed '/^[[:space:]]*$/d'
}

send_signed() {
  local event_id="$1" payload_file="$2" body_output="$3" request_id="$4"
  (
    cd service/identity-service
    go run ../../.github/scripts/clerk-webhook-client.go \
      -url "${webhook_url}" \
      -secret "${CLERK_WEBHOOK_SIGNING_SECRET}" \
      -event-id "${event_id}" \
      -payload-file "${payload_file}" \
      -body-output "${body_output}" \
      -request-id "${request_id}"
  )
}

phase=identity-projection
cat > "${work}/platform-user.json" <<'JSON'
{"type":"user.created","timestamp":1785751000000,"data":{"id":"user_ci_platform_access","primary_email_address_id":"email_ci_platform_access","email_addresses":[{"id":"email_ci_platform_access","email_address":"platform-ci@example.test","verification":{"status":"verified"}}]}}
JSON
test "$(send_signed msg_ci_platform_access "${work}/platform-user.json" "${work}/platform-user-response" platform-user-create)" = "204|platform-user-create"
id_user="$(psql_value "select id_user from app.app_users where clerk_user_id='user_ci_platform_access'")"
test -n "${id_user}"
test "$(psql_value "select count(*) from app.platform_access_assignments where identity_user_id=(select id from app.app_users where clerk_user_id='user_ci_platform_access')")" = "0"

phase=session
(
  cd service/identity-service
  go run ../../.github/scripts/clerk-session-token.go \
    -action sign \
    -private-key "${auth_work}/private.pem" \
    -issuer "${CLERK_ISSUER}" \
    -subject user_ci_platform_access \
    -session-id sess_ci_platform_access_same_session \
    -authorized-party "${CLERK_AUTHORIZED_PARTIES%%,*}" \
    -output "${auth_work}/platform-access.token"
)

test -s "${auth_work}/platform-access.token"

private_request() {
  local request_id="$1" prefix="$2" forged="${3:-false}"
  docker run --rm \
    --user "$(id -u):$(id -g)" \
    --network "${network}" \
    --volume "${auth_work}:/auth:ro" \
    --volume "${work}:/work" \
    --env "REQUEST_ID=${request_id}" \
    --env "PRIVATE_URL=${private_url}" \
    --env "BODY=/work/${prefix}-body.json" \
    --env "HEADERS=/work/${prefix}-headers" \
    --env "FORGED=${forged}" \
    --entrypoint /bin/sh \
    curlimages/curl:8.17.0 \
    -ec '
      if [ "${FORGED}" = "true" ]; then
        curl --show-error --silent \
          --output "${BODY}" \
          --dump-header "${HEADERS}" \
          --write-out "%{http_code}" \
          -H "Authorization: Bearer $(cat /auth/platform-access.token)" \
          -H "X-Request-Id: ${REQUEST_ID}" \
          -H "X-Platform-Admin: true" \
          "${PRIVATE_URL}"
      else
        curl --show-error --silent \
          --output "${BODY}" \
          --dump-header "${HEADERS}" \
          --write-out "%{http_code}" \
          -H "Authorization: Bearer $(cat /auth/platform-access.token)" \
          -H "X-Request-Id: ${REQUEST_ID}" \
          "${PRIVATE_URL}"
      fi
    '
}

assert_private_headers() {
  local prefix="$1" request_id="$2"
  grep --quiet --ignore-case "^X-Request-Id: ${request_id}" "${work}/${prefix}-headers"
  grep --quiet --ignore-case '^Cache-Control: no-store' "${work}/${prefix}-headers"
  grep --quiet --ignore-case '^Vary:.*Authorization' "${work}/${prefix}-headers"
}

phase=ordinary-and-forged-header
status="$(private_request platform-before-grant before-grant true)"
test "${status}" = "200"
assert_private_headers before-grant platform-before-grant
python3 - "${work}/before-grant-body.json" <<'PY'
import json, sys
body=json.load(open(sys.argv[1], encoding='utf-8'))
assert body == {'roles': [], 'permissions': []}
PY

run_operator() {
  local command="$1" output="$2"
  PLATFORM_ACCESS_DATABASE_URL="${PLATFORM_ACCESS_DATABASE_URL}" \
  PLATFORM_ACCESS_DATABASE_CONNECT_TIMEOUT="${PLATFORM_ACCESS_DATABASE_CONNECT_TIMEOUT}" \
  PLATFORM_ACCESS_COMMAND_TIMEOUT="${PLATFORM_ACCESS_COMMAND_TIMEOUT}" \
    docker compose run --rm --no-deps \
      -e PLATFORM_ACCESS_DATABASE_URL \
      -e PLATFORM_ACCESS_DATABASE_CONNECT_TIMEOUT \
      -e PLATFORM_ACCESS_COMMAND_TIMEOUT \
      --entrypoint /usr/local/bin/identity-platform-access \
      identity-service "${command}" --id-user "${id_user}" > "${output}"
}

phase=grant
run_operator grant "${work}/operator-grant.log"
test "$(psql_value "select role, revoked_at is null from app.platform_access_assignments where identity_user_id=(select id from app.app_users where clerk_user_id='user_ci_platform_access')")" = "platform_admin|t"
status="$(private_request platform-after-grant after-grant false)"
test "${status}" = "200"
assert_private_headers after-grant platform-after-grant
python3 - "${work}/after-grant-body.json" <<'PY'
import json, sys
body=json.load(open(sys.argv[1], encoding='utf-8'))
assert body == {
    'roles': ['platform_admin'],
    'permissions': ['organization.verification.review'],
}
PY

phase=immediate-revocation
run_operator revoke "${work}/operator-revoke.log"
test "$(psql_value "select revoked_at is not null from app.platform_access_assignments where identity_user_id=(select id from app.app_users where clerk_user_id='user_ci_platform_access')")" = "t"
status="$(private_request platform-after-revoke after-revoke false)"
test "${status}" = "200"
assert_private_headers after-revoke platform-after-revoke
python3 - "${work}/after-revoke-body.json" <<'PY'
import json, sys
body=json.load(open(sys.argv[1], encoding='utf-8'))
assert body == {'roles': [], 'permissions': []}
PY

phase=regrant
run_operator grant "${work}/operator-regrant.log"
status="$(private_request platform-after-regrant after-regrant false)"
test "${status}" = "200"
grep --quiet '"organization.verification.review"' "${work}/after-regrant-body.json"
run_operator status "${work}/operator-status.log"

phase=final-revoke
run_operator revoke "${work}/operator-final-revoke.log"
status="$(private_request platform-after-final-revoke after-final-revoke false)"
test "${status}" = "200"
grep --quiet '"permissions":\[\]' "${work}/after-final-revoke-body.json"

phase=apisix-non-exposure
public_body="${work}/public-internal-body"
public_status="$(curl --show-error --silent --output "${public_body}" --write-out '%{http_code}' \
  -H "Authorization: Bearer $(cat "${auth_work}/platform-access.token")" \
  "${public_internal_url}")"
test "${public_status}" = "404"
! grep -R --line-number --fixed-strings '/internal/v1/platform-access/me' gateway/apisix/conf/apisix.yaml gateway/apisix/conf/apisix.cloud-run.yaml

phase=sensitive-log-scan
docker compose logs --no-color identity-service > "${work}/identity-service.log"
python3 - "${work}" "${auth_work}/platform-access.token" "${DATABASE_URL}" "${MIGRATION_DATABASE_URL}" "${PLATFORM_ACCESS_DATABASE_URL}" "${IDENTITY_POSTGRES_PASSWORD}" <<'PY'
import pathlib, sys
work=pathlib.Path(sys.argv[1])
token=pathlib.Path(sys.argv[2]).read_text(encoding='utf-8').strip()
forbidden=[
    token,
    sys.argv[3],
    sys.argv[4],
    sys.argv[5],
    sys.argv[6],
    'platform-ci@example.test',
    'Authorization: Bearer',
    'CLERK_WEBHOOK_SIGNING_SECRET',
    'postgres://',
]
texts=[]
for name in [
    'identity-service.log',
    'operator-grant.log',
    'operator-revoke.log',
    'operator-regrant.log',
    'operator-status.log',
    'operator-final-revoke.log',
]:
    texts.append((work / name).read_text(encoding='utf-8'))
combined='\n'.join(texts)
for value in forbidden:
    if value:
        assert value not in combined, f'sensitive platform-access value leaked: {value[:32]!r}'
PY

phase=complete
echo 'Identity platform access integration tests passed.'