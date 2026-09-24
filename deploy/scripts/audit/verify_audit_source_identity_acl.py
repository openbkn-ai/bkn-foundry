#!/usr/bin/env python3
"""Verify Audit source registration against an explicit Kafka ACL snapshot.

The snapshot is an offline release artifact, not a runtime Kafka principal
lookup. A normal consumer therefore never receives or trusts per-record SASL
identity. Sources which remain source_adapter/not_integrated are reported as
unverified and can never be promoted by this checker.
"""

from __future__ import annotations

import argparse
import json
import sys
from pathlib import Path


TOPIC = "openbkn.audit.v1"


def verify(registry: dict, acl: dict) -> tuple[bool, list[dict]]:
    principals = acl.get("principals", [])
    by_source: dict[str, list[dict]] = {}
    for item in principals:
        for source_id in item.get("source_ids", []):
            by_source.setdefault(source_id, []).append(item)

    matrix: list[dict] = []
    ok = acl.get("topic") == TOPIC and bool(acl.get("consumer_group"))
    for source in registry.get("sources", []):
        source_id = source.get("source_id", "")
        method = source.get("collection_method")
        row = {"source_id": source_id, "collection_method": method, "status": "unverified", "reasons": []}
        if method != "kafka_audit":
            row["reasons"].append("collection_method_not_kafka_audit")
            matrix.append(row)
            continue
        matches = by_source.get(source_id, [])
        if not matches:
            row["reasons"].append("missing_principal_acl_mapping")
        for match in matches:
            if TOPIC not in match.get("write_topics", []):
                row["reasons"].append("principal_cannot_write_audit_topic")
            if match.get("read_groups"):
                row["reasons"].append("service_principal_has_consumer_read")
            if match.get("service_name") not in source.get("allowed_service_names", []):
                row["reasons"].append("service_name_not_allowed_by_registry")
            if not match.get("environments") or not set(match["environments"]).issubset(set(source.get("allowed_environments", []))):
                row["reasons"].append("environment_not_allowed_by_registry")
        if not row["reasons"] and len(matches) == 1:
            row["status"] = "verified"
        else:
            ok = False
        matrix.append(row)
    return ok, matrix


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--registry", type=Path, required=True)
    parser.add_argument("--acl-snapshot", type=Path, required=True)
    parser.add_argument("--output", type=Path)
    args = parser.parse_args()
    registry = json.loads(args.registry.read_text(encoding="utf-8"))
    acl = json.loads(args.acl_snapshot.read_text(encoding="utf-8"))
    ok, matrix = verify(registry, acl)
    result = {"topic": TOPIC, "verified": ok, "sources": matrix, "runtime_principal_read": False}
    encoded = json.dumps(result, ensure_ascii=False, indent=2, sort_keys=True) + "\n"
    if args.output:
        args.output.write_text(encoded, encoding="utf-8")
    else:
        sys.stdout.write(encoded)
    return 0 if ok else 2


if __name__ == "__main__":
    raise SystemExit(main())
