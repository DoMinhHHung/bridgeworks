#!/usr/bin/env bash
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=loadtest/scripts/harness.sh
source "${SCRIPT_DIR}/harness.sh"

profile="${1:-smoke}"
[[ "${profile}" == "smoke" ]] || loadtest_die "run-suite supports only the bounded smoke profile"
trap 'exit_code=$?; trap - EXIT; loadtest_cleanup "${exit_code}"; exit "${exit_code}"' EXIT
loadtest_initialize smoke identity_me none

scenarios=(
  identity_me
  organization_current
  organization_membership
  identity_user_unique
  identity_user_retry
  organization_unique
  organization_retry
  membership_unique
  membership_retry
)
for scenario in "${scenarios[@]}"; do
  loadtest_run_scenario "${scenario}"
done
loadtest_write_capacity_example
