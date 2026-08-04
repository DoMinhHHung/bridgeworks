#!/usr/bin/env python3
from __future__ import annotations

import argparse
import glob
import json
import math
import re
from pathlib import Path
from typing import Any, NamedTuple

POOL_METRICS = {
    "database_pool_acquired_connections",
    "database_pool_idle_connections",
    "database_pool_total_connections",
    "database_pool_max_connections",
    "database_pool_acquire_count_total",
    "database_pool_acquire_duration_seconds_total",
    "database_pool_empty_acquire_count_total",
    "database_pool_canceled_acquire_count_total",
    "http_requests_in_flight",
}
HTTP_COUNTER = "http_requests_total"
HTTP_BUCKET = "http_request_duration_seconds_bucket"
HTTP_COUNT = "http_request_duration_seconds_count"

METRIC_RE = re.compile(r'^([a-zA-Z_:][a-zA-Z0-9_:]*)(?:\{([^}]*)\})?\s+([-+a-zA-Z0-9.eE]+)$')
LABEL_RE = re.compile(r'([a-zA-Z_][a-zA-Z0-9_]*)="([^"]*)"')
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
    parser.add_argument("--environment", default="local-docker")
    parser.add_argument("--limitation", action="append", default=[])
    return parser.parse_args()


def parse_metric_samples(path: Path) -> list[MetricSample]:
    samples: list[MetricSample] = []
    if not path.exists():
        return samples
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
    return samples


def parse_prometheus(path: Path) -> dict[str, float]:
    values: dict[str, float] = {}
    for sample in parse_metric_samples(path):
        if sample.name not in POOL_METRICS:
            continue
        if sample.name.startswith("database_pool_") and sample.labels.get("pool") != "runtime":
            continue
        values[sample.name] = sample.value
    return values


def phase_files(metrics_dir: Path, phase: str, service: str) -> list[Path]:
    pattern = str(metrics_dir / f"metrics-{phase}-*-{service}.prom")
    return [Path(item) for item in sorted(glob.glob(pattern))]


def sum_snapshots(snapshots: list[dict[str, float]]) -> dict[str, float]:
    total: dict[str, float] = {}
    for snapshot in snapshots:
        for name, value in snapshot.items():
            total[name] = total.get(name, 0.0) + value
    return total


def sample_key(path: Path, phase: str) -> str:
    match = re.match(
        rf"metrics-{re.escape(phase)}-([^-]+)-\d+-(?:identity|organization)\.prom$",
        path.name,
    )
    return match.group(1) if match else path.name


def aggregate_phase(metrics_dir: Path, phase: str, service: str) -> dict[str, Any]:
    files = phase_files(metrics_dir, phase, service)
    groups: dict[str, list[dict[str, float]]] = {}
    for path in files:
        snapshot = parse_prometheus(path)
        if snapshot:
            groups.setdefault(sample_key(path, phase), []).append(snapshot)
    combined = [sum_snapshots(groups[key]) for key in sorted(groups)]
    if not combined:
        return {"sample_count": 0, "replica_snapshot_count": 0, "values": {}}
    if phase != "during":
        return {
            "sample_count": len(combined),
            "replica_snapshot_count": len(files),
            "values": combined[-1],
        }
    maxima: dict[str, float] = {}
    latest = combined[-1]
    for snapshot in combined:
        for name, value in snapshot.items():
            maxima[name] = max(maxima.get(name, value), value)
    return {
        "sample_count": len(combined),
        "replica_snapshot_count": len(files),
        "max": maxima,
        "last": latest,
    }


def pool_metrics(metrics_dir: Path) -> dict[str, Any]:
    result: dict[str, Any] = {}
    for phase in ("before", "during", "after"):
        result[phase] = {
            "identity-service": aggregate_phase(metrics_dir, phase, "identity"),
            "organization-service": aggregate_phase(metrics_dir, phase, "organization"),
        }
    return result


def http_snapshot(paths: list[Path], target: HTTPTarget) -> dict[str, Any]:
    statuses: dict[str, float] = {}
    buckets: dict[float, float] = {}
    count = 0.0
    for path in paths:
        for sample in parse_metric_samples(path):
            labels = sample.labels
            if (
                labels.get("service") != target.service
                or labels.get("route") != target.route
                or labels.get("method") != target.method
            ):
                continue
            if sample.name == HTTP_COUNTER:
                status_class = labels.get("status_class")
                if status_class:
                    statuses[status_class] = statuses.get(status_class, 0.0) + sample.value
            elif sample.name == HTTP_BUCKET:
                raw_upper = labels.get("le")
                if raw_upper is None:
                    continue
                try:
                    upper = math.inf if raw_upper == "+Inf" else float(raw_upper)
                except ValueError:
                    continue
                buckets[upper] = buckets.get(upper, 0.0) + sample.value
            elif sample.name == HTTP_COUNT:
                count += sample.value
    return {"statuses": statuses, "buckets": buckets, "count": count}


def http_series(metrics_dir: Path, target: HTTPTarget) -> list[dict[str, Any]]:
    series: list[dict[str, Any]] = []
    for phase in ("before", "during", "after"):
        files = phase_files(metrics_dir, phase, target.file_service)
        groups: dict[str, list[Path]] = {}
        for path in files:
            groups.setdefault(sample_key(path, phase), []).append(path)
        for key in sorted(groups):
            series.append(http_snapshot(groups[key], target))
    return series


def monotonic_increase(values: list[float]) -> float:
    if len(values) < 2:
        return 0.0
    increase = 0.0
    previous = values[0]
    for current in values[1:]:
        increase += current if current < previous else current - previous
        previous = current
    return max(increase, 0.0)


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
    metrics_dir: Path, scenario: str, duration_seconds: float
) -> dict[str, Any]:
    target = HTTP_TARGETS.get(scenario)
    if target is None:
        return {"telemetry_complete": False, "reason": "unsupported_scenario"}
    series = http_series(metrics_dir, target)
    status_names = sorted({name for snapshot in series for name in snapshot["statuses"]})
    status_distribution = {
        name: int(
            round(
                monotonic_increase(
                    [snapshot["statuses"].get(name, 0.0) for snapshot in series]
                )
            )
        )
        for name in status_names
    }
    status_distribution = {
        name: count for name, count in status_distribution.items() if count > 0
    }
    request_count = sum(status_distribution.values())

    bounds = sorted({bound for snapshot in series for bound in snapshot["buckets"]})
    bucket_increases = {
        bound: monotonic_increase(
            [snapshot["buckets"].get(bound, 0.0) for snapshot in series]
        )
        for bound in bounds
    }
    histogram_count = monotonic_increase([snapshot["count"] for snapshot in series])
    errors = sum(
        count
        for status_class, count in status_distribution.items()
        if status_class in {"4xx", "5xx"}
    )
    telemetry_complete = request_count > 0 and histogram_count > 0 and bool(bucket_increases)
    return {
        "telemetry_complete": telemetry_complete,
        "service": target.service,
        "route": target.route,
        "method": target.method,
        "sample_count": len(series),
        "request_count": request_count,
        "histogram_count": int(round(histogram_count)),
        "throughput_requests_per_second": round(
            request_count / duration_seconds if duration_seconds > 0 else 0.0, 6
        ),
        "latency_ms": {
            "p50": round(histogram_quantile(0.50, bucket_increases) * 1000.0, 6),
            "p95": round(histogram_quantile(0.95, bucket_increases) * 1000.0, 6),
            "p99": round(histogram_quantile(0.99, bucket_increases) * 1000.0, 6),
        },
        "error_rate": round(errors / request_count if request_count > 0 else 0.0, 8),
        "status_class_distribution": status_distribution,
    }


def threshold_passed(thresholds: dict[str, bool]) -> bool:
    return bool(thresholds) and all(thresholds.values())


def safe_number(value: Any, default: float = 0.0) -> float:
    try:
        parsed = float(value)
    except (TypeError, ValueError):
        return default
    return parsed if math.isfinite(parsed) else default


def build_report(args: argparse.Namespace) -> dict[str, Any]:
    summary = json.loads(Path(args.k6_summary).read_text(encoding="utf-8"))
    duration_seconds = safe_number(summary.get("duration_ms")) / 1000.0
    request_count = int(safe_number(summary.get("request_count")))
    throughput = request_count / duration_seconds if duration_seconds > 0 else 0.0
    thresholds = {str(key): bool(value) for key, value in summary.get("thresholds", {}).items()}
    limitations = args.limitation or [
        "GitHub-hosted and local Docker results are harness baselines, not production capacity claims.",
        "Shared runner CPU, storage, and network scheduling can vary between runs.",
        "Representative production pool sizing requires deployment-environment measurements.",
    ]
    server_http = service_http_telemetry(
        Path(args.metrics_dir), args.scenario, duration_seconds
    )
    report = {
        "schema_version": 2,
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
        "request_count": request_count,
        "throughput_requests_per_second": round(throughput, 6),
        "latency_ms": {
            key: round(safe_number(value), 6)
            for key, value in summary.get("latency_ms", {}).items()
        },
        "error_rate": round(safe_number(summary.get("failed_rate")), 8),
        "checks_rate": round(safe_number(summary.get("checks_rate")), 8),
        "dropped_iterations": int(safe_number(summary.get("dropped_iterations"))),
        "status_distribution": summary.get("status_distribution", {}),
        "service_http_telemetry": server_http,
        "pool_metrics": pool_metrics(Path(args.metrics_dir)),
        "thresholds": thresholds,
        "passed": threshold_passed(thresholds)
        and server_http.get("telemetry_complete") is True,
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
        "| Phase | Service | Samples | Acquired | Idle | Total | Max | Acquire count | Acquire wait seconds | Empty acquires | Canceled acquires | In flight |",
        "|---|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|",
    ]
    metrics = report["pool_metrics"]
    for phase in ("before", "during", "after"):
        for service in ("identity-service", "organization-service"):
            phase_data = metrics[phase][service]
            values = (
                phase_data.get("max")
                if phase == "during"
                else phase_data.get("values")
            )
            values = values or {}
            lines.append(
                "| {phase} | {service} | {samples} | {acquired} | {idle} | {total} | {max_conn} | {acquire_count} | {acquire_wait} | {empty} | {canceled} | {in_flight} |".format(
                    phase=phase,
                    service=service,
                    samples=phase_data.get("sample_count", 0),
                    acquired=values.get("database_pool_acquired_connections", 0),
                    idle=values.get("database_pool_idle_connections", 0),
                    total=values.get("database_pool_total_connections", 0),
                    max_conn=values.get("database_pool_max_connections", 0),
                    acquire_count=values.get("database_pool_acquire_count_total", 0),
                    acquire_wait=values.get(
                        "database_pool_acquire_duration_seconds_total", 0
                    ),
                    empty=values.get("database_pool_empty_acquire_count_total", 0),
                    canceled=values.get(
                        "database_pool_canceled_acquire_count_total", 0
                    ),
                    in_flight=values.get("http_requests_in_flight", 0),
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
    server = report["service_http_telemetry"]
    server_status = ", ".join(
        f"{name}={count}"
        for name, count in sorted(
            server.get("status_class_distribution", {}).items()
        )
    ) or "none"
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
    ('Request count', report['request_count']),
    ('Throughput requests/second', report['throughput_requests_per_second']),
    ('Error rate', report['error_rate']),
    ('Checks rate', report['checks_rate']),
    ('Dropped iterations', report['dropped_iterations']),
    ('p50 latency ms', latency.get('p50', 0)),
    ('p95 latency ms', latency.get('p95', 0)),
    ('p99 latency ms', latency.get('p99', 0)),
    ('Max latency ms', latency.get('max', 0)),
])}

## Service-side PR #8 HTTP telemetry

- Service: `{server.get('service', 'unknown')}`
- Route: `{server.get('route', 'unknown')}`
- Method: `{server.get('method', 'unknown')}`
- Complete: `{str(server.get('telemetry_complete', False)).lower()}`
- Status classes: `{server_status}`

{markdown_table([
    ('Request count delta', server.get('request_count', 0)),
    ('Histogram count delta', server.get('histogram_count', 0)),
    ('Throughput requests/second', server.get('throughput_requests_per_second', 0)),
    ('Error rate', server.get('error_rate', 0)),
    ('p50 latency ms', server.get('latency_ms', {}).get('p50', 0)),
    ('p95 latency ms', server.get('latency_ms', {}).get('p95', 0)),
    ('p99 latency ms', server.get('latency_ms', {}).get('p99', 0)),
])}

## Client status distribution

{status_rows}

## Service replicas and pool configuration

- Identity replicas: `{report['service_replica_count']['identity-service']}`; pool min/max: `{pool['identity-service']['min_connections']}/{pool['identity-service']['max_connections']}`
- Organization replicas: `{report['service_replica_count']['organization-service']}`; pool min/max: `{pool['organization-service']['min_connections']}/{pool['organization-service']['max_connections']}`

## Pool and in-flight telemetry

{pool_markdown(report)}

For the `during` phase, gauge values are maxima across samples; cumulative counters are the maximum observed cumulative values. Use before/after values to calculate run deltas.

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
