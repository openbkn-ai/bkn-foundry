#!/usr/bin/env python3
# Copyright openbkn.ai
#
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

"""Backfill object-scoped creator grants before narrowing network_builder.

This package intentionally does not infer ownership from a user's historical
access.  It uses only the authoritative f_creator column on Catalog and
Knowledge Network records.  That avoids preserving the old wildcard-role
overreach as a permanent object grant.
"""

from __future__ import annotations

import argparse
import hashlib
import json
import sys
from collections import Counter
from dataclasses import asdict, dataclass
from datetime import datetime, timezone
from typing import Iterable, Sequence
from urllib.parse import unquote, urlparse


ALLOW = "allow"
BUNDLE = "full_business_access"
COMMUNITY_BUNDLE = "community_bundle"
SYSTEM_DERIVED = "system_derived"
SYSTEM = "system"
MAIN_BRANCH = "main"

RESOURCE_OPERATIONS = {
    "catalog": {
        "view_detail", "modify", "delete", "query_data", "resource_manage", "task_manage", "authorize",
    },
    "knowledge_network": {
        "view_detail", "modify", "delete", "query_data", "execute", "authorize",
    },
}


@dataclass(frozen=True)
class Resource:
    resource_type: str
    resource_id: str
    creator_id: str
    creator_type: str
    internal: bool = False


@dataclass(frozen=True)
class PlanItem:
    resource_type: str
    resource_id: str
    creator_id: str
    action: str
    reason: str


class MigrationError(RuntimeError):
    """A safety condition prevented the migration from writing data."""


def projection_key(*parts: str) -> str:
    return hashlib.sha256("\x00".join(parts).encode()).hexdigest()


def object_key(resource: Resource) -> str:
    return f"{resource.resource_type}:{resource.resource_id}"


def effective_operations(resource_type: str, grants: Iterable[tuple[str, str]]) -> set[str]:
    """Return direct effective operations represented by durable grant rows."""
    operations = set()
    for operation, source in grants:
        if operation == BUNDLE and source == COMMUNITY_BUNDLE:
            operations.update(RESOURCE_OPERATIONS[resource_type] - {"authorize"})
        else:
            operations.add(operation)
    return operations


def plan_resource(resource: Resource, creator_exists: bool, grants: Iterable[tuple[str, str]]) -> PlanItem:
    if resource.internal:
        return PlanItem(resource.resource_type, resource.resource_id, resource.creator_id, "skip", "internal catalog")
    if not resource.creator_id:
        return PlanItem(resource.resource_type, resource.resource_id, resource.creator_id, "block", "missing f_creator")
    if resource.creator_type.lower() != "user":
        return PlanItem(resource.resource_type, resource.resource_id, resource.creator_id, "block", "creator is not a user")
    if not creator_exists:
        return PlanItem(resource.resource_type, resource.resource_id, resource.creator_id, "block", "creator is absent from bkn-safe users")

    expected = RESOURCE_OPERATIONS[resource.resource_type]
    actual = effective_operations(resource.resource_type, grants)
    if not actual:
        return PlanItem(resource.resource_type, resource.resource_id, resource.creator_id, "insert", "no direct creator grant")
    if expected.issubset(actual):
        return PlanItem(resource.resource_type, resource.resource_id, resource.creator_id, "keep", "creator already has complete direct access")
    return PlanItem(resource.resource_type, resource.resource_id, resource.creator_id, "block", "partial direct creator grant")


def parse_mysql_dsn(value: str) -> dict[str, object]:
    parsed = urlparse(value)
    if parsed.scheme not in {"mysql", "mariadb"} or not parsed.hostname or not parsed.path.strip("/"):
        raise MigrationError("DSN must be mysql://user:password@host:port/database")
    return {
        "host": parsed.hostname,
        "port": parsed.port or 3306,
        "user": unquote(parsed.username or ""),
        "password": unquote(parsed.password or ""),
        "database": parsed.path.strip("/"),
        "charset": "utf8mb4",
        "autocommit": False,
    }


def connect(dsn: str):
    try:
        import pymysql
    except ImportError as exc:  # pragma: no cover - depends on operator host
        raise MigrationError("PyMySQL is required: install pymysql in the migration environment") from exc
    return pymysql.connect(**parse_mysql_dsn(dsn))


def fetch_resources(cursor) -> list[Resource]:
    cursor.execute("SELECT f_id, f_creator, f_creator_type, f_internal FROM t_catalog")
    catalogs = [Resource("catalog", row[0], row[1] or "", row[2] or "", bool(row[3])) for row in cursor.fetchall()]
    cursor.execute(
        "SELECT f_id, f_creator, f_creator_type FROM t_knowledge_network "
        "WHERE COALESCE(NULLIF(f_branch, ''), %s) = %s ORDER BY f_id",
        (MAIN_BRANCH, MAIN_BRANCH),
    )
    networks = [Resource("knowledge_network", row[0], row[1] or "", row[2] or "") for row in cursor.fetchall()]
    return catalogs + networks


def creator_exists(cursor, creator_id: str) -> bool:
    cursor.execute("SELECT 1 FROM users WHERE id = %s LIMIT 1", (creator_id,))
    return cursor.fetchone() is not None


def creator_grants(cursor, resource: Resource) -> list[tuple[str, str]]:
    cursor.execute(
        "SELECT operation, policy_source FROM authorization_grant "
        "WHERE accessor_id = %s AND object = %s AND effect = %s",
        (resource.creator_id, object_key(resource), ALLOW),
    )
    return [(row[0], row[1]) for row in cursor.fetchall()]


def create_grant_rows(resource: Resource) -> list[tuple[str, str, str, str, str, str, str, str]]:
    object_name = object_key(resource)
    return [
        (
            projection_key(resource.creator_id, object_name, BUNDLE, ALLOW, COMMUNITY_BUNDLE, SYSTEM),
            resource.creator_id, object_name, BUNDLE, ALLOW, COMMUNITY_BUNDLE, SYSTEM, SYSTEM,
        ),
        (
            projection_key(resource.creator_id, object_name, "authorize", ALLOW, SYSTEM_DERIVED, SYSTEM),
            resource.creator_id, object_name, "authorize", ALLOW, SYSTEM_DERIVED, SYSTEM, SYSTEM,
        ),
    ]


def insert_resource_grants(cursor, resource: Resource) -> None:
    for grant_id, accessor, object_name, operation, effect, source, authority, created_by in create_grant_rows(resource):
        projection = projection_key(accessor, object_name, operation, effect, source, authority)
        cursor.execute(
            "INSERT INTO authorization_grant "
            "(grant_id, projection_key, accessor_id, object, operation, effect, policy_source, authority_source, created_by, created_at, updated_at) "
            "VALUES (%s, %s, %s, %s, %s, %s, %s, %s, %s, UTC_TIMESTAMP(), UTC_TIMESTAMP()) "
            "ON DUPLICATE KEY UPDATE grant_id = grant_id",
            (grant_id, projection, accessor, object_name, operation, effect, source, authority, created_by),
        )
        cursor.execute(
            "SELECT 1 FROM casbin_rule WHERE ptype = 'p' AND v0 = %s AND v1 = %s AND v2 = %s AND v3 = %s AND v4 = %s AND v5 = %s LIMIT 1",
            (accessor, object_name, operation, effect, source, authority),
        )
        if cursor.fetchone() is None:
            cursor.execute(
                "INSERT INTO casbin_rule (ptype, v0, v1, v2, v3, v4, v5) VALUES ('p', %s, %s, %s, %s, %s, %s)",
                (accessor, object_name, operation, effect, source, authority),
            )


def report(items: Sequence[PlanItem], mode: str) -> dict[str, object]:
    counts = Counter(item.action for item in items)
    return {
        "migration": "network_builder_object_scope",
        "mode": mode,
        "generated_at": datetime.now(timezone.utc).isoformat(),
        "summary": dict(sorted(counts.items())),
        "items": [asdict(item) for item in items],
    }


def run(args: argparse.Namespace) -> dict[str, object]:
    bkn = connect(args.bkn_dsn)
    safe = connect(args.safe_dsn)
    try:
        with bkn.cursor() as bkn_cursor, safe.cursor() as safe_cursor:
            items: list[PlanItem] = []
            resources = fetch_resources(bkn_cursor)
            for resource in resources:
                exists = creator_exists(safe_cursor, resource.creator_id) if resource.creator_id else False
                grants = creator_grants(safe_cursor, resource) if resource.creator_id else []
                items.append(plan_resource(resource, exists, grants))
            output = report(items, args.mode)
            blocked = [item for item in items if item.action == "block"]
            if blocked:
                return output
            if args.mode == "apply":
                if args.confirm_apply != "backfill-creator-grants":
                    raise MigrationError("apply requires --confirm-apply backfill-creator-grants")
                for resource, item in zip(resources, items):
                    if item.action == "insert":
                        insert_resource_grants(safe_cursor, resource)
                safe.commit()
            return output
    except Exception:
        safe.rollback()
        raise
    finally:
        bkn.close()
        safe.close()


def main(argv: Sequence[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--mode", choices=("dry-run", "apply"), required=True)
    parser.add_argument("--bkn-dsn", required=True, help="BKN metadata MySQL DSN")
    parser.add_argument("--safe-dsn", required=True, help="bkn-safe MySQL DSN")
    parser.add_argument("--confirm-apply", default="")
    args = parser.parse_args(argv)
    try:
        output = run(args)
    except MigrationError as exc:
        print(json.dumps({"migration": "network_builder_object_scope", "error": str(exc)}, indent=2), file=sys.stderr)
        return 2
    print(json.dumps(output, ensure_ascii=False, indent=2, sort_keys=True))
    if output["summary"].get("block", 0):
        return 2
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
