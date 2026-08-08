#!/usr/bin/env python3
from __future__ import annotations

import argparse
import json
import re
import subprocess
import sys
from pathlib import Path
from typing import Any

REPOSITORY = "DoMinhHHung/bridgeworks"
SOURCE_BRANCH = "dev"
SCHEMA_VERSION = 1
SHA_RE = re.compile(r"^[0-9a-f]{40}$")
DIGEST_RE = re.compile(r"^sha256:[0-9a-f]{64}$")
IMAGE_RE = re.compile(
    r"^[a-z0-9-]+-docker\.pkg\.dev/[a-z][a-z0-9-]{4,61}[a-z0-9]/[a-z0-9][a-z0-9._-]*/[a-z0-9][a-z0-9._-]*$"
)

COMPONENTS = {
    "identity": {
        "context": "service/identity-service",
        "image_name": "identity-service",
    },
    "organization": {
        "context": "service/organization-service",
        "image_name": "organization-service",
    },
    "apisix": {
        "context": "gateway/apisix",
        "image_name": "apisix-gateway",
    },
}

PIPELINE_SOURCES = {
    ".github/workflows/artifact-build.yml",
    ".github/scripts/ci-base-sha.sh",
    ".github/scripts/cicd-artifacts.py",
    ".github/scripts/publish-cicd-artifacts.sh",
    ".github/scripts/test-cicd-artifacts.py",
}


def fail(message: str) -> "NoReturn":
    raise ValueError(message)


def run_git(*args: str) -> str:
    completed = subprocess.run(
        ["git", *args], check=True, text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE
    )
    return completed.stdout.strip()


def validate_sha(value: str, field: str) -> str:
    if not SHA_RE.fullmatch(value):
        fail(f"{field} must be a 40-character lowercase Git SHA")
    return value


def validate_digest(value: str) -> str:
    if not DIGEST_RE.fullmatch(value):
        fail("digest must match sha256:<64 lowercase hex chars>")
    return value


def detect_components(paths: list[str]) -> dict[str, Any]:
    normalized = sorted({(path.strip()[2:] if path.strip().startswith("./") else path.strip()) for path in paths if path.strip()})
    force_all = any(path in PIPELINE_SOURCES for path in normalized)
    changed: dict[str, bool] = {}
    for name, spec in COMPONENTS.items():
        prefix = spec["context"] + "/"
        changed[name] = force_all or any(path == spec["context"] or path.startswith(prefix) for path in normalized)

    candidates = [name for name in COMPONENTS if changed[name]]
    return {
        "force_all": force_all,
        "any": bool(candidates),
        "components": changed,
        "build_candidates": candidates,
        "changed_paths": normalized,
    }


def detect_from_git(base: str, head: str) -> dict[str, Any]:
    validate_sha(run_git("rev-parse", base), "base_sha")
    validate_sha(run_git("rev-parse", head), "head_sha")
    output = run_git("diff", "--name-only", f"{base}...{head}")
    return detect_components(output.splitlines())


def source_metadata(head: str) -> dict[str, Any]:
    commit_sha = validate_sha(run_git("rev-parse", head), "source_commit_sha")
    source_tree_sha = validate_sha(run_git("rev-parse", f"{head}^{{tree}}"), "source_tree_sha")
    components: dict[str, Any] = {}
    for name, spec in COMPONENTS.items():
        tree_sha = validate_sha(
            run_git("rev-parse", f"{head}:{spec['context']}"), f"{name}.component_tree_sha"
        )
        components[name] = {
            "component_tree_sha": tree_sha,
            "context": spec["context"],
            "image_name": spec["image_name"],
            "tree_tag": f"tree-{tree_sha}",
        }
    return {
        "repository": REPOSITORY,
        "source_branch": SOURCE_BRANCH,
        "source_commit_sha": commit_sha,
        "source_tree_sha": source_tree_sha,
        "components": components,
    }


def resolution_decision(status: str, digest: str | None = None) -> str:
    if status == "missing":
        if digest:
            fail("missing lookup must not carry a digest")
        return "build"
    if status == "found":
        if digest is None:
            fail("found lookup requires a digest")
        validate_digest(digest)
        return "reuse"
    if status == "error":
        fail("artifact lookup failed ambiguously; refusing rebuild")
    fail(f"unknown lookup status: {status}")


def validate_image(image: str, expected_name: str) -> str:
    if not IMAGE_RE.fullmatch(image):
        fail(f"invalid Artifact Registry image path: {image}")
    if image.rsplit("/", 1)[-1] != expected_name:
        fail(f"image must end with /{expected_name}")
    return image


def build_manifest(source_commit_sha: str, source_tree_sha: str, result_files: dict[str, Path]) -> dict[str, Any]:
    validate_sha(source_commit_sha, "source_commit_sha")
    validate_sha(source_tree_sha, "source_tree_sha")
    if set(result_files) != set(COMPONENTS):
        fail("all and only identity, organization, and apisix results are required")

    components: dict[str, Any] = {}
    for name, spec in COMPONENTS.items():
        path = result_files[name]
        if not path.is_file():
            fail(f"missing component resolution file: {path}")
        value = json.loads(path.read_text(encoding="utf-8"))
        if set(value) != {"component_tree_sha", "image", "digest", "built"}:
            fail(f"unexpected keys in {name} resolution")
        tree_sha = validate_sha(str(value["component_tree_sha"]), f"{name}.component_tree_sha")
        image = validate_image(str(value["image"]), spec["image_name"])
        digest = validate_digest(str(value["digest"]))
        built = value["built"]
        if not isinstance(built, bool):
            fail(f"{name}.built must be boolean")
        components[name] = {
            "component_tree_sha": tree_sha,
            "image": image,
            "digest": digest,
            "reference": f"{image}@{digest}",
            "built": built,
        }

    manifest = {
        "schema_version": SCHEMA_VERSION,
        "repository": REPOSITORY,
        "source_branch": SOURCE_BRANCH,
        "source_commit_sha": source_commit_sha,
        "source_tree_sha": source_tree_sha,
        "components": components,
    }
    validate_manifest(manifest)
    return manifest


def validate_manifest(manifest: dict[str, Any]) -> None:
    expected_top = {
        "schema_version",
        "repository",
        "source_branch",
        "source_commit_sha",
        "source_tree_sha",
        "components",
    }
    if set(manifest) != expected_top:
        fail("release manifest has unexpected top-level keys")
    if manifest["schema_version"] != SCHEMA_VERSION:
        fail("unsupported schema_version")
    if manifest["repository"] != REPOSITORY:
        fail("unexpected repository")
    if manifest["source_branch"] != SOURCE_BRANCH:
        fail("source_branch must be dev")
    validate_sha(str(manifest["source_commit_sha"]), "source_commit_sha")
    validate_sha(str(manifest["source_tree_sha"]), "source_tree_sha")

    components = manifest["components"]
    if not isinstance(components, dict) or set(components) != set(COMPONENTS):
        fail("manifest must contain exactly identity, organization, and apisix")
    for name, spec in COMPONENTS.items():
        value = components[name]
        if not isinstance(value, dict) or set(value) != {
            "component_tree_sha",
            "image",
            "digest",
            "reference",
            "built",
        }:
            fail(f"invalid {name} component schema")
        validate_sha(str(value["component_tree_sha"]), f"{name}.component_tree_sha")
        image = validate_image(str(value["image"]), spec["image_name"])
        digest = validate_digest(str(value["digest"]))
        if value["reference"] != f"{image}@{digest}":
            fail(f"{name}.reference must be digest-pinned")
        if not isinstance(value["built"], bool):
            fail(f"{name}.built must be boolean")

    serialized = json.dumps(manifest, sort_keys=True)
    forbidden = (
        ":latest",
        "whsec_",
        "postgres://",
        "postgresql://",
        "authorization:",
        "bearer ",
        "access_token",
        "private_key",
        "credentials_file",
    )
    lowered = serialized.lower()
    if any(token in lowered for token in forbidden):
        fail("manifest contains a forbidden credential-like or mutable value")


def validate_policy(root: Path) -> None:
    artifact = (root / ".github/workflows/artifact-build.yml").read_text(encoding="utf-8")
    apisix = (root / ".github/workflows/apisix-cloud-run-ci.yml").read_text(encoding="utf-8")
    combined = artifact + "\n" + apisix

    if "pull_request_target" in combined:
        fail("pull_request_target is forbidden")
    if "name: Artifact build required" not in artifact:
        fail("Artifact build required gate is missing")
    if "name: APISIX Cloud Run required" not in apisix:
        fail("APISIX Cloud Run required gate is missing")
    if "github.event_name == 'push' && github.ref == 'refs/heads/dev'" not in artifact:
        fail("trusted publish job must be gated to refs/heads/dev push")
    if "id-token: write" not in artifact:
        fail("trusted publish job must request OIDC permission")
    if "pull_request:\n    branches: [dev, main]" not in artifact:
        fail("artifact PR validation must run for dev and main PRs")
    if "push:\n    branches: [dev]" not in artifact:
        fail("artifact publication workflow must only trigger on dev push")

    forbidden = (
        "gcloud run deploy",
        "gcloud run services update",
        "gcloud run jobs deploy",
        "vercel deploy",
        "render deploy",
        "@main",
        ":latest",
    )
    lowered = combined.lower()
    for token in forbidden:
        if token.lower() in lowered:
            fail(f"forbidden CI/CD command or mutable reference present: {token}")


def cmd_detect(args: argparse.Namespace) -> None:
    result = detect_from_git(args.base, args.head)
    print(json.dumps(result, sort_keys=True, separators=(",", ":")))


def cmd_metadata(args: argparse.Namespace) -> None:
    print(json.dumps(source_metadata(args.head), sort_keys=True, separators=(",", ":")))


def cmd_validate_digest(args: argparse.Namespace) -> None:
    validate_digest(args.digest)


def cmd_decision(args: argparse.Namespace) -> None:
    print(resolution_decision(args.status, args.digest))


def cmd_manifest(args: argparse.Namespace) -> None:
    files = {
        "identity": Path(args.identity),
        "organization": Path(args.organization),
        "apisix": Path(args.apisix),
    }
    manifest = build_manifest(args.source_commit_sha, args.source_tree_sha, files)
    output = Path(args.output)
    output.parent.mkdir(parents=True, exist_ok=True)
    output.write_text(json.dumps(manifest, indent=2, sort_keys=True) + "\n", encoding="utf-8")


def cmd_validate_manifest(args: argparse.Namespace) -> None:
    value = json.loads(Path(args.manifest).read_text(encoding="utf-8"))
    validate_manifest(value)


def cmd_policy(args: argparse.Namespace) -> None:
    validate_policy(Path(args.root))


def parser() -> argparse.ArgumentParser:
    root = argparse.ArgumentParser()
    sub = root.add_subparsers(dest="command", required=True)

    detect = sub.add_parser("detect")
    detect.add_argument("--base", required=True)
    detect.add_argument("--head", default="HEAD")
    detect.set_defaults(func=cmd_detect)

    metadata = sub.add_parser("metadata")
    metadata.add_argument("--head", default="HEAD")
    metadata.set_defaults(func=cmd_metadata)

    digest = sub.add_parser("validate-digest")
    digest.add_argument("digest")
    digest.set_defaults(func=cmd_validate_digest)

    decision = sub.add_parser("decision")
    decision.add_argument("--status", choices=("found", "missing", "error"), required=True)
    decision.add_argument("--digest")
    decision.set_defaults(func=cmd_decision)

    manifest = sub.add_parser("manifest")
    manifest.add_argument("--source-commit-sha", required=True)
    manifest.add_argument("--source-tree-sha", required=True)
    manifest.add_argument("--identity", required=True)
    manifest.add_argument("--organization", required=True)
    manifest.add_argument("--apisix", required=True)
    manifest.add_argument("--output", required=True)
    manifest.set_defaults(func=cmd_manifest)

    validate = sub.add_parser("validate-manifest")
    validate.add_argument("manifest")
    validate.set_defaults(func=cmd_validate_manifest)

    policy = sub.add_parser("policy")
    policy.add_argument("--root", default=".")
    policy.set_defaults(func=cmd_policy)
    return root


def main() -> int:
    args = parser().parse_args()
    try:
        args.func(args)
    except (ValueError, json.JSONDecodeError, subprocess.CalledProcessError, OSError) as exc:
        print(f"cicd-artifacts: {exc}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
