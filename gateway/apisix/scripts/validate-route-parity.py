#!/usr/bin/env python3
from __future__ import annotations

import argparse
import re
from pathlib import Path

ROUTE_START = re.compile(r"^  - id: (\S+)\s*$")
FIELD = re.compile(r"^    (uri|methods):\s*(.+?)\s*$")
PROXY_START = re.compile(r"^      proxy-rewrite:\s*$")
PROXY_VALUE = re.compile(r"^        (uri|regex_uri):\s*(.+?)\s*$")


def route_surface(path: Path) -> dict[str, tuple[str, str, tuple[tuple[str, str], ...]]]:
    lines = path.read_text(encoding="utf-8").splitlines()
    result: dict[str, tuple[str, str, tuple[tuple[str, str], ...]]] = {}
    in_routes = False
    current_id: str | None = None
    current_uri: str | None = None
    current_methods: str | None = None
    current_proxy: list[tuple[str, str]] = []
    in_proxy = False

    def finish() -> None:
        nonlocal current_id, current_uri, current_methods, current_proxy, in_proxy
        if current_id is None:
            return
        if current_uri is None or current_methods is None:
            raise ValueError(f"{path}: route {current_id} is missing uri or methods")
        if current_id in result:
            raise ValueError(f"{path}: duplicate route id {current_id}")
        result[current_id] = (current_uri, current_methods, tuple(current_proxy))
        current_id = current_uri = current_methods = None
        current_proxy = []
        in_proxy = False

    for line in lines:
        if line == "routes:":
            in_routes = True
            continue
        if not in_routes:
            continue

        match = ROUTE_START.match(line)
        if match:
            finish()
            current_id = match.group(1)
            continue
        if current_id is None:
            continue

        match = FIELD.match(line)
        if match:
            if match.group(1) == "uri":
                current_uri = match.group(2)
            else:
                current_methods = match.group(2)
            in_proxy = False
            continue

        if PROXY_START.match(line):
            in_proxy = True
            continue
        if in_proxy:
            match = PROXY_VALUE.match(line)
            if match:
                current_proxy.append((match.group(1), match.group(2)))
                continue
            if line.startswith("      ") and not line.startswith("        "):
                in_proxy = False

    finish()
    if not result:
        raise ValueError(f"{path}: no routes found")
    return result


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("render", type=Path)
    parser.add_argument("cloud_run", type=Path)
    args = parser.parse_args()

    render = route_surface(args.render)
    cloud_run = route_surface(args.cloud_run)

    if render != cloud_run:
        render_ids = set(render)
        cloud_ids = set(cloud_run)
        only_render = sorted(render_ids - cloud_ids)
        only_cloud = sorted(cloud_ids - render_ids)
        if only_render:
            print(f"route parity: only render: {only_render}")
        if only_cloud:
            print(f"route parity: only cloud-run: {only_cloud}")
        for route_id in sorted(render_ids & cloud_ids):
            if render[route_id] != cloud_run[route_id]:
                print(
                    f"route parity: {route_id} differs: "
                    f"render={render[route_id]!r} cloud-run={cloud_run[route_id]!r}"
                )
        return 1

    print(f"route_surface_parity=true routes={len(render)}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
