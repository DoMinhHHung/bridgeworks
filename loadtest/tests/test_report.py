from __future__ import annotations

import argparse
import importlib.util
import json
import tempfile
import unittest
from pathlib import Path

MODULE_PATH = Path(__file__).resolve().parents[1] / "report.py"
SPEC = importlib.util.spec_from_file_location("report", MODULE_PATH)
assert SPEC and SPEC.loader
report = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(report)


class ReportTests(unittest.TestCase):
    def test_builds_sanitized_report_with_http_and_pool_phases(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            summary = root / "k6-summary.json"
            summary.write_text(
                json.dumps(
                    {
                        "duration_ms": 2000,
                        "request_count": 20,
                        "failed_rate": 0,
                        "checks_rate": 1,
                        "dropped_iterations": 0,
                        "latency_ms": {"p50": 10, "p95": 20, "p99": 30, "max": 40},
                        "status_distribution": {"200": 20},
                        "thresholds": {"checks:rate>0.90": True},
                    }
                ),
                encoding="utf-8",
            )
            metrics = root / "metrics"
            metrics.mkdir()

            def fixture(requests: int, acquired: int) -> str:
                return (
                    f'http_requests_total{{method="GET",route="/me",service="identity-service",status_class="2xx"}} {requests}\n'
                    f'http_request_duration_seconds_count{{method="GET",route="/me",service="identity-service"}} {requests}\n'
                    f'http_request_duration_seconds_bucket{{le="0.005",method="GET",route="/me",service="identity-service"}} {requests // 4}\n'
                    f'http_request_duration_seconds_bucket{{le="0.01",method="GET",route="/me",service="identity-service"}} {requests // 2}\n'
                    f'http_request_duration_seconds_bucket{{le="0.025",method="GET",route="/me",service="identity-service"}} {requests}\n'
                    f'http_request_duration_seconds_bucket{{le="+Inf",method="GET",route="/me",service="identity-service"}} {requests}\n'
                    f'database_pool_acquired_connections{{pool="runtime",service="identity-service"}} {acquired}\n'
                    'database_pool_idle_connections{pool="runtime",service="identity-service"} 2\n'
                    'database_pool_total_connections{pool="runtime",service="identity-service"} 3\n'
                    'database_pool_max_connections{pool="runtime",service="identity-service"} 5\n'
                    f'database_pool_acquire_count_total{{pool="runtime",service="identity-service"}} {requests + 7}\n'
                    'database_pool_acquire_duration_seconds_total{pool="runtime",service="identity-service"} 0.5\n'
                    'database_pool_empty_acquire_count_total{pool="runtime",service="identity-service"} 1\n'
                    'database_pool_canceled_acquire_count_total{pool="runtime",service="identity-service"} 0\n'
                    'http_requests_in_flight{service="identity-service"} 2\n'
                )

            (metrics / "metrics-before-0000-00-identity.prom").write_text(
                fixture(0, 0), encoding="utf-8"
            )
            (metrics / "metrics-during-0001-00-identity.prom").write_text(
                fixture(10, 1), encoding="utf-8"
            )
            (metrics / "metrics-after-0000-00-identity.prom").write_text(
                fixture(20, 0), encoding="utf-8"
            )

            args = argparse.Namespace(
                k6_summary=str(summary),
                metrics_dir=str(metrics),
                output_json=str(root / "result.json"),
                output_markdown=str(root / "result.md"),
                git_sha="abc123",
                profile="smoke",
                scenario="identity_me",
                identity_replicas=1,
                organization_replicas=1,
                identity_max_conns=5,
                identity_min_conns=0,
                organization_max_conns=5,
                organization_min_conns=0,
                experimental_pool_override=False,
                environment="test",
                limitation=[],
            )
            result = report.build_report(args)
            self.assertEqual(result["throughput_requests_per_second"], 10)
            self.assertTrue(result["passed"])
            self.assertEqual(result["schema_version"], 2)
            self.assertEqual(result["service_http_telemetry"]["request_count"], 20)
            self.assertEqual(
                result["service_http_telemetry"]["status_class_distribution"],
                {"2xx": 20},
            )
            self.assertGreater(result["service_http_telemetry"]["latency_ms"]["p50"], 0)
            self.assertEqual(
                result["pool_metrics"]["during"]["identity-service"]["max"]["database_pool_total_connections"],
                3,
            )

    def test_sums_replica_snapshots_and_maximizes_during_samples(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            metrics = Path(temporary)
            first = 'database_pool_total_connections{pool="runtime",service="identity-service"} 2\n'
            second = 'database_pool_total_connections{pool="runtime",service="identity-service"} 3\n'
            later = 'database_pool_total_connections{pool="runtime",service="identity-service"} 4\n'
            (metrics / "metrics-before-0000-00-identity.prom").write_text(first, encoding="utf-8")
            (metrics / "metrics-before-0000-01-identity.prom").write_text(second, encoding="utf-8")
            (metrics / "metrics-during-0001-00-identity.prom").write_text(first, encoding="utf-8")
            (metrics / "metrics-during-0001-01-identity.prom").write_text(second, encoding="utf-8")
            (metrics / "metrics-during-0002-00-identity.prom").write_text(later, encoding="utf-8")
            (metrics / "metrics-during-0002-01-identity.prom").write_text(second, encoding="utf-8")
            before = report.aggregate_phase(metrics, "before", "identity")
            during = report.aggregate_phase(metrics, "during", "identity")
            self.assertEqual(before["values"]["database_pool_total_connections"], 5)
            self.assertEqual(during["max"]["database_pool_total_connections"], 7)
            self.assertEqual(during["sample_count"], 2)

    def test_counter_increase_handles_service_restart(self) -> None:
        self.assertEqual(report.monotonic_increase([10, 15, 2, 7]), 12)

    def test_rejects_sensitive_values(self) -> None:
        with self.assertRaisesRegex(ValueError, "forbidden"):
            report.validate_sanitized({"token": "whsec_secret"})
        with self.assertRaisesRegex(ValueError, "forbidden"):
            report.validate_sanitized({"database": "postgres:" + "//user:pass@example/db"})


if __name__ == "__main__":
    unittest.main()
