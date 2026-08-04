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


if __name__ == "__main__":
    unittest.main()
