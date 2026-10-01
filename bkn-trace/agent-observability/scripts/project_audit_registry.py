#!/usr/bin/env python3
# Copyright (c) 2026 OpenBKN
# SPDX-License-Identifier: LicenseRef-OpenBKN
# Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
# Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

"""Build the minimal Audit Writer registry from the canonical bkn-docs JSON.

Pass the canonical path, or '-' to read raw registry bytes from stdin. The
projection keeps the canonical ordering so the embedded digest is reproducible.
"""

from __future__ import annotations

import hashlib
import json
import sys
from pathlib import Path


SOURCE_FIELDS = (
    "source_id", "owner", "modules", "collection_method", "reliability",
    "schema_version", "allowed_environments",
)
EVENT_FIELDS = (
    "event_name", "log_category", "allowed_source_ids", "resource_types",
    "required_actor_fields", "required_target_fields",
    "required_attributes", "allowed_attributes", "sensitive_attributes",
    "outcome_mapping", "schema_version", "allow_unknown",
)
AUDIT_CATEGORIES = {"access.user", "audit.admin", "audit.security"}
# These describe internal authorization or read decisions. They remain in the
# canonical registry for contract documentation but are never user-facing logs.
INTERNAL_EVENTS = {"authorization.decided", "log.query.authorized", "log.query.denied"}


def project(raw: bytes) -> bytes:
    registry = json.loads(raw)
    projected = {
        "source_registry_sha256": hashlib.sha256(raw).hexdigest(),
        "registry_version": registry["registry_version"],
        "contract_version": registry["contract_version"],
        "sources": [pick(source, SOURCE_FIELDS) for source in registry["sources"]],
        "events": [
            pick(event, EVENT_FIELDS)
            for event in registry["events"]
            if event["log_category"] in AUDIT_CATEGORIES and event["event_name"] not in INTERNAL_EVENTS
        ],
    }
    return json.dumps(projected, ensure_ascii=False, separators=(",", ":")).encode() + b"\n"


def pick(value: dict, keys: tuple[str, ...]) -> dict:
    return {key: value[key] for key in keys if key in value}


def main() -> int:
    if len(sys.argv) != 3:
        print("usage: project_audit_registry.py CANONICAL_JSON OUTPUT_JSON|--check", file=sys.stderr)
        return 2
    raw = sys.stdin.buffer.read() if sys.argv[1] == "-" else Path(sys.argv[1]).read_bytes()
    result = project(raw)
    target = Path(__file__).resolve().parents[1] / "src/drivenadapter/kafkaaccess/auditvalidator/assets/registry-runtime-v1.json"
    if sys.argv[2] == "--check":
        if target.read_bytes() != result:
            print("embedded Audit registry differs from canonical projection", file=sys.stderr)
            return 1
        return 0
    target = Path(sys.argv[2])
    target.write_bytes(result)
    print(f"canonical_sha256={hashlib.sha256(raw).hexdigest()}")
    print(f"runtime_sha256={hashlib.sha256(result).hexdigest()}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
