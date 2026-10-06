#!/usr/bin/env python3
"""Post-upgrade offline historical conversion commands."""
import argparse
import json
import sys
from pathlib import Path

import plan
import apply_audit
import apply_spans
import reconcile
from snapshot import SQLSource, private_write, canonical, save_snapshot, strict_loads


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    commands = parser.add_subparsers(dest="command", required=True)
    backup = commands.add_parser("snapshot", help="read-only private snapshot; never overwrite")
    backup.add_argument("--source-profile", type=Path, required=True)
    backup.add_argument("--source-deployment", required=True)
    backup.add_argument("--output", type=Path, required=True)
    convert = commands.add_parser("plan", help="freeze conversions and native validation; no target writes")
    convert.add_argument("--source", type=Path, required=True)
    convert.add_argument("--output", type=Path, required=True)
    convert.add_argument("--environment", choices=["development", "test", "staging", "production"], required=True)
    convert.add_argument("--validation-time", required=True)
    convert.add_argument("--native-validator", type=Path, required=True)
    convert.add_argument("--span-codec", type=Path)
    verify = commands.add_parser("verify", help="verify frozen plan bytes and counts")
    verify.add_argument("--plan", type=Path, required=True)
    publish = commands.add_parser("apply-audit", help="explicit approved Audit-only Kafka publication; requires readback afterward")
    publish.add_argument("--plan", type=Path, required=True)
    publish.add_argument("--expected-items-sha256", required=True)
    publish.add_argument("--sources", nargs="+", required=True)
    publish.add_argument("--native-validator", type=Path, required=True)
    publish.add_argument("--receipt", type=Path, required=True)
    publish.add_argument("--mode", choices=["qualification", "release"], required=True)
    publish.add_argument("--expected-plan-sha256", required=True)
    publish.add_argument("--expected-profile-sha256", required=True)
    spans = commands.add_parser("apply-spans", help="qualification-only native Span CREATE and readback")
    spans.add_argument("--plan", type=Path, required=True)
    spans.add_argument("--expected-items-sha256", required=True)
    spans.add_argument("--expected-plan-sha256", required=True)
    spans.add_argument("--expected-profile-sha256", required=True)
    spans.add_argument("--target-profile", type=Path, required=True)
    spans.add_argument("--receipt", type=Path, required=True)
    spans.add_argument("--mode", choices=["qualification", "release"], required=True)
    readback = commands.add_parser("reconcile-audit", help="read native month/dedup records; no writes")
    readback.add_argument("--plan", type=Path, required=True)
    readback.add_argument("--sources", nargs="+", required=True)
    readback.add_argument("--target-profile", type=Path, required=True)
    readback.add_argument("--output", type=Path, required=True)
    args = parser.parse_args(argv)
    try:
        if args.command == "snapshot":
            profile = strict_loads(args.source_profile.read_text())
            if args.output.exists():
                raise ValueError("snapshot output already exists")
            records, tables = SQLSource(profile).export()
            manifest = save_snapshot(args.output, records, args.source_deployment, {"tables": tables,
                "source_pin": "e6445df3fd70f0768a77764d10ec084954d3a770",
                "target_pin": "b1a8e10d860bf0bf54438f8240131ebc301cf1bd"})
        elif args.command == "plan":
            manifest = plan.create(args.source, args.output, args.environment, args.validation_time, args.native_validator, args.span_codec)
        elif args.command == "verify":
            manifest = plan.verify(args.plan)
        elif args.command == "apply-audit":
            manifest = apply_audit.apply(args.plan, args.expected_items_sha256, args.sources, args.native_validator, args.receipt,
                expected_plan_sha256=args.expected_plan_sha256, expected_profile_sha256=args.expected_profile_sha256, qualification=args.mode == "qualification")
            print(json.dumps({"acknowledged_count": manifest["acknowledged_count"], "database_confirmation": False}))
            return 0
        elif args.command == "apply-spans":
            profile = strict_loads(args.target_profile.read_text())
            manifest = apply_spans.apply(args.plan, args.expected_items_sha256, profile, args.receipt,
                expected_plan_sha256=args.expected_plan_sha256, expected_profile_sha256=args.expected_profile_sha256, qualification=args.mode == "qualification")
            complete = manifest["stage"] == "native_span_readback_verified" and manifest["verified_count"] == len(manifest["entries"])
            print(json.dumps({key: manifest[key] for key in ("created_count", "verified_count", "existing_verified_count")}, sort_keys=True))
            return 0 if complete else 2
        else:
            plan.verify(args.plan)
            items = [strict_loads(line) for line in (args.plan / "items.jsonl").read_bytes().splitlines()]
            selected = [item for item in items if item["kind"] == "audit" and item["source_id"] in args.sources]
            source = SQLSource(strict_loads(args.target_profile.read_text()))
            manifest = reconcile.check(selected, lambda item: reconcile.fetch_audit(source, item))
            private_write(args.output, (canonical(manifest) + "\n").encode())
            print(json.dumps({"counts": manifest["counts"], "complete": manifest["complete"]}, sort_keys=True))
            return 0 if manifest["complete"] else 2
        # Summaries never include source or native payloads.
        print(json.dumps({key: manifest[key] for key in ("record_count", "counts", "source_counts", "reasons", "items_sha256", "records_sha256") if key in manifest}, sort_keys=True))
        return 0
    except (OSError, ValueError, KeyError, TypeError):
        if args.command in {"apply-audit", "apply-spans"}:
            print("Target write failed; outcome may be unknown. Inspect private receipt and reconcile before retrying.", file=sys.stderr)
        else:
            print("Historical read/format check failed; no target writes were requested. Check private input and command parameters.", file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
