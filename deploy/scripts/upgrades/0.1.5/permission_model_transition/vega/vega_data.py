#!/usr/bin/env python3
# Copyright openbkn.ai
#
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

"""Backfill Vega authorization state for the 0.1.5 permission transition."""

from __future__ import annotations

import argparse
import hashlib
import json
import sys
import traceback
from collections import Counter
from dataclasses import asdict, dataclass, field
from datetime import datetime, timezone
from pathlib import Path
from typing import Any, Iterable, Optional, Sequence

# The release-level database helpers remain owned by the parent migration.
PARENT_DIRECTORY = Path(__file__).resolve().parent.parent
if str(PARENT_DIRECTORY) not in sys.path:
    sys.path.insert(0, str(PARENT_DIRECTORY))

from bkn_data import (
    DBConfig,
    MigrationError,
    connect_database,
    database_config,
    normalize_text,
    require_safe_proxy_schema,
)


CATALOG_TYPE = "catalog"
RESOURCE_TYPE = "resource"
EFFECT_ALLOW = "allow"
COMMUNITY_BUNDLE = "community_bundle"
SYSTEM_DERIVED = "system_derived"
AUTHORITY_SYSTEM = "system"
FULL_BUSINESS_ACCESS = "full_business_access"
AUTHORIZE = "authorize"


@dataclass(frozen=True)
class Catalog:
    catalog_id: str
    creator_id: str
    creator_type: str
    builtin: bool


@dataclass(frozen=True, order=True)
class ResourceParent:
    resource_id: str
    catalog_id: str


@dataclass(frozen=True)
class Grant:
    grant_id: str
    projection_key: str
    accessor_id: str
    object: str
    operation: str
    effect: str
    policy_source: str
    authority_source: str
    created_by: str

    @property
    def casbin_tuple(self) -> tuple[str, ...]:
        return (
            self.accessor_id,
            self.object,
            self.operation,
            self.effect,
            self.policy_source,
            self.authority_source,
        )


@dataclass(frozen=True)
class Failure:
    code: str
    resource_type: str
    resource_id: str
    detail: str


@dataclass
class MigrationPlan:
    catalogs: list[Catalog] = field(default_factory=list)
    parents: list[ResourceParent] = field(default_factory=list)
    grants: list[Grant] = field(default_factory=list)
    failures: list[Failure] = field(default_factory=list)
    skipped_creator_accounts: list[Catalog] = field(default_factory=list)
    hidden_resources: int = 0
    existing_parents: int = 0
    existing_grants: int = 0


def stable_key(parts: Iterable[str]) -> str:
    """Match bkn-safe's deterministic grant and projection identity."""
    return hashlib.sha256("\x00".join(parts).encode("utf-8")).hexdigest()


def is_valid_resource_id(resource_type: str, resource_id: str) -> bool:
    """Match the concrete-instance constraints enforced by bkn-safe writers."""
    return (
        bool(resource_id)
        and resource_id.strip() == resource_id
        and "*" not in resource_id
        and len(f"{resource_type}:{resource_id}".encode("utf-8")) <= 100
    )


def creator_grants(catalog: Catalog) -> tuple[Grant, Grant]:
    """Return the canonical grants installed by current Catalog creation."""
    object_name = f"{CATALOG_TYPE}:{catalog.catalog_id}"
    specs = (
        (FULL_BUSINESS_ACCESS, COMMUNITY_BUNDLE),
        (AUTHORIZE, SYSTEM_DERIVED),
    )
    grants = []
    for operation, source in specs:
        values = (
            catalog.creator_id,
            object_name,
            operation,
            EFFECT_ALLOW,
            source,
            AUTHORITY_SYSTEM,
        )
        identity = stable_key(values)
        grants.append(
            Grant(
                grant_id=identity,
                projection_key=identity,
                accessor_id=catalog.creator_id,
                object=object_name,
                operation=operation,
                effect=EFFECT_ALLOW,
                policy_source=source,
                authority_source=AUTHORITY_SYSTEM,
                created_by=AUTHORITY_SYSTEM,
            )
        )
    return grants[0], grants[1]


def load_vega_rows(connection) -> tuple[list[Catalog], list[ResourceParent]]:
    """Load authoritative Catalog creators and Resource ownership edges."""
    with connection.cursor() as cursor:
        cursor.execute(
            "SELECT f_id, f_creator, f_creator_type, f_builtin "
            "FROM t_catalog ORDER BY f_id"
        )
        catalogs = [
            Catalog(
                normalize_text(row["f_id"]),
                normalize_text(row["f_creator"]),
                normalize_text(row["f_creator_type"]),
                bool(row["f_builtin"]),
            )
            for row in cursor.fetchall()
        ]
        cursor.execute(
            "SELECT f_id, f_catalog_id FROM t_resource ORDER BY f_id"
        )
        parents = [
            ResourceParent(
                normalize_text(row["f_id"]),
                normalize_text(row["f_catalog_id"]),
            )
            for row in cursor.fetchall()
        ]
    return catalogs, parents


def load_safe_state(connection) -> tuple[set[str], int, set[str]]:
    """Load accounts and existing canonical Vega authorization rows."""
    require_safe_proxy_schema(connection)
    with connection.cursor() as cursor:
        cursor.execute("SELECT id FROM users")
        users = {normalize_text(row["id"]) for row in cursor.fetchall()}
        cursor.execute(
            "SELECT COUNT(*) AS count FROM resource_parents "
            "WHERE resource_type_id = %s",
            (RESOURCE_TYPE,),
        )
        parent_count = int(cursor.fetchone()["count"])
        cursor.execute(
            "SELECT grant_id FROM authorization_grant "
            "WHERE object LIKE %s AND authority_source = %s AND "
            "policy_source IN (%s, %s)",
            (f"{CATALOG_TYPE}:%", AUTHORITY_SYSTEM, COMMUNITY_BUNDLE, SYSTEM_DERIVED),
        )
        grant_ids = {normalize_text(row["grant_id"]) for row in cursor.fetchall()}
    return users, parent_count, grant_ids


def build_plan(
    catalogs: Sequence[Catalog],
    parents: Sequence[ResourceParent],
    users: set[str],
    existing_parents: int = 0,
    existing_grant_ids: Optional[set[str]] = None,
) -> MigrationPlan:
    """Validate authoritative Vega rows and construct the complete Safe state."""
    plan = MigrationPlan(
        catalogs=list(catalogs),
        parents=sorted(set(parents)),
        existing_parents=existing_parents,
    )
    catalog_ids: set[str] = set()
    for catalog in catalogs:
        if not is_valid_resource_id(CATALOG_TYPE, catalog.catalog_id):
            plan.failures.append(
                Failure(
                    "invalid_catalog_id",
                    CATALOG_TYPE,
                    catalog.catalog_id,
                    "invalid concrete authorization resource ID",
                )
            )
            continue
        if catalog.catalog_id in catalog_ids:
            plan.failures.append(
                Failure("duplicate_catalog_id", CATALOG_TYPE, catalog.catalog_id, "duplicate f_id")
            )
            continue
        catalog_ids.add(catalog.catalog_id)
        if catalog.builtin:
            continue
        if not catalog.creator_id:
            plan.failures.append(
                Failure("missing_creator", CATALOG_TYPE, catalog.catalog_id, "empty f_creator")
            )
            continue
        if catalog.creator_type.lower() not in {"user", "app"}:
            plan.failures.append(
                Failure(
                    "invalid_creator_type",
                    CATALOG_TYPE,
                    catalog.catalog_id,
                    f"unsupported f_creator_type {catalog.creator_type!r}",
                )
            )
            continue
        if catalog.creator_id not in users:
            # A deleted account has no Safe subject to receive a grant. It must
            # not prevent the release upgrade from reconciling other catalogs
            # and all resource-parent rows.
            plan.skipped_creator_accounts.append(catalog)
            continue
        plan.grants.extend(creator_grants(catalog))

    seen_resources: dict[str, str] = {}
    plan.parents = []
    for parent in sorted(set(parents)):
        # Resource rows without a Catalog are hidden data, not standalone
        # Resources. They are intentionally outside the authorization model.
        if not parent.catalog_id:
            plan.hidden_resources += 1
            continue
        if not is_valid_resource_id(RESOURCE_TYPE, parent.resource_id):
            plan.failures.append(
                Failure(
                    "invalid_resource_id",
                    RESOURCE_TYPE,
                    parent.resource_id,
                    "invalid concrete authorization resource ID",
                )
            )
            continue
        previous = seen_resources.get(parent.resource_id)
        if previous is not None and previous != parent.catalog_id:
            plan.failures.append(
                Failure(
                    "conflicting_parent",
                    RESOURCE_TYPE,
                    parent.resource_id,
                    f"catalogs {previous!r} and {parent.catalog_id!r}",
                )
        )
        seen_resources[parent.resource_id] = parent.catalog_id
        if parent.catalog_id not in catalog_ids:
            plan.failures.append(
                Failure(
                    "missing_parent",
                    RESOURCE_TYPE,
                    parent.resource_id,
                    f"catalog {parent.catalog_id!r} does not exist",
                )
            )
            continue
        plan.parents.append(parent)

    existing = existing_grant_ids or set()
    plan.existing_grants = sum(grant.grant_id in existing for grant in plan.grants)
    return plan


def apply_plan(connection, plan: MigrationPlan) -> None:
    """Atomically replace Vega parent edges and upsert canonical creator grants."""
    if plan.failures:
        raise MigrationError("cannot apply an invalid Vega migration plan")
    try:
        with connection.cursor() as cursor:
            cursor.execute(
                "DELETE FROM resource_parents WHERE resource_type_id = %s",
                (RESOURCE_TYPE,),
            )
            if plan.parents:
                cursor.executemany(
                    "INSERT INTO resource_parents "
                    "(resource_type_id, resource_id, parent_type_id, parent_id, updated_at) "
                    "VALUES (%s, %s, %s, %s, UTC_TIMESTAMP())",
                    [
                        (RESOURCE_TYPE, parent.resource_id, CATALOG_TYPE, parent.catalog_id)
                        for parent in plan.parents
                    ],
                )
            for grant in plan.grants:
                cursor.execute(
                    "INSERT INTO authorization_grant "
                    "(grant_id, projection_key, accessor_id, object, operation, effect, "
                    "policy_source, authority_source, created_by, created_at, updated_at) "
                    "VALUES (%s, %s, %s, %s, %s, %s, %s, %s, %s, UTC_TIMESTAMP(), UTC_TIMESTAMP()) "
                    "ON DUPLICATE KEY UPDATE grant_id = grant_id",
                    (
                        grant.grant_id,
                        grant.projection_key,
                        grant.accessor_id,
                        grant.object,
                        grant.operation,
                        grant.effect,
                        grant.policy_source,
                        grant.authority_source,
                        grant.created_by,
                    ),
                )
                cursor.execute(
                    "SELECT 1 FROM casbin_rule WHERE ptype = 'p' AND v0 = %s AND v1 = %s "
                    "AND v2 = %s AND v3 = %s AND v4 = %s AND v5 = %s LIMIT 1",
                    grant.casbin_tuple,
                )
                if cursor.fetchone() is None:
                    cursor.execute(
                        "INSERT INTO casbin_rule (ptype, v0, v1, v2, v3, v4, v5) "
                        "VALUES ('p', %s, %s, %s, %s, %s, %s)",
                        grant.casbin_tuple,
                    )
        verify_plan(connection, plan)
        connection.commit()
    except Exception:
        connection.rollback()
        raise


def verify_plan(connection, plan: MigrationPlan) -> None:
    """Verify the exact parent inventory and every canonical grant projection."""
    with connection.cursor() as cursor:
        cursor.execute(
            "SELECT resource_id, parent_type_id, parent_id FROM resource_parents "
            "WHERE resource_type_id = %s ORDER BY resource_id",
            (RESOURCE_TYPE,),
        )
        actual = [
            ResourceParent(normalize_text(row["resource_id"]), normalize_text(row["parent_id"]))
            for row in cursor.fetchall()
            if normalize_text(row["parent_type_id"]) == CATALOG_TYPE
        ]
        if actual != plan.parents:
            raise MigrationError("Vega resource-parent verification failed")
        for grant in plan.grants:
            cursor.execute(
                "SELECT accessor_id, object, operation, effect, policy_source, "
                "authority_source, created_by, projection_key FROM authorization_grant "
                "WHERE grant_id = %s",
                (grant.grant_id,),
            )
            row = cursor.fetchone()
            if row is None or any(
                normalize_text(row[field]) != getattr(grant, field)
                for field in (
                    "accessor_id",
                    "object",
                    "operation",
                    "effect",
                    "policy_source",
                    "authority_source",
                    "created_by",
                    "projection_key",
                )
            ):
                raise MigrationError(f"Vega creator grant verification failed: {grant.grant_id}")
            cursor.execute(
                "SELECT COUNT(*) AS count FROM casbin_rule WHERE ptype = 'p' AND v0 = %s "
                "AND v1 = %s AND v2 = %s AND v3 = %s AND v4 = %s AND v5 = %s",
                grant.casbin_tuple,
            )
            if int(cursor.fetchone()["count"]) != 1:
                raise MigrationError(f"Vega creator policy verification failed: {grant.grant_id}")


def migration_report(mode: str, plan: MigrationPlan) -> dict[str, Any]:
    """Return the Vega step's machine-readable report."""
    return {
        "migration": "vega_authorization_backfill",
        "mode": mode,
        "generated_at": datetime.now(timezone.utc).isoformat(),
        "catalogs": {
            "total": len(plan.catalogs),
            "builtin_skipped": sum(catalog.builtin for catalog in plan.catalogs),
            "creator_grants": len(plan.grants),
            "existing_creator_grants": plan.existing_grants,
            "creator_accounts_skipped": [
                {
                    "catalog_id": catalog.catalog_id,
                    "creator_id": catalog.creator_id,
                }
                for catalog in plan.skipped_creator_accounts
            ],
        },
        "resource_parents": {
            "existing": plan.existing_parents,
            "planned": len(plan.parents),
            "hidden_resources_skipped": plan.hidden_resources,
        },
        "failures": [asdict(failure) for failure in plan.failures],
        "failure_summary": dict(sorted(Counter(item.code for item in plan.failures).items())),
    }


def write_report(report: dict[str, Any], output_path: str = "") -> None:
    content = json.dumps(report, indent=2, sort_keys=True) + "\n"
    if output_path:
        with open(output_path, "w", encoding="utf-8") as output:
            output.write(content)
    else:
        print(content, end="")


def database_configs() -> tuple[DBConfig, DBConfig]:
    """Resolve Vega and Safe connections from the shared server environment."""
    server = database_config("BKN_DB", "openbkn")
    return (
        database_config("VEGA_DB", "openbkn", fallback=server),
        database_config("SAFE_DB", "safe", fallback=server),
    )


def run(mode: str, report_path: str = "") -> int:
    """Plan or apply the Vega portion of the release migration."""
    if mode not in {"dry-run", "apply"}:
        raise MigrationError(f"unsupported migration mode: {mode}")
    vega_connection = None
    safe_connection = None
    try:
        vega_config, safe_config = database_configs()
        vega_connection = connect_database(vega_config)
        safe_connection = connect_database(safe_config)
        catalogs, parents = load_vega_rows(vega_connection)
        users, existing_parents, existing_grants = load_safe_state(safe_connection)
        plan = build_plan(
            catalogs,
            parents,
            users,
            existing_parents,
            existing_grants,
        )
        if plan.failures:
            write_report(migration_report(mode, plan), report_path)
            raise MigrationError("Vega migration validation failed")
        if mode == "apply":
            apply_plan(safe_connection, plan)
        write_report(migration_report(mode, plan), report_path)
        return 0
    finally:
        if vega_connection is not None:
            vega_connection.close()
        if safe_connection is not None:
            safe_connection.close()


def parse_args(argv: Optional[Sequence[str]] = None) -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--mode", choices=("dry-run", "apply"), required=True)
    parser.add_argument("--report", default="", help="write the JSON step report here")
    return parser.parse_args(argv)


def main(argv: Optional[Sequence[str]] = None) -> int:
    try:
        args = parse_args(argv)
        return run(args.mode, args.report)
    except KeyboardInterrupt:
        print("Migration interrupted.", file=sys.stderr)
        return 130
    except Exception:
        traceback.print_exc()
        return 1


if __name__ == "__main__":
    sys.exit(main())
