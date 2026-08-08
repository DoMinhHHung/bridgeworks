#!/usr/bin/env bash
set -Eeuo pipefail

fail() {
  printf 'publish-cicd-artifacts: %s\n' "$*" >&2
  exit 1
}

lookup_tree_tag() {
  local tag_ref="$1"
  local stdout_file stderr_file digest

  stdout_file="$(mktemp "${work:?publication work directory is required}/lookup.XXXXXX.out")"
  stderr_file="$(mktemp "${work}/lookup.XXXXXX.err")"

  if docker buildx imagetools inspect "${tag_ref}" \
      --format '{{json .Manifest}}' \
      >"${stdout_file}" 2>"${stderr_file}"; then
    digest="$(jq -r '.digest // empty' "${stdout_file}" 2>/dev/null || true)"
    rm -f "${stdout_file}" "${stderr_file}"
    if [[ "${digest}" =~ ^sha256:[0-9a-f]{64}$ ]]; then
      printf '%s\n' "${digest}"
      return 0
    fi
    printf 'publish-cicd-artifacts: exact tree tag resolved without a strict sha256 digest: %s\n' "${tag_ref}" >&2
    return 20
  fi

  if grep -Eqi 'manifest[ _-]?unknown|name[ _-]?unknown' "${stderr_file}" || \
      grep -Fqi "${tag_ref}: not found" "${stderr_file}"; then
    rm -f "${stdout_file}" "${stderr_file}"
    return 10
  fi

  cat "${stderr_file}" >&2
  rm -f "${stdout_file}" "${stderr_file}"
  return 20
}

resolve_pushed_tree_tag() {
  local tag_ref="$1" expected_digest="$2" name="$3"
  local attempts base_delay attempt delay digest lookup_rc

  attempts="${PUBLISH_POST_PUSH_LOOKUP_ATTEMPTS:-5}"
  base_delay="${PUBLISH_POST_PUSH_RETRY_BASE_DELAY_SECONDS:-1}"
  [[ "${attempts}" =~ ^[1-9][0-9]*$ ]] || fail "invalid post-push lookup attempt count"
  [[ "${base_delay}" =~ ^[0-9]+$ ]] || fail "invalid post-push retry base delay"

  for ((attempt = 1; attempt <= attempts; attempt++)); do
    set +e
    digest="$(lookup_tree_tag "${tag_ref}")"
    lookup_rc=$?
    set -e

    case "${lookup_rc}" in
      0)
        [[ "${digest}" == "${expected_digest}" ]] || \
          fail "pushed tree tag digest mismatch for ${name}: push=${expected_digest} remote=${digest}"
        printf '%s\n' "${digest}"
        return 0
        ;;
      10)
        if (( attempt == attempts )); then
          break
        fi
        delay=$((base_delay * (1 << (attempt - 1))))
        (( delay > base_delay * 4 )) && delay=$((base_delay * 4))
        (( delay > 0 )) && sleep "${delay}"
        ;;
      *)
        fail "post-push tree tag lookup failed ambiguously for ${name}"
        ;;
    esac
  done

  fail "pushed tree tag cannot be resolved after ${attempts} attempts for ${name}"
}

extract_pushed_digest() {
  local push_log="$1" name="$2"
  local -a digests=()

  mapfile -t digests < <(
    sed -nE 's/.*[Dd]igest:[[:space:]]+(sha256:[0-9a-f]{64})([[:space:]].*)?$/\1/p' "${push_log}" |
      sort -u
  )

  [[ "${#digests[@]}" == "1" ]] || \
    fail "docker push did not report exactly one strict digest for ${name}"
  printf '%s\n' "${digests[0]}"
}

write_resolution() {
  local output="$1" tree_sha="$2" image="$3" digest="$4" built="$5"
  python3 - "${output}" "${tree_sha}" "${image}" "${digest}" "${built}" <<'PY'
import json
import sys
from pathlib import Path

output, tree_sha, image, digest, built = sys.argv[1:]
Path(output).write_text(json.dumps({
    "component_tree_sha": tree_sha,
    "image": image,
    "digest": digest,
    "built": built == "true",
}, sort_keys=True) + "\n", encoding="utf-8")
PY
}

resolve_component() {
  local name="$1" image_name="$2" context="$3"
  local tree_sha tree_tag image tag_ref digest decision built output lookup_rc push_log pushed_digest

  tree_sha="$(jq -r --arg name "${name}" '.components[$name].component_tree_sha' "${metadata_file}")"
  [[ "${tree_sha}" =~ ^[0-9a-f]{40}$ ]] || fail "invalid component tree SHA for ${name}"
  tree_tag="tree-${tree_sha}"
  image="${registry}/${image_name}"
  tag_ref="${image}:${tree_tag}"
  output="${work}/components/${name}.json"

  set +e
  digest="$(lookup_tree_tag "${tag_ref}")"
  lookup_rc=$?
  set -e

  case "${lookup_rc}" in
    0)
      decision="$(python3 "${helper}" decision --status found --digest "${digest}")"
      [[ "${decision}" == "reuse" ]] || fail "unexpected reuse decision for ${name}"
      built=false
      ;;
    10)
      decision="$(python3 "${helper}" decision --status missing)"
      [[ "${decision}" == "build" ]] || fail "unexpected build decision for ${name}"
      docker build --tag "${tag_ref}" "${context}"

      # Re-check the exact registry tag immediately before the write. Workflow-level
      # publication concurrency serializes dev runs; immutable GAR tags close external races.
      set +e
      digest="$(lookup_tree_tag "${tag_ref}")"
      lookup_rc=$?
      set -e
      if [[ "${lookup_rc}" == "0" ]]; then
        python3 "${helper}" decision --status found --digest "${digest}" >/dev/null
        fail "tree tag appeared during build for ${name}; refusing to overwrite it"
      fi
      [[ "${lookup_rc}" == "10" ]] || fail "tree tag re-check failed ambiguously for ${name}"

      push_log="${work}/push-${name}.log"
      if ! docker push "${tag_ref}" 2>&1 | tee "${push_log}"; then
        fail "docker push failed for ${name}"
      fi
      pushed_digest="$(extract_pushed_digest "${push_log}" "${name}")"
      python3 "${helper}" validate-digest "${pushed_digest}"

      # Registry metadata can lag a completed push. Retry only exact not-found results,
      # then require the exact tag to resolve to the same digest Docker reported.
      digest="$(resolve_pushed_tree_tag "${tag_ref}" "${pushed_digest}" "${name}")"
      built=true
      ;;
    *)
      fail "tree tag lookup failed ambiguously for ${name}"
      ;;
  esac

  python3 "${helper}" validate-digest "${digest}"
  docker buildx imagetools inspect "${image}@${digest}" >/dev/null
  write_resolution "${output}" "${tree_sha}" "${image}" "${digest}" "${built}"
}

main() {
  : "${GITHUB_EVENT_NAME:?GITHUB_EVENT_NAME is required}"
  : "${GITHUB_REF:?GITHUB_REF is required}"
  : "${GITHUB_SHA:?GITHUB_SHA is required}"
  : "${GCP_PROJECT_ID:?GCP_PROJECT_ID is required}"
  : "${GCP_REGION:?GCP_REGION is required}"
  : "${GCP_ARTIFACT_REGISTRY_REPOSITORY:?GCP_ARTIFACT_REGISTRY_REPOSITORY is required}"

  [[ "${GITHUB_EVENT_NAME}" == "push" ]] || fail "trusted publication requires a push event"
  [[ "${GITHUB_REF}" == "refs/heads/dev" ]] || fail "trusted publication requires refs/heads/dev"
  [[ "${GITHUB_SHA}" =~ ^[0-9a-f]{40}$ ]] || fail "invalid GITHUB_SHA"
  [[ "${GCP_PROJECT_ID}" =~ ^[a-z][a-z0-9-]{4,61}[a-z0-9]$ ]] || fail "invalid GCP_PROJECT_ID"
  [[ "${GCP_REGION}" =~ ^[a-z0-9-]+$ ]] || fail "invalid GCP_REGION"
  [[ "${GCP_ARTIFACT_REGISTRY_REPOSITORY}" =~ ^[a-z0-9][a-z0-9._-]*$ ]] || fail "invalid Artifact Registry repository"

  root="$(git rev-parse --show-toplevel)"
  cd "${root}"

  helper="${root}/.github/scripts/cicd-artifacts.py"
  work="${RUNNER_TEMP:?RUNNER_TEMP is required}/bridgeworks-artifact-publication"
  rm -rf "${work}"
  mkdir -p "${work}/components"

  metadata_file="${work}/metadata.json"
  python3 "${helper}" metadata --head HEAD > "${metadata_file}"
  source_commit_sha="$(jq -r '.source_commit_sha' "${metadata_file}")"
  source_tree_sha="$(jq -r '.source_tree_sha' "${metadata_file}")"
  [[ "${source_commit_sha}" == "${GITHUB_SHA}" ]] || fail "checkout SHA does not match GitHub push SHA"

  registry="${GCP_REGION}-docker.pkg.dev/${GCP_PROJECT_ID}/${GCP_ARTIFACT_REGISTRY_REPOSITORY}"

  resolve_component identity identity-service service/identity-service
  resolve_component organization organization-service service/organization-service
  resolve_component apisix apisix-gateway gateway/apisix

  manifest_path="${RELEASE_MANIFEST_PATH:-${root}/release-manifest.json}"
  rm -f "${manifest_path}"
  python3 "${helper}" manifest \
    --source-commit-sha "${source_commit_sha}" \
    --source-tree-sha "${source_tree_sha}" \
    --identity "${work}/components/identity.json" \
    --organization "${work}/components/organization.json" \
    --apisix "${work}/components/apisix.json" \
    --output "${manifest_path}"
  python3 "${helper}" validate-manifest "${manifest_path}"
  printf 'release_manifest=%s\n' "${manifest_path}"
}

if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
  main "$@"
fi
