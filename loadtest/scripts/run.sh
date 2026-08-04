#!/usr/bin/env bash
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=loadtest/scripts/harness.sh
source "${SCRIPT_DIR}/harness.sh"

profile="${1:-${LOAD_PROFILE:-smoke}}"
scenario="${2:-${LOAD_SCENARIO:-identity_me}}"
degradation_mode="${3:-${DEGRADATION_MODE:-none}}"

trap 'exit_code=$?; trap - EXIT; loadtest_cleanup "${exit_code}"; exit "${exit_code}"' EXIT
loadtest_initialize "${profile}" "${degradation_mode}"
loadtest_run_scenario "${scenario}"
loadtest_write_capacity_example
