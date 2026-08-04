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


if __name__ == "__main__":
    unittest.main()
