from __future__ import annotations

import argparse
import importlib.util
import json
import sys
import tempfile
import unittest
from pathlib import Path

MODULE_PATH = Path(__file__).resolve().parents[1] / "report.py"
SPEC = importlib.util.spec_from_file_location("report", MODULE_PATH)
assert SPEC and SPEC.loader
report = importlib.util.module_from_spec(SPEC)
sys.modules[SPEC.name] = report
SPEC.loader.exec_module(report)


class SnapshotWriter:
    def __init__(self, root: Path) -> None:
        self.root = root

    def write(
        self,
        phase: str,
        sample: int,
        service: str,
        replica: str,
        *,
        requests: int | None = None,
        status: str = "2xx",
        histogram_count: int | None = None,
        bucket_small: int | None = None,
        bucket_large: int | None = None,
        pool_counters: dict[str, float] | None = None,
        gauges: dict[str, float] | None = None,
    ) -> None:
        service_name = "identity-service" if service == "identity" else "organization-service"
        route = "/me" if service == "identity" else "/organizations/current"
        lines: list[str] = []
        if requests is not None:
            lines.append(
                f'http_requests_total{{method="GET",route="{route}",service="{service_name}",status_class="{status}"}} {requests}'
            )
            count = requests if histogram_count is None else histogram_count
            small = count // 2 if bucket_small is None else bucket_small
            large = count if bucket_large is None else bucket_large
            lines.extend(
                [
                    f'http_request_duration_seconds_count{{method="GET",route="{route}",service="{service_name}"}} {count}',
                    f'http_request_duration_seconds_bucket{{le="0.01",method="GET",route="{route}",service="{service_name}"}} {small}',
                    f'http_request_duration_seconds_bucket{{le="0.1",method="GET",route="{route}",service="{service_name}"}} {large}',
                    f'http_request_duration_seconds_bucket{{le="+Inf",method="GET",route="{route}",service="{service_name}"}} {count}',
                ]
            )
        for name, value in (pool_counters or {}).items():
            lines.append(f'{name}{{pool="runtime",service="{service_name}"}} {value}')
        for name, value in (gauges or {}).items():
            if name == "http_requests_in_flight":
                lines.append(f'{name}{{service="{service_name}"}} {value}')
            else:
                lines.append(f'{name}{{pool="runtime",service="{service_name}"}} {value}')
        path = self.root / f"metrics-{phase}-{sample:04d}-{service}-{replica}.prom"
        path.write_text("\n".join(lines) + "\n", encoding="utf-8")


class PerReplicaCounterTests(unittest.TestCase):
    def telemetry(self, write) -> dict:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            writer = SnapshotWriter(root)
            write(writer)
            return report.service_http_telemetry(root, "identity_me", 10.0)

    def test_two_replicas_increment_normally(self) -> None:
        result = self.telemetry(
            lambda w: (
                w.write("before", 0, "identity", "aaaaaaaaaaaa", requests=100),
                w.write("after", 0, "identity", "aaaaaaaaaaaa", requests=110),
                w.write("before", 0, "identity", "bbbbbbbbbbbb", requests=200),
                w.write("after", 0, "identity", "bbbbbbbbbbbb", requests=205),
            )
        )
        self.assertEqual(result["request_count"], 15)
        self.assertEqual(result["histogram_count"], 15)

    def test_one_replica_resets_without_aggregate_overcount(self) -> None:
        result = self.telemetry(
            lambda w: (
                w.write("before", 0, "identity", "aaaaaaaaaaaa", requests=100),
                w.write("after", 0, "identity", "aaaaaaaaaaaa", requests=110),
                w.write("before", 0, "identity", "bbbbbbbbbbbb", requests=100),
                w.write("during", 1, "identity", "bbbbbbbbbbbb", requests=0),
                w.write("after", 0, "identity", "bbbbbbbbbbbb", requests=5),
            )
        )
        self.assertEqual(result["request_count"], 15)
        self.assertEqual(
            result["replica_request_increase"],
            {"aaaaaaaaaaaa": 10, "bbbbbbbbbbbb": 5},
        )

    def test_temporarily_missing_replica_is_not_zero_reset(self) -> None:
        result = self.telemetry(
            lambda w: (
                w.write("before", 0, "identity", "aaaaaaaaaaaa", requests=100),
                w.write("during", 1, "identity", "aaaaaaaaaaaa", requests=106),
                w.write("after", 0, "identity", "aaaaaaaaaaaa", requests=110),
                w.write("before", 0, "identity", "bbbbbbbbbbbb", requests=100),
                w.write("after", 0, "identity", "bbbbbbbbbbbb", requests=105),
            )
        )
        self.assertEqual(result["request_count"], 15)

    def test_replacement_container_starts_new_series_once(self) -> None:
        result = self.telemetry(
            lambda w: (
                w.write("before", 0, "identity", "aaaaaaaaaaaa", requests=100),
                w.write("after", 0, "identity", "aaaaaaaaaaaa", requests=110),
                w.write("before", 0, "identity", "bbbbbbbbbbbb", requests=100),
                w.write("during", 1, "identity", "cccccccccccc", requests=3),
                w.write("after", 0, "identity", "cccccccccccc", requests=5),
            )
        )
        self.assertEqual(result["request_count"], 15)
        self.assertEqual(result["replica_request_increase"]["cccccccccccc"], 5)

    def test_both_replicas_reset(self) -> None:
        result = self.telemetry(
            lambda w: (
                w.write("before", 0, "identity", "aaaaaaaaaaaa", requests=100),
                w.write("during", 1, "identity", "aaaaaaaaaaaa", requests=0),
                w.write("after", 0, "identity", "aaaaaaaaaaaa", requests=3),
                w.write("before", 0, "identity", "bbbbbbbbbbbb", requests=100),
                w.write("during", 1, "identity", "bbbbbbbbbbbb", requests=0),
                w.write("after", 0, "identity", "bbbbbbbbbbbb", requests=4),
            )
        )
        self.assertEqual(result["request_count"], 7)
        self.assertEqual(result["histogram_count"], 7)

    def test_histogram_buckets_and_count_reset_per_replica(self) -> None:
        result = self.telemetry(
            lambda w: (
                w.write(
                    "before", 0, "identity", "aaaaaaaaaaaa", requests=100,
                    histogram_count=100, bucket_small=50, bucket_large=100,
                ),
                w.write(
                    "after", 0, "identity", "aaaaaaaaaaaa", requests=110,
                    histogram_count=110, bucket_small=55, bucket_large=110,
                ),
                w.write(
                    "before", 0, "identity", "bbbbbbbbbbbb", requests=100,
                    histogram_count=100, bucket_small=50, bucket_large=100,
                ),
                w.write(
                    "during", 1, "identity", "bbbbbbbbbbbb", requests=0,
                    histogram_count=0, bucket_small=0, bucket_large=0,
                ),
                w.write(
                    "after", 0, "identity", "bbbbbbbbbbbb", requests=5,
                    histogram_count=5, bucket_small=2, bucket_large=5,
                ),
            )
        )
        self.assertEqual(result["request_count"], 15)
        self.assertEqual(result["histogram_count"], 15)
        self.assertGreater(result["latency_ms"]["p50"], 0)

    def test_pool_counters_reset_per_replica(self) -> None:
        names = {
            "database_pool_acquire_count_total": (100, 110, 100, 0, 5),
            "database_pool_acquire_duration_seconds_total": (10, 11, 10, 0, 0.5),
            "database_pool_empty_acquire_count_total": (20, 22, 20, 0, 1),
            "database_pool_canceled_acquire_count_total": (5, 6, 5, 0, 2),
        }
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            writer = SnapshotWriter(root)
            writer.write(
                "before", 0, "identity", "aaaaaaaaaaaa",
                pool_counters={name: values[0] for name, values in names.items()},
            )
            writer.write(
                "after", 0, "identity", "aaaaaaaaaaaa",
                pool_counters={name: values[1] for name, values in names.items()},
            )
            writer.write(
                "before", 0, "identity", "bbbbbbbbbbbb",
                pool_counters={name: values[2] for name, values in names.items()},
            )
            writer.write(
                "during", 1, "identity", "bbbbbbbbbbbb",
                pool_counters={name: values[3] for name, values in names.items()},
            )
            writer.write(
                "after", 0, "identity", "bbbbbbbbbbbb",
                pool_counters={name: values[4] for name, values in names.items()},
            )
            deltas = report.pool_counter_deltas(root, "identity")
        self.assertEqual(deltas["database_pool_acquire_count_total"], 15)
        self.assertEqual(deltas["database_pool_acquire_duration_seconds_total"], 1.5)
        self.assertEqual(deltas["database_pool_empty_acquire_count_total"], 3)
        self.assertEqual(deltas["database_pool_canceled_acquire_count_total"], 3)

    def test_gauge_sums_and_maxima_across_two_replicas(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            writer = SnapshotWriter(root)
            gauge = lambda acquired, idle, total, maximum, inflight: {
                "database_pool_acquired_connections": acquired,
                "database_pool_idle_connections": idle,
                "database_pool_total_connections": total,
                "database_pool_max_connections": maximum,
                "http_requests_in_flight": inflight,
            }
            writer.write("before", 0, "identity", "aaaaaaaaaaaa", gauges=gauge(1, 2, 3, 5, 2))
            writer.write("before", 0, "identity", "bbbbbbbbbbbb", gauges=gauge(2, 1, 3, 5, 3))
            writer.write("during", 1, "identity", "aaaaaaaaaaaa", gauges=gauge(3, 1, 4, 5, 4))
            writer.write("during", 1, "identity", "bbbbbbbbbbbb", gauges=gauge(4, 0, 4, 5, 5))
            writer.write("during", 2, "identity", "aaaaaaaaaaaa", gauges=gauge(2, 2, 4, 5, 2))
            writer.write("during", 2, "identity", "bbbbbbbbbbbb", gauges=gauge(3, 1, 4, 5, 3))
            before = report.aggregate_gauge_phase(root, "before", "identity")
            during = report.aggregate_gauge_phase(root, "during", "identity")
        self.assertEqual(before["values"]["database_pool_acquired_connections"], 3)
        self.assertEqual(before["values"]["database_pool_max_connections"], 10)
        self.assertEqual(during["max"]["database_pool_acquired_connections"], 7)
        self.assertEqual(during["max"]["http_requests_in_flight"], 9)


class ReportBuildTests(unittest.TestCase):
    def test_normal_profile_requires_exact_reconciliation(self) -> None:
        value = report.request_reconciliation("smoke", 10, 9, 9)
        self.assertFalse(value["request_count_reconciliation_complete"])
        self.assertEqual(value["gateway_or_transport_only_failures"], 1)
        self.assertEqual(value["request_count_tolerance"], 0)

    def test_disruption_profile_records_gateway_only_gap(self) -> None:
        value = report.request_reconciliation("dependency-degradation", 10, 7, 7)
        self.assertTrue(value["request_count_reconciliation_complete"])
        self.assertEqual(value["gateway_or_transport_only_failures"], 3)

    def test_builds_schema_three_report(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            metrics = root / "metrics"
            metrics.mkdir()
            writer = SnapshotWriter(metrics)
            writer.write("before", 0, "identity", "aaaaaaaaaaaa", requests=0)
            writer.write("after", 0, "identity", "aaaaaaaaaaaa", requests=20)
            summary = root / "summary.json"
            summary.write_text(
                json.dumps({
                    "duration_ms": 2000,
                    "request_count": 20,
                    "failed_rate": 0,
                    "checks_rate": 1,
                    "dropped_iterations": 0,
                    "latency_ms": {"p50": 10, "p95": 20, "p99": 30, "max": 40},
                    "status_distribution": {"200": 20},
                    "thresholds": {"checks:rate>0.90": True},
                }),
                encoding="utf-8",
            )
            disruption = root / "disruption.json"
            disruption.write_text('{"mode":"none"}\n', encoding="utf-8")
            args = argparse.Namespace(
                k6_summary=str(summary), metrics_dir=str(metrics),
                output_json=str(root / "result.json"), output_markdown=str(root / "result.md"),
                git_sha="abc123", profile="smoke", scenario="identity_me",
                identity_replicas=1, organization_replicas=1,
                identity_max_conns=5, identity_min_conns=0,
                organization_max_conns=5, organization_min_conns=0,
                experimental_pool_override=False, disruption_metadata=str(disruption),
                environment="test", limitation=[],
            )
            result = report.build_report(args)
        self.assertEqual(result["schema_version"], 3)
        self.assertTrue(result["passed"])
        self.assertEqual(result["client_request_count"], 20)
        self.assertEqual(result["service_request_count"], 20)
        self.assertEqual(result["service_histogram_count"], 20)
        self.assertTrue(result["request_count_reconciliation_complete"])

    def test_rejects_sensitive_values(self) -> None:
        with self.assertRaisesRegex(ValueError, "forbidden"):
            report.validate_sanitized({"token": "whsec_secret"})
        with self.assertRaisesRegex(ValueError, "forbidden"):
            report.validate_sanitized({"database": "postgres:" + "//user:pass@example/db"})


if __name__ == "__main__":
    unittest.main()
