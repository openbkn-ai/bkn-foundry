#!/usr/bin/env python3
# Copyright openbkn.ai
#
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

"""Single operator entry for the OpenBKN 0.1.5 permission transition."""

from __future__ import annotations

import argparse
import json
import os
import subprocess
import sys
from dataclasses import dataclass
from datetime import datetime, timezone
from pathlib import Path
from typing import Optional, Sequence


SCRIPT_DIRECTORY = Path(__file__).resolve().parent
DEFAULT_AUTHZ_MIGRATOR = "/opt/bkn-safe/authz-migrate"
DEFAULT_STATE_FILE = "/tmp/openbkn-permission-model-transition-workloads.tsv"


class OrchestrationError(RuntimeError):
    """Raised when a release migration step cannot be executed safely."""


@dataclass(frozen=True)
class Step:
    name: str
    command: tuple[str, ...]
    report_path: Path


def build_parser() -> argparse.ArgumentParser:
    """Build the release-specific command line."""
    parser = argparse.ArgumentParser(
        description="Run the one-time OpenBKN 0.1.5 permission-model transition."
    )
    commands = parser.add_subparsers(dest="command", required=True)

    for action in ("stop", "start"):
        control = commands.add_parser(action, help=f"{action} migration workloads")
        control.add_argument("--namespace", default="openbkn")
        control.add_argument("--expected-context", default="")
        control.add_argument("--state-file", default=DEFAULT_STATE_FILE)
        control.add_argument("--timeout-seconds", type=int, default=300)

    for mode in ("dry-run", "apply"):
        migration = commands.add_parser(mode, help=f"{mode} all registered steps")
        migration.add_argument(
            "--source-version",
            required=True,
            help="installed version; this workflow accepts only 0.1.4",
        )
        migration.add_argument("--manifest", required=True)
        migration.add_argument("--authz-config", default="")
        migration.add_argument(
            "--authz-migrator",
            default=os.getenv("OPENBKN_AUTHZ_MIGRATOR", DEFAULT_AUTHZ_MIGRATOR),
        )
        migration.add_argument("--report-dir", required=True)
        migration.add_argument("--state-file", default=DEFAULT_STATE_FILE)
        migration.add_argument("--namespace", default="openbkn")
        migration.add_argument("--expected-context", default="")

    return parser


def control_services(args: argparse.Namespace) -> int:
    """Delegate workload control through the versioned release script."""
    command = [
        str(SCRIPT_DIRECTORY / "service_control.sh"),
        args.command,
        "--namespace",
        args.namespace,
        "--state-file",
        args.state_file,
        "--timeout-seconds",
        str(args.timeout_seconds),
    ]
    if args.expected_context:
        command.extend(["--expected-context", args.expected_context])
    return subprocess.run(command, check=False).returncode


def prepare_report_directory(path: str) -> Path:
    """Create an empty report directory without overwriting prior evidence."""
    report_dir = Path(path).resolve()
    if report_dir.exists() and any(report_dir.iterdir()):
        raise OrchestrationError(f"report directory is not empty: {report_dir}")
    report_dir.mkdir(parents=True, exist_ok=True)
    return report_dir


def validate_source_version(value: str) -> str:
    """Limit this one-time package to its reviewed source release."""
    normalized = value.strip().removeprefix("v").split("+", 1)[0]
    if normalized != "0.1.4":
        raise OrchestrationError(
            f"unsupported source version {value!r}; this package only migrates 0.1.4 to 0.1.5"
        )
    return normalized


def build_steps(args: argparse.Namespace, report_dir: Path) -> list[Step]:
    """Return the fixed step order for this target release.

    Future Vega work for the same target release is added here as another
    explicit step. It must not introduce another operator-facing entry point.
    """
    manifest = Path(args.manifest).resolve()
    if not manifest.is_file():
        raise OrchestrationError(f"migration manifest does not exist: {manifest}")
    authz_migrator = Path(args.authz_migrator)
    if not authz_migrator.is_file():
        raise OrchestrationError(
            f"bkn-safe migration executable does not exist: {authz_migrator}"
        )

    bkn_report = report_dir / "01-bkn-data.json"
    authz_report = report_dir / "02-authorization.json"
    authz_command = [
        str(authz_migrator),
        "--mode",
        args.command,
        "--manifest",
        str(manifest),
    ]
    if args.authz_config:
        authz_command.extend(["--config", str(Path(args.authz_config).resolve())])
    return [
        Step(
            "bkn-data",
            (
                sys.executable,
                str(SCRIPT_DIRECTORY / "bkn_data.py"),
                "--mode",
                args.command,
            ),
            bkn_report,
        ),
        Step("authorization", tuple(authz_command), authz_report),
    ]


def require_stopped_workloads(args: argparse.Namespace) -> None:
    """Require persisted and live evidence that workloads are stopped."""
    state = Path(args.state_file)
    if not state.is_file() or state.stat().st_size == 0:
        raise OrchestrationError(
            "apply requires a workload replica snapshot; run the unified stop command first"
        )
    command = [
        str(SCRIPT_DIRECTORY / "service_control.sh"),
        "verify-stopped",
        "--namespace",
        args.namespace,
        "--state-file",
        args.state_file,
    ]
    if args.expected_context:
        command.extend(["--expected-context", args.expected_context])
    result = subprocess.run(command, check=False)
    if result.returncode != 0:
        raise OrchestrationError("migration workloads are not fully stopped")


def run_step(step: Step) -> None:
    """Run one step and preserve its machine-readable standard output."""
    with step.report_path.open("w", encoding="utf-8") as output:
        result = subprocess.run(step.command, stdout=output, check=False)
    if result.returncode != 0:
        raise OrchestrationError(
            f"migration step {step.name} failed with exit code {result.returncode}"
        )
    if not step.report_path.is_file() or step.report_path.stat().st_size == 0:
        raise OrchestrationError(f"migration step {step.name} produced no report")


def write_summary(
    report_dir: Path,
    mode: str,
    steps: Sequence[Step],
    source_version: str,
    error: str = "",
) -> None:
    """Write a release-level report that links every module result."""
    summary = {
        "target_version": "0.1.5",
        "source_version": source_version,
        "migration": "permission_model_transition",
        "mode": mode,
        "generated_at": datetime.now(timezone.utc).isoformat(),
        "steps": [
            {
                "name": step.name,
                "report": str(step.report_path),
                "completed": step.report_path.is_file()
                and step.report_path.stat().st_size > 0,
            }
            for step in steps
        ],
    }
    if error:
        summary["error"] = error
    (report_dir / "summary.json").write_text(
        json.dumps(summary, indent=2, sort_keys=True) + "\n",
        encoding="utf-8",
    )


def run_migration(args: argparse.Namespace) -> int:
    """Run BKN first and authorization second, stopping on the first failure."""
    source_version = validate_source_version(args.source_version)
    if args.command == "apply":
        require_stopped_workloads(args)
    report_dir = prepare_report_directory(args.report_dir)
    steps = build_steps(args, report_dir)
    try:
        for step in steps:
            run_step(step)
    except Exception as exc:
        write_summary(report_dir, args.command, steps, source_version, str(exc))
        raise
    write_summary(report_dir, args.command, steps, source_version)
    return 0


def main(argv: Optional[Sequence[str]] = None) -> int:
    """Run the selected release migration action."""
    args = build_parser().parse_args(argv)
    try:
        if args.command in {"stop", "start"}:
            return control_services(args)
        return run_migration(args)
    except KeyboardInterrupt:
        print("Migration interrupted; keep all workloads stopped.", file=sys.stderr)
        return 130
    except Exception as exc:
        print(f"Migration failed: {exc}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    sys.exit(main())
