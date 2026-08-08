#!/usr/bin/env python3
from __future__ import annotations

import importlib.util
import json
import os
import subprocess
import tempfile
import unittest
from pathlib import Path

SCRIPT = Path(__file__).with_name("cicd-artifacts.py")
PUBLISH_SCRIPT = Path(__file__).with_name("publish-cicd-artifacts.sh")
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


class PublisherResolutionTests(unittest.TestCase):
    def _fake_docker(self, root: Path) -> Path:
        bin_dir = root / "bin"
        bin_dir.mkdir()
        docker = bin_dir / "docker"
        docker.write_text(
            """#!/usr/bin/env bash
set -Eeuo pipefail

mode="${FAKE_DOCKER_MODE:?FAKE_DOCKER_MODE is required}"
expected="${FAKE_DOCKER_DIGEST:-}"

if [[ "$1 $2 $3" != "buildx imagetools inspect" ]]; then
  printf 'unexpected docker invocation: %s\\n' "$*" >&2
  exit 99
fi

case "${mode}" in
  found)
    printf '{"digest":"%s"}\\n' "${expected}"
    ;;
  missing)
    printf 'ERROR: manifest unknown: requested tag not found\\n' >&2
    exit 1
    ;;
  auth-error)
    printf 'ERROR: unauthorized: authentication required\\n' >&2
    exit 1
    ;;
  malformed)
    printf '{"digest":"sha256:bad"}\\n'
    ;;
  eventual)
    state="${FAKE_DOCKER_STATE:?FAKE_DOCKER_STATE is required}"
    count=0
    [[ -f "${state}" ]] && count="$(cat "${state}")"
    count=$((count + 1))
    printf '%s\\n' "${count}" > "${state}"
    if (( count < 3 )); then
      printf 'ERROR: manifest unknown: requested tag not found\\n' >&2
      exit 1
    fi
    printf '{"digest":"%s"}\\n' "${expected}"
    ;;
  *)
    printf 'unknown fake mode: %s\\n' "${mode}" >&2
    exit 98
    ;;
esac
""",
            encoding="utf-8",
        )
        docker.chmod(0o755)
        return bin_dir

    def _run_shell(
        self,
        root: Path,
        command: str,
        *,
        mode: str,
        digest: str = "",
        state: Path | None = None,
    ) -> subprocess.CompletedProcess[str]:
        bin_dir = self._fake_docker(root)
        work = root / "work"
        work.mkdir()
        env = os.environ.copy()
        env.update(
            {
                "PATH": f"{bin_dir}:{env['PATH']}",
                "FAKE_DOCKER_MODE": mode,
                "FAKE_DOCKER_DIGEST": digest,
                "PUBLISH_POST_PUSH_LOOKUP_ATTEMPTS": "4",
                "PUBLISH_POST_PUSH_RETRY_BASE_DELAY_SECONDS": "0",
            }
        )
        if state is not None:
            env["FAKE_DOCKER_STATE"] = str(state)
        return subprocess.run(
            [
                "bash",
                "-c",
                'set -Eeuo pipefail; source "$1"; work="$2"; shift 2; eval "$*"',
                "publisher-test",
                str(PUBLISH_SCRIPT),
                str(work),
                command,
            ],
            check=False,
            text=True,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            env=env,
        )

    def test_exact_registry_lookup_returns_digest(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            result = self._run_shell(
                Path(tmp),
                "lookup_tree_tag example.invalid/release/identity-service:tree-found",
                mode="found",
                digest=DIGEST_A,
            )
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stdout.strip(), DIGEST_A)

    def test_exact_registry_lookup_distinguishes_missing_from_auth_error(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            missing = self._run_shell(
                Path(tmp),
                "lookup_tree_tag example.invalid/release/identity-service:tree-missing",
                mode="missing",
            )
        self.assertEqual(missing.returncode, 10)

        with tempfile.TemporaryDirectory() as tmp:
            auth_error = self._run_shell(
                Path(tmp),
                "lookup_tree_tag example.invalid/release/identity-service:tree-auth",
                mode="auth-error",
            )
        self.assertEqual(auth_error.returncode, 20)
        self.assertIn("unauthorized", auth_error.stderr)

    def test_exact_registry_lookup_rejects_malformed_digest(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            result = self._run_shell(
                Path(tmp),
                "lookup_tree_tag example.invalid/release/identity-service:tree-malformed",
                mode="malformed",
            )
        self.assertEqual(result.returncode, 20)
        self.assertIn("without a strict sha256 digest", result.stderr)

    def test_post_push_lookup_retries_not_found_then_matches_push_digest(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            state = root / "state"
            result = self._run_shell(
                root,
                f"resolve_pushed_tree_tag example.invalid/release/organization-service:tree-eventual {DIGEST_B} organization",
                mode="eventual",
                digest=DIGEST_B,
                state=state,
            )
            attempts = state.read_text(encoding="utf-8").strip()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stdout.strip(), DIGEST_B)
        self.assertEqual(attempts, "3")

    def test_post_push_lookup_rejects_digest_mismatch(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            result = self._run_shell(
                Path(tmp),
                f"resolve_pushed_tree_tag example.invalid/release/apisix-gateway:tree-mismatch {DIGEST_A} apisix",
                mode="found",
                digest=DIGEST_B,
            )
        self.assertEqual(result.returncode, 1)
        self.assertIn("digest mismatch", result.stderr)

    def test_push_digest_extraction_requires_one_strict_digest(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            push_log = root / "push.log"
            push_log.write_text(
                f"tree-test: digest: {DIGEST_A} size: 1234\n",
                encoding="utf-8",
            )
            result = self._run_shell(
                root,
                f"extract_pushed_digest {push_log} identity",
                mode="found",
            )
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stdout.strip(), DIGEST_A)


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
