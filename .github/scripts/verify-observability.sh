#!/usr/bin/env bash
set -euo pipefail

set -a
source .env
set +a

work="${RUNNER_TEMP:?RUNNER_TEMP is required}/observability"
mkdir -p "${work}"
base_url='http://127.0.0.1:9080'
identity_webhook_url="${base_url}/api/v1/identity/webhooks/clerk"
organization_webhook_url="${base_url}/api/v1/organizations/webhooks/clerk"
apisix_config='gateway/apisix/conf/apisix.yaml'
apisix_backup="${work}/apisix.yaml"
cp "${apisix_config}" "${apisix_backup}"

identity_container="$(docker compose ps -q identity-service)"
network_name="$(docker inspect --format '{{range $name, $_ := .NetworkSettings.Networks}}{{$name}}{{end}}' "${identity_container}")"
test -n "${network_name}"
probe_container="bridgeworks-observability-probe-${GITHUB_RUN_ID:-local}"
docker rm --force "${probe_container}" >/dev/null 2>&1 || true
docker run --detach --rm --name "${probe_container}" --network "${network_name}" \
  --entrypoint sleep curlimages/curl:8.12.1 300 >/dev/null

cleanup() {
  cp "${apisix_backup}" "${apisix_config}" >/dev/null 2>&1 || true
  docker rm --force "${probe_container}" >/dev/null 2>&1 || true
}
trap cleanup EXIT

probe_status() {
  local method="$1" url="$2"
  docker exec "${probe_container}" curl --show-error --silent --output /dev/null \
    --write-out '%{http_code}' --request "${method}" "${url}"
}

send_signed() {
  local url="$1" secret="$2" event_id="$3" payload="$4" output="$5" request_id="$6"
  (
    cd service/identity-service
    go run ../../.github/scripts/clerk-webhook-client.go \
      -url "${url}" -secret "${secret}" -event-id "${event_id}" \
      -payload-file "${payload}" -body-output "${output}" -request-id "${request_id}"
  )
}

# Private listeners expose only metrics behavior, never application handlers.
for service in identity-service organization-service; do
  test "$(probe_status GET "http://${service}:9090/metrics")" = "200"
  test "$(probe_status POST "http://${service}:9090/metrics")" = "405"
  test "$(probe_status GET "http://${service}:9090/health/live")" = "404"
  test "$(probe_status GET "http://${service}:9090/health/ready")" = "404"
  test "$(probe_status GET "http://${service}:9090/arbitrary/private/path")" = "404"
  test "$(probe_status GET "http://${service}:8080/metrics")" = "404"
done
test "$(probe_status GET 'http://identity-service:9090/me')" = "404"
test "$(probe_status GET 'http://organization-service:9090/organizations/current')" = "404"
test "$(probe_status GET 'http://organization-service:9090/organizations/current/membership')" = "404"

# Add an ephemeral CI-only APISIX route for the APISIX-supported PURGE
# extension method. PURGE is intentionally outside the service metric allowlist,
# so it must collapse to OTHER while the access log retains the raw method. The
# committed public route contract remains unchanged and cleanup restores the file.
python3 - "${apisix_config}" <<'PY'
from pathlib import Path
import sys

path = Path(sys.argv[1])
text = path.read_text()
marker = "#END"
route = """  - id: bridgeworks-observability-custom-method-probe
    name: bridgeworks-observability-custom-method-probe
    uri: /__observability/custom-method
    plugins:
      request-id: { header_name: X-Request-Id, include_in_response: true, algorithm: uuid }
      proxy-rewrite: { uri: /__observability/custom-method }
    upstream:
      type: roundrobin
      timeout: { connect: 1, send: 2, read: 2 }
      nodes: { "identity-service:8080": 1 }
"""
if marker not in text:
    raise SystemExit("APISIX standalone end marker missing")
path.write_text(text.replace(marker, route + marker, 1))
PY

custom_method_status=''
for _ in $(seq 1 30); do
  custom_method_status="$(curl --show-error --silent --output /dev/null --write-out '%{http_code}' \
    --request PURGE -H 'X-Request-Id: obs-custom-method' \
    "${base_url}/__observability/custom-method")"
  if [ "${custom_method_status}" = "404" ]; then
    break
  fi
  sleep 1
done
test "${custom_method_status}" = "404"

cat > "${work}/identity.json" <<'JSON'
{"type":"user.created","timestamp":1785826800000,"data":{"id":"user_obs_metrics","primary_email_address_id":"email_obs_metrics","email_addresses":[{"id":"email_obs_metrics","email_address":"observability@example.test","verification":{"status":"verified"}}]}}
JSON
test "$(send_signed "${identity_webhook_url}" "${CLERK_WEBHOOK_SIGNING_SECRET}" msg_obs_identity "${work}/identity.json" "${work}/identity-body" obs-identity-success)" = "204|obs-identity-success"
test "$(send_signed "${identity_webhook_url}" "${CLERK_WEBHOOK_SIGNING_SECRET}" msg_obs_identity "${work}/identity.json" "${work}/identity-duplicate-body" obs-identity-duplicate)" = "204|obs-identity-duplicate"
identity_rejected_status="$(curl --show-error --silent --output "${work}/identity-rejected-body" --write-out '%{http_code}' \
  -H 'Content-Type: application/json' -H 'X-Request-Id: obs-identity-rejected' \
  -H 'svix-id: msg_obs_identity_rejected' -H "svix-timestamp: $(date +%s)" \
  -H 'svix-signature: v1,invalid' --data-binary "@${work}/identity.json" "${identity_webhook_url}")"
test "${identity_rejected_status}" = "400"
identity_failure_status="$(curl --show-error --silent --output "${work}/identity-failure-body" --write-out '%{http_code}' \
  -H 'X-Request-Id: obs-identity-failure' "${base_url}/api/v1/me")"
test "${identity_failure_status}" = "401"

cat > "${work}/organization.json" <<'JSON'
{"type":"organization.created","timestamp":1785826810000,"data":{"id":"org_obs_metrics","name":"Observability","slug":"observability"}}
JSON
test "$(send_signed "${organization_webhook_url}" "${CLERK_ORGANIZATION_WEBHOOK_SIGNING_SECRET}" msg_obs_organization "${work}/organization.json" "${work}/organization-body" obs-organization-success)" = "204|obs-organization-success"
test "$(send_signed "${organization_webhook_url}" "${CLERK_ORGANIZATION_WEBHOOK_SIGNING_SECRET}" msg_obs_organization "${work}/organization.json" "${work}/organization-duplicate-body" obs-organization-duplicate)" = "204|obs-organization-duplicate"
cat > "${work}/membership.json" <<'JSON'
{"type":"organizationMembership.created","timestamp":1785826811000,"data":{"id":"mem_obs_metrics","organization":{"id":"org_obs_metrics"},"public_user_data":{"user_id":"user_obs_metrics"},"role":"org:member"}}
JSON
test "$(send_signed "${organization_webhook_url}" "${CLERK_ORGANIZATION_WEBHOOK_SIGNING_SECRET}" msg_obs_membership "${work}/membership.json" "${work}/membership-body" obs-membership-success)" = "204|obs-membership-success"
organization_rejected_status="$(curl --show-error --silent --output "${work}/organization-rejected-body" --write-out '%{http_code}' \
  -H 'Content-Type: application/json' -H 'X-Request-Id: obs-organization-rejected' \
  -H 'svix-id: msg_obs_organization_rejected' -H "svix-timestamp: $(date +%s)" \
  -H 'svix-signature: v1,invalid' --data-binary "@${work}/membership.json" "${organization_webhook_url}")"
test "${organization_rejected_status}" = "400"
for path in current current/membership; do
  status="$(curl --show-error --silent --output /dev/null --write-out '%{http_code}' \
    -H "X-Request-Id: obs-organization-${path//\//-}" "${base_url}/api/v1/organizations/${path}")"
  test "${status}" = "401"
done

identity_metrics="${work}/identity.metrics"
organization_metrics="${work}/organization.metrics"
docker compose exec -T identity-service wget --quiet --output-document=- http://127.0.0.1:9090/metrics > "${identity_metrics}"
docker compose exec -T identity-service wget --quiet --output-document=- http://organization-service:9090/metrics > "${organization_metrics}"

for metric in http_requests_total http_request_duration_seconds http_requests_in_flight \
  clerk_webhook_events_total database_pool_acquired_connections database_pool_idle_connections \
  database_pool_total_connections database_pool_max_connections database_pool_acquire_count_total \
  database_pool_acquire_duration_seconds_total database_pool_empty_acquire_count_total \
  database_pool_canceled_acquire_count_total; do
  grep --quiet "${metric}" "${identity_metrics}"
  grep --quiet "${metric}" "${organization_metrics}"
done

grep --quiet 'route="/me"' "${identity_metrics}"
grep --quiet 'method="OTHER",route="unknown"' "${identity_metrics}"
! grep --quiet --fixed-strings 'PURGE' "${identity_metrics}"
grep --quiet 'aggregate="user",outcome="processed"' "${identity_metrics}"
grep --quiet 'aggregate="user",outcome="duplicate"' "${identity_metrics}"
grep --quiet 'aggregate="user",outcome="rejected"' "${identity_metrics}"
grep --quiet 'route="/organizations/current"' "${organization_metrics}"
grep --quiet 'route="/organizations/current/membership"' "${organization_metrics}"
grep --quiet 'aggregate="organization",outcome="processed"' "${organization_metrics}"
grep --quiet 'aggregate="organization",outcome="duplicate"' "${organization_metrics}"
grep --quiet 'aggregate="membership",outcome="processed"' "${organization_metrics}"
grep --quiet 'aggregate="membership",outcome="rejected"' "${organization_metrics}"

for secret in user_obs_metrics org_obs_metrics mem_obs_metrics observability@example.test \
  obs-identity-success obs-identity-failure obs-organization-success; do
  ! grep --quiet --fixed-strings "${secret}" "${identity_metrics}"
  ! grep --quiet --fixed-strings "${secret}" "${organization_metrics}"
done

identity_logs="${work}/identity.logs"
organization_logs="${work}/organization.logs"
docker compose logs --no-color identity-service > "${identity_logs}"
docker compose logs --no-color organization-service > "${organization_logs}"
for request_id in obs-identity-success obs-identity-failure obs-custom-method; do
  test "$(grep 'http request completed' "${identity_logs}" | grep -c "\"request_id\":\"${request_id}\"")" = "1"
done
for request_id in obs-organization-success obs-organization-current obs-organization-current-membership; do
  test "$(grep 'http request completed' "${organization_logs}" | grep -c "\"request_id\":\"${request_id}\"")" = "1"
done
grep 'http request completed' "${identity_logs}" | grep '"request_id":"obs-custom-method"' | grep --quiet '"method":"PURGE"'
grep 'http request completed' "${identity_logs}" | grep --quiet '"route":"/webhooks/clerk"'
grep 'http request completed' "${organization_logs}" | grep --quiet '"route":"/organizations/current"'
for secret in user_obs_metrics org_obs_metrics mem_obs_metrics observability@example.test; do
  ! grep 'http request completed' "${identity_logs}" | grep --quiet --fixed-strings "${secret}"
  ! grep 'http request completed' "${organization_logs}" | grep --quiet --fixed-strings "${secret}"
done

metrics_gateway_status="$(curl --show-error --silent --output /dev/null --write-out '%{http_code}' "${base_url}/metrics")"
test "${metrics_gateway_status}" = "404"
for service in identity-service organization-service; do
  container_id="$(docker compose ps -q "${service}")"
  bindings="$(docker inspect --format '{{json .HostConfig.PortBindings}}' "${container_id}")"
  case "${bindings}" in null|'{}') ;; *) echo "${service} published private ports: ${bindings}" >&2; exit 1;; esac
done

curl --fail --show-error --silent --output /dev/null "${base_url}/api/v1/identity/health/ready"
curl --fail --show-error --silent --output /dev/null "${base_url}/api/v1/organizations/health/ready"
test "$(curl --show-error --silent --output /dev/null --write-out '%{http_code}' "${base_url}/api/v1/me")" = "401"
test "$(curl --show-error --silent --output /dev/null --write-out '%{http_code}' "${base_url}/api/v1/organizations/current")" = "401"

echo "Private metrics, bounded methods, and structured access-log regressions passed."
