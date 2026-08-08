#!/usr/bin/env python3
from __future__ import annotations

import importlib.util
import json
import tempfile
import unittest
from pathlib import Path

SCRIPT = Path(__file__).with_name("cicd-artifacts.py")
spec = importlib.util.spec_from_file_location("cicd_artifacts", SCRIPT)
assert spec and spec.loader
cicd = importlib.util.module_from_spec(spec)
spec.loader.exec_module(cicd)

SHA_A = "a" * 40
SHA_B = "b" * 40
DIGEST_A = "sha256:" + "1" * 64
DIGEST_B = "sha256:" + "2" * 64
DIGEST_C = "sha256:" + "3" * 64
REGISTRY = "us-central1-docker.pkg.dev/test-project/release"


class DetectionTests(unittest.TestCase):
    def test_identity_only(self) -> None:
        result = cicd.detect_components(["service/identity-service/internal/httpapi/me.go"])
        self.assertEqual(result["build_candidates"], ["identity"])
        self.assertFalse(result["force_all"])

    def test_organization_only(self) -> None:
        result = cicd.detect_components(["service/organization-service/internal/store/repository.go"])
        self.assertEqual(result["build_candidates"], ["organization"])

    def test_apisix_only(self) -> None:
        result = cicd.detect_components(["gateway/apisix/conf/apisix.cloud-run.yaml"])
        self.assertEqual(result["build_candidates"], ["apisix"])

    def test_pipeline_change_forces_all(self) -> None:
        result = cicd.detect_components([".github/workflows/artifact-build.yml"])
        self.assertTrue(result["force_all"])
        self.assertEqual(result["build_candidates"], ["identity", "organization", "apisix"])

    def test_docs_only_requires_no_runtime_build(self) -> None:
        result = cicd.detect_components(["docs/cicd-artifact-pipeline.md"])
        self.assertFalse(result["any"])
        self.assertEqual(result["build_candidates"], [])


class ResolutionTests(unittest.TestCase):
    def test_existing_valid_tree_tag_reuses(self) -> None:
        self.assertEqual(cicd.resolution_decision("found", DIGEST_A), "reuse")

    def test_missing_tree_tag_builds(self) -> None:
        self.assertEqual(cicd.resolution_decision("missing"), "build")

    def test_invalid_existing_digest_fails_closed(self) -> None:
        with self.assertRaises(ValueError):
            cicd.resolution_decision("found", "sha256:bad")

    def test_ambiguous_lookup_fails_closed(self) -> None:
        with self.assertRaises(ValueError):
            cicd.resolution_decision("error")


class ManifestTests(unittest.TestCase):
    def _result(self, root: Path, name: str, digest: str, built: bool) -> Path:
        image_name = cicd.COMPONENTS[name]["image_name"]
        path = root / f"{name}.json"
        path.write_text(
            json.dumps(
                {
                    "component_tree_sha": SHA_B,
                    "image": f"{REGISTRY}/{image_name}",
                    "digest": digest,
                    "built": built,
                }
            ),
            encoding="utf-8",
        )
        return path

    def test_complete_manifest_records_built_and_reused(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            files = {
                "identity": self._result(root, "identity", DIGEST_A, True),
                "organization": self._result(root, "organization", DIGEST_B, False),
                "apisix": self._result(root, "apisix", DIGEST_C, False),
            }
            manifest = cicd.build_manifest(SHA_A, SHA_B, files)
            cicd.validate_manifest(manifest)
            self.assertEqual(set(manifest["components"]), {"identity", "organization", "apisix"})
            self.assertTrue(manifest["components"]["identity"]["built"])
            self.assertFalse(manifest["components"]["organization"]["built"])
            for component in manifest["components"].values():
                self.assertRegex(component["digest"], r"^sha256:[0-9a-f]{64}$")
                self.assertEqual(component["reference"], f"{component['image']}@{component['digest']}")
                self.assertNotIn(":latest", component["reference"])

    def test_partial_resolution_cannot_emit_manifest(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            files = {
                "identity": self._result(root, "identity", DIGEST_A, True),
                "organization": self._result(root, "organization", DIGEST_B, False),
            }
            with self.assertRaises(ValueError):
                cicd.build_manifest(SHA_A, SHA_B, files)

    def test_missing_component_file_cannot_emit_manifest(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            files = {
                "identity": self._result(root, "identity", DIGEST_A, True),
                "organization": self._result(root, "organization", DIGEST_B, False),
                "apisix": root / "missing.json",
            }
            with self.assertRaises(ValueError):
                cicd.build_manifest(SHA_A, SHA_B, files)

    def test_invalid_digest_rejected(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            files = {
                "identity": self._result(root, "identity", "sha256:bad", True),
                "organization": self._result(root, "organization", DIGEST_B, False),
                "apisix": self._result(root, "apisix", DIGEST_C, False),
            }
            with self.assertRaises(ValueError):
                cicd.build_manifest(SHA_A, SHA_B, files)


if __name__ == "__main__":
    unittest.main(verbosity=2)
