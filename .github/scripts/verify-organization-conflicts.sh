#!/usr/bin/env bash
set -euo pipefail

set -a
source .env
set +a

work="${RUNNER_TEMP:?RUNNER_TEMP is required}/organization-conflicts"
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
  local body="$1" request_id="$2"
  grep --quiet '"code":"service_unavailable"' "${body}"
  grep --quiet '"message":"service temporarily unavailable"' "${body}"
  grep --quiet "\"request_id\":\"${request_id}\"" "${body}"
  grep --quiet '"details":null' "${body}"
  ! grep --quiet --ignore-case 'current transaction is aborted' "${body}"
  ! grep --quiet --ignore-case '23505' "${body}"
  ! grep --quiet --ignore-case 'memberships_active_organization_user_uq' "${body}"
  ! grep --quiet --ignore-case 'postgres' "${body}"
}

# A. Active-membership conflict must roll back both mutation and inbox.
cat > "${work}/conflict-org.json" <<'JSON'
{"type":"organization.created","timestamp":1785744100000,"data":{"id":"org_conflict_regression","name":"Conflict Regression","slug":"conflict-regression"}}
JSON
test "$(send_signed msg_conflict_org "${work}/conflict-org.json" "${work}/conflict-org-body" conflict-org)" = "204|conflict-org"

cat > "${work}/membership-a.json" <<'JSON'
{"type":"organizationMembership.created","timestamp":1785744101000,"data":{"id":"mem_conflict_a","organization":{"id":"org_conflict_regression"},"public_user_data":{"user_id":"user_conflict_regression"},"role":"org:member"}}
JSON
test "$(send_signed msg_conflict_a "${work}/membership-a.json" "${work}/membership-a-body" conflict-a)" = "204|conflict-a"

cat > "${work}/membership-b.json" <<'JSON'
{"type":"organizationMembership.created","timestamp":1785744102000,"data":{"id":"mem_conflict_b","organization":{"id":"org_conflict_regression"},"public_user_data":{"user_id":"user_conflict_regression"},"role":"org:member"}}
JSON
conflict_status="$(send_signed msg_conflict_b "${work}/membership-b.json" "${work}/membership-b-body" conflict-b)"
test "${conflict_status}" = "503|conflict-b"
assert_sanitized_503 "${work}/membership-b-body" conflict-b
test "$(organization_sql "select count(*) from organization.memberships where clerk_membership_id='mem_conflict_b'")" = "0"
test "$(organization_sql "select count(*) from organization.clerk_webhook_events where event_id='msg_conflict_b'")" = "0"
test "$(organization_sql "select status from organization.memberships where clerk_membership_id='mem_conflict_a'")" = "active"

# B. The failed event ID is replayable after the old membership is deleted.
cat > "${work}/membership-a-delete.json" <<'JSON'
{"type":"organizationMembership.deleted","timestamp":1785744103000,"data":{"id":"mem_conflict_a","organization":{"id":"org_conflict_regression"},"public_user_data":{"user_id":"user_conflict_regression"},"role":"org:member"}}
JSON
test "$(send_signed msg_conflict_a_delete "${work}/membership-a-delete.json" "${work}/membership-a-delete-body" conflict-a-delete)" = "204|conflict-a-delete"
test "$(organization_sql "select status from organization.memberships where clerk_membership_id='mem_conflict_a'")" = "deleted"
test "$(send_signed msg_conflict_b "${work}/membership-b.json" "${work}/membership-b-replay-body" conflict-b-replay)" = "204|conflict-b-replay"
test "$(organization_sql "select status, substring(id::text from 15 for 1) from organization.memberships where clerk_membership_id='mem_conflict_b'")" = "active|7"
test "$(organization_sql "select count(*) from organization.clerk_webhook_events where event_id='msg_conflict_b'")" = "1"

# C. Concurrent duplicate delivery commits one inbox row and one mutation; every caller receives 204.
cat > "${work}/duplicate-org.json" <<'JSON'
{"type":"organization.created","timestamp":1785744110000,"data":{"id":"org_duplicate_regression","name":"Duplicate Regression","slug":"duplicate-regression"}}
JSON
test "$(send_signed msg_duplicate_org "${work}/duplicate-org.json" "${work}/duplicate-org-body" duplicate-org)" = "204|duplicate-org"
cat > "${work}/duplicate-membership.json" <<'JSON'
{"type":"organizationMembership.created","timestamp":1785744111000,"data":{"id":"mem_duplicate_regression","organization":{"id":"org_duplicate_regression"},"public_user_data":{"user_id":"user_duplicate_regression"},"role":"org:member"}}
JSON
pids=()
for index in 1 2 3 4; do
  (
    send_signed msg_duplicate_membership "${work}/duplicate-membership.json" \
      "${work}/duplicate-${index}-body" "duplicate-${index}" > "${work}/duplicate-${index}-result"
  ) &
  pids+=("$!")
done
for pid in "${pids[@]}"; do
  wait "${pid}"
done
for index in 1 2 3 4; do
  test "$(cat "${work}/duplicate-${index}-result")" = "204|duplicate-${index}"
  test ! -s "${work}/duplicate-${index}-body"
done
test "$(organization_sql "select count(*) from organization.memberships where clerk_membership_id='mem_duplicate_regression'")" = "1"
test "$(organization_sql "select count(*) from organization.clerk_webhook_events where event_id='msg_duplicate_membership'")" = "1"

# D. Concurrent distinct memberships for one organization/user leave at most one active row.
cat > "${work}/race-org.json" <<'JSON'
{"type":"organization.created","timestamp":1785744120000,"data":{"id":"org_distinct_race","name":"Distinct Race","slug":"distinct-race"}}
JSON
test "$(send_signed msg_distinct_org "${work}/race-org.json" "${work}/race-org-body" distinct-org)" = "204|distinct-org"
cat > "${work}/race-a.json" <<'JSON'
{"type":"organizationMembership.created","timestamp":1785744121000,"data":{"id":"mem_distinct_a","organization":{"id":"org_distinct_race"},"public_user_data":{"user_id":"user_distinct_race"},"role":"org:member"}}
JSON
cat > "${work}/race-b.json" <<'JSON'
{"type":"organizationMembership.created","timestamp":1785744121000,"data":{"id":"mem_distinct_b","organization":{"id":"org_distinct_race"},"public_user_data":{"user_id":"user_distinct_race"},"role":"org:member"}}
JSON
(
  send_signed msg_distinct_a "${work}/race-a.json" "${work}/race-a-body" distinct-a > "${work}/race-a-result"
) &
pid_a="$!"
(
  send_signed msg_distinct_b "${work}/race-b.json" "${work}/race-b-body" distinct-b > "${work}/race-b-result"
) &
pid_b="$!"
wait "${pid_a}"
wait "${pid_b}"
status_a="$(cut -d'|' -f1 "${work}/race-a-result")"
status_b="$(cut -d'|' -f1 "${work}/race-b-result")"
test "$(printf '%s\n%s\n' "${status_a}" "${status_b}" | sort | tr '\n' ' ')" = "204 503 "
if [[ "${status_a}" = "503" ]]; then
  losing_event='msg_distinct_a'
  losing_membership='mem_distinct_a'
  assert_sanitized_503 "${work}/race-a-body" distinct-a
else
  losing_event='msg_distinct_b'
  losing_membership='mem_distinct_b'
  assert_sanitized_503 "${work}/race-b-body" distinct-b
fi
test "$(organization_sql "select count(*) from organization.memberships m join organization.organizations o on o.id=m.organization_id where o.clerk_organization_id='org_distinct_race' and m.clerk_user_id='user_distinct_race' and m.status='active'")" = "1"
test "$(organization_sql "select count(*) from organization.clerk_webhook_events where event_id in ('msg_distinct_a','msg_distinct_b')")" = "1"
test "$(organization_sql "select count(*) from organization.memberships where clerk_membership_id='${losing_membership}'")" = "0"
test "$(organization_sql "select count(*) from organization.clerk_webhook_events where event_id='${losing_event}'")" = "0"

# No response body may leak PostgreSQL aborted-transaction or constraint details.
for body in "${work}"/*-body; do
  ! grep --quiet --ignore-case 'current transaction is aborted' "${body}"
  ! grep --quiet --ignore-case 'memberships_active_organization_user_uq' "${body}"
  ! grep --quiet --ignore-case '23505' "${body}"
done

echo "Organization membership conflict and concurrency regressions passed."
