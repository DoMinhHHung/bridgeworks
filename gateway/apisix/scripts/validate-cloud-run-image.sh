#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
context_dir="${repo_root}/gateway/apisix"
image_tag="bridgeworks-apisix-cloud-run:test"
container_name="bridgeworks-apisix-cloud-run-test-${RANDOM}"
container_logs="$(mktemp)"
response_headers="$(mktemp)"
response_body="$(mktemp)"

cleanup() {
  docker rm --force "${container_name}" >/dev/null 2>&1 || true
  rm -f "${container_logs}" "${response_headers}" "${response_body}"
}

fail() {
  echo "$*" >&2
  exit 1
}

trap cleanup EXIT

sh -n "${context_dir}/scripts/cloud-run-entrypoint.sh"
bash -n "${context_dir}/scripts/validate-cloud-run-image.sh"

test "$(tail -n 1 "${context_dir}/conf/apisix.cloud-run.yaml")" = "#END" ||
  fail "apisix.cloud-run.yaml must end with #END"

grep --quiet 'enable_admin: false' \
  "${context_dir}/conf/config.cloud-run.yaml" ||
  fail "APISIX Admin API must be disabled"
grep --quiet 'enable_control: false' \
  "${context_dir}/conf/config.cloud-run.yaml" ||
  fail "APISIX Control API must be disabled"
grep --quiet 'google-cloud-run-auth' \
  "${context_dir}/conf/config.cloud-run.yaml" ||
  fail "google-cloud-run-auth must be registered"
grep --quiet 'X-Serverless-Authorization' \
  "${context_dir}/custom/apisix/plugins/google-cloud-run-auth.lua" ||
  fail "Cloud Run platform authorization header injection is missing"
grep --quiet 'refresh_retry_seconds' \
  "${context_dir}/custom/apisix/plugins/google-cloud-run-auth.lua" ||
  fail "metadata token refresh backoff is missing"
grep --quiet '@sha256:0e5377839f4ff5e322a5686ab6ce6797ba768008aca1bfc9b71149c3b326c4df' \
  "${context_dir}/Dockerfile" ||
  fail "APISIX base image digest is not pinned"

if ! awk '
  /^  - id: bridgeworks-gateway-health[[:space:]]*$/ {
    in_health=1
    next
  }
  in_health && /^  - id: / {
    exit(found ? 0 : 1)
  }
  in_health && /^[[:space:]]*uri:[[:space:]]*\/health\/live[[:space:]]*$/ {
    found=1
  }
  END { if (!found) exit 1 }
' "${context_dir}/conf/apisix.cloud-run.yaml"; then
  fail "Cloud Run gateway health route must use /health/live"
fi

if grep -Eq '^[[:space:]]*uri:[[:space:]]+[^[:space:]]*z[[:space:]]*$' \
  "${context_dir}/conf/apisix.cloud-run.yaml"; then
  fail "Cloud Run production route paths must not end in z"
fi

test "$(grep -c '^[[:space:]]*retries: 0$' "${context_dir}/conf/apisix.cloud-run.yaml")" = "2" ||
  fail "private Cloud Run upstream retries must be disabled"
test "$(grep -c '^[[:space:]]*connect: 3$' "${context_dir}/conf/apisix.cloud-run.yaml")" = "2" ||
  fail "private Cloud Run connect timeouts must be 3 seconds"

if grep -Eqi \
  'BEGIN (RSA )?PRIVATE KEY|whsec_|postgres(ql)?://' \
  "${context_dir}/conf/apisix.cloud-run.yaml" \
  "${context_dir}/conf/config.cloud-run.yaml" \
  "${context_dir}/custom/apisix/plugins/google-cloud-run-auth.lua"; then
  fail "Cloud Run APISIX source contains a secret-shaped value"
fi

docker build \
  --pull \
  --tag "${image_tag}" \
  "${context_dir}"

image_user="$(docker image inspect "${image_tag}" --format '{{.Config.User}}')"
test "${image_user}" = "apisix" ||
  fail "Cloud Run APISIX image must run as the apisix user"

echo "cloud_run_image_non_root=true"

if docker run --rm "${image_tag}" >"${container_logs}" 2>&1; then
  fail "container unexpectedly started without required environment"
fi

grep --quiet 'required environment variable is missing' "${container_logs}" ||
  fail "missing environment validation did not return the expected error"
echo "required_environment_validation=true"

: >"${container_logs}"

if docker run \
  --rm \
  --env PORT=9080 \
  --env IDENTITY_SERVICE_HOST=identity-service.example.run.app \
  --env 'IDENTITY_SERVICE_AUDIENCE=https://identity service.example.run.app' \
  --env ORGANIZATION_SERVICE_HOST=organization-service.example.run.app \
  --env ORGANIZATION_SERVICE_AUDIENCE=https://organization-service.example.run.app \
  --env CLERK_AUTHORIZED_PARTIES=http://localhost:3000,http://127.0.0.1:5173 \
  "${image_tag}" \
  >"${container_logs}" 2>&1; then
  fail "container unexpectedly accepted a malformed Cloud Run audience"
fi

grep --quiet 'IDENTITY_SERVICE_AUDIENCE contains invalid hostname characters' \
  "${container_logs}" ||
  fail "malformed Cloud Run audience did not return the expected error"
echo "cloud_run_audience_validation=true"

: >"${container_logs}"

docker run \
  --detach \
  --name "${container_name}" \
  --publish 127.0.0.1::9080 \
  --add-host metadata.google.internal:127.0.0.1 \
  --env PORT=9080 \
  --env IDENTITY_SERVICE_HOST=identity-service.example.run.app \
  --env IDENTITY_SERVICE_AUDIENCE=https://identity-service.example.run.app \
  --env ORGANIZATION_SERVICE_HOST=organization-service.example.run.app \
  --env ORGANIZATION_SERVICE_AUDIENCE=https://organization-service.example.run.app \
  --env CLERK_AUTHORIZED_PARTIES=http://localhost:3000,http://127.0.0.1:5173 \
  "${image_tag}" \
  >/dev/null

host_port="$(
  docker port "${container_name}" 9080/tcp |
    awk -F: 'NR == 1 {print $NF}'
)"

test -n "${host_port}" ||
  fail "Docker did not publish the APISIX test port"

ready=false
for attempt in $(seq 1 60); do
  if ! docker inspect "${container_name}" \
    --format '{{.State.Running}}' |
    grep --quiet '^true$'; then
    docker logs "${container_name}" >&2 || true
    fail "APISIX test container stopped before becoming ready"
  fi

  status="$(
    curl \
      --silent \
      --max-time 2 \
      --output /dev/null \
      --write-out '%{http_code}' \
      "http://127.0.0.1:${host_port}/health/live" || true
  )"

  if [[ "${status}" == "200" ]]; then
    ready=true
    break
  fi

  echo "waiting for APISIX test container (${attempt}/60): ${status}"
  sleep 1
done

[[ "${ready}" == "true" ]] ||
  fail "APISIX test container did not become ready"
echo "cloud_run_apisix_started=true"

request_id="apisix-cloud-run-ci-request-id"

curl \
  --silent \
  --show-error \
  --fail \
  --max-time 5 \
  --header "X-Request-Id: ${request_id}" \
  --dump-header "${response_headers}" \
  --output "${response_body}" \
  "http://127.0.0.1:${host_port}/health/live"

grep --quiet --ignore-case "^X-Request-Id: ${request_id}" \
  "${response_headers}" ||
  fail "gateway health response did not preserve X-Request-Id"
grep --quiet '"status":"ok"' "${response_body}" ||
  fail "gateway health response status is invalid"
grep --quiet '"component":"apisix"' "${response_body}" ||
  fail "gateway health response component is invalid"

echo "gateway_health_route_valid=true"

test_clerk_token="clerk-token-must-not-be-logged"
protected_status="$(
  curl \
    --silent \
    --show-error \
    --max-time 10 \
    --header "Authorization: Bearer ${test_clerk_token}" \
    --output "${response_body}" \
    --write-out '%{http_code}' \
    "http://127.0.0.1:${host_port}/api/v1/identity/health/live"
)"

[[ "${protected_status}" == "503" ]] ||
  fail "protected route returned ${protected_status}, expected 503"
grep --quiet '"code":"gateway_upstream_auth_unavailable"' \
  "${response_body}" ||
  fail "protected route did not return the stable fail-closed error code"

echo "metadata_unavailable_failure_is_closed=true"

docker logs "${container_name}" >"${container_logs}" 2>&1

if grep -q "${test_clerk_token}" "${container_logs}"; then
  fail "Authorization credential leaked into APISIX logs"
fi

if grep -Eqi \
  'X-Serverless-Authorization:[[:space:]]*Bearer[[:space:]]+[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+' \
  "${container_logs}"; then
  fail "Google identity token leaked into APISIX logs"
fi

echo "gateway_credentials_absent_from_logs=true"
echo "APISIX Cloud Run image validation passed."
