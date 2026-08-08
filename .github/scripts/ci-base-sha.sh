#!/usr/bin/env bash
set -euo pipefail

fail() {
  printf 'ci-base-sha: %s\n' "$*" >&2
  exit 1
}

require_commit() {
  local sha="$1"
  [[ "${sha}" =~ ^[0-9a-f]{40}$ ]] || fail "invalid base SHA: ${sha:-<empty>}"
  git cat-file -e "${sha}^{commit}" 2>/dev/null || fail "base commit is not available in checkout: ${sha}"
  printf '%s\n' "${sha}"
}

: "${GITHUB_EVENT_NAME:?GITHUB_EVENT_NAME is required}"
: "${GITHUB_EVENT_PATH:?GITHUB_EVENT_PATH is required}"

case "${GITHUB_EVENT_NAME}" in
  pull_request)
    base_sha="$(jq -r '.pull_request.base.sha // empty' "${GITHUB_EVENT_PATH}")"
    require_commit "${base_sha}"
    ;;
  push)
    base_sha="$(jq -r '.before // empty' "${GITHUB_EVENT_PATH}")"
    if [[ "${base_sha}" == '0000000000000000000000000000000000000000' || -z "${base_sha}" ]]; then
      base_sha="$(git rev-list --max-parents=0 HEAD | tail -n 1)"
    fi
    require_commit "${base_sha}"
    ;;
  workflow_dispatch)
    if base_sha="$(git rev-parse --verify HEAD^ 2>/dev/null)"; then
      require_commit "${base_sha}"
    else
      require_commit "$(git rev-parse HEAD)"
    fi
    ;;
  *)
    fail "unsupported event: ${GITHUB_EVENT_NAME}"
    ;;
esac
