#!/usr/bin/env python3
"""Export/verify legacy t_operation_audit snapshots without live-ledger import.

The exporter consumes a caller-provided frozen JSON snapshot. It never opens a
database, generates INSERT statements, or executes DROP. The archive command
only writes a verification receipt after an independent manifest check.
"""

from __future__ import annotations

import argparse
import hashlib
import json
import sys
from pathlib import Path


def jcs(value: object) -> bytes:
    return json.dumps(value, ensure_ascii=False, sort_keys=True, separators=(",", ":"), allow_nan=False).encode("utf-8")


def stream_digest(rows: list[dict]) -> tuple[bytes, str]:
    ordered = sorted(rows, key=lambda row: (row["event_time"], row["event_id"]))
    output = bytearray()
    for row in ordered:
        output.extend(jcs(row))
        output.append(0x0A)
    return bytes(output), hashlib.sha256(output).hexdigest()


def export_snapshot(input_path: Path, output_path: Path, manifest_path: Path) -> dict:
    rows = [json.loads(line) for line in input_path.read_text(encoding="utf-8").splitlines() if line.strip()]
    payload, digest = stream_digest(rows)
    output_path.write_bytes(payload)
    manifest = {
        "source_table": "t_operation_audit",
        "source_snapshot": str(input_path),
        "row_count": len(rows),
        "stream_sha256": digest,
        "archive_file": str(output_path),
        "drop_executed": False,
    }
    manifest_path.write_bytes(jcs(manifest) + b"\n")
    return manifest


def verify_archive(archive_path: Path, manifest_path: Path) -> dict:
    manifest = json.loads(manifest_path.read_text(encoding="utf-8"))
    rows = [json.loads(line) for line in archive_path.read_text(encoding="utf-8").splitlines() if line.strip()]
    payload, digest = stream_digest(rows)
    if payload != archive_path.read_bytes():
        raise ValueError("archive is not canonical JCS+LF JSONL")
    if manifest.get("row_count") != len(rows) or manifest.get("stream_sha256") != digest:
        raise ValueError("archive manifest digest or row count mismatch")
    if manifest.get("drop_executed") is not False:
        raise ValueError("archive manifest claims a destructive operation")
    return {"verified": True, "row_count": len(rows), "stream_sha256": digest, "drop_executed": False}


def main() -> int:
    parser = argparse.ArgumentParser()
    sub = parser.add_subparsers(dest="command", required=True)
    export = sub.add_parser("export")
    export.add_argument("--input", type=Path, required=True, help="frozen read-only JSONL snapshot")
    export.add_argument("--archive", type=Path, required=True)
    export.add_argument("--manifest", type=Path, required=True)
    verify = sub.add_parser("verify")
    verify.add_argument("--archive", type=Path, required=True)
    verify.add_argument("--manifest", type=Path, required=True)
    archive = sub.add_parser("archive")
    archive.add_argument("--archive", type=Path, required=True)
    archive.add_argument("--manifest", type=Path, required=True)
    args = parser.parse_args()
    if args.command == "export":
        result = export_snapshot(args.input, args.archive, args.manifest)
    else:
        result = verify_archive(args.archive, args.manifest)
        if args.command == "archive":
            result["archive_receipt"] = "verified-only; no DROP executed"
    sys.stdout.write(json.dumps(result, ensure_ascii=False, sort_keys=True) + "\n")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
