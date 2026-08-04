#!/usr/bin/env bash
set -euo pipefail

set -a
source .env
set +a

work="${RUNNER_TEMP:?RUNNER_TEMP is required}/organization-integration"
auth_work="${RUNNER_TEMP}/clerk-auth"
mkdir -p "${work}" "${auth_work}"

base_url='http://127.0.0.1:9080'
identity_webhook_url="${base_url}/api/v1/identity/webhooks/clerk"
organization_webhook_url="${base_url}/api/v1/organizations/webhooks/clerk"
current_url="${base_url}/api/v1/organizations/current"
membership_url="${base_url}/api/v1/organizations/current/membership"
allowed_origin="${CLERK_AUTHORIZED_PARTIES%%,*}"

identity_postgres_id="$(docker compose ps -q identity-postgres)"
identity_postgres_user="$(docker inspect --format '{{range .Config.Env}}{{println .}}{{end}}' "${identity_postgres_id}" | sed -n 's/^POSTGRES_USER=//p')"
identity_postgres_db="$(docker inspect --format '{{range .Config.Env}}{{println .}}{{end}}' "${identity_postgres_id}" | sed -n 's/^POSTGRES_DB=//p')"
organization_postgres_id="$(docker compose ps -q organization-postgres)"
organization_postgres_user="$(docker inspect --format '{{range .Config.Env}}{{println .}}{{end}}' "${organization_postgres_id}" | sed -n 's/^POSTGRES_USER=//p')"
organization_postgres_db="$(docker inspect --format '{{range .Config.Env}}{{println .}}{{end}}' "${organization_postgres_id}" | sed -n 's/^POSTGRES_DB=//p')"

identity_sql() {
  docker compose exec -T identity-postgres psql \
    --username "${identity_postgres_user}" --dbname "${identity_postgres_db}" \
    --set ON_ERROR_STOP=1 --tuples-only --no-align --field-separator '|' \
    --command "$1" | sed '/^[[:space:]]*$/d'
}

organization_sql() {
  docker compose exec -T organization-postgres psql \
    --username "${organization_postgres_user}" --dbname "${organization_postgres_db}" \
    --set ON_ERROR_STOP=1 --tuples-only --no-align --field-separator '|' \
    --command "$1" | sed '/^[[:space:]]*$/d'
}

send_signed() {
  local url="$1" secret="$2" event_id="$3" payload="$4" output="$5" request_id="$6"
  (
    cd service/organization-service
    go run ../../.github/scripts/clerk-webhook-client.go \
      -url "${url}" -secret "${secret}" -event-id "${event_id}" \
      -payload-file "${payload}" -body-output "${output}" -request-id "${request_id}"
  )
}

sign_token() {
  local subject="$1" session_id="$2" organization_id="$3" role="$4" output="$5"
  local args=(
    -action sign
    -private-key "${auth_work}/private.pem"
    -issuer "${CLERK_ISSUER}"
    -subject "${subject}"
    -session-id "${session_id}"
    -authorized-party "${allowed_origin}"
    -output "${output}"
  )
  if [[ -n "${organization_id}" ]]; then
    args+=(-organization-id "${organization_id}")
  fi
  if [[ -n "${role}" ]]; then
    args+=(-organization-role "${role}" -organization-permissions 'organization:manage,membership:manage')
  fi
  (cd service/organization-service && go run ../../.github/scripts/clerk-session-token.go "${args[@]}")
}

request_get() {
  local url="$1" token_file="$2" request_id="$3" body_file="$4" header_file="$5"
  curl --show-error --silent --output "${body_file}" --dump-header "${header_file}" \
    --write-out '%{http_code}' \
    -H "Authorization: Bearer $(cat "${token_file}")" \
    -H "X-Request-Id: ${request_id}" \
    -H "Origin: ${allowed_origin}" \
    "${url}"
}

assert_header_token() {
  local file="$1" header="$2" token="$3"
  python3 - "${file}" "${header}" "${token}" <<'PY'
import pathlib, sys
path, wanted, token = sys.argv[1:]
values=[]
for line in pathlib.Path(path).read_text(encoding='utf-8').replace('\r','').splitlines():
    if ':' not in line: continue
    key, value=line.split(':',1)
    if key.strip().lower()==wanted.lower():
        values.extend(part.strip().lower() for part in value.split(',') if part.strip())
assert token.lower() in values, (wanted, values, token)
PY
}

assert_header_exact() {
  local file="$1" header="$2" value="$3"
  grep --quiet --ignore-case "^${header}: ${value}" "${file}"
}

assert_error() {
  local body="$1" code="$2" message="$3" request_id="$4"
  grep --quiet "\"code\":\"${code}\"" "${body}"
  grep --quiet "\"message\":\"${message}\"" "${body}"
  grep --quiet "\"request_id\":\"${request_id}\"" "${body}"
  grep --quiet '"details":null' "${body}"
}

# Invalid signature must not mutate Organization database.
cat > "${work}/invalid.json" <<'JSON'
{"type":"organization.created","timestamp":1785744000000,"data":{"id":"org_ci_invalid","name":"Sensitive Invalid Org","slug":"invalid"}}
JSON
invalid_status="$(curl --show-error --silent --output "${work}/invalid-body" --dump-header "${work}/invalid-headers" --write-out '%{http_code}' \
  -H 'Content-Type: application/json' -H 'X-Request-Id: org-invalid-webhook' \
  -H 'svix-id: msg_org_invalid' -H "svix-timestamp: $(date +%s)" \
  -H 'svix-signature: v1,invalid' --data-binary "@${work}/invalid.json" "${organization_webhook_url}")"
test "${invalid_status}" = "400"
assert_error "${work}/invalid-body" invalid_webhook 'invalid webhook request' org-invalid-webhook
test "$(organization_sql 'select count(*) from organization.organizations')" = "0"
test "$(organization_sql 'select count(*) from organization.memberships')" = "0"
test "$(organization_sql 'select count(*) from organization.clerk_webhook_events')" = "0"

# Synchronize Identity user used by authenticated Organization requests.
cat > "${work}/identity-user.json" <<'JSON'
{"type":"user.created","timestamp":1785744000000,"data":{"id":"user_org_ci_admin","primary_email_address_id":"email_org_ci","email_addresses":[{"id":"email_org_ci","email_address":"organization-ci@example.test","verification":{"status":"verified"}}]}}
JSON
test "$(send_signed "${identity_webhook_url}" "${CLERK_WEBHOOK_SIGNING_SECRET}" msg_identity_org_ci "${work}/identity-user.json" "${work}/identity-user-response" identity-org-ci)" = "204|identity-org-ci"
test "$(identity_sql "select status from app.app_users where clerk_user_id='user_org_ci_admin'")" = "active"

# Membership-first creates a pending organization and an active admin membership.
cat > "${work}/membership-first.json" <<'JSON'
{"type":"organizationMembership.created","timestamp":1785744010000,"data":{"id":"mem_org_ci_admin","organization":{"id":"org_ci_primary"},"public_user_data":{"user_id":"user_org_ci_admin","identifier":"must-not-persist@example.test"},"role":"org:admin"}}
JSON
test "$(send_signed "${organization_webhook_url}" "${CLERK_ORGANIZATION_WEBHOOK_SIGNING_SECRET}" msg_membership_first "${work}/membership-first.json" "${work}/membership-first-response" membership-first-ci)" = "204|membership-first-ci"
test "$(organization_sql "select status from organization.organizations where clerk_organization_id='org_ci_primary'")" = "pending"
test "$(organization_sql "select status, application_role from organization.memberships where clerk_membership_id='mem_org_ci_admin'")" = "active|admin"

sign_token user_org_ci_admin sess_org_ci_admin org_ci_primary org:member "${auth_work}/organization-admin.token"
sign_token user_org_ci_admin sess_org_ci_no_context '' '' "${auth_work}/organization-no-context.token"

pending_status="$(request_get "${current_url}" "${auth_work}/organization-admin.token" pending-org "${work}/pending-body" "${work}/pending-headers")"
test "${pending_status}" = "409"
assert_error "${work}/pending-body" organization_not_ready 'organization is not ready' pending-org
assert_header_exact "${work}/pending-headers" Retry-After 2
assert_header_exact "${work}/pending-headers" Cache-Control no-store
assert_header_token "${work}/pending-headers" Vary Authorization

# Organization create promotes placeholder while preserving local UUID.
pending_id="$(organization_sql "select id::text from organization.organizations where clerk_organization_id='org_ci_primary'")"
cat > "${work}/organization-create.json" <<'JSON'
{"type":"organization.created","timestamp":1785744020000,"data":{"id":"org_ci_primary","name":"  BridgeWorks CI  ","slug":" bridgeworks-ci "}}
JSON
test "$(send_signed "${organization_webhook_url}" "${CLERK_ORGANIZATION_WEBHOOK_SIGNING_SECRET}" msg_org_create "${work}/organization-create.json" "${work}/organization-create-response" organization-create-ci)" = "204|organization-create-ci"
test "$(organization_sql "select id::text, status, name, slug from organization.organizations where clerk_organization_id='org_ci_primary'")" = "${pending_id}|active|BridgeWorks CI|bridgeworks-ci"

# Duplicate event is idempotent.
organization_before="$(organization_sql "select id::text, created_at::text, updated_at::text from organization.organizations where clerk_organization_id='org_ci_primary'")"
test "$(send_signed "${organization_webhook_url}" "${CLERK_ORGANIZATION_WEBHOOK_SIGNING_SECRET}" msg_org_create "${work}/organization-create.json" "${work}/organization-duplicate-response" organization-duplicate-ci)" = "204|organization-duplicate-ci"
test "$(organization_sql "select id::text, created_at::text, updated_at::text from organization.organizations where clerk_organization_id='org_ci_primary'")" = "${organization_before}"
test "$(organization_sql "select count(*) from organization.clerk_webhook_events where event_id='msg_org_create'")" = "1"

# JWT claims admin while local role remains authoritative admin from webhook initialization.
active_status="$(request_get "${current_url}" "${auth_work}/organization-admin.token" current-org "${work}/current-body" "${work}/current-headers")"
test "${active_status}" = "200"
assert_header_exact "${work}/current-headers" Cache-Control no-store
assert_header_exact "${work}/current-headers" Access-Control-Allow-Origin "${allowed_origin}"
assert_header_token "${work}/current-headers" Vary Authorization
assert_header_token "${work}/current-headers" Vary Origin
python3 - "${work}/current-body" "${pending_id}" <<'PY'
import json, sys
with open(sys.argv[1], encoding='utf-8') as f: body=json.load(f)
assert body == {'id':sys.argv[2], 'name':'BridgeWorks CI', 'slug':'bridgeworks-ci', 'status':'active'}
assert all('clerk' not in key for key in body)
PY

membership_status="$(request_get "${membership_url}" "${auth_work}/organization-admin.token" current-membership "${work}/membership-body" "${work}/membership-headers")"
test "${membership_status}" = "200"
python3 - "${work}/membership-body" <<'PY'
import json, sys
with open(sys.argv[1], encoding='utf-8') as f: body=json.load(f)
assert body['role']=='admin'
assert body['permissions']==['membership.manage','membership.read','organization.manage','organization.read']
assert set(body)=={'id','organization_id','role','permissions'}
PY

# Valid token without active organization context is a conflict and does not mutate rows.
row_count_before="$(organization_sql 'select count(*) from organization.organizations')|$(organization_sql 'select count(*) from organization.memberships')"
no_context_status="$(request_get "${current_url}" "${auth_work}/organization-no-context.token" no-org-context "${work}/no-context-body" "${work}/no-context-headers")"
test "${no_context_status}" = "409"
assert_error "${work}/no-context-body" organization_context_required 'active organization context required' no-org-context
test "$(organization_sql 'select count(*) from organization.organizations')|$(organization_sql 'select count(*) from organization.memberships')" = "${row_count_before}"

# Browser CORS allowed/disallowed preflight.
allowed_preflight_status="$(curl --show-error --silent --request OPTIONS --output "${work}/preflight-body" --dump-header "${work}/preflight-headers" --write-out '%{http_code}' \
  -H "Origin: ${allowed_origin}" -H 'Access-Control-Request-Method: GET' -H 'Access-Control-Request-Headers: Authorization' "${current_url}")"
case "${allowed_preflight_status}" in 2??) ;; *) exit 1 ;; esac
assert_header_exact "${work}/preflight-headers" Access-Control-Allow-Origin "${allowed_origin}"
assert_header_token "${work}/preflight-headers" Access-Control-Allow-Methods GET
assert_header_token "${work}/preflight-headers" Access-Control-Allow-Headers Authorization
! grep --quiet --ignore-case '^Access-Control-Allow-Credentials:' "${work}/preflight-headers"

curl --show-error --silent --request OPTIONS --output /dev/null --dump-header "${work}/blocked-preflight-headers" \
  -H 'Origin: https://attacker.example' -H 'Access-Control-Request-Method: GET' "${current_url}"
! grep --quiet --ignore-case '^Access-Control-Allow-Origin:' "${work}/blocked-preflight-headers"

# Identity outage leaves Organization health and webhook available, while auth route becomes 503.
organization_service_id="$(docker compose ps -q organization-service)"
docker compose stop identity-service
curl --fail --show-error --silent --output /dev/null "${base_url}/api/v1/organizations/health/live"
curl --fail --show-error --silent --output /dev/null "${base_url}/api/v1/organizations/health/ready"
cat > "${work}/organization-update.json" <<'JSON'
{"type":"organization.updated","timestamp":1785744030000,"data":{"id":"org_ci_primary","name":"BridgeWorks CI Updated","slug":"bridgeworks-ci-updated"}}
JSON
test "$(send_signed "${organization_webhook_url}" "${CLERK_ORGANIZATION_WEBHOOK_SIGNING_SECRET}" msg_org_update "${work}/organization-update.json" "${work}/organization-update-response" organization-update-ci)" = "204|organization-update-ci"
outage_status="$(request_get "${current_url}" "${auth_work}/organization-admin.token" identity-outage "${work}/identity-outage-body" "${work}/identity-outage-headers")"
test "${outage_status}" = "503"
assert_error "${work}/identity-outage-body" service_unavailable 'service temporarily unavailable' identity-outage
assert_header_exact "${work}/identity-outage-headers" Cache-Control no-store

docker compose start identity-service
make gateway-smoke
make organization-smoke
test "$(docker compose ps -q organization-service)" = "${organization_service_id}"
recovery_status="$(request_get "${current_url}" "${auth_work}/organization-admin.token" identity-recovery "${work}/identity-recovery-body" "${work}/identity-recovery-headers")"
test "${recovery_status}" = "200"

# Organization database outage is isolated from Identity and returns retryable failures.
docker compose stop organization-postgres
curl --fail --show-error --silent --output /dev/null "${base_url}/api/v1/organizations/health/live"
ready_status="$(curl --show-error --silent --output "${work}/org-db-ready-body" --write-out '%{http_code}' "${base_url}/api/v1/organizations/health/ready")"
test "${ready_status}" = "503"
identity_status="$(curl --show-error --silent --output /dev/null --write-out '%{http_code}' -H "Authorization: Bearer $(cat "${auth_work}/organization-admin.token")" "${base_url}/api/v1/me")"
test "${identity_status}" = "200"
db_outage_status="$(request_get "${current_url}" "${auth_work}/organization-admin.token" org-db-outage "${work}/org-db-outage-body" "${work}/org-db-outage-headers")"
test "${db_outage_status}" = "503"

docker compose start organization-postgres
make organization-smoke
test "$(docker compose ps -q organization-service)" = "${organization_service_id}"

# Port isolation.
for service in identity-service organization-service identity-postgres organization-postgres; do
  container_id="$(docker compose ps -q "${service}")"
  bindings="$(docker inspect --format '{{json .HostConfig.PortBindings}}' "${container_id}")"
  case "${bindings}" in null|'{}') ;; *) echo "${service} published ports: ${bindings}" >&2; exit 1 ;; esac
done

# Security scan: no provider IDs, tokens, secrets, DB credentials, or fixture profile data in service logs/errors.
docker compose logs --no-color organization-service > "${work}/organization-service.log"
python3 - "${work}" "${auth_work}" "${ORGANIZATION_DATABASE_URL}" "${ORGANIZATION_POSTGRES_PASSWORD}" <<'PY'
import pathlib, sys
work=pathlib.Path(sys.argv[1]); auth=pathlib.Path(sys.argv[2])
forbidden=[
  'user_org_ci_admin','org_ci_primary','mem_org_ci_admin','must-not-persist@example.test',
  'Sensitive Invalid Org','svix-signature',sys.argv[3],sys.argv[4],
  'postgres://','pgx','connection refused',
]
for path in auth.glob('*.token'):
  forbidden.append(path.read_text(encoding='utf-8'))
forbidden=[v for v in forbidden if v]
logs=(work/'organization-service.log').read_text(encoding='utf-8')
for value in forbidden:
  assert value not in logs, f'log leaked {value[:24]!r}'
for path in work.glob('*-body'):
  body=path.read_text(encoding='utf-8')
  for value in forbidden:
    assert value not in body, f'{path.name} leaked {value[:24]!r}'
PY

echo "Organization synchronization and authorization integration tests passed."
