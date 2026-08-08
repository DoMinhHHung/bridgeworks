#!/usr/bin/env bash
set -Eeuo pipefail

fail() {
  printf 'publish-cicd-artifacts: %s\n' "$*" >&2
  exit 1
}

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

lookup_tree_tag() {
  local image="$1"
  local tree_tag="$2"
  local stdout_file stderr_file
  stdout_file="$(mktemp "${work}/lookup.XXXXXX.out")"
  stderr_file="$(mktemp "${work}/lookup.XXXXXX.err")"

  if gcloud artifacts docker tags list "${image}" \
      --filter="tag=${tree_tag}" \
      --limit=2 \
      --format='value(version)' \
      >"${stdout_file}" 2>"${stderr_file}"; then
    mapfile -t versions < <(sed '/^[[:space:]]*$/d' "${stdout_file}")
    rm -f "${stdout_file}" "${stderr_file}"
    case "${#versions[@]}" in
      0)
        return 10
        ;;
      1)
        if [[ "${versions[0]}" =~ (sha256:[0-9a-f]{64})$ ]]; then
          printf '%s\n' "${BASH_REMATCH[1]}"
          return 0
        fi
        fail "tree tag resolved without a strict sha256 digest for ${image}"
        ;;
      *)
        fail "tree tag resolved to multiple versions for ${image}"
        ;;
    esac
  fi

  if grep -Eqi 'NOT_FOUND|not found|does not exist' "${stderr_file}"; then
    rm -f "${stdout_file}" "${stderr_file}"
    return 10
  fi
  rm -f "${stdout_file}" "${stderr_file}"
  return 20
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
  local tree_sha tree_tag image tag_ref digest decision built output lookup_rc

  tree_sha="$(jq -r --arg name "${name}" '.components[$name].component_tree_sha' "${metadata_file}")"
  [[ "${tree_sha}" =~ ^[0-9a-f]{40}$ ]] || fail "invalid component tree SHA for ${name}"
  tree_tag="tree-${tree_sha}"
  image="${registry}/${image_name}"
  tag_ref="${image}:${tree_tag}"
  output="${work}/components/${name}.json"

  set +e
  digest="$(lookup_tree_tag "${image}" "${tree_tag}")"
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

      # Re-check immediately before the write. Workflow-level publication concurrency
      # serializes dev runs; an immutable-tag GAR repository closes external races.
      set +e
      digest="$(lookup_tree_tag "${image}" "${tree_tag}")"
      lookup_rc=$?
      set -e
      if [[ "${lookup_rc}" == "0" ]]; then
        python3 "${helper}" decision --status found --digest "${digest}" >/dev/null
        fail "tree tag appeared during build for ${name}; refusing to overwrite it"
      fi
      [[ "${lookup_rc}" == "10" ]] || fail "tree tag re-check failed ambiguously for ${name}"

      docker push "${tag_ref}"
      set +e
      digest="$(lookup_tree_tag "${image}" "${tree_tag}")"
      lookup_rc=$?
      set -e
      [[ "${lookup_rc}" == "0" ]] || fail "pushed tree tag cannot be resolved for ${name}"
      python3 "${helper}" validate-digest "${digest}"
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
