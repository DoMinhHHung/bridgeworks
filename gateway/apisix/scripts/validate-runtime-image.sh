#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
context_dir="${repo_root}/gateway/apisix"
image_tag="bridgeworks-apisix-runtime:test"
render_container="bridgeworks-apisix-render-test-${RANDOM}"
cloud_container="bridgeworks-apisix-cloud-run-test-${RANDOM}"
render_generated="$(mktemp -d)"
cloud_generated="$(mktemp -d)"
render_logs="$(mktemp)"
cloud_logs="$(mktemp)"
headers="$(mktemp)"
body="$(mktemp)"
error_log="$(mktemp)"

cleanup() {
  docker rm --force "${render_container}" >/dev/null 2>&1 || true
  docker rm --force "${cloud_container}" >/dev/null 2>&1 || true
  rm -rf "${render_generated}" "${cloud_generated}"
  rm -f "${render_logs}" "${cloud_logs}" "${headers}" "${body}" "${error_log}"
}
trap cleanup EXIT

fail() { echo "$*" >&2; exit 1; }

expect_failure() {
  local expected="$1"
  shift
  : >"${error_log}"
  if "$@" >"${error_log}" 2>&1; then
    cat "${error_log}" >&2
    fail "command unexpectedly succeeded; expected: ${expected}"
  fi
  grep --fixed-strings --quiet "${expected}" "${error_log}" || {
    cat "${error_log}" >&2
    fail "failed command did not report: ${expected}"
  }
}

published_port() {
  docker port "$1" 9080/tcp | awk -F: 'NR == 1 {print $NF}'
}

wait_health() {
  local container="$1" port="$2" label="$3" status
  for attempt in $(seq 1 60); do
    docker inspect "${container}" --format '{{.State.Running}}' | grep --quiet '^true$' || {
      docker logs "${container}" >&2 || true
      fail "${label} container stopped before ready"
    }
    status="$(curl --silent --max-time 2 --output /dev/null --write-out '%{http_code}' "http://127.0.0.1:${port}/health/live" || true)"
    [[ "${status}" == "200" ]] && return 0
    sleep 1
  done
  fail "${label} /health/live did not become ready"
}

assert_gateway_contract() {
  local port="$1" request_id="$2" status
  curl --silent --show-error --fail --max-time 5 \
    --header "X-Request-Id: ${request_id}" \
    --dump-header "${headers}" --output "${body}" \
    "http://127.0.0.1:${port}/health/live"
  grep --quiet --ignore-case "^X-Request-Id: ${request_id}" "${headers}" || fail "X-Request-Id was not preserved"
  grep --quiet '"status":"ok"' "${body}" || fail "gateway health body is invalid"

  status="$(curl --silent --show-error --max-time 5 --output "${body}" --write-out '%{http_code}' \
    "http://127.0.0.1:${port}/internal/v1/platform-access/me")"
  [[ "${status}" == "404" ]] || fail "private Identity platform route returned ${status}, expected 404"
}

sh -n "${context_dir}/scripts/runtime-config.sh"
sh -n "${context_dir}/scripts/runtime-entrypoint.sh"
bash -n "${context_dir}/scripts/validate-runtime-image.sh"
python3 -m py_compile "${context_dir}/scripts/validate-route-parity.py"

test "$(tail -n 1 "${context_dir}/conf/apisix.cloud-run.yaml")" = "#END" || fail "canonical APISIX profile must end with #END"
for target in cloud-run render; do
  grep --quiet 'enable_admin: false' "${context_dir}/conf/config.${target}.yaml" || fail "Admin API must be disabled for ${target}"
  grep --quiet 'enable_control: false' "${context_dir}/conf/config.${target}.yaml" || fail "Control API must be disabled for ${target}"
done
grep --quiet 'google-cloud-run-auth' "${context_dir}/conf/config.cloud-run.yaml" || fail "Cloud Run auth plugin is not registered"
! grep --quiet 'google-cloud-run-auth' "${context_dir}/conf/config.render.yaml" || fail "Render must not register Cloud Run auth"
grep --quiet 'X-Serverless-Authorization' "${context_dir}/custom/apisix/plugins/google-cloud-run-auth.lua" || fail "Cloud Run auth header injection is missing"
grep --quiet 'refresh_retry_seconds' "${context_dir}/custom/apisix/plugins/google-cloud-run-auth.lua" || fail "Cloud Run token refresh backoff is missing"
grep --quiet '@sha256:0e5377839f4ff5e322a5686ab6ce6797ba768008aca1bfc9b71149c3b326c4df' "${context_dir}/Dockerfile" || fail "APISIX base image is not digest pinned"
! grep -R --line-number --fixed-strings '/internal/v1/platform-access/me' "${context_dir}/conf/apisix.cloud-run.yaml" "${context_dir}/conf/apisix.yaml" || fail "private Identity route is published"
! grep -Eqi 'BEGIN (RSA )?PRIVATE KEY|whsec_|postgres(ql)?://' \
  "${context_dir}/conf/apisix.cloud-run.yaml" "${context_dir}/conf/config.cloud-run.yaml" \
  "${context_dir}/conf/config.render.yaml" "${context_dir}/custom/apisix/plugins/google-cloud-run-auth.lua" || fail "APISIX source contains a secret-shaped value"

expect_failure 'required environment variable is missing: RUNTIME_TARGET' \
  env CLERK_AUTHORIZED_PARTIES=https://example.com sh "${context_dir}/scripts/runtime-config.sh" '' "${context_dir}/conf" "${render_generated}"
expect_failure 'RUNTIME_TARGET must be one of render, cloud-run' \
  env CLERK_AUTHORIZED_PARTIES=https://example.com sh "${context_dir}/scripts/runtime-config.sh" invalid "${context_dir}/conf" "${render_generated}"
expect_failure 'IDENTITY_SERVICE_HOST must be a hostname without scheme, path, query, or fragment' \
  env CLERK_AUTHORIZED_PARTIES=https://staging.example.com IDENTITY_SERVICE_HOST=https://identity.internal IDENTITY_SERVICE_PORT=8080 IDENTITY_SERVICE_SCHEME=http ORGANIZATION_SERVICE_HOST=organization.internal ORGANIZATION_SERVICE_PORT=8080 ORGANIZATION_SERVICE_SCHEME=http \
  sh "${context_dir}/scripts/runtime-config.sh" render "${context_dir}/conf" "${render_generated}"
expect_failure 'IDENTITY_SERVICE_SCHEME must be http or https' \
  env CLERK_AUTHORIZED_PARTIES=https://staging.example.com IDENTITY_SERVICE_HOST=identity.internal IDENTITY_SERVICE_PORT=8080 IDENTITY_SERVICE_SCHEME=tcp ORGANIZATION_SERVICE_HOST=organization.internal ORGANIZATION_SERVICE_PORT=8080 ORGANIZATION_SERVICE_SCHEME=http \
  sh "${context_dir}/scripts/runtime-config.sh" render "${context_dir}/conf" "${render_generated}"
expect_failure 'IDENTITY_SERVICE_PORT must be between 1 and 65535' \
  env CLERK_AUTHORIZED_PARTIES=https://staging.example.com IDENTITY_SERVICE_HOST=identity.internal IDENTITY_SERVICE_PORT=70000 IDENTITY_SERVICE_SCHEME=http ORGANIZATION_SERVICE_HOST=organization.internal ORGANIZATION_SERVICE_PORT=8080 ORGANIZATION_SERVICE_SCHEME=http \
  sh "${context_dir}/scripts/runtime-config.sh" render "${context_dir}/conf" "${render_generated}"
expect_failure 'IDENTITY_SERVICE_HOST must be a Cloud Run run.app hostname' \
  env CLERK_AUTHORIZED_PARTIES=https://app.example.com IDENTITY_SERVICE_HOST=identity.internal IDENTITY_SERVICE_AUDIENCE=https://identity.internal ORGANIZATION_SERVICE_HOST=organization-service.example.run.app ORGANIZATION_SERVICE_AUDIENCE=https://organization-service.example.run.app \
  sh "${context_dir}/scripts/runtime-config.sh" cloud-run "${context_dir}/conf" "${cloud_generated}"
expect_failure 'IDENTITY_SERVICE_AUDIENCE must be an HTTPS Cloud Run origin without a path' \
  env CLERK_AUTHORIZED_PARTIES=https://app.example.com IDENTITY_SERVICE_HOST=identity-service.example.run.app IDENTITY_SERVICE_AUDIENCE=http://identity-service.example.run.app ORGANIZATION_SERVICE_HOST=organization-service.example.run.app ORGANIZATION_SERVICE_AUDIENCE=https://organization-service.example.run.app \
  sh "${context_dir}/scripts/runtime-config.sh" cloud-run "${context_dir}/conf" "${cloud_generated}"

rm -f "${render_generated}"/* "${cloud_generated}"/*
env CLERK_AUTHORIZED_PARTIES=https://staging.example.com IDENTITY_SERVICE_HOST=identity.internal IDENTITY_SERVICE_PORT=8080 IDENTITY_SERVICE_SCHEME=http ORGANIZATION_SERVICE_HOST=organization.internal ORGANIZATION_SERVICE_PORT=8080 ORGANIZATION_SERVICE_SCHEME=http \
  sh "${context_dir}/scripts/runtime-config.sh" render "${context_dir}/conf" "${render_generated}"
env CLERK_AUTHORIZED_PARTIES=https://app.example.com IDENTITY_SERVICE_HOST=identity-service.example.run.app IDENTITY_SERVICE_AUDIENCE=https://identity-service.example.run.app ORGANIZATION_SERVICE_HOST=organization-service.example.run.app ORGANIZATION_SERVICE_AUDIENCE=https://organization-service.example.run.app \
  sh "${context_dir}/scripts/runtime-config.sh" cloud-run "${context_dir}/conf" "${cloud_generated}"

! grep --quiet 'google-cloud-run-auth' "${render_generated}/apisix.yaml" || fail "Render routes use Cloud Run auth"
grep --quiet 'google-cloud-run-auth' "${cloud_generated}/apisix.yaml" || fail "Cloud Run routes lost Cloud Run auth"
grep --quiet 'scheme: "${{IDENTITY_SERVICE_SCHEME}}"' "${render_generated}/apisix.yaml" || fail "Render Identity scheme is not runtime-configured"
grep --quiet 'port: ${{IDENTITY_SERVICE_PORT}}' "${render_generated}/apisix.yaml" || fail "Render Identity port is not runtime-configured"
grep --quiet 'host: "${{IDENTITY_SERVICE_HOST}}"' "${render_generated}/apisix.yaml" || fail "Render Identity host is not runtime-configured"
python3 "${context_dir}/scripts/validate-route-parity.py" "${render_generated}/apisix.yaml" "${cloud_generated}/apisix.yaml"
echo 'runtime_configuration_validation=true'

# Exactly one build; both runtime modes below execute this same image tag.
docker build --pull --tag "${image_tag}" "${context_dir}"
test "$(docker image inspect "${image_tag}" --format '{{.Config.User}}')" = 'apisix' || fail "runtime image must run as apisix"

expect_failure 'required environment variable is missing: RUNTIME_TARGET' \
  docker run --rm --env PORT=9080 --env CLERK_AUTHORIZED_PARTIES=https://example.com "${image_tag}"
expect_failure 'RUNTIME_TARGET must be one of render, cloud-run' \
  docker run --rm --env PORT=9080 --env RUNTIME_TARGET=invalid --env CLERK_AUTHORIZED_PARTIES=https://example.com "${image_tag}"
expect_failure 'APISIX container port must be configured as 9080' \
  docker run --rm --env PORT=10000 --env RUNTIME_TARGET=render "${image_tag}"

docker run --detach --name "${render_container}" --publish 127.0.0.1::9080 \
  --add-host identity.internal:127.0.0.1 --add-host organization.internal:127.0.0.1 \
  --env PORT=9080 --env RUNTIME_TARGET=render --env CLERK_AUTHORIZED_PARTIES=https://staging.example.com \
  --env IDENTITY_SERVICE_HOST=identity.internal --env IDENTITY_SERVICE_PORT=8080 --env IDENTITY_SERVICE_SCHEME=http \
  --env ORGANIZATION_SERVICE_HOST=organization.internal --env ORGANIZATION_SERVICE_PORT=8080 --env ORGANIZATION_SERVICE_SCHEME=http \
  "${image_tag}" >/dev/null
render_port="$(published_port "${render_container}")"
test -n "${render_port}" || fail "Render test port was not published"
wait_health "${render_container}" "${render_port}" render
assert_gateway_contract "${render_port}" apisix-render-ci-request-id
docker exec "${render_container}" sh -c '! grep -q google-cloud-run-auth /usr/local/apisix/conf/config.yaml && ! grep -q google-cloud-run-auth /usr/local/apisix/conf/apisix.yaml' || fail "Render generated Cloud Run auth configuration"
render_token='render-clerk-token-must-not-be-logged'
curl --silent --show-error --max-time 10 --header "Authorization: Bearer ${render_token}" --output "${body}" "http://127.0.0.1:${render_port}/api/v1/identity/health/live" || true
docker logs "${render_container}" >"${render_logs}" 2>&1
! grep -q "${render_token}" "${render_logs}" || fail "Render Authorization token leaked into logs"
! grep -q 'X-Serverless-Authorization' "${render_logs}" || fail "Render attempted Cloud Run authorization"
echo 'render_runtime_validation=true'

docker run --detach --name "${cloud_container}" --publish 127.0.0.1::9080 \
  --add-host metadata.google.internal:127.0.0.1 \
  --env PORT=9080 --env RUNTIME_TARGET=cloud-run --env CLERK_AUTHORIZED_PARTIES=https://app.example.com \
  --env IDENTITY_SERVICE_HOST=identity-service.example.run.app --env IDENTITY_SERVICE_AUDIENCE=https://identity-service.example.run.app \
  --env ORGANIZATION_SERVICE_HOST=organization-service.example.run.app --env ORGANIZATION_SERVICE_AUDIENCE=https://organization-service.example.run.app \
  "${image_tag}" >/dev/null
cloud_port="$(published_port "${cloud_container}")"
test -n "${cloud_port}" || fail "Cloud Run test port was not published"
wait_health "${cloud_container}" "${cloud_port}" cloud-run
assert_gateway_contract "${cloud_port}" apisix-cloud-run-ci-request-id
docker exec "${cloud_container}" grep --quiet google-cloud-run-auth /usr/local/apisix/conf/config.yaml || fail "Cloud Run plugin is not registered at runtime"
docker exec "${cloud_container}" grep --quiet google-cloud-run-auth /usr/local/apisix/conf/apisix.yaml || fail "Cloud Run routes lost auth plugin"
cloud_token='cloud-run-clerk-token-must-not-be-logged'
status="$(curl --silent --show-error --max-time 10 --header "Authorization: Bearer ${cloud_token}" --output "${body}" --write-out '%{http_code}' "http://127.0.0.1:${cloud_port}/api/v1/identity/health/live")"
[[ "${status}" == '503' ]] || fail "Cloud Run protected route returned ${status}, expected 503"
grep --quiet '"code":"gateway_upstream_auth_unavailable"' "${body}" || fail "Cloud Run metadata failure did not fail closed"
docker logs "${cloud_container}" >"${cloud_logs}" 2>&1
! grep -q "${cloud_token}" "${cloud_logs}" || fail "Cloud Run Authorization token leaked into logs"
! grep -Eqi 'X-Serverless-Authorization:[[:space:]]*Bearer[[:space:]]+[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+' "${cloud_logs}" || fail "Google identity token leaked into logs"

echo 'cloud_run_metadata_failure_is_closed=true'
echo 'cloud_run_runtime_validation=true'
echo 'same_image_both_runtime_targets=true'
echo 'APISIX runtime portability validation passed.'
