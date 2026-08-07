#!/usr/bin/env bash
set -Eeuo pipefail

phase=bootstrap
trap 'printf "Organization production completion failed phase=%s line=%s\n" "${phase}" "${LINENO}" >&2' ERR

set -a
source .env
set +a

work="${RUNNER_TEMP:?RUNNER_TEMP is required}/organization-production-completion"
auth_work="${RUNNER_TEMP}/clerk-auth"
mkdir -p "${work}"

base_url='http://127.0.0.1:9080'
identity_webhook_url="${base_url}/api/v1/identity/webhooks/clerk"
organization_webhook_url="${base_url}/api/v1/organizations/webhooks/clerk"
organization_api="${base_url}/api/v1/organizations"
queue_url="${base_url}/api/v1/platform/organizations/verification-queue"

identity_postgres_id="$(docker compose ps -q identity-postgres)"
organization_postgres_id="$(docker compose ps -q organization-postgres)"
organization_service_id="$(docker compose ps -q organization-service)"
identity_postgres_user="$(docker inspect --format '{{range .Config.Env}}{{println .}}{{end}}' "${identity_postgres_id}" | sed -n 's/^POSTGRES_USER=//p')"
identity_postgres_db="$(docker inspect --format '{{range .Config.Env}}{{println .}}{{end}}' "${identity_postgres_id}" | sed -n 's/^POSTGRES_DB=//p')"
organization_postgres_user="$(docker inspect --format '{{range .Config.Env}}{{println .}}{{end}}' "${organization_postgres_id}" | sed -n 's/^POSTGRES_USER=//p')"
organization_postgres_db="$(docker inspect --format '{{range .Config.Env}}{{println .}}{{end}}' "${organization_postgres_id}" | sed -n 's/^POSTGRES_DB=//p')"
network="$(docker inspect --format '{{range $name, $_ := .NetworkSettings.Networks}}{{println $name}}{{end}}' "${organization_service_id}" | head -n 1)"
test -n "${identity_postgres_user}"
test -n "${identity_postgres_db}"
test -n "${organization_postgres_user}"
test -n "${organization_postgres_db}"
test -n "${network}"

if docker inspect --format '{{range .Config.Env}}{{println .}}{{end}}' "${organization_service_id}" | grep --quiet '^ORGANIZATION_MAINTENANCE_DATABASE_URL='; then
  echo 'organization-service runtime must not receive maintenance database authority' >&2
  exit 1
fi
if docker inspect --format '{{range .Config.Env}}{{println .}}{{end}}' "$(docker compose ps -q identity-service)" | grep --quiet '^PLATFORM_ACCESS_DATABASE_URL='; then
  echo 'identity-service runtime must not receive platform-access operator database authority' >&2
  exit 1
fi

identity_sql() {
  docker compose exec -T identity-postgres psql --username "${identity_postgres_user}" --dbname "${identity_postgres_db}" \
    --set ON_ERROR_STOP=1 --tuples-only --no-align --field-separator '|' --command "$1" | sed '/^[[:space:]]*$/d'
}
organization_sql() {
  docker compose exec -T organization-postgres psql --username "${organization_postgres_user}" --dbname "${organization_postgres_db}" \
    --set ON_ERROR_STOP=1 --tuples-only --no-align --field-separator '|' --command "$1" | sed '/^[[:space:]]*$/d'
}

send_identity_webhook() {
  local event_id="$1" payload="$2" request_id="$3"
  (cd service/identity-service && go run ../../.github/scripts/clerk-webhook-client.go \
    -url "${identity_webhook_url}" -secret "${CLERK_WEBHOOK_SIGNING_SECRET}" \
    -event-id "${event_id}" -payload-file "${payload}" -body-output "${work}/${event_id}.body" -request-id "${request_id}")
}
send_organization_webhook() {
  local event_id="$1" payload="$2" request_id="$3"
  (cd service/organization-service && go run ../../.github/scripts/clerk-webhook-client.go \
    -url "${organization_webhook_url}" -secret "${CLERK_ORGANIZATION_WEBHOOK_SIGNING_SECRET}" \
    -event-id "${event_id}" -payload-file "${payload}" -body-output "${work}/${event_id}.body" -request-id "${request_id}")
}
sign_token() {
  local subject="$1" session_id="$2" output="$3" organization_id="${4:-}" organization_role="${5:-}"
  local args=(
    -action sign -private-key "${auth_work}/private.pem" -issuer "${CLERK_ISSUER}"
    -subject "${subject}" -session-id "${session_id}" -authorized-party "${CLERK_AUTHORIZED_PARTIES%%,*}" -output "${output}"
  )
  if [[ -n "${organization_id}" ]]; then
    args+=( -organization-id "${organization_id}" -organization-role "${organization_role}" )
  fi
  (cd service/organization-service && go run ../../.github/scripts/clerk-session-token.go "${args[@]}")
}
request_json() {
  local method="$1" url="$2" token_file="$3" request_id="$4" body_file="$5" headers_file="$6" payload="${7:-}"
  local args=(--show-error --silent --output "${body_file}" --dump-header "${headers_file}" --write-out '%{http_code}'
    -X "${method}" -H "Authorization: Bearer $(cat "${token_file}")" -H "X-Request-Id: ${request_id}")
  if [[ -n "${payload}" ]]; then
    args+=( -H 'Content-Type: application/json' --data-binary "${payload}" )
  fi
  curl "${args[@]}" "${url}"
}
assert_auth_headers() {
  local headers="$1" request_id="$2"
  grep --quiet --ignore-case "^X-Request-Id: ${request_id}" "${headers}"
  grep --quiet --ignore-case '^Cache-Control: no-store' "${headers}"
  grep --quiet --ignore-case '^Vary:.*Authorization' "${headers}"
}

phase=provider-mock
mock_name='bridgeworks-pr4-clerk-backend-mock'
docker rm -f "${mock_name}" >/dev/null 2>&1 || true
docker run --detach --rm --name "${mock_name}" --network "${network}" \
  -e "CLERK_BACKEND_MOCK_SECRET=${CLERK_SECRET_KEY}" \
  -v "${PWD}:/src:ro" -w /src golang:1.26.5-alpine \
  go run .github/scripts/clerk-backend-mock.go >/dev/null
mock_ip=''
for _ in $(seq 1 30); do
  mock_ip="$(docker inspect --format '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' "${mock_name}" 2>/dev/null || true)"
  [[ -n "${mock_ip}" ]] && break
  sleep 1
done
test -n "${mock_ip}"
python3 - "${mock_ip}" <<'PY'
import ipaddress, sys
assert ipaddress.ip_address(sys.argv[1]).is_private
PY
export CLERK_BACKEND_API_URL="http://${mock_ip}:8081"
sed -i "s|^CLERK_BACKEND_API_URL=.*$|CLERK_BACKEND_API_URL=${CLERK_BACKEND_API_URL}|" .env
docker compose up --detach --force-recreate organization-service
curl --retry 30 --retry-all-errors --retry-delay 1 --fail --show-error --silent \
  "${organization_api}/health/ready" >/dev/null

phase=identity-fixtures
users=(owner admin viewer recruiter delivery reviewer removal_present removal_absent removal_outage)
for user in "${users[@]}"; do
  email="${user}-pr4@company-pr4.example"
  cat > "${work}/identity-${user}.json" <<JSON
{"type":"user.created","timestamp":1786147200000,"data":{"id":"user_pr4_${user}","primary_email_address_id":"email_pr4_${user}","email_addresses":[{"id":"email_pr4_${user}","email_address":"${email}","verification":{"status":"verified"}}]}}
JSON
  test "$(send_identity_webhook "msg_pr4_identity_${user}" "${work}/identity-${user}.json" "pr4-identity-${user}")" = "204|pr4-identity-${user}"
done

org_uuid='018f0c76-8f6c-7cc4-8000-000000000401'
other_org_uuid='018f0c76-8f6c-7cc4-8000-000000000402'
owner_mem='018f0c76-8f6c-7cc4-8000-000000000411'
admin_mem='018f0c76-8f6c-7cc4-8000-000000000412'
viewer_mem='018f0c76-8f6c-7cc4-8000-000000000413'
recruiter_mem='018f0c76-8f6c-7cc4-8000-000000000414'
delivery_mem='018f0c76-8f6c-7cc4-8000-000000000415'

owner_identity_uuid="$(identity_sql "select id from app.app_users where clerk_user_id='user_pr4_owner'")"
reviewer_identity_uuid="$(identity_sql "select id from app.app_users where clerk_user_id='user_pr4_reviewer'")"
test -n "${owner_identity_uuid}"
test -n "${reviewer_identity_uuid}"

phase=organization-fixture
organization_sql "
insert into organization.organizations (
  id, clerk_organization_id, name, slug, status, verification_status, trust_status,
  clerk_created_by_user_id, owner_bootstrapped, owner_bootstrap_eligible
) values
  ('${org_uuid}','org_pr4_target','PR4 Target','pr4-target','active','unverified','unassessed','user_pr4_owner',true,false),
  ('${other_org_uuid}','org_pr4_other','PR4 Other','pr4-other','active','unverified','unassessed','user_pr4_admin',true,false)
on conflict do nothing;
insert into organization.memberships (id,clerk_membership_id,organization_id,clerk_user_id,clerk_role,application_role,status) values
  ('${owner_mem}','mem_pr4_owner','${org_uuid}','user_pr4_owner','org:admin','owner','active'),
  ('${admin_mem}','mem_pr4_admin','${org_uuid}','user_pr4_admin','org:admin','admin','active'),
  ('${viewer_mem}','mem_pr4_viewer','${org_uuid}','user_pr4_viewer','org:member','viewer','active'),
  ('${recruiter_mem}','mem_pr4_recruiter','${org_uuid}','user_pr4_recruiter','org:member','recruiter','active'),
  ('${delivery_mem}','mem_pr4_delivery','${org_uuid}','user_pr4_delivery','org:member','delivery_manager','active')
on conflict do nothing;" >/dev/null

sign_token user_pr4_owner sess_pr4_owner "${work}/owner.token" org_pr4_target org:admin
sign_token user_pr4_admin sess_pr4_admin "${work}/admin.token" org_pr4_target org:admin
sign_token user_pr4_viewer sess_pr4_viewer "${work}/viewer.token" org_pr4_target org:member
sign_token user_pr4_recruiter sess_pr4_recruiter "${work}/recruiter.token" org_pr4_target org:member
sign_token user_pr4_delivery sess_pr4_delivery "${work}/delivery.token" org_pr4_target org:member
sign_token user_pr4_reviewer sess_pr4_reviewer_same_session "${work}/reviewer.token"

phase=business-proof
status="$(request_json POST "${organization_api}/current/business-email-verification" "${work}/owner.token" pr4-business-proof "${work}/business.body" "${work}/business.headers")"
test "${status}" = '200'
assert_auth_headers "${work}/business.headers" pr4-business-proof
business_before="$(organization_sql "select business_email_domain, business_email_verified_at::text, business_email_verified_by_user_id::text from organization.organizations where id='${org_uuid}'")"
test -n "${business_before}"

phase=customer-request
status="$(request_json POST "${organization_api}/current/verification" "${work}/owner.token" pr4-verification-request "${work}/request.body" "${work}/request.headers")"
test "${status}" = '200'
assert_auth_headers "${work}/request.headers" pr4-verification-request
test "$(organization_sql "select verification_status from organization.organizations where id='${org_uuid}'")" = 'pending'
test "$(organization_sql "select count(*) from organization.audit_events where organization_id='${org_uuid}' and event_type='organization.verification.requested'")" = '1'

phase=tenant-cannot-review
for actor in owner admin; do
  status="$(request_json POST "${base_url}/api/v1/platform/organizations/${org_uuid}/verification-decisions" "${work}/${actor}.token" "pr4-${actor}-review-denied" "${work}/${actor}-denied.body" "${work}/${actor}-denied.headers" '{"decision":"verified"}')"
  test "${status}" = '403'
  grep --quiet '"code":"platform_permission_required"' "${work}/${actor}-denied.body"
done

phase=platform-grant
reviewer_me_status="$(request_json GET "${base_url}/api/v1/me" "${work}/reviewer.token" pr4-reviewer-me "${work}/reviewer-me.body" "${work}/reviewer-me.headers")"
test "${reviewer_me_status}" = '200'
reviewer_id_user="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["id_user"])' "${work}/reviewer-me.body")"
run_identity_operator() {
  local command="$1" output="$2"
  PLATFORM_ACCESS_DATABASE_URL="${PLATFORM_ACCESS_DATABASE_URL}" \
  PLATFORM_ACCESS_DATABASE_CONNECT_TIMEOUT="${PLATFORM_ACCESS_DATABASE_CONNECT_TIMEOUT}" \
  PLATFORM_ACCESS_COMMAND_TIMEOUT="${PLATFORM_ACCESS_COMMAND_TIMEOUT}" \
    docker compose run --rm --no-deps -e PLATFORM_ACCESS_DATABASE_URL -e PLATFORM_ACCESS_DATABASE_CONNECT_TIMEOUT -e PLATFORM_ACCESS_COMMAND_TIMEOUT \
      --entrypoint /usr/local/bin/identity-platform-access identity-service "${command}" --id-user "${reviewer_id_user}" >"${output}"
}
run_identity_operator grant "${work}/platform-grant.log"

status="$(request_json GET "${queue_url}?limit=10" "${work}/reviewer.token" pr4-queue "${work}/queue.body" "${work}/queue.headers")"
test "${status}" = '200'
assert_auth_headers "${work}/queue.headers" pr4-queue
python3 - "${work}/queue.body" "${org_uuid}" <<'PY'
import json,sys
body=json.load(open(sys.argv[1])); target=sys.argv[2]
assert any(item['id']==target and item['verification_status']=='pending' for item in body['items'])
for item in body['items']:
    forbidden={'clerk_organization_id','clerk_membership_id','clerk_user_id','actor_identity_user_id'}
    assert not forbidden.intersection(item)
PY

phase=reject
status="$(request_json POST "${base_url}/api/v1/platform/organizations/${org_uuid}/verification-decisions" "${work}/reviewer.token" pr4-reject "${work}/reject.body" "${work}/reject.headers" '{"decision":"rejected"}')"
test "${status}" = '200'
test "$(organization_sql "select verification_status from organization.organizations where id='${org_uuid}'")" = 'rejected'
test "$(organization_sql "select count(*) from organization.audit_events where organization_id='${org_uuid}' and event_type='organization.verification.reviewed' and actor_kind='platform_admin' and from_value='pending' and to_value='rejected'")" = '1'

phase=resubmit
status="$(request_json POST "${organization_api}/current/verification" "${work}/owner.token" pr4-resubmit "${work}/resubmit.body" "${work}/resubmit.headers")"
test "${status}" = '200'
test "$(organization_sql "select verification_status from organization.organizations where id='${org_uuid}'")" = 'pending'
test "$(organization_sql "select count(*) from organization.audit_events where organization_id='${org_uuid}' and event_type='organization.verification.requested'")" = '2'

phase=approve-idempotency
status="$(request_json POST "${base_url}/api/v1/platform/organizations/${org_uuid}/verification-decisions" "${work}/reviewer.token" pr4-approve "${work}/approve.body" "${work}/approve.headers" '{"decision":"verified"}')"
test "${status}" = '200'
review_audits="$(organization_sql "select count(*) from organization.audit_events where organization_id='${org_uuid}' and event_type='organization.verification.reviewed'")"
test "${review_audits}" = '2'
status="$(request_json POST "${base_url}/api/v1/platform/organizations/${org_uuid}/verification-decisions" "${work}/reviewer.token" pr4-approve-duplicate "${work}/approve-duplicate.body" "${work}/approve-duplicate.headers" '{"decision":"verified"}')"
test "${status}" = '200'
test "$(organization_sql "select count(*) from organization.audit_events where organization_id='${org_uuid}' and event_type='organization.verification.reviewed'")" = "${review_audits}"
status="$(request_json POST "${base_url}/api/v1/platform/organizations/${org_uuid}/verification-decisions" "${work}/reviewer.token" pr4-opposite "${work}/opposite.body" "${work}/opposite.headers" '{"decision":"rejected"}')"
test "${status}" = '409'
grep --quiet '"code":"verification_decision_conflict"' "${work}/opposite.body"
test "$(organization_sql "select count(*) from organization.audit_events where organization_id='${org_uuid}' and event_type='organization.verification.reviewed'")" = "${review_audits}"
test "$(organization_sql "select trust_status from organization.organizations where id='${org_uuid}'")" = 'unassessed'

phase=provider-update-preserves-product-state
audit_before_update="$(organization_sql "select count(*) from organization.audit_events where organization_id='${org_uuid}'")"
cat >"${work}/organization-update.json" <<'JSON'
{"type":"organization.updated","timestamp":1786147300000,"data":{"id":"org_pr4_target","name":"PR4 Provider Updated","slug":"pr4-provider-updated","created_by":"user_pr4_owner"}}
JSON
test "$(send_organization_webhook msg_pr4_provider_update "${work}/organization-update.json" pr4-provider-update)" = '204|pr4-provider-update'
test "$(organization_sql "select name,slug,verification_status,trust_status from organization.organizations where id='${org_uuid}'")" = 'PR4 Provider Updated|pr4-provider-updated|verified|unassessed'
test "$(organization_sql "select business_email_domain, business_email_verified_at::text, business_email_verified_by_user_id::text from organization.organizations where id='${org_uuid}'")" = "${business_before}"
test "$(organization_sql "select count(*) from organization.audit_events where organization_id='${org_uuid}'")" = "${audit_before_update}"

phase=audit-api
for actor in owner admin; do
  status="$(request_json GET "${organization_api}/current/audit-events?limit=50" "${work}/${actor}.token" "pr4-audit-${actor}" "${work}/audit-${actor}.body" "${work}/audit-${actor}.headers")"
  test "${status}" = '200'
  assert_auth_headers "${work}/audit-${actor}.headers" "pr4-audit-${actor}"
  ! grep --quiet --fixed-strings "${reviewer_identity_uuid}" "${work}/audit-${actor}.body"
  ! grep --quiet --extended-regexp 'user_pr4_|org_pr4_|mem_pr4_|@company-pr4\.example' "${work}/audit-${actor}.body"
done
for actor in viewer recruiter delivery; do
  status="$(request_json GET "${organization_api}/current/audit-events" "${work}/${actor}.token" "pr4-audit-${actor}" "${work}/audit-${actor}.body" "${work}/audit-${actor}.headers")"
  test "${status}" = '403'
done

phase=same-session-revocation
run_identity_operator revoke "${work}/platform-revoke.log"
status="$(request_json GET "${queue_url}" "${work}/reviewer.token" pr4-revoked-same-session "${work}/revoked.body" "${work}/revoked.headers")"
test "${status}" = '403'
run_identity_operator grant "${work}/platform-regrant.log"
status="$(request_json GET "${queue_url}" "${work}/reviewer.token" pr4-regranted-same-session "${work}/regranted.body" "${work}/regranted.headers")"
test "${status}" = '200'

phase=dependency-outage
outage_org='018f0c76-8f6c-7cc4-8000-000000000421'
organization_sql "insert into organization.organizations (id,clerk_organization_id,name,status,verification_status,trust_status) values ('${outage_org}','org_pr4_outage_target','Outage Target','active','pending','unassessed')" >/dev/null
outage_audit_before="$(organization_sql "select count(*) from organization.audit_events where organization_id='${outage_org}'")"
docker compose stop identity-service >/dev/null
status="$(request_json POST "${base_url}/api/v1/platform/organizations/${outage_org}/verification-decisions" "${work}/reviewer.token" pr4-identity-outage "${work}/identity-outage.body" "${work}/identity-outage.headers" '{"decision":"verified"}')"
test "${status}" = '503'
grep --quiet '"code":"service_unavailable"' "${work}/identity-outage.body"
test "$(organization_sql "select verification_status from organization.organizations where id='${outage_org}'")" = 'pending'
test "$(organization_sql "select count(*) from organization.audit_events where organization_id='${outage_org}'")" = "${outage_audit_before}"
docker compose start identity-service >/dev/null
curl --retry 30 --retry-all-errors --retry-delay 1 --fail --show-error --silent "${base_url}/api/v1/identity/health/ready" >/dev/null
status="$(request_json POST "${base_url}/api/v1/platform/organizations/${outage_org}/verification-decisions" "${work}/reviewer.token" pr4-identity-recovered "${work}/identity-recovered.body" "${work}/identity-recovered.headers" '{"decision":"verified"}')"
test "${status}" = '200'

phase=concurrent-decisions
concurrent_opposite='018f0c76-8f6c-7cc4-8000-000000000422'
concurrent_duplicate='018f0c76-8f6c-7cc4-8000-000000000423'
organization_sql "insert into organization.organizations (id,clerk_organization_id,name,status,verification_status,trust_status) values
('${concurrent_opposite}','org_pr4_concurrent_opposite','Concurrent Opposite','active','pending','unassessed'),
('${concurrent_duplicate}','org_pr4_concurrent_duplicate','Concurrent Duplicate','active','pending','unassessed')" >/dev/null
concurrent_request() {
  local org="$1" decision="$2" prefix="$3"
  request_json POST "${base_url}/api/v1/platform/organizations/${org}/verification-decisions" "${work}/reviewer.token" "${prefix}" "${work}/${prefix}.body" "${work}/${prefix}.headers" "{\"decision\":\"${decision}\"}" >"${work}/${prefix}.status"
}
concurrent_request "${concurrent_opposite}" verified pr4-concurrent-verified & p1=$!
concurrent_request "${concurrent_opposite}" rejected pr4-concurrent-rejected & p2=$!
wait "${p1}"; wait "${p2}"
statuses="$(sort "${work}/pr4-concurrent-verified.status" "${work}/pr4-concurrent-rejected.status" | tr '\n' ' ')"
test "${statuses}" = '200 409 '
test "$(organization_sql "select count(*) from organization.audit_events where organization_id='${concurrent_opposite}' and event_type='organization.verification.reviewed'")" = '1'
concurrent_request "${concurrent_duplicate}" verified pr4-duplicate-a & p1=$!
concurrent_request "${concurrent_duplicate}" verified pr4-duplicate-b & p2=$!
wait "${p1}"; wait "${p2}"
test "$(cat "${work}/pr4-duplicate-a.status")" = '200'
test "$(cat "${work}/pr4-duplicate-b.status")" = '200'
test "$(organization_sql "select verification_status from organization.organizations where id='${concurrent_duplicate}'")" = 'verified'
test "$(organization_sql "select count(*) from organization.audit_events where organization_id='${concurrent_duplicate}' and event_type='organization.verification.reviewed'")" = '1'

phase=maintenance-retention
consumed_intent='018f0c76-8f6c-7cc4-8000-000000000431'
pending_intent='018f0c76-8f6c-7cc4-8000-000000000432'
ambiguous_intent='018f0c76-8f6c-7cc4-8000-000000000433'
organization_sql "
insert into organization.membership_invitation_intents (id,organization_id,application_role,created_by_identity_user_id,consumed_membership_id,consumed_at,created_at,updated_at)
values ('${consumed_intent}','${org_uuid}','viewer','${owner_identity_uuid}','${viewer_mem}',now()-interval '40 days',now()-interval '40 days',now()-interval '40 days');
insert into organization.membership_invitation_intents (id,organization_id,application_role,created_by_identity_user_id,created_at,updated_at) values
('${pending_intent}','${org_uuid}','recruiter','${owner_identity_uuid}',now()-interval '90 days',now()-interval '90 days'),
('${ambiguous_intent}','${org_uuid}','delivery_manager','${owner_identity_uuid}',now()-interval '90 days',now()-interval '90 days');" >/dev/null
roles_before="$(organization_sql "select id::text||':'||application_role from organization.memberships where organization_id='${org_uuid}' order by id")"
run_maintenance() {
  local command="$1" output="$2"
  ORGANIZATION_MAINTENANCE_DATABASE_URL="${ORGANIZATION_MAINTENANCE_DATABASE_URL}" \
  ORGANIZATION_MAINTENANCE_DATABASE_CONNECT_TIMEOUT="${ORGANIZATION_MAINTENANCE_DATABASE_CONNECT_TIMEOUT}" \
  ORGANIZATION_MAINTENANCE_COMMAND_TIMEOUT="${ORGANIZATION_MAINTENANCE_COMMAND_TIMEOUT}" \
  ORGANIZATION_INVITATION_INTENT_RETENTION="${ORGANIZATION_INVITATION_INTENT_RETENTION}" \
  ORGANIZATION_REMOVAL_RECONCILE_AFTER="${ORGANIZATION_REMOVAL_RECONCILE_AFTER}" \
  ORGANIZATION_MAINTENANCE_BATCH_SIZE="${ORGANIZATION_MAINTENANCE_BATCH_SIZE}" \
  CLERK_SECRET_KEY="${CLERK_SECRET_KEY}" CLERK_BACKEND_API_URL="${CLERK_BACKEND_API_URL}" CLERK_BACKEND_API_TIMEOUT="${CLERK_BACKEND_API_TIMEOUT}" \
    docker compose run --rm --no-deps \
      -e ORGANIZATION_MAINTENANCE_DATABASE_URL -e ORGANIZATION_MAINTENANCE_DATABASE_CONNECT_TIMEOUT -e ORGANIZATION_MAINTENANCE_COMMAND_TIMEOUT \
      -e ORGANIZATION_INVITATION_INTENT_RETENTION -e ORGANIZATION_REMOVAL_RECONCILE_AFTER -e ORGANIZATION_MAINTENANCE_BATCH_SIZE \
      -e CLERK_SECRET_KEY -e CLERK_BACKEND_API_URL -e CLERK_BACKEND_API_TIMEOUT \
      --entrypoint /usr/local/bin/organization-maintenance organization-service "${command}" >"${output}"
}
run_maintenance prune-consumed-invitations "${work}/maintenance-prune.log"
test "$(organization_sql "select count(*) from organization.membership_invitation_intents where id='${consumed_intent}'")" = '0'
test "$(organization_sql "select count(*) from organization.membership_invitation_intents where id in ('${pending_intent}','${ambiguous_intent}')")" = '2'
test "$(organization_sql "select id::text||':'||application_role from organization.memberships where organization_id='${org_uuid}' order by id")" = "${roles_before}"

phase=maintenance-removals
present_mem='018f0c76-8f6c-7cc4-8000-000000000441'
absent_mem='018f0c76-8f6c-7cc4-8000-000000000442'
outage_mem='018f0c76-8f6c-7cc4-8000-000000000443'
organization_sql "
insert into organization.memberships (id,clerk_membership_id,organization_id,clerk_user_id,application_role,status) values
('${present_mem}','mem_pr4_removal_present','${org_uuid}','user_pr4_removal_present','viewer','active'),
('${absent_mem}','mem_pr4_removal_absent','${org_uuid}','user_pr4_removal_absent','viewer','active');
insert into organization.membership_removal_intents (membership_id,organization_id,requested_by_identity_user_id,created_at) values
('${present_mem}','${org_uuid}','${owner_identity_uuid}',now()-interval '1 hour'),
('${absent_mem}','${org_uuid}','${owner_identity_uuid}',now()-interval '1 hour');" >/dev/null
sign_token user_pr4_removal_present sess_pr4_removal_present "${work}/removal-present.token" org_pr4_target org:member
status="$(request_json GET "${organization_api}/current" "${work}/removal-present.token" pr4-removal-fenced "${work}/removal-fenced.body" "${work}/removal-fenced.headers")"
test "${status}" = '403'
run_maintenance reconcile-removals "${work}/maintenance-reconcile.log"
test "$(organization_sql "select status from organization.memberships where id='${present_mem}'")" = 'active'
test "$(organization_sql "select count(*) from organization.membership_removal_intents where membership_id='${present_mem}'")" = '1'
test "$(organization_sql "select status from organization.memberships where id='${absent_mem}'")" = 'deleted'
test "$(organization_sql "select count(*) from organization.membership_removal_intents where membership_id='${absent_mem}'")" = '0'
test "$(organization_sql "select count(*) from organization.audit_events where organization_id='${org_uuid}' and event_type='membership.removal.completed' and subject_membership_id='${absent_mem}'")" = '1'
run_maintenance reconcile-removals "${work}/maintenance-reconcile-idempotent.log"
test "$(organization_sql "select count(*) from organization.audit_events where organization_id='${org_uuid}' and event_type='membership.removal.completed' and subject_membership_id='${absent_mem}'")" = '1'
organization_sql "
insert into organization.memberships (id,clerk_membership_id,organization_id,clerk_user_id,application_role,status)
values ('${outage_mem}','mem_pr4_removal_outage','${org_uuid}','user_pr4_removal_outage','viewer','active');
insert into organization.membership_removal_intents (membership_id,organization_id,requested_by_identity_user_id,created_at)
values ('${outage_mem}','${org_uuid}','${owner_identity_uuid}',now()-interval '2 hours');" >/dev/null
if run_maintenance reconcile-removals "${work}/maintenance-reconcile-outage.log"; then
  echo 'provider outage reconciliation unexpectedly succeeded' >&2
  exit 1
fi
test "$(organization_sql "select status from organization.memberships where id='${outage_mem}'")" = 'active'
test "$(organization_sql "select count(*) from organization.membership_removal_intents where membership_id='${outage_mem}'")" = '1'

phase=cors-and-private-route
origin="${CLERK_AUTHORIZED_PARTIES%%,*}"
for spec in \
  'GET|/api/v1/platform/organizations/verification-queue' \
  'POST|/api/v1/platform/organizations/'"${org_uuid}"'/verification-decisions' \
  'GET|/api/v1/organizations/current/audit-events'; do
  method="${spec%%|*}"; path="${spec#*|}"
  headers="${work}/cors-$(echo "${method}-${path}" | tr '/:' '--').headers"
  status="$(curl --show-error --silent --output /dev/null --dump-header "${headers}" --write-out '%{http_code}' -X OPTIONS \
    -H "Origin: ${origin}" -H 'Access-Control-Request-Headers: authorization,content-type,x-request-id' \
    -H "Access-Control-Request-Method: ${method}" "${base_url}${path}")"
  test "${status}" = '200' -o "${status}" = '204'
  grep --quiet --ignore-case "^Access-Control-Allow-Origin: ${origin}" "${headers}"
done
private_status="$(curl --show-error --silent --output "${work}/private-route.body" --write-out '%{http_code}' \
  -H "Authorization: Bearer $(cat "${work}/reviewer.token")" "${base_url}/internal/v1/platform-access/me")"
test "${private_status}" = '404'
! grep -R --line-number --fixed-strings '/internal/v1/platform-access/me' gateway/apisix/conf/apisix.yaml gateway/apisix/conf/apisix.cloud-run.yaml

phase=sensitive-log-scan
docker compose logs --no-color organization-service >"${work}/organization-service.log"
docker compose logs --no-color identity-service >"${work}/identity-service.log"
docker logs "${mock_name}" >"${work}/clerk-mock.log" 2>&1 || true
python3 - "${work}" "${reviewer_identity_uuid}" "${DATABASE_URL}" "${ORGANIZATION_DATABASE_URL}" "${PLATFORM_ACCESS_DATABASE_URL}" "${ORGANIZATION_MAINTENANCE_DATABASE_URL}" "${IDENTITY_POSTGRES_PASSWORD}" "${ORGANIZATION_POSTGRES_PASSWORD}" <<'PY'
import pathlib,sys
work=pathlib.Path(sys.argv[1])
forbidden=[
    sys.argv[2], *sys.argv[3:],
    'Authorization: Bearer', 'postgres://', 'svix-signature',
    'sk_test_bridgeworks_local_membership_administration',
    'user_pr4_', 'org_pr4_', 'mem_pr4_', '@company-pr4.example',
]
files=['organization-service.log','identity-service.log','clerk-mock.log','platform-grant.log','platform-revoke.log','platform-regrant.log',
       'maintenance-prune.log','maintenance-reconcile.log','maintenance-reconcile-idempotent.log','maintenance-reconcile-outage.log']
combined='\n'.join((work/name).read_text(encoding='utf-8', errors='replace') for name in files if (work/name).exists())
for value in forbidden:
    if value:
        assert value not in combined, f'sensitive PR4 value leaked: {value[:32]!r}'
# Organization public responses must never expose reviewer Identity UUID, provider IDs, or fixture emails.
for path in work.glob('*.body'):
    if path.name == 'reviewer-me.body':
        continue
    text=path.read_text(encoding='utf-8', errors='replace')
    for value in [sys.argv[2], 'user_pr4_', 'org_pr4_', 'mem_pr4_', '@company-pr4.example', 'Authorization: Bearer', 'postgres://']:
        assert value not in text, f'{path.name} leaked forbidden value: {value[:32]!r}'
PY

phase=complete
docker rm -f "${mock_name}" >/dev/null 2>&1 || true
echo 'Organization production completion integration tests passed.'
