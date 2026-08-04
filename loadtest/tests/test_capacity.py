from __future__ import annotations

import importlib.util
import sys
import unittest
from pathlib import Path

MODULE_PATH = Path(__file__).resolve().parents[1] / "capacity.py"
SPEC = importlib.util.spec_from_file_location("capacity", MODULE_PATH)
assert SPEC and SPEC.loader
capacity = importlib.util.module_from_spec(SPEC)
sys.modules[SPEC.name] = capacity
SPEC.loader.exec_module(capacity)


class CapacityBudgetTests(unittest.TestCase):
    def test_calculates_separate_per_replica_ceilings(self) -> None:
        budget = capacity.calculate_budget(
            postgres_max_connections=100,
            reserved_admin_connections=5,
            reserved_migration_connections=5,
            operational_headroom_connections=10,
            identity_allocation=30,
            identity_max_replicas=3,
            organization_allocation=40,
            organization_max_replicas=4,
        )
        self.assertEqual(budget.usable_service_connections, 80)
        self.assertEqual(budget.unallocated_service_connections, 10)
        self.assertEqual(budget.identity.per_replica_max_connections, 10)
        self.assertEqual(budget.organization.per_replica_max_connections, 10)

    def test_rejects_allocations_above_usable_budget(self) -> None:
        with self.assertRaisesRegex(ValueError, "allocations exceed"):
            capacity.calculate_budget(
                postgres_max_connections=50,
                reserved_admin_connections=5,
                reserved_migration_connections=5,
                operational_headroom_connections=10,
                identity_allocation=20,
                identity_max_replicas=2,
                organization_allocation=20,
                organization_max_replicas=2,
            )

    def test_rejects_zero_replica_count(self) -> None:
        with self.assertRaisesRegex(ValueError, "identity_max_replicas"):
            capacity.calculate_budget(
                postgres_max_connections=100,
                reserved_admin_connections=5,
                reserved_migration_connections=5,
                operational_headroom_connections=10,
                identity_allocation=30,
                identity_max_replicas=0,
                organization_allocation=30,
                organization_max_replicas=3,
            )


if __name__ == "__main__":
    unittest.main()
