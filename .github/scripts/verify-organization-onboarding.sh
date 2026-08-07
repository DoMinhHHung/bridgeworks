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

# The same PostgreSQL database was prepared by a real v2 -> v3 migration fixture
# immediately before stack startup. A later verified Clerk creator signal may be
# projected, but it must never elevate a legacy local viewer or rewrite admin.
cat > "${work}/legacy-organization-updated.json" <<'JSON'
{"type":"organization.updated","timestamp":1785744990000,"data":{"id":"org_onboarding_legacy","name":"Legacy Provider Renamed","slug":"legacy-provider-renamed","created_by":"user_onboarding_legacy_creator"}}
JSON
test "$(send_signed "${organization_webhook_url}" "${CLERK_ORGANIZATION_WEBHOOK_SIGNING_SECRET}" msg_org_onboarding_legacy_updated "${work}/legacy-organization-updated.json" "${work}/legacy-organization-updated-response" onboarding-legacy-update)" = "204|onboarding-legacy-update"
legacy_state="$(organization_sql "select coalesce(o.clerk_created_by_user_id, ''), o.owner_bootstrap_eligible, o.owner_bootstrapped, viewer.application_role, admin.application_role, (select count(*) from organization.memberships owners where owners.organization_id=o.id and owners.application_role='owner') from organization.organizations o join organization.memberships viewer on viewer.organization_id=o.id and viewer.clerk_membership_id='mem_onboarding_legacy_viewer' join organization.memberships admin on admin.organization_id=o.id and admin.clerk_membership_id='mem_onboarding_legacy_admin' where o.clerk_organization_id='org_onboarding_legacy'")"
test "${legacy_state}" = "user_onboarding_legacy_creator|f|f|viewer|admin|0"

cat > "${work}/identity.json" <<'JSON'
{"type":"user.created","timestamp":1785745000000,"data":{"id":"user_onboarding_ci","primary_email_address_id":"email_onboarding_ci","email_addresses":[{"id":"email_onboarding_ci","email_address":"onboarding-ci@example.test","verification":{"status":"verified"}}]}}
JSON
test "$(send_signed "${identity_webhook_url}" "${CLERK_WEBHOOK_SIGNING_SECRET}" msg_identity_onboarding "${work}/identity.json" "${work}/identity-response" onboarding-identity)" = "204|onboarding-identity"

# New organization, organization-first ordering.
cat > "${work}/organization-created.json" <<'JSON'
{"type":"organization.created","timestamp":1785745010000,"data":{"id":"org_onboarding_ci","name":"BridgeWorks Onboarding","slug":"bridgeworks-onboarding","created_by":"user_onboarding_ci"}}
JSON
test "$(send_signed "${organization_webhook_url}" "${CLERK_ORGANIZATION_WEBHOOK_SIGNING_SECRET}" msg_org_onboarding_created "${work}/organization-created.json" "${work}/organization-created-response" onboarding-org-create)" = "204|onboarding-org-create"
test "$(organization_sql "select status, coalesce(clerk_created_by_user_id, ''), owner_bootstrapped, owner_bootstrap_eligible from organization.organizations where clerk_organization_id='org_onboarding_ci'")" = "active|user_onboarding_ci|f|t"

cat > "${work}/membership-created.json" <<'JSON'
{"type":"organizationMembership.created","timestamp":1785745020000,"data":{"id":"mem_onboarding_ci","organization":{"id":"org_onboarding_ci"},"public_user_data":{"user_id":"user_onboarding_ci"},"role":"org:admin"}}
JSON
test "$(send_signed "${organization_webhook_url}" "${CLERK_ORGANIZATION_WEBHOOK_SIGNING_SECRET}" msg_mem_onboarding_created "${work}/membership-created.json" "${work}/membership-created-response" onboarding-membership-create)" = "204|onboarding-membership-create"
test "$(organization_sql "select m.application_role, o.owner_bootstrapped, o.owner_bootstrap_eligible from organization.memberships m join organization.organizations o on o.id=m.organization_id where m.clerk_membership_id='mem_onboarding_ci'")" = "owner|t|t"

# New organization, membership-first ordering must also bootstrap exactly the
# verified creator after the provider organization projection arrives.
cat > "${work}/membership-first-created.json" <<'JSON'
{"type":"organizationMembership.created","timestamp":1785745200000,"data":{"id":"mem_onboarding_membership_first","organization":{"id":"org_onboarding_membership_first"},"public_user_data":{"user_id":"user_onboarding_membership_first"},"role":"org:admin"}}
JSON
test "$(send_signed "${organization_webhook_url}" "${CLERK_ORGANIZATION_WEBHOOK_SIGNING_SECRET}" msg_mem_onboarding_membership_first "${work}/membership-first-created.json" "${work}/membership-first-created-response" onboarding-membership-first-membership)" = "204|onboarding-membership-first-membership"
test "$(organization_sql "select o.status, o.owner_bootstrap_eligible, o.owner_bootstrapped, m.application_role from organization.organizations o join organization.memberships m on m.organization_id=o.id and m.clerk_membership_id='mem_onboarding_membership_first' where o.clerk_organization_id='org_onboarding_membership_first'")" = "pending|t|f|admin"
cat > "${work}/membership-first-organization.json" <<'JSON'
{"type":"organization.created","timestamp":1785745210000,"data":{"id":"org_onboarding_membership_first","name":"Membership First","slug":"membership-first","created_by":"user_onboarding_membership_first"}}
JSON
test "$(send_signed "${organization_webhook_url}" "${CLERK_ORGANIZATION_WEBHOOK_SIGNING_SECRET}" msg_org_onboarding_membership_first "${work}/membership-first-organization.json" "${work}/membership-first-organization-response" onboarding-membership-first-organization)" = "204|onboarding-membership-first-organization"
test "$(organization_sql "select o.status, coalesce(o.clerk_created_by_user_id, ''), o.owner_bootstrap_eligible, o.owner_bootstrapped, m.application_role from organization.organizations o join organization.memberships m on m.organization_id=o.id and m.clerk_membership_id='mem_onboarding_membership_first' where o.clerk_organization_id='org_onboarding_membership_first'")" = "active|user_onboarding_membership_first|t|t|owner"

# Eligible rows still require an active organization lifecycle.
cat > "${work}/disabled-membership.json" <<'JSON'
{"type":"organizationMembership.created","timestamp":1785745300000,"data":{"id":"mem_onboarding_disabled","organization":{"id":"org_onboarding_disabled"},"public_user_data":{"user_id":"user_onboarding_disabled"},"role":"org:admin"}}
JSON
test "$(send_signed "${organization_webhook_url}" "${CLERK_ORGANIZATION_WEBHOOK_SIGNING_SECRET}" msg_mem_onboarding_disabled "${work}/disabled-membership.json" "${work}/disabled-membership-response" onboarding-disabled-membership)" = "204|onboarding-disabled-membership"
organization_sql "update organization.organizations set status='disabled' where clerk_organization_id='org_onboarding_disabled'" >/dev/null
cat > "${work}/disabled-organization.json" <<'JSON'
{"type":"organization.created","timestamp":1785745310000,"data":{"id":"org_onboarding_disabled","name":"Disabled Organization","slug":"disabled-organization","created_by":"user_onboarding_disabled"}}
JSON
test "$(send_signed "${organization_webhook_url}" "${CLERK_ORGANIZATION_WEBHOOK_SIGNING_SECRET}" msg_org_onboarding_disabled "${work}/disabled-organization.json" "${work}/disabled-organization-response" onboarding-disabled-organization)" = "204|onboarding-disabled-organization"
test "$(organization_sql "select o.status, coalesce(o.clerk_created_by_user_id, ''), o.owner_bootstrap_eligible, o.owner_bootstrapped, m.application_role from organization.organizations o join organization.memberships m on m.organization_id=o.id and m.clerk_membership_id='mem_onboarding_disabled' where o.clerk_organization_id='org_onboarding_disabled'")" = "disabled|user_onboarding_disabled|t|f|admin"

cat > "${work}/deleted-membership.json" <<'JSON'
{"type":"organizationMembership.created","timestamp":1785745400000,"data":{"id":"mem_onboarding_deleted","organization":{"id":"org_onboarding_deleted"},"public_user_data":{"user_id":"user_onboarding_deleted"},"role":"org:admin"}}
JSON
test "$(send_signed "${organization_webhook_url}" "${CLERK_ORGANIZATION_WEBHOOK_SIGNING_SECRET}" msg_mem_onboarding_deleted "${work}/deleted-membership.json" "${work}/deleted-membership-response" onboarding-deleted-membership)" = "204|onboarding-deleted-membership"
organization_sql "update organization.organizations set status='deleted' where clerk_organization_id='org_onboarding_deleted'" >/dev/null
cat > "${work}/deleted-organization.json" <<'JSON'
{"type":"organization.created","timestamp":1785745410000,"data":{"id":"org_onboarding_deleted","name":"Deleted Organization","slug":"deleted-organization","created_by":"user_onboarding_deleted"}}
JSON
test "$(send_signed "${organization_webhook_url}" "${CLERK_ORGANIZATION_WEBHOOK_SIGNING_SECRET}" msg_org_onboarding_deleted "${work}/deleted-organization.json" "${work}/deleted-organization-response" onboarding-deleted-organization)" = "204|onboarding-deleted-organization"
test "$(organization_sql "select o.status, coalesce(o.clerk_created_by_user_id, ''), o.owner_bootstrap_eligible, o.owner_bootstrapped, m.application_role from organization.organizations o join organization.memberships m on m.organization_id=o.id and m.clerk_membership_id='mem_onboarding_deleted' where o.clerk_organization_id='org_onboarding_deleted'")" = "deleted||t|f|admin"

# Deleted creator membership history permanently cancels automatic initial-owner
# eligibility. A later rejoin with a fresh Clerk membership ID must initialize
# normally and must never revive the historical creator privilege.
cat > "${work}/inactive-membership-created.json" <<'JSON'
{"type":"organizationMembership.created","timestamp":1785745500000,"data":{"id":"mem_onboarding_inactive","organization":{"id":"org_onboarding_inactive"},"public_user_data":{"user_id":"user_onboarding_inactive"},"role":"org:admin"}}
JSON
test "$(send_signed "${organization_webhook_url}" "${CLERK_ORGANIZATION_WEBHOOK_SIGNING_SECRET}" msg_mem_onboarding_inactive_created "${work}/inactive-membership-created.json" "${work}/inactive-membership-created-response" onboarding-inactive-membership-create)" = "204|onboarding-inactive-membership-create"
cat > "${work}/inactive-membership-deleted.json" <<'JSON'
{"type":"organizationMembership.deleted","timestamp":1785745510000,"data":{"id":"mem_onboarding_inactive","organization":{"id":"org_onboarding_inactive"},"public_user_data":{"user_id":"user_onboarding_inactive"},"role":"org:admin"}}
JSON
test "$(send_signed "${organization_webhook_url}" "${CLERK_ORGANIZATION_WEBHOOK_SIGNING_SECRET}" msg_mem_onboarding_inactive_deleted "${work}/inactive-membership-deleted.json" "${work}/inactive-membership-deleted-response" onboarding-inactive-membership-delete)" = "204|onboarding-inactive-membership-delete"
cat > "${work}/inactive-organization.json" <<'JSON'
{"type":"organization.created","timestamp":1785745520000,"data":{"id":"org_onboarding_inactive","name":"Inactive Creator Organization","slug":"inactive-creator-organization","created_by":"user_onboarding_inactive"}}
JSON
test "$(send_signed "${organization_webhook_url}" "${CLERK_ORGANIZATION_WEBHOOK_SIGNING_SECRET}" msg_org_onboarding_inactive "${work}/inactive-organization.json" "${work}/inactive-organization-response" onboarding-inactive-organization)" = "204|onboarding-inactive-organization"
test "$(organization_sql "select o.status, coalesce(o.clerk_created_by_user_id, ''), o.owner_bootstrap_eligible, o.owner_bootstrapped, m.status, m.application_role, (select count(*) from organization.memberships owners where owners.organization_id=o.id and owners.application_role='owner') from organization.organizations o join organization.memberships m on m.organization_id=o.id and m.clerk_membership_id='mem_onboarding_inactive' where o.clerk_organization_id='org_onboarding_inactive'")" = "active|user_onboarding_inactive|f|f|deleted|admin|0"

cat > "${work}/inactive-membership-rejoin.json" <<'JSON'
{"type":"organizationMembership.created","timestamp":1785745530000,"data":{"id":"mem_onboarding_inactive_rejoin","organization":{"id":"org_onboarding_inactive"},"public_user_data":{"user_id":"user_onboarding_inactive"},"role":"org:member"}}
JSON
test "$(send_signed "${organization_webhook_url}" "${CLERK_ORGANIZATION_WEBHOOK_SIGNING_SECRET}" msg_mem_onboarding_inactive_rejoin "${work}/inactive-membership-rejoin.json" "${work}/inactive-membership-rejoin-response" onboarding-inactive-membership-rejoin)" = "204|onboarding-inactive-membership-rejoin"
test "$(organization_sql "select rejoin.application_role, o.owner_bootstrap_eligible, o.owner_bootstrapped, (select count(*) from organization.memberships owners where owners.organization_id=o.id and owners.application_role='owner') from organization.organizations o join organization.memberships rejoin on rejoin.organization_id=o.id and rejoin.clerk_membership_id='mem_onboarding_inactive_rejoin' where o.clerk_organization_id='org_onboarding_inactive'")" = "viewer|f|f|0"

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

# Concurrent partial PATCH requests must merge against freshly locked state rather
# than overwriting each other's omitted fields.
(
  request_json PATCH "${current_url}" "${auth_work}/organization-onboarding.token" onboarding-profile-a \
    '{"legal_name":"Concurrent BridgeWorks Legal Name"}' \
    "${work}/patch-a-body" "${work}/patch-a-headers" > "${work}/patch-a-status"
) &
patch_a_pid=$!
(
  request_json PATCH "${current_url}" "${auth_work}/organization-onboarding.token" onboarding-profile-b \
    '{"country":"us"}' \
    "${work}/patch-b-body" "${work}/patch-b-headers" > "${work}/patch-b-status"
) &
patch_b_pid=$!
wait "${patch_a_pid}"
wait "${patch_b_pid}"
test "$(cat "${work}/patch-a-status")" = "200"
test "$(cat "${work}/patch-b-status")" = "200"
test "$(organization_sql "select legal_name, website, country, company_type from organization.organizations where clerk_organization_id='org_onboarding_ci'")" = "Concurrent BridgeWorks Legal Name|https://example.com/about|US|software-agency"

# Concurrent verification requests serialize on the same row. Both are stable
# successes and the resulting lifecycle state is pending.
(
  request_json POST "${verification_url}" "${auth_work}/organization-onboarding.token" onboarding-verification-a '{}' \
    "${work}/verify-a-body" "${work}/verify-a-headers" > "${work}/verify-a-status"
) &
verify_a_pid=$!
(
  request_json POST "${verification_url}" "${auth_work}/organization-onboarding.token" onboarding-verification-b '{}' \
    "${work}/verify-b-body" "${work}/verify-b-headers" > "${work}/verify-b-status"
) &
verify_b_pid=$!
wait "${verify_a_pid}"
wait "${verify_b_pid}"
test "$(cat "${work}/verify-a-status")" = "200"
test "$(cat "${work}/verify-b-status")" = "200"
grep --quiet '"verification_status":"pending"' "${work}/verify-a-body"
grep --quiet '"verification_status":"pending"' "${work}/verify-b-body"
test "$(organization_sql "select verification_status from organization.organizations where clerk_organization_id='org_onboarding_ci'")" = "pending"

verification_before="$(organization_sql "select verification_status, updated_at::text from organization.organizations where clerk_organization_id='org_onboarding_ci'")"
verify_repeat_status="$(request_json POST "${verification_url}" "${auth_work}/organization-onboarding.token" onboarding-verification-repeat '{}' "${work}/verify-repeat-body" "${work}/verify-repeat-headers")"
test "${verify_repeat_status}" = "200"
test "$(organization_sql "select verification_status, updated_at::text from organization.organizations where clerk_organization_id='org_onboarding_ci'")" = "${verification_before}"

cat > "${work}/organization-updated.json" <<'JSON'
{"type":"organization.updated","timestamp":1785745030000,"data":{"id":"org_onboarding_ci","name":"BridgeWorks Provider Renamed","slug":"bridgeworks-provider-renamed","created_by":"user_onboarding_ci"}}
JSON
test "$(send_signed "${organization_webhook_url}" "${CLERK_ORGANIZATION_WEBHOOK_SIGNING_SECRET}" msg_org_onboarding_updated "${work}/organization-updated.json" "${work}/organization-updated-response" onboarding-org-update)" = "204|onboarding-org-update"
expected='BridgeWorks Provider Renamed|bridgeworks-provider-renamed|Concurrent BridgeWorks Legal Name|https://example.com/about|US|software-agency|pending|unassessed|owner|t|t'
actual="$(organization_sql "select o.name, o.slug, o.legal_name, o.website, o.country, o.company_type, o.verification_status, o.trust_status, m.application_role, o.owner_bootstrapped, o.owner_bootstrap_eligible from organization.organizations o join organization.memberships m on m.organization_id=o.id and m.clerk_membership_id='mem_onboarding_ci' where o.clerk_organization_id='org_onboarding_ci'")"
test "${actual}" = "${expected}"

before_duplicate="$(organization_sql "select o.updated_at::text, m.updated_at::text from organization.organizations o join organization.memberships m on m.organization_id=o.id and m.clerk_membership_id='mem_onboarding_ci' where o.clerk_organization_id='org_onboarding_ci'")"
test "$(send_signed "${organization_webhook_url}" "${CLERK_ORGANIZATION_WEBHOOK_SIGNING_SECRET}" msg_org_onboarding_updated "${work}/organization-updated.json" "${work}/organization-duplicate-response" onboarding-org-duplicate)" = "204|onboarding-org-duplicate"
test "$(organization_sql "select o.updated_at::text, m.updated_at::text from organization.organizations o join organization.memberships m on m.organization_id=o.id and m.clerk_membership_id='mem_onboarding_ci' where o.clerk_organization_id='org_onboarding_ci'")" = "${before_duplicate}"

cat > "${work}/organization-stale.json" <<'JSON'
{"type":"organization.updated","timestamp":1785745005000,"data":{"id":"org_onboarding_ci","name":"Stale Provider Name","slug":"stale-provider-name","created_by":"user_onboarding_ci"}}
JSON
test "$(send_signed "${organization_webhook_url}" "${CLERK_ORGANIZATION_WEBHOOK_SIGNING_SECRET}" msg_org_onboarding_stale "${work}/organization-stale.json" "${work}/organization-stale-response" onboarding-org-stale)" = "204|onboarding-org-stale"
actual="$(organization_sql "select o.name, o.slug, o.legal_name, o.website, o.country, o.company_type, o.verification_status, o.trust_status, m.application_role, o.owner_bootstrapped, o.owner_bootstrap_eligible from organization.organizations o join organization.memberships m on m.organization_id=o.id and m.clerk_membership_id='mem_onboarding_ci' where o.clerk_organization_id='org_onboarding_ci'")"
test "${actual}" = "${expected}"

# Once a successful bootstrap is fenced, a future local role change remains
# authoritative; provider updates must never silently re-grant owner.
organization_sql "update organization.memberships set application_role='viewer' where clerk_membership_id='mem_onboarding_ci'" >/dev/null
cat > "${work}/organization-fence-updated.json" <<'JSON'
{"type":"organization.updated","timestamp":1785745040000,"data":{"id":"org_onboarding_ci","name":"BridgeWorks Provider Final","slug":"bridgeworks-provider-final","created_by":"user_onboarding_ci"}}
JSON
test "$(send_signed "${organization_webhook_url}" "${CLERK_ORGANIZATION_WEBHOOK_SIGNING_SECRET}" msg_org_onboarding_fence_updated "${work}/organization-fence-updated.json" "${work}/organization-fence-updated-response" onboarding-org-fence-update)" = "204|onboarding-org-fence-update"
test "$(organization_sql "select m.application_role, o.owner_bootstrapped, o.owner_bootstrap_eligible from organization.organizations o join organization.memberships m on m.organization_id=o.id and m.clerk_membership_id='mem_onboarding_ci' where o.clerk_organization_id='org_onboarding_ci'")" = "viewer|t|t"

docker compose logs --no-color organization-service > "${work}/organization-service.log"
python3 - "${work}/organization-service.log" "${auth_work}/organization-onboarding.token" <<'PY'
import pathlib, sys
logs=pathlib.Path(sys.argv[1]).read_text(encoding='utf-8')
token=pathlib.Path(sys.argv[2]).read_text(encoding='utf-8')
for forbidden in [
    token,
    'user_onboarding_ci', 'org_onboarding_ci', 'mem_onboarding_ci',
    'user_onboarding_legacy_creator', 'org_onboarding_legacy', 'mem_onboarding_legacy_viewer',
    'user_onboarding_membership_first', 'org_onboarding_membership_first', 'mem_onboarding_membership_first',
    'user_onboarding_disabled', 'org_onboarding_disabled', 'mem_onboarding_disabled',
    'user_onboarding_deleted', 'org_onboarding_deleted', 'mem_onboarding_deleted',
    'user_onboarding_inactive', 'org_onboarding_inactive', 'mem_onboarding_inactive', 'mem_onboarding_inactive_rejoin',
    'onboarding-ci@example.test', 'Concurrent BridgeWorks Legal Name',
    'svix-signature', 'postgres://',
]:
    assert forbidden not in logs, f'organization log leaked {forbidden[:24]!r}'
PY

echo "Organization onboarding ownership integration tests passed."
