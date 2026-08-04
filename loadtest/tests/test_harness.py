from __future__ import annotations

import subprocess
import tempfile
import unittest
from pathlib import Path

HARNESS = Path(__file__).resolve().parents[1] / "scripts/harness.sh"


class HarnessValidationTests(unittest.TestCase):
    def run_validation(
        self,
        profile: str,
        scenario: str,
        mode: str,
        identity_replicas: int,
        organization_replicas: int,
    ) -> subprocess.CompletedProcess[str]:
        script = f'''
source "{HARNESS}"
loadtest_validate_configuration \
  "{profile}" "{scenario}" "{mode}" \
  "{identity_replicas}" "{organization_replicas}"
'''
        return subprocess.run(
            ["bash", "-c", script],
            text=True,
            capture_output=True,
            check=False,
        )

    def assert_invalid_before_initialization(
        self,
        profile: str,
        scenario: str,
        mode: str,
        identity_replicas: int,
        organization_replicas: int,
        message: str,
    ) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            marker = Path(temporary) / "side-effect"
            script = f'''
source "{HARNESS}"
loadtest_require_command() {{ printf called > "{marker}"; }}
loadtest_random_suffix() {{ printf called > "{marker}"; }}
loadtest_compose() {{ printf called > "{marker}"; }}
IDENTITY_REPLICAS={identity_replicas}
ORGANIZATION_REPLICAS={organization_replicas}
loadtest_initialize "{profile}" "{scenario}" "{mode}"
'''
            result = subprocess.run(
                ["bash", "-c", script],
                text=True,
                capture_output=True,
                check=False,
            )
            self.assertNotEqual(result.returncode, 0)
            self.assertIn(message, result.stderr)
            self.assertFalse(marker.exists(), "invalid configuration reached initialization side effects")

    def test_identity_unavailable_requires_organization_dependency(self) -> None:
        self.assert_invalid_before_initialization(
            "dependency-degradation", "identity_me", "identity-unavailable", 2, 1,
            "identity dependency degradation requires scenario=organization_dependency",
        )

    def test_identity_delayed_requires_organization_dependency(self) -> None:
        self.assert_invalid_before_initialization(
            "dependency-degradation", "organization_current", "identity-delayed", 1, 1,
            "identity dependency degradation requires scenario=organization_dependency",
        )

    def test_replica_restart_requires_two_target_replicas(self) -> None:
        self.assert_invalid_before_initialization(
            "dependency-degradation", "identity_me", "replica-restart", 1, 3,
            "replica-restart requires at least two target service replicas",
        )
        self.assert_invalid_before_initialization(
            "dependency-degradation", "organization_current", "replica-restart", 3, 1,
            "replica-restart requires at least two target service replicas",
        )

    def test_non_degradation_profile_requires_none(self) -> None:
        self.assert_invalid_before_initialization(
            "smoke", "identity_me", "replica-restart", 2, 1,
            "non-degradation profiles require degradation_mode=none",
        )

    def test_dependency_degradation_requires_explicit_mode(self) -> None:
        self.assert_invalid_before_initialization(
            "dependency-degradation", "identity_me", "none", 2, 1,
            "dependency-degradation requires an explicit degradation mode",
        )

    def test_replica_counts_are_bounded(self) -> None:
        result = self.run_validation("smoke", "identity_me", "none", 6, 1)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("IDENTITY_REPLICAS must be between 1 and 5", result.stderr)

    def test_valid_replica_restart_combinations(self) -> None:
        identity = self.run_validation(
            "dependency-degradation", "identity_me", "replica-restart", 2, 1
        )
        organization = self.run_validation(
            "dependency-degradation", "organization_current", "replica-restart", 1, 2
        )
        self.assertEqual(identity.returncode, 0, identity.stderr)
        self.assertEqual(organization.returncode, 0, organization.stderr)

    def test_http_target_matches_identity_me_regression(self) -> None:
        script = f"""
source "{HARNESS}"
loadtest_http_target_for_scenario identity_me
"""
        result = subprocess.run(
            ["bash", "-c", script], text=True, capture_output=True, check=False
        )
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stdout, "identity-service\t/me\tGET\n")

    def test_no_request_progress_cannot_produce_success_metadata(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            metadata = root / "disruption.json"
            stop = root / "stop"
            ready = root / "ready"
            failure = root / "failure"
            delta = root / "delta"
            stop.touch()
            script = f"""
source "{HARNESS}"
LOADTEST_K6_CONTAINER=fake-k6
LOADTEST_DISRUPTION_METADATA="{metadata}"
docker() {{
  if [[ "$1" == "inspect" ]]; then
    printf 'true\n'
    return 0
  fi
  return 1
}}
loadtest_scrape_http_request_counter() {{ printf '100\n'; }}
if loadtest_monitor_non_target_request_progress \
  "{stop}" "{ready}" "{failure}" "{delta}" \
  identity-service /me GET bbbbbbbbbbbb; then
  exit 91
fi
loadtest_write_restart_metadata \
  identity-service aaaaaaaaaaaa bbbbbbbbbbbb "$(cat "{delta}")" 1
"""
            result = subprocess.run(
                ["bash", "-c", script],
                text=True,
                capture_output=True,
                check=False,
            )
            self.assertNotEqual(result.returncode, 0)
            self.assertIn("measured non-target request progress", result.stderr)
            self.assertFalse(metadata.exists())
            self.assertEqual(delta.read_text(encoding="utf-8").strip(), "0")

    def test_harness_does_not_equate_k6_process_with_continuity(self) -> None:
        harness = HARNESS.read_text(encoding="utf-8")
        self.assertNotIn("traffic_active_during_restart", harness)
        self.assertIn("non_target_request_delta_during_restart", harness)
        self.assertIn("target_request_delta_after_recovery", harness)


if __name__ == "__main__":
    unittest.main()
