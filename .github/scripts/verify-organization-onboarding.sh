#!/usr/bin/env bash
set -euo pipefail

set -a
source .env
set +a

work="${RUNNER_TEMP:?RUNNER_TEMP is required}/organization-onboarding"
auth_work="${RUNNER_TEMP}/clerk-auth"
mkdir -p "${work}"

base_url='http://127.0.0.1:9080'
identity_webhook_url="${base_url}/api/v1/identity/webhooks/clerk"
organization_webhook_url="${base_url}/api/v1/organizations/webhooks/clerk"
current_url="${base_url}/api/v1/organizations/current"
verification_url="${current_url}/verification"
allowed_origin="${CLERK_AUTHORIZED_PARTIES%%,*}"

organization_postgres_id="$(docker compose ps -q organization-postgres)"
organization_postgres_user="$(docker inspect --format '{{range .Config.Env}}{{println .}}{{end}}' "${organization_postgres_id}" | sed -n 's/^POSTGRES_USER=//p')"
organization_postgres_db="$(docker inspect --format '{{range .Config.Env}}{{println .}}{{end}}' "${organization_postgres_id}" | sed -n 's/^POSTGRES_DB=//p')"

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
  local subject="$1" session_id="$2" organization_id="$3" output="$4"
  (
    cd service/organization-service
    go run ../../.github/scripts/clerk-session-token.go \
      -action sign \
      -private-key "${auth_work}/private.pem" \
      -issuer "${CLERK_ISSUER}" \
      -subject "${subject}" \
      -session-id "${session_id}" \
      -authorized-party "${allowed_origin}" \
      -organization-id "${organization_id}" \
      -organization-role 'org:member' \
      -organization-permissions 'organization:manage,membership:manage' \
      -output "${output}"
  )
}

request_json() {
  local method="$1" url="$2" token_file="$3" request_id="$4" data="$5" body="$6" headers="$7"
  curl --show-error --silent --output "${body}" --dump-header "${headers}" \
    --write-out '%{http_code}' \
    --request "${method}" \
    -H "Authorization: Bearer $(cat "${token_file}")" \
    -H "X-Request-Id: ${request_id}" \
    -H "Origin: ${allowed_origin}" \
    -H 'Content-Type: application/json' \
    --data-binary "${data}" \
    "${url}"
}

assert_header_exact() {
  local file="$1" header="$2" value="$3"
  grep --quiet --ignore-case "^${header}: ${value}" "${file}"
}

cat > "${work}/identity.json" <<'JSON'
{"type":"user.created","timestamp":1785745000000,"data":{"id":"user_onboarding_ci","primary_email_address_id":"email_onboarding_ci","email_addresses":[{"id":"email_onboarding_ci","email_address":"onboarding-ci@example.test","verification":{"status":"verified"}}]}}
JSON
test "$(send_signed "${identity_webhook_url}" "${CLERK_WEBHOOK_SIGNING_SECRET}" msg_identity_onboarding "${work}/identity.json" "${work}/identity-response" onboarding-identity)" = "204|onboarding-identity"

cat > "${work}/organization-created.json" <<'JSON'
{"type":"organization.created","timestamp":1785745010000,"data":{"id":"org_onboarding_ci","name":"BridgeWorks Onboarding","slug":"bridgeworks-onboarding","created_by":"user_onboarding_ci"}}
JSON
test "$(send_signed "${organization_webhook_url}" "${CLERK_ORGANIZATION_WEBHOOK_SIGNING_SECRET}" msg_org_onboarding_created "${work}/organization-created.json" "${work}/organization-created-response" onboarding-org-create)" = "204|onboarding-org-create"
test "$(organization_sql "select status, coalesce(clerk_created_by_user_id, ''), owner_bootstrapped from organization.organizations where clerk_organization_id='org_onboarding_ci'")" = "active|user_onboarding_ci|f"

cat > "${work}/membership-created.json" <<'JSON'
{"type":"organizationMembership.created","timestamp":1785745020000,"data":{"id":"mem_onboarding_ci","organization":{"id":"org_onboarding_ci"},"public_user_data":{"user_id":"user_onboarding_ci","identifier":"onboarding-ci@example.test"},"role":"org:admin"}}
JSON
test "$(send_signed "${organization_webhook_url}" "${CLERK_ORGANIZATION_WEBHOOK_SIGNING_SECRET}" msg_mem_onboarding_created "${work}/membership-created.json" "${work}/membership-created-response" onboarding-membership-create)" = "204|onboarding-membership-create"
test "$(organization_sql "select m.application_role, o.owner_bootstrapped from organization.memberships m join organization.organizations o on o.id=m.organization_id where m.clerk_membership_id='mem_onboarding_ci'")" = "owner|t"

sign_token user_onboarding_ci sess_onboarding_ci org_onboarding_ci "${auth_work}/organization-onboarding.token"

patch_status="$(request_json PATCH "${current_url}" "${auth_work}/organization-onboarding.token" onboarding-profile \
  '{"legal_name":"  BridgeWorks Company Limited  ","website":"https://EXAMPLE.com:443/about","country":"vn","company_type":"Software-Agency"}' \
  "${work}/patch-body" "${work}/patch-headers")"
test "${patch_status}" = "200"
assert_header_exact "${work}/patch-headers" Cache-Control no-store
assert_header_exact "${work}/patch-headers" X-Request-Id onboarding-profile
python3 - "${work}/patch-body" <<'PY'
import json, sys
with open(sys.argv[1], encoding='utf-8') as f: body=json.load(f)
assert body['legal_name']=='BridgeWorks Company Limited'
assert body['website']=='https://example.com/about'
assert body['country']=='VN'
assert body['company_type']=='software-agency'
assert body['verification_status']=='unverified'
assert body['trust_status']=='unassessed'
assert all('clerk' not in key for key in body)
PY

test "$(organization_sql "select legal_name, website, country, company_type, verification_status, trust_status from organization.organizations where clerk_organization_id='org_onboarding_ci'")" = "BridgeWorks Company Limited|https://example.com/about|VN|software-agency|unverified|unassessed"

verify_status="$(request_json POST "${verification_url}" "${auth_work}/organization-onboarding.token" onboarding-verification '{}' "${work}/verify-body" "${work}/verify-headers")"
test "${verify_status}" = "200"
assert_header_exact "${work}/verify-headers" Cache-Control no-store
assert_header_exact "${work}/verify-headers" X-Request-Id onboarding-verification
grep --quiet '"verification_status":"pending"' "${work}/verify-body"
verification_before="$(organization_sql "select verification_status, updated_at::text from organization.organizations where clerk_organization_id='org_onboarding_ci'")"

verify_repeat_status="$(request_json POST "${verification_url}" "${auth_work}/organization-onboarding.token" onboarding-verification-repeat '{}' "${work}/verify-repeat-body" "${work}/verify-repeat-headers")"
test "${verify_repeat_status}" = "200"
test "$(organization_sql "select verification_status, updated_at::text from organization.organizations where clerk_organization_id='org_onboarding_ci'")" = "${verification_before}"

cat > "${work}/organization-updated.json" <<'JSON'
{"type":"organization.updated","timestamp":1785745030000,"data":{"id":"org_onboarding_ci","name":"BridgeWorks Provider Renamed","slug":"bridgeworks-provider-renamed","created_by":"user_onboarding_ci"}}
JSON
test "$(send_signed "${organization_webhook_url}" "${CLERK_ORGANIZATION_WEBHOOK_SIGNING_SECRET}" msg_org_onboarding_updated "${work}/organization-updated.json" "${work}/organization-updated-response" onboarding-org-update)" = "204|onboarding-org-update"
expected='BridgeWorks Provider Renamed|bridgeworks-provider-renamed|BridgeWorks Company Limited|https://example.com/about|VN|software-agency|pending|unassessed|owner|t'
actual="$(organization_sql "select o.name, o.slug, o.legal_name, o.website, o.country, o.company_type, o.verification_status, o.trust_status, m.application_role, o.owner_bootstrapped from organization.organizations o join organization.memberships m on m.organization_id=o.id and m.clerk_membership_id='mem_onboarding_ci' where o.clerk_organization_id='org_onboarding_ci'")"
test "${actual}" = "${expected}"

before_duplicate="$(organization_sql "select o.updated_at::text, m.updated_at::text from organization.organizations o join organization.memberships m on m.organization_id=o.id and m.clerk_membership_id='mem_onboarding_ci' where o.clerk_organization_id='org_onboarding_ci'")"
test "$(send_signed "${organization_webhook_url}" "${CLERK_ORGANIZATION_WEBHOOK_SIGNING_SECRET}" msg_org_onboarding_updated "${work}/organization-updated.json" "${work}/organization-duplicate-response" onboarding-org-duplicate)" = "204|onboarding-org-duplicate"
test "$(organization_sql "select o.updated_at::text, m.updated_at::text from organization.organizations o join organization.memberships m on m.organization_id=o.id and m.clerk_membership_id='mem_onboarding_ci' where o.clerk_organization_id='org_onboarding_ci'")" = "${before_duplicate}"

cat > "${work}/organization-stale.json" <<'JSON'
{"type":"organization.updated","timestamp":1785745005000,"data":{"id":"org_onboarding_ci","name":"Stale Provider Name","slug":"stale-provider-name","created_by":"user_onboarding_ci"}}
JSON
test "$(send_signed "${organization_webhook_url}" "${CLERK_ORGANIZATION_WEBHOOK_SIGNING_SECRET}" msg_org_onboarding_stale "${work}/organization-stale.json" "${work}/organization-stale-response" onboarding-org-stale)" = "204|onboarding-org-stale"
actual="$(organization_sql "select o.name, o.slug, o.legal_name, o.website, o.country, o.company_type, o.verification_status, o.trust_status, m.application_role, o.owner_bootstrapped from organization.organizations o join organization.memberships m on m.organization_id=o.id and m.clerk_membership_id='mem_onboarding_ci' where o.clerk_organization_id='org_onboarding_ci'")"
test "${actual}" = "${expected}"

docker compose logs --no-color organization-service > "${work}/organization-service.log"
python3 - "${work}/organization-service.log" "${auth_work}/organization-onboarding.token" <<'PY'
import pathlib, sys
logs=pathlib.Path(sys.argv[1]).read_text(encoding='utf-8')
token=pathlib.Path(sys.argv[2]).read_text(encoding='utf-8')
for forbidden in [
    token, 'user_onboarding_ci', 'org_onboarding_ci', 'mem_onboarding_ci',
    'onboarding-ci@example.test', 'BridgeWorks Company Limited',
    'svix-signature', 'postgres://',
]:
    assert forbidden not in logs, f'organization log leaked {forbidden[:24]!r}'
PY

echo "Organization onboarding ownership integration tests passed."
