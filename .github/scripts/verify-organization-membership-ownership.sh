#!/usr/bin/env bash
set -euo pipefail

set -a
source .env
set +a

work="${RUNNER_TEMP:?RUNNER_TEMP is required}/organization-membership-ownership"
mkdir -p "${work}"

base_url='http://127.0.0.1:9080'
webhook_url="${base_url}/api/v1/organizations/webhooks/clerk"

organization_postgres_id="$(docker compose ps -q organization-postgres)"
organization_postgres_user="$(docker inspect --format '{{range .Config.Env}}{{println .}}{{end}}' "${organization_postgres_id}" | sed -n 's/^POSTGRES_USER=//p')"
organization_postgres_db="$(docker inspect --format '{{range .Config.Env}}{{println .}}{{end}}' "${organization_postgres_id}" | sed -n 's/^POSTGRES_DB=//p')"

organization_sql() {
  docker compose exec -T organization-postgres psql \
    --username "${organization_postgres_user}" \
    --dbname "${organization_postgres_db}" \
    --set ON_ERROR_STOP=1 \
    --tuples-only --no-align --field-separator '|' \
    --command "$1" | sed '/^[[:space:]]*$/d'
}

(
  cd service/organization-service
  go build -o "${work}/clerk-webhook-client" ../../.github/scripts/clerk-webhook-client.go
)

send_signed() {
  local event_id="$1" payload="$2" output="$3" request_id="$4"
  "${work}/clerk-webhook-client" \
    -url "${webhook_url}" \
    -secret "${CLERK_ORGANIZATION_WEBHOOK_SIGNING_SECRET}" \
    -event-id "${event_id}" \
    -payload-file "${payload}" \
    -body-output "${output}" \
    -request-id "${request_id}"
}

assert_sanitized_503() {
  local body="$1" request_id="$2" local_id="$3"
  grep --quiet '"code":"service_unavailable"' "${body}"
  grep --quiet '"message":"service temporarily unavailable"' "${body}"
  grep --quiet "\"request_id\":\"${request_id}\"" "${body}"
  grep --quiet '"details":null' "${body}"
  for forbidden in \
    org_ownership_a org_ownership_b mem_ownership user_ownership \
    memberships_clerk_membership_id_uq memberships_active_organization_user_uq \
    'inconsistent Clerk membership projection' 'current transaction is aborted' \
    23505 postgres "${local_id}"; do
    ! grep --quiet --fixed-strings --ignore-case "${forbidden}" "${body}"
  done
}

log_since="$(date -u +%Y-%m-%dT%H:%M:%SZ)"

cat > "${work}/organization-a.json" <<'JSON'
{"type":"organization.created","timestamp":1785744200000,"data":{"id":"org_ownership_a","name":"Ownership A","slug":"ownership-a"}}
JSON
test "$(send_signed msg_ownership_org_a "${work}/organization-a.json" "${work}/organization-a-body" ownership-org-a)" = "204|ownership-org-a"

cat > "${work}/organization-b.json" <<'JSON'
{"type":"organization.created","timestamp":1785744201000,"data":{"id":"org_ownership_b","name":"Ownership B","slug":"ownership-b"}}
JSON
test "$(send_signed msg_ownership_org_b "${work}/organization-b.json" "${work}/organization-b-body" ownership-org-b)" = "204|ownership-org-b"

cat > "${work}/membership-create.json" <<'JSON'
{"type":"organizationMembership.created","timestamp":1785744202000,"data":{"id":"mem_ownership","organization":{"id":"org_ownership_a"},"public_user_data":{"user_id":"user_ownership"},"role":"org:admin"}}
JSON
test "$(send_signed msg_ownership_membership_create "${work}/membership-create.json" "${work}/membership-create-body" ownership-create)" = "204|ownership-create"

membership_local_id="$(organization_sql "select id::text from organization.memberships where clerk_membership_id='mem_ownership'")"
test -n "${membership_local_id}"
test "$(organization_sql "select o.clerk_organization_id, m.clerk_user_id, m.clerk_role, m.status from organization.memberships m join organization.organizations o on o.id=m.organization_id where m.clerk_membership_id='mem_ownership'")" = "org_ownership_a|user_ownership|org:admin|active"

cat > "${work}/membership-update-mismatch.json" <<'JSON'
{"type":"organizationMembership.updated","timestamp":1785744203000,"data":{"id":"mem_ownership","organization":{"id":"org_ownership_b"},"public_user_data":{"user_id":"user_ownership"},"role":"org:member"}}
JSON
update_status="$(send_signed msg_ownership_update_mismatch "${work}/membership-update-mismatch.json" "${work}/membership-update-mismatch-body" ownership-update-mismatch)"
test "${update_status}" = "503|ownership-update-mismatch"
assert_sanitized_503 "${work}/membership-update-mismatch-body" ownership-update-mismatch "${membership_local_id}"
test "$(organization_sql "select o.clerk_organization_id, m.clerk_user_id, m.clerk_role, m.status from organization.memberships m join organization.organizations o on o.id=m.organization_id where m.clerk_membership_id='mem_ownership'")" = "org_ownership_a|user_ownership|org:admin|active"
test "$(organization_sql "select count(*) from organization.clerk_webhook_events where event_id='msg_ownership_update_mismatch'")" = "0"

cat > "${work}/membership-delete-mismatch.json" <<'JSON'
{"type":"organizationMembership.deleted","timestamp":1785744204000,"data":{"id":"mem_ownership","organization":{"id":"org_ownership_b"},"public_user_data":{"user_id":"user_ownership"},"role":"org:admin"}}
JSON
delete_status="$(send_signed msg_ownership_delete_mismatch "${work}/membership-delete-mismatch.json" "${work}/membership-delete-mismatch-body" ownership-delete-mismatch)"
test "${delete_status}" = "503|ownership-delete-mismatch"
assert_sanitized_503 "${work}/membership-delete-mismatch-body" ownership-delete-mismatch "${membership_local_id}"
test "$(organization_sql "select o.clerk_organization_id, m.clerk_user_id, m.clerk_role, m.status from organization.memberships m join organization.organizations o on o.id=m.organization_id where m.clerk_membership_id='mem_ownership'")" = "org_ownership_a|user_ownership|org:admin|active"
test "$(organization_sql "select count(*) from organization.clerk_webhook_events where event_id='msg_ownership_delete_mismatch'")" = "0"

# Matching ownership still follows the existing update and delete behavior.
cat > "${work}/membership-update-match.json" <<'JSON'
{"type":"organizationMembership.updated","timestamp":1785744205000,"data":{"id":"mem_ownership","organization":{"id":"org_ownership_a"},"public_user_data":{"user_id":"user_ownership"},"role":"org:member"}}
JSON
test "$(send_signed msg_ownership_update_match "${work}/membership-update-match.json" "${work}/membership-update-match-body" ownership-update-match)" = "204|ownership-update-match"
test "$(organization_sql "select o.clerk_organization_id, m.clerk_user_id, m.clerk_role, m.status from organization.memberships m join organization.organizations o on o.id=m.organization_id where m.clerk_membership_id='mem_ownership'")" = "org_ownership_a|user_ownership|org:member|active"

cat > "${work}/membership-delete-match.json" <<'JSON'
{"type":"organizationMembership.deleted","timestamp":1785744206000,"data":{"id":"mem_ownership","organization":{"id":"org_ownership_a"},"public_user_data":{"user_id":"user_ownership"},"role":"org:member"}}
JSON
test "$(send_signed msg_ownership_delete_match "${work}/membership-delete-match.json" "${work}/membership-delete-match-body" ownership-delete-match)" = "204|ownership-delete-match"
test "$(organization_sql "select o.clerk_organization_id, m.clerk_user_id, m.clerk_role, m.status from organization.memberships m join organization.organizations o on o.id=m.organization_id where m.clerk_membership_id='mem_ownership'")" = "org_ownership_a|user_ownership|org:member|deleted"

docker compose logs --no-color --since "${log_since}" organization-service > "${work}/organization-service.log"
for forbidden in \
  org_ownership_a org_ownership_b mem_ownership user_ownership \
  memberships_clerk_membership_id_uq memberships_active_organization_user_uq \
  'inconsistent Clerk membership projection' 'current transaction is aborted' \
  23505 "${membership_local_id}"; do
  ! grep --quiet --fixed-strings --ignore-case "${forbidden}" "${work}/organization-service.log"
done

echo "Organization membership ownership regression passed."
