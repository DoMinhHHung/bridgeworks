#!/usr/bin/env python3
from __future__ import annotations

import argparse
import json
from dataclasses import asdict, dataclass
from pathlib import Path


@dataclass(frozen=True)
class ServiceBudget:
    allocation: int
    maximum_replicas: int
    per_replica_max_connections: int


@dataclass(frozen=True)
class CapacityBudget:
    postgres_max_connections: int
    reserved_admin_connections: int
    reserved_migration_connections: int
    operational_headroom_connections: int
    usable_service_connections: int
    unallocated_service_connections: int
    identity: ServiceBudget
    organization: ServiceBudget


def positive(name: str, value: int) -> int:
    if value <= 0:
        raise ValueError(f"{name} must be greater than zero")
    return value


def non_negative(name: str, value: int) -> int:
    if value < 0:
        raise ValueError(f"{name} must not be negative")
    return value


def calculate_budget(
    postgres_max_connections: int,
    reserved_admin_connections: int,
    reserved_migration_connections: int,
    operational_headroom_connections: int,
    identity_allocation: int,
    identity_max_replicas: int,
    organization_allocation: int,
    organization_max_replicas: int,
) -> CapacityBudget:
    postgres_max_connections = positive("postgres_max_connections", postgres_max_connections)
    reserved_admin_connections = non_negative("reserved_admin_connections", reserved_admin_connections)
    reserved_migration_connections = non_negative("reserved_migration_connections", reserved_migration_connections)
    operational_headroom_connections = non_negative(
        "operational_headroom_connections", operational_headroom_connections
    )
    identity_allocation = positive("identity_allocation", identity_allocation)
    organization_allocation = positive("organization_allocation", organization_allocation)
    identity_max_replicas = positive("identity_max_replicas", identity_max_replicas)
    organization_max_replicas = positive("organization_max_replicas", organization_max_replicas)

    reserved_total = (
        reserved_admin_connections
        + reserved_migration_connections
        + operational_headroom_connections
    )
    usable = postgres_max_connections - reserved_total
    if usable <= 0:
        raise ValueError("reserved connections and headroom consume the PostgreSQL limit")

    allocated = identity_allocation + organization_allocation
    if allocated > usable:
        raise ValueError("service allocations exceed the usable PostgreSQL connection budget")

    return CapacityBudget(
        postgres_max_connections=postgres_max_connections,
        reserved_admin_connections=reserved_admin_connections,
        reserved_migration_connections=reserved_migration_connections,
        operational_headroom_connections=operational_headroom_connections,
        usable_service_connections=usable,
        unallocated_service_connections=usable - allocated,
        identity=ServiceBudget(
            allocation=identity_allocation,
            maximum_replicas=identity_max_replicas,
            per_replica_max_connections=identity_allocation // identity_max_replicas,
        ),
        organization=ServiceBudget(
            allocation=organization_allocation,
            maximum_replicas=organization_max_replicas,
            per_replica_max_connections=organization_allocation // organization_max_replicas,
        ),
    )


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description="Calculate deterministic BridgeWorks database capacity ceilings")
    parser.add_argument("--postgres-max-connections", type=int, required=True)
    parser.add_argument("--reserved-admin-connections", type=int, required=True)
    parser.add_argument("--reserved-migration-connections", type=int, required=True)
    parser.add_argument("--operational-headroom-connections", type=int, required=True)
    parser.add_argument("--identity-allocation", type=int, required=True)
    parser.add_argument("--identity-max-replicas", type=int, required=True)
    parser.add_argument("--organization-allocation", type=int, required=True)
    parser.add_argument("--organization-max-replicas", type=int, required=True)
    parser.add_argument("--output-json")
    parser.add_argument("--output-markdown")
    return parser.parse_args()


def render_markdown(budget: CapacityBudget) -> str:
    return f"""# BridgeWorks Database Capacity Budget

## Shared PostgreSQL/Supabase budget

| Item | Connections |
|---|---:|
| Maximum connections | {budget.postgres_max_connections} |
| Reserved admin | {budget.reserved_admin_connections} |
| Reserved migrations | {budget.reserved_migration_connections} |
| Operational headroom | {budget.operational_headroom_connections} |
| Usable service budget | {budget.usable_service_connections} |
| Unallocated service budget | {budget.unallocated_service_connections} |

## Per-service ceilings

| Service | Allocation | Maximum replicas | Per-replica maximum ceiling |
|---|---:|---:|---:|
| Identity | {budget.identity.allocation} | {budget.identity.maximum_replicas} | {budget.identity.per_replica_max_connections} |
| Organization | {budget.organization.allocation} | {budget.organization.maximum_replicas} | {budget.organization.per_replica_max_connections} |

Formula:

```text
per-replica max connections
  <= floor(service connection budget / maximum service replica count)
```

These values are capacity ceilings, not production defaults. Pool changes require representative deployment measurements and rollback criteria.
"""


def main() -> None:
    args = parse_args()
    try:
        budget = calculate_budget(
            postgres_max_connections=args.postgres_max_connections,
            reserved_admin_connections=args.reserved_admin_connections,
            reserved_migration_connections=args.reserved_migration_connections,
            operational_headroom_connections=args.operational_headroom_connections,
            identity_allocation=args.identity_allocation,
            identity_max_replicas=args.identity_max_replicas,
            organization_allocation=args.organization_allocation,
            organization_max_replicas=args.organization_max_replicas,
        )
    except ValueError as exc:
        raise SystemExit(str(exc)) from exc

    payload = asdict(budget)
    serialized = json.dumps(payload, indent=2, sort_keys=True) + "\n"
    markdown = render_markdown(budget)

    if args.output_json:
        path = Path(args.output_json)
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(serialized, encoding="utf-8")
    else:
        print(serialized, end="")

    if args.output_markdown:
        path = Path(args.output_markdown)
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(markdown, encoding="utf-8")


if __name__ == "__main__":
    main()
