from __future__ import annotations

import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]


class ProfileContractTests(unittest.TestCase):
    def test_profiles_and_scenarios_are_bounded(self) -> None:
        common = (ROOT / "k6/lib/common.js").read_text(encoding="utf-8")
        for profile in ("smoke", "baseline", "burst", "saturation", "dependency-degradation"):
            self.assertIn(f"'{profile}'", common)
        self.assertIn("abortOnFail: true", common)

        authenticated = (ROOT / "k6/authenticated-read.js").read_text(encoding="utf-8")
        webhook = (ROOT / "k6/webhook.js").read_text(encoding="utf-8")
        for scenario in ("identity_me", "organization_current", "organization_membership", "organization_dependency"):
            self.assertIn(scenario, authenticated)
        for scenario in (
            "identity_user_unique", "identity_user_retry", "organization_unique",
            "organization_retry", "membership_unique", "membership_retry",
        ):
            self.assertIn(scenario, webhook)

    def test_pinned_images_and_no_production_module_dependency(self) -> None:
        harness = (ROOT / "scripts/harness.sh").read_text(encoding="utf-8")
        self.assertIn("grafana/k6:2.0.0", harness)
        self.assertIn("python:3.13.5-alpine3.22", harness)
        self.assertNotIn("go get", harness)

    def test_replica_restart_is_single_container_only(self) -> None:
        harness = (ROOT / "scripts/harness.sh").read_text(encoding="utf-8")
        self.assertIn('docker restart "${target}"', harness)
        self.assertNotIn("loadtest_compose restart identity-service", harness)
        self.assertNotIn("loadtest_compose restart organization-service", harness)
        self.assertIn("loadtest_container_replica_key", harness)
        self.assertIn("metrics-${phase}-${sample}-${file_service}-${replica_key}.prom", harness)

    def test_summary_contains_required_percentiles(self) -> None:
        common = (ROOT / "k6/lib/common.js").read_text(encoding="utf-8")
        self.assertIn("summaryTrendStats", common)
        self.assertIn("p(50)", common)
        self.assertIn("p(95)", common)
        self.assertIn("p(99)", common)


if __name__ == "__main__":
    unittest.main()
