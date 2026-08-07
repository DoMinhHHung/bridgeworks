#!/usr/bin/env bash
set -euo pipefail

set -a
source .env
set +a

work="${RUNNER_TEMP:?RUNNER_TEMP is required}/organization-membership-administration"
auth_work="${RUNNER_TEMP}/clerk-auth"
mkdir -p "${work}"

base_url='http://127.0.0.1:9080'
identity_webhook_url="${base_url}/api/v1/identity/webhooks/clerk"
organization_webhook_url="${base_url}/api/v1/organizations/webhooks/clerk"
current_url="${base_url}/api/v1/organizations/current"
invitation_url="${current_url}/invitations"
business_email_url="${current_url}/business-email-verification"
transfer_url="${current_url}/ownership-transfer"
allowed_origin="${CLERK_AUTHORIZED_PARTIES%%,*}"
mock_name='bridgeworks-clerk-backend-mock'

organization_postgres_id="$(docker compose ps -q organization-postgres)"
organization_postgres_user="$(docker inspect --format '{{range .Config.Env}}{{println .}}{{end}}' "${organization_postgres_id}" | sed -n 's/^POSTGRES_USER=//p')"
organization_postgres_db="$(docker inspect --format '{{range .Config.Env}}{{println .}}{{end}}' "${organization_postgres_id}" | sed -n 's/^POSTGRES_DB=//p')"
network="$(docker inspect --format '{{range $name, $_ := .NetworkSettings.Networks}}{{println $name}}{{end}}' "${organization_postgres_id}" | head -n 1)"

test -n "${organization_postgres_user}"
test -n "${organization_postgres_db}"
test -n "${network}"

cleanup() {
  docker rm -f "${mock_name}" >/dev/null 2>&1 || true
}
trap cleanup EXIT

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
  local -a args=(
    curl --show-error --silent --output "${body}" --dump-header "${headers}"
    --write-out '%{http_code}' --request "${method}"
    -H "Authorization: Bearer $(cat "${token_file}")"
    -H "X-Request-Id: ${request_id}"
    -H "Origin: ${allowed_origin}"
    -H 'Content-Type: application/json'
  )
  if [[ -n "${data}" ]]; then
    args+=(--data-binary "${data}")
  fi
  args+=("${url}")
  "${args[@]}"
}

assert_status() {
  local actual="$1" expected="$2"
  test "${actual}" = "${expected}"
}

assert_no_sensitive_logs() {
  docker compose logs --no-color organization-service > "${work}/organization-service.log"
  docker logs "${mock_name}" > "${work}/provider.log" 2>&1 || true
  python3 - "${work}/organization-service.log" "${work}/provider.log" <<'PY'
import pathlib, sys
text='\n'.join(pathlib.Path(p).read_text(encoding='utf-8') for p in sys.argv[1:])
for forbidden in [
    'owner@Acme.Example', 'admin@acme.example', 'viewer@acme.example',
    'recruiter@acme.example', 'delivery@acme.example', 'personal@gmail.com',
    'creator-rejoin@company.example',
    'user_pr3_', 'org_pr3_', 'mem_pr3_',
    'sk_test_bridgeworks_local_membership_administration',
    'svix-signature', 'postgres://', 'Authorization: Bearer',
    'bridgeworks_invitation_id',
]:
    assert forbidden not in text, f'sensitive integration value leaked: {forbidden[:32]!r}'
PY
}

# Run an isolated Clerk Backend API mock on the existing private Compose network.
(
  cd service/organization-service
  go build -o "${work}/clerk-backend-mock" ../../.github/scripts/clerk-backend-mock.go
)
docker rm -f "${mock_name}" >/dev/null 2>&1 || true
docker run --detach --name "${mock_name}" --network "${network}" \
  --env "CLERK_BACKEND_MOCK_SECRET=${CLERK_SECRET_KEY}" \
  --volume "${work}/clerk-backend-mock:/usr/local/bin/clerk-backend-mock:ro" \
  debian:bookworm-slim /usr/local/bin/clerk-backend-mock >/dev/null
sed -i 's|^CLERK_BACKEND_API_URL=.*$|CLERK_BACKEND_API_URL=http://bridgeworks-clerk-backend-mock:8081|' .env
docker compose up --detach --force-recreate organization-service
curl --retry 30 --retry-all-errors --retry-delay 1 --fail --show-error --silent \
  "${base_url}/api/v1/organizations/health/ready" >/dev/null

# Identity projections. primary_email is authoritative only when the Clerk address is verified.
cat > "${work}/identity-owner.json" <<'JSON'
{"type":"user.created","timestamp":1785750000000,"data":{"id":"user_pr3_owner","primary_email_address_id":"email_pr3_owner","email_addresses":[{"id":"email_pr3_owner","email_address":"owner@Acme.Example","verification":{"status":"verified"}}]}}
JSON
cat > "${work}/identity-admin.json" <<'JSON'
{"type":"user.created","timestamp":1785750001000,"data":{"id":"user_pr3_admin","primary_email_address_id":"email_pr3_admin","email_addresses":[{"id":"email_pr3_admin","email_address":"admin@acme.example","verification":{"status":"verified"}}]}}
JSON
cat > "${work}/identity-viewer.json" <<'JSON'
{"type":"user.created","timestamp":1785750002000,"data":{"id":"user_pr3_viewer","primary_email_address_id":"email_pr3_viewer","email_addresses":[{"id":"email_pr3_viewer","email_address":"viewer@acme.example","verification":{"status":"verified"}}]}}
JSON
cat > "${work}/identity-recruiter.json" <<'JSON'
{"type":"user.created","timestamp":1785750003000,"data":{"id":"user_pr3_recruiter","primary_email_address_id":"email_pr3_recruiter","email_addresses":[{"id":"email_pr3_recruiter","email_address":"recruiter@acme.example","verification":{"status":"verified"}}]}}
JSON
cat > "${work}/identity-delivery.json" <<'JSON'
{"type":"user.created","timestamp":1785750004000,"data":{"id":"user_pr3_delivery","primary_email_address_id":"email_pr3_delivery","email_addresses":[{"id":"email_pr3_delivery","email_address":"delivery@acme.example","verification":{"status":"verified"}}]}}
JSON
cat > "${work}/identity-personal.json" <<'JSON'
{"type":"user.created","timestamp":1785750005000,"data":{"id":"user_pr3_personal","primary_email_address_id":"email_pr3_personal","email_addresses":[{"id":"email_pr3_personal","email_address":"personal@gmail.com","verification":{"status":"verified"}}]}}
JSON
cat > "${work}/identity-unverified.json" <<'JSON'
{"type":"user.created","timestamp":1785750006000,"data":{"id":"user_pr3_unverified","primary_email_address_id":"email_pr3_unverified","email_addresses":[{"id":"email_pr3_unverified","email_address":"unverified@company.example","verification":{"status":"unverified"}}]}}
JSON
for name in owner admin viewer recruiter delivery personal unverified; do
  assert_status "$(send_signed "${identity_webhook_url}" "${CLERK_WEBHOOK_SIGNING_SECRET}" "msg_pr3_identity_${name}" "${work}/identity-${name}.json" "${work}/identity-${name}-response" "pr3-identity-${name}")" "204|pr3-identity-${name}"
done

# Main organization: verified creator -> exact initial owner; later org:admin stays local admin.
cat > "${work}/org-main.json" <<'JSON'
{"type":"organization.created","timestamp":1785750010000,"data":{"id":"org_pr3_main","name":"PR3 Main","slug":"pr3-main","created_by":"user_pr3_owner"}}
JSON
assert_status "$(send_signed "${organization_webhook_url}" "${CLERK_ORGANIZATION_WEBHOOK_SIGNING_SECRET}" msg_pr3_org_main "${work}/org-main.json" "${work}/org-main-response" pr3-org-main)" "204|pr3-org-main"
cat > "${work}/mem-owner.json" <<'JSON'
{"type":"organizationMembership.created","timestamp":1785750011000,"data":{"id":"mem_pr3_owner","organization":{"id":"org_pr3_main"},"public_user_data":{"user_id":"user_pr3_owner"},"role":"org:admin"}}
JSON
assert_status "$(send_signed "${organization_webhook_url}" "${CLERK_ORGANIZATION_WEBHOOK_SIGNING_SECRET}" msg_pr3_mem_owner "${work}/mem-owner.json" "${work}/mem-owner-response" pr3-mem-owner)" "204|pr3-mem-owner"
cat > "${work}/mem-admin.json" <<'JSON'
{"type":"organizationMembership.created","timestamp":1785750012000,"data":{"id":"mem_pr3_admin","organization":{"id":"org_pr3_main"},"public_user_data":{"user_id":"user_pr3_admin"},"role":"org:admin"}}
JSON
assert_status "$(send_signed "${organization_webhook_url}" "${CLERK_ORGANIZATION_WEBHOOK_SIGNING_SECRET}" msg_pr3_mem_admin "${work}/mem-admin.json" "${work}/mem-admin-response" pr3-mem-admin)" "204|pr3-mem-admin"
owner_membership_id="$(organization_sql "select id from organization.memberships where clerk_membership_id='mem_pr3_owner'")"
admin_membership_id="$(organization_sql "select id from organization.memberships where clerk_membership_id='mem_pr3_admin'")"
test -n "${owner_membership_id}"
test -n "${admin_membership_id}"
test "$(organization_sql "select application_role from organization.memberships where id='${owner_membership_id}'")" = "owner"
test "$(organization_sql "select application_role from organization.memberships where id='${admin_membership_id}'")" = "admin"

sign_token user_pr3_owner sess_pr3_owner org_pr3_main "${auth_work}/pr3-owner.token"
sign_token user_pr3_admin sess_pr3_admin org_pr3_main "${auth_work}/pr3-admin.token"

# Business-email proof is a point-in-time verified Identity domain proof, separate from company verification_status.
assert_status "$(request_json POST "${business_email_url}" "${auth_work}/pr3-owner.token" pr3-business-owner '{}' "${work}/business-owner-body" "${work}/business-owner-headers")" "200"
python3 - "${work}/business-owner-body" <<'PY'
import json, sys
body=json.load(open(sys.argv[1], encoding='utf-8'))
assert body['domain']=='acme.example'
assert body['verified_at']
PY
test "$(organization_sql "select business_email_domain, verification_status from organization.organizations where clerk_organization_id='org_pr3_main'")" = "acme.example|unverified"

# Personal and unverified Identity projections cannot establish business proof.
cat > "${work}/mem-personal.json" <<'JSON'
{"type":"organizationMembership.created","timestamp":1785750013000,"data":{"id":"mem_pr3_personal","organization":{"id":"org_pr3_main"},"public_user_data":{"user_id":"user_pr3_personal"},"role":"org:admin"}}
JSON
cat > "${work}/mem-unverified.json" <<'JSON'
{"type":"organizationMembership.created","timestamp":1785750014000,"data":{"id":"mem_pr3_unverified","organization":{"id":"org_pr3_main"},"public_user_data":{"user_id":"user_pr3_unverified"},"role":"org:admin"}}
JSON
assert_status "$(send_signed "${organization_webhook_url}" "${CLERK_ORGANIZATION_WEBHOOK_SIGNING_SECRET}" msg_pr3_mem_personal "${work}/mem-personal.json" "${work}/mem-personal-response" pr3-mem-personal)" "204|pr3-mem-personal"
assert_status "$(send_signed "${organization_webhook_url}" "${CLERK_ORGANIZATION_WEBHOOK_SIGNING_SECRET}" msg_pr3_mem_unverified "${work}/mem-unverified.json" "${work}/mem-unverified-response" pr3-mem-unverified)" "204|pr3-mem-unverified"
sign_token user_pr3_personal sess_pr3_personal org_pr3_main "${auth_work}/pr3-personal.token"
sign_token user_pr3_unverified sess_pr3_unverified org_pr3_main "${auth_work}/pr3-unverified.token"
assert_status "$(request_json POST "${business_email_url}" "${auth_work}/pr3-personal.token" pr3-business-personal '{}' "${work}/business-personal-body" "${work}/business-personal-headers")" "422"
grep --quiet '"code":"personal_email_domain"' "${work}/business-personal-body"
assert_status "$(request_json POST "${business_email_url}" "${auth_work}/pr3-unverified.token" pr3-business-unverified '{}' "${work}/business-unverified-body" "${work}/business-unverified-headers")" "409"
grep --quiet '"code":"verified_primary_email_required"' "${work}/business-unverified-body"

# Owner invitation creates only local intent + Clerk invitation. Membership remains absent until signed provider projection.
assert_status "$(request_json POST "${invitation_url}" "${auth_work}/pr3-owner.token" pr3-invite-viewer '{"email":"viewer@acme.example","application_role":"viewer"}' "${work}/invite-viewer-body" "${work}/invite-viewer-headers")" "202"
viewer_intent_id="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["id"])' "${work}/invite-viewer-body")"
test "$(organization_sql "select count(*) from organization.memberships where organization_id=(select id from organization.organizations where clerk_organization_id='org_pr3_main') and clerk_user_id='user_pr3_viewer' and status='active'")" = "0"
cat > "${work}/mem-viewer.json" <<JSON
{"type":"organizationMembership.created","timestamp":1785750020000,"data":{"id":"mem_pr3_viewer","organization":{"id":"org_pr3_main"},"public_user_data":{"user_id":"user_pr3_viewer"},"role":"org:member","public_metadata":{"bridgeworks_invitation_id":"${viewer_intent_id}"}}}
JSON
assert_status "$(send_signed "${organization_webhook_url}" "${CLERK_ORGANIZATION_WEBHOOK_SIGNING_SECRET}" msg_pr3_mem_viewer "${work}/mem-viewer.json" "${work}/mem-viewer-response" pr3-mem-viewer)" "204|pr3-mem-viewer"
viewer_membership_id="$(organization_sql "select id from organization.memberships where clerk_membership_id='mem_pr3_viewer'")"
test "$(organization_sql "select application_role from organization.memberships where id='${viewer_membership_id}'")" = "viewer"
test "$(organization_sql "select consumed_membership_id is not null from organization.membership_invitation_intents where id='${viewer_intent_id}'")" = "t"
sign_token user_pr3_viewer sess_pr3_viewer org_pr3_main "${auth_work}/pr3-viewer.token"

# Admin can invite non-owner but never owner. Owner can establish recruiter/delivery members; none can invite.
assert_status "$(request_json POST "${invitation_url}" "${auth_work}/pr3-admin.token" pr3-invite-recruiter '{"email":"recruiter@acme.example","application_role":"recruiter"}' "${work}/invite-recruiter-body" "${work}/invite-recruiter-headers")" "202"
recruiter_intent_id="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["id"])' "${work}/invite-recruiter-body")"
cat > "${work}/mem-recruiter.json" <<JSON
{"type":"organizationMembership.created","timestamp":1785750021000,"data":{"id":"mem_pr3_recruiter","organization":{"id":"org_pr3_main"},"public_user_data":{"user_id":"user_pr3_recruiter"},"role":"org:member","public_metadata":{"bridgeworks_invitation_id":"${recruiter_intent_id}"}}}
JSON
assert_status "$(send_signed "${organization_webhook_url}" "${CLERK_ORGANIZATION_WEBHOOK_SIGNING_SECRET}" msg_pr3_mem_recruiter "${work}/mem-recruiter.json" "${work}/mem-recruiter-response" pr3-mem-recruiter)" "204|pr3-mem-recruiter"
recruiter_membership_id="$(organization_sql "select id from organization.memberships where clerk_membership_id='mem_pr3_recruiter'")"
sign_token user_pr3_recruiter sess_pr3_recruiter org_pr3_main "${auth_work}/pr3-recruiter.token"

assert_status "$(request_json POST "${invitation_url}" "${auth_work}/pr3-owner.token" pr3-invite-delivery '{"email":"delivery@acme.example","application_role":"delivery_manager"}' "${work}/invite-delivery-body" "${work}/invite-delivery-headers")" "202"
delivery_intent_id="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["id"])' "${work}/invite-delivery-body")"
cat > "${work}/mem-delivery.json" <<JSON
{"type":"organizationMembership.created","timestamp":1785750022000,"data":{"id":"mem_pr3_delivery","organization":{"id":"org_pr3_main"},"public_user_data":{"user_id":"user_pr3_delivery"},"role":"org:member","public_metadata":{"bridgeworks_invitation_id":"${delivery_intent_id}"}}}
JSON
assert_status "$(send_signed "${organization_webhook_url}" "${CLERK_ORGANIZATION_WEBHOOK_SIGNING_SECRET}" msg_pr3_mem_delivery "${work}/mem-delivery.json" "${work}/mem-delivery-response" pr3-mem-delivery)" "204|pr3-mem-delivery"
delivery_membership_id="$(organization_sql "select id from organization.memberships where clerk_membership_id='mem_pr3_delivery'")"
sign_token user_pr3_delivery sess_pr3_delivery org_pr3_main "${auth_work}/pr3-delivery.token"

assert_status "$(request_json POST "${invitation_url}" "${auth_work}/pr3-admin.token" pr3-admin-owner-invite '{"email":"blocked-owner@company.example","application_role":"owner"}' "${work}/admin-owner-invite-body" "${work}/admin-owner-invite-headers")" "403"
for role in viewer recruiter delivery; do
  token="${auth_work}/pr3-${role}.token"
  assert_status "$(request_json POST "${invitation_url}" "${token}" "pr3-${role}-invite-denied" '{"email":"denied@company.example","application_role":"viewer"}' "${work}/${role}-invite-body" "${work}/${role}-invite-headers")" "403"
done
assert_status "$(request_json POST "${invitation_url}" "${auth_work}/pr3-owner.token" pr3-invite-cross-tenant '{"organization_id":"org_pr3_foreign","email":"nobody@company.example","application_role":"viewer"}' "${work}/invite-cross-body" "${work}/invite-cross-headers")" "400"

# Local role authority: admin may manage non-owner only; owner-only owner transitions.
assert_status "$(request_json PATCH "${current_url}/members/${viewer_membership_id}/role" "${auth_work}/pr3-admin.token" pr3-role-viewer-recruiter '{"application_role":"recruiter"}' "${work}/role-viewer-body" "${work}/role-viewer-headers")" "204"
test "$(organization_sql "select application_role from organization.memberships where id='${viewer_membership_id}'")" = "recruiter"
assert_status "$(request_json PATCH "${current_url}/members/${admin_membership_id}/role" "${auth_work}/pr3-admin.token" pr3-admin-self-owner '{"application_role":"owner"}' "${work}/admin-self-owner-body" "${work}/admin-self-owner-headers")" "403"
assert_status "$(request_json PATCH "${current_url}/members/${owner_membership_id}/role" "${auth_work}/pr3-admin.token" pr3-admin-owner-demote '{"application_role":"viewer"}' "${work}/admin-owner-demote-body" "${work}/admin-owner-demote-headers")" "403"
assert_status "$(request_json PATCH "${current_url}/members/${viewer_membership_id}/role" "${auth_work}/pr3-owner.token" pr3-owner-add-owner '{"application_role":"owner"}' "${work}/owner-add-owner-body" "${work}/owner-add-owner-headers")" "204"
test "$(organization_sql "select count(*) from organization.memberships where organization_id=(select id from organization.organizations where clerk_organization_id='org_pr3_main') and status='active' and application_role='owner'")" = "2"
assert_status "$(request_json PATCH "${current_url}/members/${admin_membership_id}/role" "${auth_work}/pr3-owner.token" pr3-role-invalid '{"application_role":"super_owner"}' "${work}/role-invalid-body" "${work}/role-invalid-headers")" "400"

# Foreign membership IDs never escape the actor organization boundary.
cat > "${work}/identity-foreign.json" <<'JSON'
{"type":"user.created","timestamp":1785750030000,"data":{"id":"user_pr3_foreign","primary_email_address_id":"email_pr3_foreign","email_addresses":[{"id":"email_pr3_foreign","email_address":"foreign@other.example","verification":{"status":"verified"}}]}}
JSON
assert_status "$(send_signed "${identity_webhook_url}" "${CLERK_WEBHOOK_SIGNING_SECRET}" msg_pr3_identity_foreign "${work}/identity-foreign.json" "${work}/identity-foreign-response" pr3-identity-foreign)" "204|pr3-identity-foreign"
cat > "${work}/org-foreign.json" <<'JSON'
{"type":"organization.created","timestamp":1785750031000,"data":{"id":"org_pr3_foreign","name":"Foreign","slug":"foreign","created_by":"user_pr3_foreign"}}
JSON
cat > "${work}/mem-foreign.json" <<'JSON'
{"type":"organizationMembership.created","timestamp":1785750032000,"data":{"id":"mem_pr3_foreign","organization":{"id":"org_pr3_foreign"},"public_user_data":{"user_id":"user_pr3_foreign"},"role":"org:admin"}}
JSON
assert_status "$(send_signed "${organization_webhook_url}" "${CLERK_ORGANIZATION_WEBHOOK_SIGNING_SECRET}" msg_pr3_org_foreign "${work}/org-foreign.json" "${work}/org-foreign-response" pr3-org-foreign)" "204|pr3-org-foreign"
assert_status "$(send_signed "${organization_webhook_url}" "${CLERK_ORGANIZATION_WEBHOOK_SIGNING_SECRET}" msg_pr3_mem_foreign "${work}/mem-foreign.json" "${work}/mem-foreign-response" pr3-mem-foreign)" "204|pr3-mem-foreign"
foreign_membership_id="$(organization_sql "select id from organization.memberships where clerk_membership_id='mem_pr3_foreign'")"
assert_status "$(request_json PATCH "${current_url}/members/${foreign_membership_id}/role" "${auth_work}/pr3-owner.token" pr3-cross-role '{"application_role":"viewer"}' "${work}/cross-role-body" "${work}/cross-role-headers")" "404"
assert_status "$(request_json DELETE "${current_url}/members/${foreign_membership_id}" "${auth_work}/pr3-owner.token" pr3-cross-remove '' "${work}/cross-remove-body" "${work}/cross-remove-headers")" "404"
assert_status "$(request_json POST "${transfer_url}" "${auth_work}/pr3-owner.token" pr3-cross-transfer "{\"target_membership_id\":\"${foreign_membership_id}\"}" "${work}/cross-transfer-body" "${work}/cross-transfer-headers")" "404"

# Last-owner self-demotion/leave are rejected before provider mutation.
cat > "${work}/identity-last.json" <<'JSON'
{"type":"user.created","timestamp":1785750040000,"data":{"id":"user_pr3_last","primary_email_address_id":"email_pr3_last","email_addresses":[{"id":"email_pr3_last","email_address":"last@company.example","verification":{"status":"verified"}}]}}
JSON
assert_status "$(send_signed "${identity_webhook_url}" "${CLERK_WEBHOOK_SIGNING_SECRET}" msg_pr3_identity_last "${work}/identity-last.json" "${work}/identity-last-response" pr3-identity-last)" "204|pr3-identity-last"
cat > "${work}/org-last.json" <<'JSON'
{"type":"organization.created","timestamp":1785750041000,"data":{"id":"org_pr3_last","name":"Last Owner","slug":"last-owner","created_by":"user_pr3_last"}}
JSON
cat > "${work}/mem-last.json" <<'JSON'
{"type":"organizationMembership.created","timestamp":1785750042000,"data":{"id":"mem_pr3_last","organization":{"id":"org_pr3_last"},"public_user_data":{"user_id":"user_pr3_last"},"role":"org:admin"}}
JSON
assert_status "$(send_signed "${organization_webhook_url}" "${CLERK_ORGANIZATION_WEBHOOK_SIGNING_SECRET}" msg_pr3_org_last "${work}/org-last.json" "${work}/org-last-response" pr3-org-last)" "204|pr3-org-last"
assert_status "$(send_signed "${organization_webhook_url}" "${CLERK_ORGANIZATION_WEBHOOK_SIGNING_SECRET}" msg_pr3_mem_last "${work}/mem-last.json" "${work}/mem-last-response" pr3-mem-last)" "204|pr3-mem-last"
last_membership_id="$(organization_sql "select id from organization.memberships where clerk_membership_id='mem_pr3_last'")"
sign_token user_pr3_last sess_pr3_last org_pr3_last "${auth_work}/pr3-last.token"
assert_status "$(request_json PATCH "${current_url}/members/${last_membership_id}/role" "${auth_work}/pr3-last.token" pr3-last-demote '{"application_role":"admin"}' "${work}/last-demote-body" "${work}/last-demote-headers")" "409"
assert_status "$(request_json DELETE "${current_url}/membership" "${auth_work}/pr3-last.token" pr3-last-leave '' "${work}/last-leave-body" "${work}/last-leave-headers")" "409"
test "$(organization_sql "select count(*) from organization.membership_removal_intents where membership_id='${last_membership_id}'")" = "0"

# Atomic ownership transfer: target becomes owner before actor is demoted, one transaction, never zero owner.
cat > "${work}/identity-transfer.json" <<'JSON'
{"type":"user.created","timestamp":1785750043000,"data":{"id":"user_pr3_transfer","primary_email_address_id":"email_pr3_transfer","email_addresses":[{"id":"email_pr3_transfer","email_address":"transfer@company.example","verification":{"status":"verified"}}]}}
JSON
assert_status "$(send_signed "${identity_webhook_url}" "${CLERK_WEBHOOK_SIGNING_SECRET}" msg_pr3_identity_transfer "${work}/identity-transfer.json" "${work}/identity-transfer-response" pr3-identity-transfer)" "204|pr3-identity-transfer"
cat > "${work}/mem-transfer.json" <<'JSON'
{"type":"organizationMembership.created","timestamp":1785750044000,"data":{"id":"mem_pr3_transfer","organization":{"id":"org_pr3_last"},"public_user_data":{"user_id":"user_pr3_transfer"},"role":"org:admin"}}
JSON
assert_status "$(send_signed "${organization_webhook_url}" "${CLERK_ORGANIZATION_WEBHOOK_SIGNING_SECRET}" msg_pr3_mem_transfer "${work}/mem-transfer.json" "${work}/mem-transfer-response" pr3-mem-transfer)" "204|pr3-mem-transfer"
transfer_membership_id="$(organization_sql "select id from organization.memberships where clerk_membership_id='mem_pr3_transfer'")"
assert_status "$(request_json POST "${transfer_url}" "${auth_work}/pr3-last.token" pr3-transfer "{\"target_membership_id\":\"${transfer_membership_id}\"}" "${work}/transfer-body" "${work}/transfer-headers")" "204"
test "$(organization_sql "select string_agg(application_role, ',' order by id::text) from organization.memberships where organization_id=(select id from organization.organizations where clerk_organization_id='org_pr3_last') and status='active'")" != ""
test "$(organization_sql "select count(*) from organization.memberships where organization_id=(select id from organization.organizations where clerk_organization_id='org_pr3_last') and status='active' and application_role='owner'")" = "1"
test "$(organization_sql "select application_role from organization.memberships where id='${last_membership_id}'")" = "admin"
test "$(organization_sql "select application_role from organization.memberships where id='${transfer_membership_id}'")" = "owner"

# Two-owner leave: reservation immediately removes authorization; signed provider delete finalizes local projection.
assert_status "$(request_json DELETE "${current_url}/membership" "${auth_work}/pr3-owner.token" pr3-owner-leave '' "${work}/owner-leave-body" "${work}/owner-leave-headers")" "202"
test "$(organization_sql "select count(*) from organization.membership_removal_intents where membership_id='${owner_membership_id}'")" = "1"
test "$(organization_sql "select status from organization.memberships where id='${owner_membership_id}'")" = "active"
assert_status "$(request_json GET "${current_url}/membership" "${auth_work}/pr3-owner.token" pr3-owner-after-leave '' "${work}/owner-after-leave-body" "${work}/owner-after-leave-headers")" "403"
cat > "${work}/mem-owner-deleted.json" <<'JSON'
{"type":"organizationMembership.deleted","timestamp":1785750050000,"data":{"id":"mem_pr3_owner","organization":{"id":"org_pr3_main"},"public_user_data":{"user_id":"user_pr3_owner"},"role":"org:admin"}}
JSON
assert_status "$(send_signed "${organization_webhook_url}" "${CLERK_ORGANIZATION_WEBHOOK_SIGNING_SECRET}" msg_pr3_mem_owner_deleted "${work}/mem-owner-deleted.json" "${work}/mem-owner-deleted-response" pr3-mem-owner-deleted)" "204|pr3-mem-owner-deleted"
test "$(organization_sql "select status from organization.memberships where id='${owner_membership_id}'")" = "deleted"
test "$(organization_sql "select count(*) from organization.membership_removal_intents where membership_id='${owner_membership_id}'")" = "0"
test "$(organization_sql "select count(*) from organization.memberships where organization_id=(select id from organization.organizations where clerk_organization_id='org_pr3_main') and status='active' and application_role='owner'")" = "1"

# Historical creator deleted before bootstrap stays permanently ineligible when invited with a fresh membership ID.
cat > "${work}/identity-rejoin-creator.json" <<'JSON'
{"type":"user.created","timestamp":1785750060000,"data":{"id":"user_pr3_rejoin_creator","primary_email_address_id":"email_pr3_rejoin_creator","email_addresses":[{"id":"email_pr3_rejoin_creator","email_address":"creator-rejoin@company.example","verification":{"status":"verified"}}]}}
JSON
cat > "${work}/identity-rejoin-admin.json" <<'JSON'
{"type":"user.created","timestamp":1785750061000,"data":{"id":"user_pr3_rejoin_admin","primary_email_address_id":"email_pr3_rejoin_admin","email_addresses":[{"id":"email_pr3_rejoin_admin","email_address":"rejoin-admin@company.example","verification":{"status":"verified"}}]}}
JSON
assert_status "$(send_signed "${identity_webhook_url}" "${CLERK_WEBHOOK_SIGNING_SECRET}" msg_pr3_identity_rejoin_creator "${work}/identity-rejoin-creator.json" "${work}/identity-rejoin-creator-response" pr3-identity-rejoin-creator)" "204|pr3-identity-rejoin-creator"
assert_status "$(send_signed "${identity_webhook_url}" "${CLERK_WEBHOOK_SIGNING_SECRET}" msg_pr3_identity_rejoin_admin "${work}/identity-rejoin-admin.json" "${work}/identity-rejoin-admin-response" pr3-identity-rejoin-admin)" "204|pr3-identity-rejoin-admin"
cat > "${work}/rejoin-old-created.json" <<'JSON'
{"type":"organizationMembership.created","timestamp":1785750062000,"data":{"id":"mem_pr3_rejoin_old","organization":{"id":"org_pr3_rejoin"},"public_user_data":{"user_id":"user_pr3_rejoin_creator"},"role":"org:member"}}
JSON
cat > "${work}/rejoin-old-deleted.json" <<'JSON'
{"type":"organizationMembership.deleted","timestamp":1785750063000,"data":{"id":"mem_pr3_rejoin_old","organization":{"id":"org_pr3_rejoin"},"public_user_data":{"user_id":"user_pr3_rejoin_creator"},"role":"org:member"}}
JSON
cat > "${work}/rejoin-org.json" <<'JSON'
{"type":"organization.created","timestamp":1785750064000,"data":{"id":"org_pr3_rejoin","name":"Rejoin Fence","slug":"rejoin-fence","created_by":"user_pr3_rejoin_creator"}}
JSON
cat > "${work}/rejoin-admin-created.json" <<'JSON'
{"type":"organizationMembership.created","timestamp":1785750065000,"data":{"id":"mem_pr3_rejoin_admin","organization":{"id":"org_pr3_rejoin"},"public_user_data":{"user_id":"user_pr3_rejoin_admin"},"role":"org:admin"}}
JSON
assert_status "$(send_signed "${organization_webhook_url}" "${CLERK_ORGANIZATION_WEBHOOK_SIGNING_SECRET}" msg_pr3_rejoin_old_created "${work}/rejoin-old-created.json" "${work}/rejoin-old-created-response" pr3-rejoin-old-created)" "204|pr3-rejoin-old-created"
assert_status "$(send_signed "${organization_webhook_url}" "${CLERK_ORGANIZATION_WEBHOOK_SIGNING_SECRET}" msg_pr3_rejoin_old_deleted "${work}/rejoin-old-deleted.json" "${work}/rejoin-old-deleted-response" pr3-rejoin-old-deleted)" "204|pr3-rejoin-old-deleted"
assert_status "$(send_signed "${organization_webhook_url}" "${CLERK_ORGANIZATION_WEBHOOK_SIGNING_SECRET}" msg_pr3_rejoin_org "${work}/rejoin-org.json" "${work}/rejoin-org-response" pr3-rejoin-org)" "204|pr3-rejoin-org"
assert_status "$(send_signed "${organization_webhook_url}" "${CLERK_ORGANIZATION_WEBHOOK_SIGNING_SECRET}" msg_pr3_rejoin_admin_created "${work}/rejoin-admin-created.json" "${work}/rejoin-admin-created-response" pr3-rejoin-admin-created)" "204|pr3-rejoin-admin-created"
test "$(organization_sql "select owner_bootstrap_eligible, owner_bootstrapped from organization.organizations where clerk_organization_id='org_pr3_rejoin'")" = "f|f"
sign_token user_pr3_rejoin_admin sess_pr3_rejoin_admin org_pr3_rejoin "${auth_work}/pr3-rejoin-admin.token"
assert_status "$(request_json POST "${invitation_url}" "${auth_work}/pr3-rejoin-admin.token" pr3-rejoin-invite '{"email":"creator-rejoin@company.example","application_role":"viewer"}' "${work}/rejoin-invite-body" "${work}/rejoin-invite-headers")" "202"
rejoin_intent_id="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["id"])' "${work}/rejoin-invite-body")"
test "$(organization_sql "select count(*) from organization.memberships where organization_id=(select id from organization.organizations where clerk_organization_id='org_pr3_rejoin') and clerk_user_id='user_pr3_rejoin_creator' and status='active'")" = "0"
cat > "${work}/rejoin-new-created.json" <<JSON
{"type":"organizationMembership.created","timestamp":1785750066000,"data":{"id":"mem_pr3_rejoin_new","organization":{"id":"org_pr3_rejoin"},"public_user_data":{"user_id":"user_pr3_rejoin_creator"},"role":"org:member","public_metadata":{"bridgeworks_invitation_id":"${rejoin_intent_id}"}}}
JSON
assert_status "$(send_signed "${organization_webhook_url}" "${CLERK_ORGANIZATION_WEBHOOK_SIGNING_SECRET}" msg_pr3_rejoin_new_created "${work}/rejoin-new-created.json" "${work}/rejoin-new-created-response" pr3-rejoin-new-created)" "204|pr3-rejoin-new-created"
test "$(organization_sql "select m.application_role, o.owner_bootstrap_eligible, o.owner_bootstrapped, (select count(*) from organization.memberships owners where owners.organization_id=o.id and owners.status='active' and owners.application_role='owner') from organization.organizations o join organization.memberships m on m.organization_id=o.id and m.clerk_membership_id='mem_pr3_rejoin_new' where o.clerk_organization_id='org_pr3_rejoin'")" = "viewer|f|f|0"

# Current verified email may change; a new command reads current Identity and replaces the timestamped domain proof.
cat > "${work}/identity-owner-updated.json" <<'JSON'
{"type":"user.updated","timestamp":1785750070000,"data":{"id":"user_pr3_owner","primary_email_address_id":"email_pr3_owner_new","email_addresses":[{"id":"email_pr3_owner_new","email_address":"owner@newco.example","verification":{"status":"verified"}}]}}
JSON
assert_status "$(send_signed "${identity_webhook_url}" "${CLERK_WEBHOOK_SIGNING_SECRET}" msg_pr3_identity_owner_updated "${work}/identity-owner-updated.json" "${work}/identity-owner-updated-response" pr3-identity-owner-updated)" "204|pr3-identity-owner-updated"
# owner left earlier and is no longer authorized; use the remaining owner membership to prove role authority remains local.
sign_token user_pr3_viewer sess_pr3_remaining_owner org_pr3_main "${auth_work}/pr3-remaining-owner.token"
assert_status "$(request_json GET "${current_url}" "${auth_work}/pr3-remaining-owner.token" pr3-current-read '' "${work}/current-read-body" "${work}/current-read-headers")" "200"
python3 - "${work}/current-read-body" <<'PY'
import json, sys
body=json.load(open(sys.argv[1], encoding='utf-8'))
assert body['business_email_domain']=='acme.example'
assert body['business_email_verified_at']
assert 'business_email_verified_by_user_id' not in body
assert all('clerk' not in key for key in body)
PY

# CORS/preflight coverage for every newly public exact route.
for route_method in \
  "${business_email_url}|POST" \
  "${invitation_url}|POST" \
  "${current_url}/membership|DELETE" \
  "${current_url}/members/${admin_membership_id}/role|PATCH" \
  "${current_url}/members/${admin_membership_id}|DELETE" \
  "${transfer_url}|POST"; do
  route="${route_method%%|*}"
  method="${route_method##*|}"
  status="$(curl --show-error --silent --output /dev/null --write-out '%{http_code}' --request OPTIONS \
    -H "Origin: ${allowed_origin}" \
    -H "Access-Control-Request-Method: ${method}" \
    -H 'Access-Control-Request-Headers: authorization,content-type,x-request-id' \
    "${route}")"
  test "${status}" = "200" -o "${status}" = "204"
done

assert_no_sensitive_logs

echo "Organization membership administration and business verification integration tests passed."
