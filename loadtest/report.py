#!/usr/bin/env python3
from __future__ import annotations

import argparse
import json
import math
import re
from collections import defaultdict
from dataclasses import dataclass
from pathlib import Path
from typing import Any, Callable, Iterable, NamedTuple

POOL_GAUGES = {
    "database_pool_acquired_connections",
    "database_pool_idle_connections",
    "database_pool_total_connections",
    "database_pool_max_connections",
    "http_requests_in_flight",
}
POOL_COUNTERS = {
    "database_pool_acquire_count_total",
    "database_pool_acquire_duration_seconds_total",
    "database_pool_empty_acquire_count_total",
    "database_pool_canceled_acquire_count_total",
}
HTTP_COUNTER = "http_requests_total"
HTTP_BUCKET = "http_request_duration_seconds_bucket"
HTTP_COUNT = "http_request_duration_seconds_count"
PHASE_ORDER = {"before": 0, "during": 1, "after": 2}
STRICT_REQUEST_COUNT_TOLERANCE = 0

METRIC_RE = re.compile(
    r'^([a-zA-Z_:][a-zA-Z0-9_:]*)(?:\{([^}]*)\})?\s+([-+a-zA-Z0-9.eE]+)$'
)
LABEL_RE = re.compile(r'([a-zA-Z_][a-zA-Z0-9_]*)="([^"]*)"')
SNAPSHOT_RE = re.compile(
    r"^metrics-(before|during|after)-([0-9]{4})-(identity|organization)-([a-z0-9]{6,24})\.prom$"
)
REPLICA_KEY_RE = re.compile(r"^[a-f0-9]{12}$")
FORBIDDEN = [
    re.compile(r"whsec_", re.IGNORECASE),
    re.compile(r"postgres(?:ql)?://", re.IGNORECASE),
    re.compile(r"\beyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\b"),
    re.compile(r"[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}"),
    re.compile(r"\b(?:user|org|mem|email|sess|msg)_load_[A-Za-z0-9_-]+\b"),
]


class MetricSample(NamedTuple):
    name: str
    labels: dict[str, str]
    value: float


class HTTPTarget(NamedTuple):
    file_service: str
    service: str
    route: str
    method: str


@dataclass(frozen=True)
class SnapshotIdentity:
    phase: str
    sample: int
    file_service: str
    replica_key: str


@dataclass(frozen=True)
class Snapshot:
    identity: SnapshotIdentity
    path: Path
    metrics: tuple[MetricSample, ...]


HTTP_TARGETS = {
    "identity_me": HTTPTarget("identity", "identity-service", "/me", "GET"),
    "organization_current": HTTPTarget(
        "organization", "organization-service", "/organizations/current", "GET"
    ),
    "organization_membership": HTTPTarget(
        "organization",
        "organization-service",
        "/organizations/current/membership",
        "GET",
    ),
    "organization_dependency": HTTPTarget(
        "organization", "organization-service", "/organizations/current", "GET"
    ),
    "identity_user_unique": HTTPTarget(
        "identity", "identity-service", "/webhooks/clerk", "POST"
    ),
    "identity_user_retry": HTTPTarget(
        "identity", "identity-service", "/webhooks/clerk", "POST"
    ),
    "organization_unique": HTTPTarget(
        "organization", "organization-service", "/webhooks/clerk", "POST"
    ),
    "organization_retry": HTTPTarget(
        "organization", "organization-service", "/webhooks/clerk", "POST"
    ),
    "membership_unique": HTTPTarget(
        "organization", "organization-service", "/webhooks/clerk", "POST"
    ),
    "membership_retry": HTTPTarget(
        "organization", "organization-service", "/webhooks/clerk", "POST"
    ),
}


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description="Build sanitized BridgeWorks load-test artifacts")
    parser.add_argument("--k6-summary", required=True)
    parser.add_argument("--metrics-dir", required=True)
    parser.add_argument("--output-json", required=True)
    parser.add_argument("--output-markdown", required=True)
    parser.add_argument("--git-sha", required=True)
    parser.add_argument("--profile", required=True)
    parser.add_argument("--scenario", required=True)
    parser.add_argument("--identity-replicas", type=int, required=True)
    parser.add_argument("--organization-replicas", type=int, required=True)
    parser.add_argument("--identity-max-conns", type=int, required=True)
    parser.add_argument("--identity-min-conns", type=int, required=True)
    parser.add_argument("--organization-max-conns", type=int, required=True)
    parser.add_argument("--organization-min-conns", type=int, required=True)
    parser.add_argument("--experimental-pool-override", action="store_true")
    parser.add_argument("--disruption-metadata")
    parser.add_argument("--environment", default="local-docker")
    parser.add_argument("--limitation", action="append", default=[])
    return parser.parse_args()


def parse_metric_samples(path: Path) -> tuple[MetricSample, ...]:
    samples: list[MetricSample] = []
    if not path.exists():
        return tuple(samples)
    for raw_line in path.read_text(encoding="utf-8").splitlines():
        line = raw_line.strip()
        if not line or line.startswith("#"):
            continue
        match = METRIC_RE.match(line)
        if not match:
            continue
        name, labels_raw, value_raw = match.groups()
        try:
            value = float(value_raw)
        except ValueError:
            continue
        if not math.isfinite(value):
            continue
        samples.append(MetricSample(name, dict(LABEL_RE.findall(labels_raw or "")), value))
    return tuple(samples)


def parse_snapshot_identity(path: Path) -> SnapshotIdentity | None:
    match = SNAPSHOT_RE.match(path.name)
    if match is None:
        return None
    phase, sample_raw, file_service, replica_key = match.groups()
    return SnapshotIdentity(
        phase=phase,
        sample=int(sample_raw),
        file_service=file_service,
        replica_key=replica_key,
    )


def load_snapshots(metrics_dir: Path, file_service: str | None = None) -> list[Snapshot]:
    snapshots: list[Snapshot] = []
    for path in metrics_dir.glob("metrics-*.prom"):
        identity = parse_snapshot_identity(path)
        if identity is None:
            continue
        if file_service is not None and identity.file_service != file_service:
            continue
        snapshots.append(Snapshot(identity, path, parse_metric_samples(path)))
    snapshots.sort(
        key=lambda snapshot: (
            PHASE_ORDER[snapshot.identity.phase],
            snapshot.identity.sample,
            snapshot.identity.file_service,
            snapshot.identity.replica_key,
        )
    )
    return snapshots


def snapshots_by_replica(
    metrics_dir: Path, file_service: str
) -> dict[str, list[Snapshot]]:
    result: dict[str, list[Snapshot]] = defaultdict(list)
    for snapshot in load_snapshots(metrics_dir, file_service):
        result[snapshot.identity.replica_key].append(snapshot)
    return dict(result)


def counter_increase(values: Iterable[float], starts_from_zero: bool) -> float:
    ordered = list(values)
    if not ordered:
        return 0.0
    increase = ordered[0] if starts_from_zero else 0.0
    previous = ordered[0]
    for current in ordered[1:]:
        increase += current if current < previous else current - previous
        previous = current
    return max(increase, 0.0)


def counter_series_increase(
    snapshots: list[Snapshot],
    extractor: Callable[[Snapshot], dict[Any, float]],
) -> dict[Any, float]:
    observations: dict[Any, list[tuple[str, float]]] = defaultdict(list)
    for snapshot in snapshots:
        for key, value in extractor(snapshot).items():
            observations[key].append((snapshot.identity.phase, value))

    increases: dict[Any, float] = {}
    for key, values in observations.items():
        baseline_present = any(phase == "before" for phase, _ in values)
        increases[key] = counter_increase(
            (value for _, value in values), starts_from_zero=not baseline_present
        )
    return increases


def labels_match(sample: MetricSample, target: HTTPTarget) -> bool:
    labels = sample.labels
    return (
        labels.get("service") == target.service
        and labels.get("route") == target.route
        and labels.get("method") == target.method
    )


def http_counter_values(snapshot: Snapshot, target: HTTPTarget) -> dict[tuple[str, str], float]:
    values: dict[tuple[str, str], float] = {}
    for sample in snapshot.metrics:
        if not labels_match(sample, target):
            continue
        if sample.name == HTTP_COUNTER:
            status_class = sample.labels.get("status_class")
            if status_class:
                values[(HTTP_COUNTER, status_class)] = sample.value
        elif sample.name == HTTP_BUCKET:
            upper = sample.labels.get("le")
            if upper is not None:
                values[(HTTP_BUCKET, upper)] = sample.value
        elif sample.name == HTTP_COUNT:
            values[(HTTP_COUNT, "count")] = sample.value
    return values


def parse_upper_bound(raw: str) -> float | None:
    if raw == "+Inf":
        return math.inf
    try:
        value = float(raw)
    except ValueError:
        return None
    return value if math.isfinite(value) else None


def histogram_quantile(quantile: float, buckets: dict[float, float]) -> float:
    if not buckets:
        return 0.0
    ordered = sorted(buckets.items(), key=lambda item: item[0])
    total = buckets.get(math.inf, ordered[-1][1])
    if total <= 0:
        return 0.0
    rank = quantile * total
    previous_upper = 0.0
    previous_count = 0.0
    for upper, count in ordered:
        if count < rank:
            if math.isfinite(upper):
                previous_upper = upper
            previous_count = count
            continue
        if math.isinf(upper):
            return previous_upper
        bucket_count = count - previous_count
        if bucket_count <= 0:
            return upper
        fraction = (rank - previous_count) / bucket_count
        return previous_upper + (upper - previous_upper) * fraction
    return previous_upper


def service_http_telemetry(
    metrics_dir: Path,
    scenario: str,
    duration_seconds: float,
) -> dict[str, Any]:
    target = HTTP_TARGETS.get(scenario)
    if target is None:
        return {"telemetry_complete": False, "reason": "unsupported_scenario"}

    replica_series = snapshots_by_replica(metrics_dir, target.file_service)
    status_totals: dict[str, float] = defaultdict(float)
    bucket_totals: dict[float, float] = defaultdict(float)
    histogram_count = 0.0
    replica_request_increase: dict[str, int] = {}

    for replica_key, snapshots in sorted(replica_series.items()):
        increases = counter_series_increase(
            snapshots, lambda snapshot: http_counter_values(snapshot, target)
        )
        replica_requests = 0.0
        for (metric_name, dimension), value in increases.items():
            if metric_name == HTTP_COUNTER:
                status_totals[dimension] += value
                replica_requests += value
            elif metric_name == HTTP_BUCKET:
                upper = parse_upper_bound(dimension)
                if upper is not None:
                    bucket_totals[upper] += value
            elif metric_name == HTTP_COUNT:
                histogram_count += value
        if replica_requests > 0:
            replica_request_increase[replica_key] = int(round(replica_requests))

    status_distribution = {
        name: int(round(count))
        for name, count in sorted(status_totals.items())
        if count > 0
    }
    request_count = sum(status_distribution.values())
    histogram_count_int = int(round(histogram_count))
    errors = sum(
        count
        for status_class, count in status_distribution.items()
        if status_class in {"4xx", "5xx"}
    )
    telemetry_complete = (
        histogram_count_int == request_count
        and (request_count == 0 or bool(bucket_totals))
    )
    return {
        "telemetry_complete": telemetry_complete,
        "service": target.service,
        "route": target.route,
        "method": target.method,
        "replica_series_count": len(replica_series),
        "replica_request_increase": replica_request_increase,
        "request_count": request_count,
        "histogram_count": histogram_count_int,
        "throughput_requests_per_second": round(
            request_count / duration_seconds if duration_seconds > 0 else 0.0, 6
        ),
        "latency_ms": {
            "p50": round(histogram_quantile(0.50, dict(bucket_totals)) * 1000.0, 6),
            "p95": round(histogram_quantile(0.95, dict(bucket_totals)) * 1000.0, 6),
            "p99": round(histogram_quantile(0.99, dict(bucket_totals)) * 1000.0, 6),
        },
        "error_rate": round(errors / request_count if request_count > 0 else 0.0, 8),
        "status_class_distribution": status_distribution,
    }


def pool_counter_values(snapshot: Snapshot) -> dict[str, float]:
    values: dict[str, float] = {}
    for sample in snapshot.metrics:
        if sample.name not in POOL_COUNTERS:
            continue
        if sample.labels.get("pool") != "runtime":
            continue
        values[sample.name] = sample.value
    return values


def pool_counter_deltas(metrics_dir: Path, file_service: str) -> dict[str, float]:
    totals: dict[str, float] = defaultdict(float)
    for snapshots in snapshots_by_replica(metrics_dir, file_service).values():
        for name, increase in counter_series_increase(snapshots, pool_counter_values).items():
            totals[name] += increase
    return {name: round(totals.get(name, 0.0), 9) for name in sorted(POOL_COUNTERS)}


def pool_gauge_values(snapshot: Snapshot) -> dict[str, float]:
    values: dict[str, float] = {}
    for sample in snapshot.metrics:
        if sample.name not in POOL_GAUGES:
            continue
        if sample.name.startswith("database_pool_") and sample.labels.get("pool") != "runtime":
            continue
        values[sample.name] = sample.value
    return values


def aggregate_gauge_phase(
    metrics_dir: Path, phase: str, file_service: str
) -> dict[str, Any]:
    snapshots = [
        snapshot
        for snapshot in load_snapshots(metrics_dir, file_service)
        if snapshot.identity.phase == phase
    ]
    by_sample: dict[int, list[Snapshot]] = defaultdict(list)
    for snapshot in snapshots:
        by_sample[snapshot.identity.sample].append(snapshot)

    combined: list[dict[str, float]] = []
    for sample in sorted(by_sample):
        summed: dict[str, float] = defaultdict(float)
        for snapshot in by_sample[sample]:
            for name, value in pool_gauge_values(snapshot).items():
                summed[name] += value
        combined.append(dict(summed))

    if not combined:
        return {"sample_count": 0, "replica_snapshot_count": 0, "values": {}}
    if phase != "during":
        return {
            "sample_count": len(combined),
            "replica_snapshot_count": len(snapshots),
            "values": combined[-1],
        }

    maxima: dict[str, float] = {}
    for sample in combined:
        for name, value in sample.items():
            maxima[name] = max(maxima.get(name, value), value)
    return {
        "sample_count": len(combined),
        "replica_snapshot_count": len(snapshots),
        "max": maxima,
        "last": combined[-1],
    }


def pool_metrics(metrics_dir: Path) -> dict[str, Any]:
    services = {
        "identity-service": "identity",
        "organization-service": "organization",
    }
    gauges: dict[str, Any] = {}
    for phase in ("before", "during", "after"):
        gauges[phase] = {
            service: aggregate_gauge_phase(metrics_dir, phase, file_service)
            for service, file_service in services.items()
        }
    return {
        "gauges": gauges,
        "counter_deltas": {
            service: pool_counter_deltas(metrics_dir, file_service)
            for service, file_service in services.items()
        },
    }


def threshold_passed(thresholds: dict[str, bool]) -> bool:
    return bool(thresholds) and all(thresholds.values())


def safe_number(value: Any, default: float = 0.0) -> float:
    try:
        parsed = float(value)
    except (TypeError, ValueError):
        return default
    return parsed if math.isfinite(parsed) else default


def load_disruption_metadata(path: str | None) -> dict[str, Any]:
    if not path:
        return {"mode": "none"}
    metadata_path = Path(path)
    if not metadata_path.exists():
        return {"mode": "none"}
    value = json.loads(metadata_path.read_text(encoding="utf-8"))
    if not isinstance(value, dict):
        raise ValueError("disruption metadata must be an object")
    allowed_keys = {
        "mode",
        "target_service",
        "restarted_replica_key",
        "non_target_replica_keys",
        "non_target_remained_running",
        "target_healthy_after_restart",
        "expected_replica_count_restored",
        "traffic_active_during_restart",
    }
    if set(value) - allowed_keys:
        raise ValueError("disruption metadata contains unsupported fields")
    replica_key = value.get("restarted_replica_key")
    if replica_key is not None and not REPLICA_KEY_RE.fullmatch(str(replica_key)):
        raise ValueError("disruption metadata contains an invalid replica key")
    non_targets = value.get("non_target_replica_keys", [])
    if not isinstance(non_targets, list) or any(
        not REPLICA_KEY_RE.fullmatch(str(item)) for item in non_targets
    ):
        raise ValueError("disruption metadata contains invalid non-target replica keys")
    return value


def request_reconciliation(
    profile: str,
    client_request_count: int,
    service_request_count: int,
    service_histogram_count: int,
) -> dict[str, Any]:
    gateway_only = max(client_request_count - service_request_count, 0)
    histogram_matches = service_histogram_count == service_request_count
    service_not_above_client = service_request_count <= client_request_count
    if profile == "dependency-degradation":
        complete = histogram_matches and service_not_above_client
        tolerance = None
        policy = "gateway-only gaps allowed during explicit disruption"
    else:
        complete = (
            histogram_matches
            and abs(client_request_count - service_request_count)
            <= STRICT_REQUEST_COUNT_TOLERANCE
        )
        tolerance = STRICT_REQUEST_COUNT_TOLERANCE
        policy = "exact client/service match required"
    return {
        "gateway_or_transport_only_failures": gateway_only,
        "request_count_reconciliation_complete": complete,
        "request_count_tolerance": tolerance,
        "request_count_reconciliation_policy": policy,
    }


def build_report(args: argparse.Namespace) -> dict[str, Any]:
    summary = json.loads(Path(args.k6_summary).read_text(encoding="utf-8"))
    duration_seconds = safe_number(summary.get("duration_ms")) / 1000.0
    client_request_count = int(safe_number(summary.get("request_count")))
    throughput = client_request_count / duration_seconds if duration_seconds > 0 else 0.0
    thresholds = {str(key): bool(value) for key, value in summary.get("thresholds", {}).items()}
    limitations = args.limitation or [
        "GitHub-hosted and local Docker results are harness baselines, not production capacity claims.",
        "Shared runner CPU, storage, and network scheduling can vary between runs.",
        "Representative production pool sizing requires deployment-environment measurements.",
    ]
    service_http = service_http_telemetry(
        Path(args.metrics_dir), args.scenario, duration_seconds
    )
    service_request_count = int(service_http.get("request_count", 0))
    service_histogram_count = int(service_http.get("histogram_count", 0))
    reconciliation = request_reconciliation(
        args.profile,
        client_request_count,
        service_request_count,
        service_histogram_count,
    )
    disruption = load_disruption_metadata(getattr(args, "disruption_metadata", None))

    report = {
        "schema_version": 3,
        "git_sha": args.git_sha,
        "scenario": args.scenario,
        "load_profile": args.profile,
        "environment": args.environment,
        "service_replica_count": {
            "identity-service": args.identity_replicas,
            "organization-service": args.organization_replicas,
        },
        "database_pool_configuration": {
            "experimental_override": args.experimental_pool_override,
            "identity-service": {
                "max_connections": args.identity_max_conns,
                "min_connections": args.identity_min_conns,
            },
            "organization-service": {
                "max_connections": args.organization_max_conns,
                "min_connections": args.organization_min_conns,
            },
        },
        "duration_seconds": round(duration_seconds, 6),
        "request_count": client_request_count,
        "client_request_count": client_request_count,
        "service_request_count": service_request_count,
        "service_histogram_count": service_histogram_count,
        "gateway_or_transport_only_failures": reconciliation[
            "gateway_or_transport_only_failures"
        ],
        "request_count_reconciliation_complete": reconciliation[
            "request_count_reconciliation_complete"
        ],
        "request_count_tolerance": reconciliation["request_count_tolerance"],
        "request_count_reconciliation_policy": reconciliation[
            "request_count_reconciliation_policy"
        ],
        "throughput_requests_per_second": round(throughput, 6),
        "latency_ms": {
            key: round(safe_number(value), 6)
            for key, value in summary.get("latency_ms", {}).items()
        },
        "error_rate": round(safe_number(summary.get("failed_rate")), 8),
        "checks_rate": round(safe_number(summary.get("checks_rate")), 8),
        "dropped_iterations": int(safe_number(summary.get("dropped_iterations"))),
        "status_distribution": summary.get("status_distribution", {}),
        "service_http_telemetry": service_http,
        "pool_metrics": pool_metrics(Path(args.metrics_dir)),
        "disruption": disruption,
        "thresholds": thresholds,
        "passed": (
            threshold_passed(thresholds)
            and service_http.get("telemetry_complete") is True
            and reconciliation["request_count_reconciliation_complete"] is True
        ),
        "test_environment_limitations": limitations,
    }
    validate_sanitized(report)
    return report


def validate_sanitized(report: dict[str, Any]) -> None:
    serialized = json.dumps(report, sort_keys=True)
    for pattern in FORBIDDEN:
        if pattern.search(serialized):
            raise ValueError("load-test artifact contains forbidden sensitive data")


def markdown_table(rows: list[tuple[str, Any]]) -> str:
    body = ["| Metric | Value |", "|---|---:|"]
    body.extend(f"| {name} | {value} |" for name, value in rows)
    return "\n".join(body)


def pool_markdown(report: dict[str, Any]) -> str:
    lines = [
        "| Phase | Service | Samples | Acquired | Idle | Total | Max | In flight |",
        "|---|---|---:|---:|---:|---:|---:|---:|",
    ]
    gauges = report["pool_metrics"]["gauges"]
    for phase in ("before", "during", "after"):
        for service in ("identity-service", "organization-service"):
            phase_data = gauges[phase][service]
            values = phase_data.get("max") if phase == "during" else phase_data.get("values")
            values = values or {}
            lines.append(
                "| {phase} | {service} | {samples} | {acquired} | {idle} | {total} | {max_conn} | {in_flight} |".format(
                    phase=phase,
                    service=service,
                    samples=phase_data.get("sample_count", 0),
                    acquired=values.get("database_pool_acquired_connections", 0),
                    idle=values.get("database_pool_idle_connections", 0),
                    total=values.get("database_pool_total_connections", 0),
                    max_conn=values.get("database_pool_max_connections", 0),
                    in_flight=values.get("http_requests_in_flight", 0),
                )
            )

    lines.extend(
        [
            "",
            "| Service | Acquire count delta | Acquire wait seconds delta | Empty acquire delta | Canceled acquire delta |",
            "|---|---:|---:|---:|---:|",
        ]
    )
    for service in ("identity-service", "organization-service"):
        counters = report["pool_metrics"]["counter_deltas"][service]
        lines.append(
            "| {service} | {acquire} | {wait} | {empty} | {canceled} |".format(
                service=service,
                acquire=counters.get("database_pool_acquire_count_total", 0),
                wait=counters.get("database_pool_acquire_duration_seconds_total", 0),
                empty=counters.get("database_pool_empty_acquire_count_total", 0),
                canceled=counters.get("database_pool_canceled_acquire_count_total", 0),
            )
        )
    return "\n".join(lines)


def render_markdown(report: dict[str, Any]) -> str:
    latency = report["latency_ms"]
    status_rows = "\n".join(
        f"- `{status}`: {count}"
        for status, count in sorted(report["status_distribution"].items())
    ) or "- no status counters recorded"
    threshold_rows = "\n".join(
        f"- `{'PASS' if passed else 'FAIL'}` `{name}`"
        for name, passed in sorted(report["thresholds"].items())
    ) or "- no thresholds recorded"
    limitations = "\n".join(
        f"- {item}" for item in report["test_environment_limitations"]
    )
    pool = report["database_pool_configuration"]
    service = report["service_http_telemetry"]
    service_status = ", ".join(
        f"{name}={count}"
        for name, count in sorted(service.get("status_class_distribution", {}).items())
    ) or "none"
    disruption = report.get("disruption", {"mode": "none"})
    disruption_rows = "\n".join(
        f"- {key}: `{value}`" for key, value in sorted(disruption.items())
    )
    return f"""# BridgeWorks Load-Test Result

- Git SHA: `{report['git_sha']}`
- Scenario: `{report['scenario']}`
- Profile: `{report['load_profile']}`
- Environment: `{report['environment']}`
- Result: `{'PASS' if report['passed'] else 'FAIL'}`
- Experimental pool override: `{str(pool['experimental_override']).lower()}`

## Client-observed request measurements

{markdown_table([
    ('Duration seconds', report['duration_seconds']),
    ('Client request count', report['client_request_count']),
    ('Throughput requests/second', report['throughput_requests_per_second']),
    ('Error rate', report['error_rate']),
    ('Checks rate', report['checks_rate']),
    ('Dropped iterations', report['dropped_iterations']),
    ('p50 latency ms', latency.get('p50', 0)),
    ('p95 latency ms', latency.get('p95', 0)),
    ('p99 latency ms', latency.get('p99', 0)),
    ('Max latency ms', latency.get('max', 0)),
])}

## Client/service reconciliation

{markdown_table([
    ('Client request count', report['client_request_count']),
    ('Service request count', report['service_request_count']),
    ('Service histogram count', report['service_histogram_count']),
    ('Gateway or transport-only failures', report['gateway_or_transport_only_failures']),
    ('Reconciliation complete', str(report['request_count_reconciliation_complete']).lower()),
    ('Strict normal-profile tolerance', report['request_count_tolerance']),
])}

Policy: {report['request_count_reconciliation_policy']}.

## Service-side PR #8 HTTP telemetry

- Service: `{service.get('service', 'unknown')}`
- Route: `{service.get('route', 'unknown')}`
- Method: `{service.get('method', 'unknown')}`
- Complete: `{str(service.get('telemetry_complete', False)).lower()}`
- Replica series: `{service.get('replica_series_count', 0)}`
- Per-replica request increases: `{json.dumps(service.get('replica_request_increase', {}), sort_keys=True)}`
- Status classes: `{service_status}`

{markdown_table([
    ('Request count delta', service.get('request_count', 0)),
    ('Histogram count delta', service.get('histogram_count', 0)),
    ('Throughput requests/second', service.get('throughput_requests_per_second', 0)),
    ('Error rate', service.get('error_rate', 0)),
    ('p50 latency ms', service.get('latency_ms', {}).get('p50', 0)),
    ('p95 latency ms', service.get('latency_ms', {}).get('p95', 0)),
    ('p99 latency ms', service.get('latency_ms', {}).get('p99', 0)),
])}

## Client status distribution

{status_rows}

## Service replicas and pool configuration

- Identity replicas: `{report['service_replica_count']['identity-service']}`; pool min/max: `{pool['identity-service']['min_connections']}/{pool['identity-service']['max_connections']}`
- Organization replicas: `{report['service_replica_count']['organization-service']}`; pool min/max: `{pool['organization-service']['min_connections']}/{pool['organization-service']['max_connections']}`

## Pool and in-flight telemetry

{pool_markdown(report)}

Gauge values are summed across the replicas present in each scrape and the `during` phase reports the maximum summed value. Counter deltas are calculated independently for each stable replica key, including per-replica reset detection, and only then summed across replicas.

## Disruption metadata

{disruption_rows}

## Thresholds

{threshold_rows}

## Environment limitations

{limitations}
"""


def main() -> None:
    args = parse_args()
    report = build_report(args)
    output_json = Path(args.output_json)
    output_markdown = Path(args.output_markdown)
    output_json.parent.mkdir(parents=True, exist_ok=True)
    output_markdown.parent.mkdir(parents=True, exist_ok=True)
    output_json.write_text(
        json.dumps(report, indent=2, sort_keys=True) + "\n", encoding="utf-8"
    )
    output_markdown.write_text(render_markdown(report), encoding="utf-8")


if __name__ == "__main__":
    main()
