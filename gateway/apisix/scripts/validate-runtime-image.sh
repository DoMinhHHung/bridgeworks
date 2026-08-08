#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
context_dir="${repo_root}/gateway/apisix"
image_tag="bridgeworks-apisix-runtime:test"
render_container="bridgeworks-apisix-render-test-${RANDOM}"
cloud_run_container="bridgeworks-apisix-cloud-run-test-${RANDOM}"
render_logs="$(mktemp)"
cloud_run_logs="$(mktemp)"
response_headers="$(mktemp)"
response_body="$(mktemp)"
render_generated="$(mktemp -d)"
cloud_run_generated="$(mktemp -d)"
validation_log="$(mktemp)"

cleanup() {
  docker rm --force "${render_container}" >/dev/null 2>&1 || true
  docker rm --force "${cloud_run_container}" >/dev/null 2>&1 || true
  rm -f \
    "${render_logs}" \
    "${cloud_run_logs}" \
    "${response_headers}" \
    "${response_body}" \
    "${validation_log}"
  rm -rf "${render_generated}" "${cloud_run_generated}"
}

fail() {
  echo "$*" >&2
  exit 1
}

expect_failure() {
  local expected="$1"
  shift
  : >"${validation_log}"
  if "$@" >"${validation_log}" 2>&1; then
    cat "${validation_log}" >&2
    fail "command unexpectedly succeeded while expecting: ${expected}"
  fi
  grep --fixed-strings --quiet "${expected}" "${validation_log}" || {
    cat "${validation_log}" >&2
    fail "failed command did not report expected error: ${expected}"
  }
}

wait_for_health() {
  local container_name="$1"
  local host_port="$2"
  local label="$3"
  local attempt status

  for attempt in $(seq 1 60); do
    if ! docker inspect "${container_name}" --format '{{.State.Running}}' | grep --quiet '^true$'; then
      docker logs "${container_name}" >&2 || true
      fail "${label} APISIX container stopped before becoming ready"
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
      return 0
    fi

    echo "waiting for ${label} APISIX (${attempt}/60): ${status}"
    sleep 1
  done

  fail "${label} APISIX did not become ready"
}

assert_request_id() {
  local host_port="$1"
  local request_id="$2"

  curl \
    --silent \
    --show-error \
    --fail \
    --max-time 5 \
    --header "X-Request-Id: ${request_id}" \
    --dump-header "${response_headers}" \
    --output "${response_body}" \
    "http://127.0.0.1:${host_port}/health/live"

  grep --quiet --ignore-case "^X-Request-Id: ${request_id}" "${response_headers}" ||
    fail "gateway health response did not preserve X-Request-Id"
  grep --quiet '"status":"ok"' "${response_body}" ||
    fail "gateway health response status is invalid"
  grep --quiet '"component":"apisix"' "${response_body}" ||
    fail "gateway health response component is invalid"
}

assert_internal_route_absent() {
  local host_port="$1"
  local status

  status="$(
    curl \
      --silent \
      --show-error \
      --max-time 5 \
      --output "${response_body}" \
      --write-out '%{http_code}' \
      "http://127.0.0.1:${host_port}/internal/v1/platform-access/me"
  )"
  [[ "${status}" == "404" ]] ||
    fail "private Identity platform route returned ${status}, expected 404"
}

sh -n "${context_dir}/scripts/runtime-config.sh"
sh -n "${context_dir}/scripts/runtime-entrypoint.sh"
bash -n "${context_dir}/scripts/validate-runtime-image.sh"
python3 -m py_compile "${context_dir}/scripts/validate-route-parity.py"

test "$(tail -n 1 "${context_dir}/conf/routes.runtime.yaml")" = "#END" ||
  fail "shared runtime routes must end with #END"

for target in cloud-run render; do
  grep --quiet 'enable_admin: false' "${context_dir}/conf/config.${target}.yaml" ||
    fail "APISIX Admin API must be disabled for ${target}"
  grep --quiet 'enable_control: false' "${context_dir}/conf/config.${target}.yaml" ||
    fail "APISIX Control API must be disabled for ${target}"
done

grep --quiet 'google-cloud-run-auth' "${context_dir}/conf/config.cloud-run.yaml" ||
  fail "google-cloud-run-auth must be registered in Cloud Run mode"
if grep --quiet 'google-cloud-run-auth' "${context_dir}/conf/config.render.yaml"; then
  fail "Render mode must not register google-cloud-run-auth"
fi

grep --quiet 'X-Serverless-Authorization' \
  "${context_dir}/custom/apisix/plugins/google-cloud-run-auth.lua" ||
  fail "Cloud Run platform authorization header injection is missing"
grep --quiet 'refresh_retry_seconds' \
  "${context_dir}/custom/apisix/plugins/google-cloud-run-auth.lua" ||
  fail "metadata token refresh backoff is missing"
grep --quiet '@sha256:0e5377839f4ff5e322a5686ab6ce6797ba768008aca1bfc9b71149c3b326c4df' \
  "${context_dir}/Dockerfile" ||
  fail "APISIX base image digest is not pinned"

if grep -R --line-number --fixed-strings '/internal/v1/platform-access/me' \
    "${context_dir}/conf"; then
  fail "private Identity platform route must not be published"
fi

if grep -Eqi \
  'BEGIN (RSA )?PRIVATE KEY|whsec_|postgres(ql)?://' \
  "${context_dir}/conf/config.cloud-run.yaml" \
  "${context_dir}/conf/config.render.yaml" \
  "${context_dir}/conf/upstreams.cloud-run.yaml" \
  "${context_dir}/conf/upstreams.render.yaml" \
  "${context_dir}/conf/routes.runtime.yaml" \
  "${context_dir}/custom/apisix/plugins/google-cloud-run-auth.lua"; then
  fail "APISIX runtime source contains a secret-shaped value"
fi

expect_failure \
  'required environment variable is missing: RUNTIME_TARGET' \
  env CLERK_AUTHORIZED_PARTIES=https://example.com \
  sh "${context_dir}/scripts/runtime-config.sh" '' "${context_dir}/conf" "${render_generated}"

expect_failure \
  'RUNTIME_TARGET must be one of render, cloud-run' \
  env CLERK_AUTHORIZED_PARTIES=https://example.com \
  sh "${context_dir}/scripts/runtime-config.sh" invalid "${context_dir}/conf" "${render_generated}"

expect_failure \
  'IDENTITY_SERVICE_HOST must be a hostname without scheme, path, query, or fragment' \
  env \
    CLERK_AUTHORIZED_PARTIES=https://staging.example.com \
    IDENTITY_SERVICE_HOST=https://identity.internal \
    IDENTITY_SERVICE_PORT=8080 \
    IDENTITY_SERVICE_SCHEME=http \
    ORGANIZATION_SERVICE_HOST=organization.internal \
    ORGANIZATION_SERVICE_PORT=8080 \
    ORGANIZATION_SERVICE_SCHEME=http \
  sh "${context_dir}/scripts/runtime-config.sh" render "${context_dir}/conf" "${render_generated}"

expect_failure \
  'IDENTITY_SERVICE_SCHEME must be http or https' \
  env \
    CLERK_AUTHORIZED_PARTIES=https://staging.example.com \
    IDENTITY_SERVICE_HOST=identity.internal \
    IDENTITY_SERVICE_PORT=8080 \
    IDENTITY_SERVICE_SCHEME=tcp \
    ORGANIZATION_SERVICE_HOST=organization.internal \
    ORGANIZATION_SERVICE_PORT=8080 \
    ORGANIZATION_SERVICE_SCHEME=http \
  sh "${context_dir}/scripts/runtime-config.sh" render "${context_dir}/conf" "${render_generated}"

expect_failure \
  'IDENTITY_SERVICE_PORT must be between 1 and 65535' \
  env \
    CLERK_AUTHORIZED_PARTIES=https://staging.example.com \
    IDENTITY_SERVICE_HOST=identity.internal \
    IDENTITY_SERVICE_PORT=70000 \
    IDENTITY_SERVICE_SCHEME=http \
    ORGANIZATION_SERVICE_HOST=organization.internal \
    ORGANIZATION_SERVICE_PORT=8080 \
    ORGANIZATION_SERVICE_SCHEME=http \
  sh "${context_dir}/scripts/runtime-config.sh" render "${context_dir}/conf" "${render_generated}"

expect_failure \
  'IDENTITY_SERVICE_AUDIENCE must be an HTTPS Cloud Run origin without a path' \
  env \
    CLERK_AUTHORIZED_PARTIES=https://app.example.com \
    IDENTITY_SERVICE_HOST=identity-service.example.run.app \
    IDENTITY_SERVICE_AUDIENCE=http://identity-service.example.run.app \
    ORGANIZATION_SERVICE_HOST=organization-service.example.run.app \
    ORGANIZATION_SERVICE_AUDIENCE=https://organization-service.example.run.app \
  sh "${context_dir}/scripts/runtime-config.sh" cloud-run "${context_dir}/conf" "${cloud_run_generated}"

env \
  CLERK_AUTHORIZED_PARTIES=https://staging.example.com \
  IDENTITY_SERVICE_HOST=identity.internal \
  IDENTITY_SERVICE_PORT=8080 \
  IDENTITY_SERVICE_SCHEME=http \
  ORGANIZATION_SERVICE_HOST=organization.internal \
  ORGANIZATION_SERVICE_PORT=8080 \
  ORGANIZATION_SERVICE_SCHEME=http \
  sh "${context_dir}/scripts/runtime-config.sh" render "${context_dir}/conf" "${render_generated}"

env \
  CLERK_AUTHORIZED_PARTIES=https://app.example.com \
  IDENTITY_SERVICE_HOST=identity-service.example.run.app \
  IDENTITY_SERVICE_AUDIENCE=https://identity-service.example.run.app \
  ORGANIZATION_SERVICE_HOST=organization-service.example.run.app \
  ORGANIZATION_SERVICE_AUDIENCE=https://organization-service.example.run.app \
  sh "${context_dir}/scripts/runtime-config.sh" cloud-run "${context_dir}/conf" "${cloud_run_generated}"

if grep --quiet 'google-cloud-run-auth' "${render_generated}/apisix.yaml"; then
  fail "Render routes must not use google-cloud-run-auth"
fi
grep --quiet 'google-cloud-run-auth' "${cloud_run_generated}/apisix.yaml" ||
  fail "Cloud Run routes must use google-cloud-run-auth"

grep --quiet 'scheme: "${{IDENTITY_SERVICE_SCHEME}}"' "${render_generated}/apisix.yaml" ||
  fail "Render Identity scheme must come from runtime configuration"
grep --quiet 'port: ${{IDENTITY_SERVICE_PORT}}' "${render_generated}/apisix.yaml" ||
  fail "Render Identity port must come from runtime configuration"
grep --quiet 'host: "${{IDENTITY_SERVICE_HOST}}"' "${render_generated}/apisix.yaml" ||
  fail "Render Identity host must come from runtime configuration"

python3 "${context_dir}/scripts/validate-route-parity.py" \
  "${render_generated}/apisix.yaml" \
  "${cloud_run_generated}/apisix.yaml"

echo "runtime_configuration_validation=true"

# Build exactly once. Both runtime modes below execute this same image tag.
docker build \
  --pull \
  --tag "${image_tag}" \
  "${context_dir}"

image_user="$(docker image inspect "${image_tag}" --format '{{.Config.User}}')"
test "${image_user}" = "apisix" ||
  fail "portable APISIX image must run as the apisix user"
echo "runtime_image_non_root=true"

expect_failure \
  'required environment variable is missing: RUNTIME_TARGET' \
  docker run --rm \
    --env PORT=9080 \
    --env CLERK_AUTHORIZED_PARTIES=https://example.com \
    "${image_tag}"

expect_failure \
  'RUNTIME_TARGET must be one of render, cloud-run' \
  docker run --rm \
    --env PORT=9080 \
    --env RUNTIME_TARGET=invalid \
    --env CLERK_AUTHORIZED_PARTIES=https://example.com \
    "${image_tag}"

expect_failure \
  'APISIX container port must be configured as 9080' \
  docker run --rm \
    --env PORT=10000 \
    --env RUNTIME_TARGET=render \
    "${image_tag}"

docker run \
  --detach \
  --name "${render_container}" \
  --publish 127.0.0.1::9080 \
  --add-host identity.internal:127.0.0.1 \
  --add-host organization.internal:127.0.0.1 \
  --env PORT=9080 \
  --env RUNTIME_TARGET=render \
  --env CLERK_AUTHORIZED_PARTIES=https://staging.example.com \
  --env IDENTITY_SERVICE_HOST=identity.internal \
  --env IDENTITY_SERVICE_PORT=8080 \
  --env IDENTITY_SERVICE_SCHEME=http \
  --env ORGANIZATION_SERVICE_HOST=organization.internal \
  --env ORGANIZATION_SERVICE_PORT=8080 \
  --env ORGANIZATION_SERVICE_SCHEME=http \
  "${image_tag}" \
  >/dev/null

render_port="$(docker port "${render_container}" 9080/tcp | awk -F: 'NR == 1 {print $NF}')"
test -n "${render_port}" || fail "Docker did not publish the Render APISIX test port"
wait_for_health "${render_container}" "${render_port}" render
assert_request_id "${render_port}" "apisix-render-ci-request-id"
assert_internal_route_absent "${render_port}"

docker exec "${render_container}" sh -c \
  '! grep -q google-cloud-run-auth /usr/local/apisix/conf/config.yaml && ! grep -q google-cloud-run-auth /usr/local/apisix/conf/apisix.yaml' ||
  fail "Render runtime unexpectedly contains google-cloud-run-auth configuration"

render_test_token="render-clerk-token-must-not-be-logged"
curl \
  --silent \
  --show-error \
  --max-time 10 \
  --header "Authorization: Bearer ${render_test_token}" \
  --output "${response_body}" \
  "http://127.0.0.1:${render_port}/api/v1/identity/health/live" || true

docker logs "${render_container}" >"${render_logs}" 2>&1
if grep -q "${render_test_token}" "${render_logs}"; then
  fail "Render Authorization credential leaked into APISIX logs"
fi
if grep -q 'X-Serverless-Authorization' "${render_logs}"; then
  fail "Render runtime attempted to use the Cloud Run authorization header"
fi

echo "render_runtime_validation=true"

docker run \
  --detach \
  --name "${cloud_run_container}" \
  --publish 127.0.0.1::9080 \
  --add-host metadata.google.internal:127.0.0.1 \
  --env PORT=9080 \
  --env RUNTIME_TARGET=cloud-run \
  --env IDENTITY_SERVICE_HOST=identity-service.example.run.app \
  --env IDENTITY_SERVICE_AUDIENCE=https://identity-service.example.run.app \
  --env ORGANIZATION_SERVICE_HOST=organization-service.example.run.app \
  --env ORGANIZATION_SERVICE_AUDIENCE=https://organization-service.example.run.app \
  --env CLERK_AUTHORIZED_PARTIES=https://app.example.com \
  "${image_tag}" \
  >/dev/null

cloud_run_port="$(docker port "${cloud_run_container}" 9080/tcp | awk -F: 'NR == 1 {print $NF}')"
test -n "${cloud_run_port}" || fail "Docker did not publish the Cloud Run APISIX test port"
wait_for_health "${cloud_run_container}" "${cloud_run_port}" cloud-run
assert_request_id "${cloud_run_port}" "apisix-cloud-run-ci-request-id"
assert_internal_route_absent "${cloud_run_port}"

docker exec "${cloud_run_container}" grep --quiet 'google-cloud-run-auth' /usr/local/apisix/conf/config.yaml ||
  fail "Cloud Run runtime did not register google-cloud-run-auth"
docker exec "${cloud_run_container}" grep --quiet 'google-cloud-run-auth' /usr/local/apisix/conf/apisix.yaml ||
  fail "Cloud Run runtime routes did not enable google-cloud-run-auth"

cloud_test_token="cloud-run-clerk-token-must-not-be-logged"
protected_status="$(
  curl \
    --silent \
    --show-error \
    --max-time 10 \
    --header "Authorization: Bearer ${cloud_test_token}" \
    --output "${response_body}" \
    --write-out '%{http_code}' \
    "http://127.0.0.1:${cloud_run_port}/api/v1/identity/health/live"
)"

[[ "${protected_status}" == "503" ]] ||
  fail "Cloud Run protected route returned ${protected_status}, expected 503"
grep --quiet '"code":"gateway_upstream_auth_unavailable"' "${response_body}" ||
  fail "Cloud Run protected route did not return the stable fail-closed error code"

docker logs "${cloud_run_container}" >"${cloud_run_logs}" 2>&1
if grep -q "${cloud_test_token}" "${cloud_run_logs}"; then
  fail "Cloud Run Authorization credential leaked into APISIX logs"
fi
if grep -Eqi \
  'X-Serverless-Authorization:[[:space:]]*Bearer[[:space:]]+[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+' \
  "${cloud_run_logs}"; then
  fail "Google identity token leaked into APISIX logs"
fi

echo "cloud_run_metadata_failure_is_closed=true"
echo "cloud_run_runtime_validation=true"
echo "same_image_both_runtime_targets=true"
echo "APISIX runtime portability validation passed."
