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

trap cleanup EXIT

bash -n \
  "${context_dir}/scripts/cloud-run-entrypoint.sh" \
  "${context_dir}/scripts/validate-cloud-run-image.sh"

test "$(tail -n 1 "${context_dir}/conf/apisix.cloud-run.yaml")" = "#END"

grep --quiet 'enable_admin: false' \
  "${context_dir}/conf/config.cloud-run.yaml"
grep --quiet 'enable_control: false' \
  "${context_dir}/conf/config.cloud-run.yaml"
grep --quiet 'google-cloud-run-auth' \
  "${context_dir}/conf/config.cloud-run.yaml"
grep --quiet 'X-Serverless-Authorization' \
  "${context_dir}/custom/apisix/plugins/google-cloud-run-auth.lua"

if grep -Eqi \
  'BEGIN (RSA )?PRIVATE KEY|whsec_|postgres(ql)?://' \
  "${context_dir}/conf/apisix.cloud-run.yaml" \
  "${context_dir}/conf/config.cloud-run.yaml" \
  "${context_dir}/custom/apisix/plugins/google-cloud-run-auth.lua"; then
  echo "Cloud Run APISIX source contains a secret-shaped value" >&2
  exit 1
fi

docker build \
  --pull \
  --tag "${image_tag}" \
  "${context_dir}"

image_user="$(docker image inspect "${image_tag}" --format '{{.Config.User}}')"
test "${image_user}" = "apisix"

echo "cloud_run_image_non_root=true"

if docker run --rm "${image_tag}" >"${container_logs}" 2>&1; then
  echo "container unexpectedly started without required environment" >&2
  exit 1
fi

grep --quiet 'required environment variable is missing' "${container_logs}"
echo "required_environment_validation=true"

: >"${container_logs}"

docker run \
  --detach \
  --name "${container_name}" \
  --publish 127.0.0.1::9080 \
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

test -n "${host_port}"

ready=false
for attempt in $(seq 1 60); do
  if ! docker inspect "${container_name}" \
    --format '{{.State.Running}}' |
    grep --quiet '^true$'; then
    docker logs "${container_name}" >&2 || true
    exit 1
  fi

  status="$(
    curl \
      --silent \
      --max-time 2 \
      --output /dev/null \
      --write-out '%{http_code}' \
      "http://127.0.0.1:${host_port}/healthz" || true
  )"

  if [[ "${status}" == "200" ]]; then
    ready=true
    break
  fi

  echo "waiting for APISIX test container (${attempt}/60): ${status}"
  sleep 1
done

test "${ready}" = "true"
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
  "http://127.0.0.1:${host_port}/healthz"

grep --quiet --ignore-case "^X-Request-Id: ${request_id}" \
  "${response_headers}"
grep --quiet '"status":"ok"' "${response_body}"
grep --quiet '"component":"apisix"' "${response_body}"

echo "gateway_health_route_valid=true"

protected_status="$(
  curl \
    --silent \
    --show-error \
    --max-time 10 \
    --header 'Authorization: Bearer clerk-token-must-not-be-logged' \
    --output "${response_body}" \
    --write-out '%{http_code}' \
    "http://127.0.0.1:${host_port}/api/v1/identity/health/live"
)"

test "${protected_status}" = "503"
grep --quiet '"code":"gateway_upstream_auth_unavailable"' \
  "${response_body}"

echo "metadata_unavailable_failure_is_closed=true"

docker logs "${container_name}" >"${container_logs}" 2>&1

if grep -q 'clerk-token-must-not-be-logged' "${container_logs}"; then
  echo "Authorization credential leaked into APISIX logs" >&2
  exit 1
fi

if grep -Eqi \
  'X-Serverless-Authorization:[[:space:]]*Bearer[[:space:]]+[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+' \
  "${container_logs}"; then
  echo "Google identity token leaked into APISIX logs" >&2
  exit 1
fi

echo "gateway_credentials_absent_from_logs=true"
echo "APISIX Cloud Run image validation passed."
