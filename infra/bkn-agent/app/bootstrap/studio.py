# Copyright openbkn.ai
#
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

"""Trusted deployment/admin CLI. Never called during HTTP app startup.

DB credentials are the privilege boundary; actor/owner identify the management
operation, not the effective identity of future end-user tool executions.
"""

import argparse
import asyncio
import json
import sys
from pathlib import Path
from typing import Literal

from pydantic import BaseModel, Field
from sqlalchemy.exc import SQLAlchemyError

from app.bootstrap.presets import (
    PresetError,
    adopt,
    enroll,
    initialize,
    inspect_legacy,
    load_package,
    preview,
)

DEFAULT_MANIFEST = (
    Path(__file__).resolve().parents[2] / "deploy/agents/studio-presets.json"
)


class ManifestEntry(BaseModel):
    model_config = {"extra": "forbid"}

    scenario: str = Field(min_length=1, max_length=100, pattern=r"^[a-z0-9_]+$")
    package: str | None = Field(default=None, min_length=1, max_length=255)
    required_features: list[str] = Field(default_factory=list)


class PresetManifest(BaseModel):
    model_config = {"extra": "forbid"}

    format: Literal["bkn-agent-preset-manifest/v1"]
    scenarios: list[ManifestEntry] = Field(min_length=1)


async def bootstrap_manifest(
    sessions, manifest: Path, *, owner, actor, installed_features, dependencies
):
    definition = PresetManifest.model_validate_json(
        manifest.read_text(encoding="utf-8")
    )
    entries = definition.scenarios
    seen = set()
    packages = []
    # Validate the entire installed manifest before the first mutation.
    for entry in entries:
        scenario = entry.scenario
        if scenario in seen:
            raise PresetError("duplicate scenario in preset manifest")
        seen.add(scenario)
        missing = sorted(set(entry.required_features) - installed_features)
        if missing:
            packages.append((scenario, None, missing))
            continue
        if not entry.package:
            packages.append((scenario, None, []))
            continue
        path = (manifest.parent / entry.package).resolve()
        if not path.is_relative_to(manifest.parent.resolve()):
            raise PresetError("preset package must be inside the manifest directory")
        package = load_package(path)
        if package.scenario != scenario:
            raise PresetError("package scenario does not match its manifest")
        packages.append((scenario, package, []))
    results = []
    for scenario, package, missing in packages:
        if missing:
            results.append(
                {"scenario": scenario, "action": "skipped", "missing_features": missing}
            )
        elif package is None:
            results.append(
                {
                    "scenario": scenario,
                    "action": "failed",
                    "reason": "installed feature has no executable preset package",
                }
            )
        else:
            try:
                results.append(
                    await initialize(
                        sessions,
                        package,
                        owner=owner,
                        actor=actor,
                        installed_features=installed_features,
                        dependencies=dependencies,
                    )
                )
            except (PresetError, SQLAlchemyError) as error:
                # Do not expose driver exceptions/SQL parameters/credentials.
                reason = (
                    str(error)
                    if isinstance(error, PresetError)
                    else "preset database transaction failed"
                )
                results.append(
                    {"scenario": scenario, "action": "failed", "reason": reason}
                )
    return {"results": results}


def parser():
    cli = argparse.ArgumentParser(description=__doc__)
    cli.add_argument(
        "command", choices=["initialize", "diff", "adopt", "inspect-legacy", "enroll"]
    )
    cli.add_argument(
        "--owner-id", required=True, help="Provisioned preset management principal"
    )
    cli.add_argument("--actor-id", help="Operator ID; required for writes")
    cli.add_argument("--manifest", type=Path, default=DEFAULT_MANIFEST)
    cli.add_argument("--installed-feature", action="append", default=[])
    cli.add_argument(
        "--dependency",
        action="append",
        default=[],
        metavar="NAME=ready|missing|unknown",
    )
    cli.add_argument("--package-id")
    cli.add_argument("--version")
    cli.add_argument(
        "--package", type=Path, help="Preset file for legacy inspection/enrollment"
    )
    cli.add_argument(
        "--expected-digest", help="Digest returned by diff; required for adoption"
    )
    return cli


async def execute(args, sessions):
    features = set(args.installed_feature)
    if args.command in {"inspect-legacy", "enroll"}:
        if not args.package:
            raise PresetError("legacy inspection/enrollment requires --package")
        package = load_package(args.package)
        if args.command == "inspect-legacy":
            return await inspect_legacy(sessions, package, owner=args.owner_id)
        if not args.expected_digest:
            raise PresetError("enroll requires --expected-digest from inspect-legacy")
        if set(package.required_features) - features:
            raise PresetError("required preset feature is not installed")
        return await enroll(
            sessions,
            package,
            owner=args.owner_id,
            actor=args.actor_id,
            expected_digest=args.expected_digest,
        )
    if args.command == "initialize":
        dependencies = {}
        for entry in args.dependency:
            name, separator, state = entry.partition("=")
            if (
                not separator
                or not name
                or state not in {"ready", "missing", "unknown"}
            ):
                raise PresetError("--dependency requires NAME=ready|missing|unknown")
            dependencies[name] = state
        return await bootstrap_manifest(
            sessions,
            args.manifest,
            owner=args.owner_id,
            actor=args.actor_id,
            installed_features=features,
            dependencies=dependencies,
        )
    if not args.package_id or not args.version:
        raise PresetError("diff/adopt require --package-id and --version")
    if args.command == "diff":
        return await preview(
            sessions, args.package_id, args.version, owner=args.owner_id
        )
    if not args.expected_digest:
        raise PresetError("adopt requires --expected-digest from a fresh comparison")
    return await adopt(
        sessions,
        args.package_id,
        args.version,
        owner=args.owner_id,
        actor=args.actor_id,
        expected_digest=args.expected_digest,
        installed_features=features,
    )


def main():
    cli = parser()
    args = cli.parse_args()
    if args.command not in {"diff", "inspect-legacy"} and not args.actor_id:
        cli.error("--actor-id is required for initialization/adoption")
    # Lazy import: --help and package validation do not open a DB connection.
    from app.db import SessionLocal, engine

    async def run():
        try:
            return await execute(args, SessionLocal)
        finally:
            await engine.dispose()

    try:
        result = asyncio.run(run())
    except (PresetError, OSError, ValueError, SQLAlchemyError) as error:
        reason = (
            str(error)
            if isinstance(error, (PresetError, ValueError))
            else "preset initialization failed; check dependencies/schema"
        )
        print(
            json.dumps({"action": "failed", "reason": reason}, ensure_ascii=False),
            file=sys.stderr,
        )
        return 1
    print(json.dumps(result, ensure_ascii=False, indent=2))
    return int(
        any(item.get("action") == "failed" for item in result.get("results", []))
    )


if __name__ == "__main__":
    raise SystemExit(main())
