#!/usr/bin/env python3
# Copyright openbkn.ai
#
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

"""Rebuild knowledge-network authorization data in bkn-safe."""

from __future__ import annotations

import argparse
import json
import os
import sys
from collections import Counter, defaultdict
from dataclasses import asdict, dataclass, field
from pathlib import Path
from typing import Any, Iterable, Mapping, Optional, Sequence


MAIN_BRANCH = "main"
KN_RESOURCE_TYPE = "knowledge_network"
NETWORK_BUILDER_ROLE_ID = "1572fb82-526f-11f0-bde6-e674ec8dde71"
KN_CREATOR_OPERATIONS = (
    "view_detail",
    "modify",
    "delete",
    "query_data",
    "authorize",
    "task_manage",
)


@dataclass(frozen=True)
class ResourceSpec:
    resource_type: str
    table: str
    root: bool = False
    creator_operations: tuple[str, ...] = ()


RESOURCE_SPECS = (
    ResourceSpec(
        KN_RESOURCE_TYPE,
        "t_knowledge_network",
        root=True,
        creator_operations=KN_CREATOR_OPERATIONS,
    ),
    ResourceSpec("concept_group", "t_concept_group"),
    ResourceSpec("object_type", "t_object_type"),
    ResourceSpec("relation_type", "t_relation_type"),
    ResourceSpec("action_type", "t_action_type", creator_operations=("execute",)),
    ResourceSpec("metric", "t_metric_definition"),
    ResourceSpec("risk_type", "t_risk_type"),
)

# These tables carry branch-qualified data related to the seven resource tables.
BRANCH_TABLES = (
    "t_knowledge_network",
    "t_object_type",
    "t_object_type_status",
    "t_relation_type",
    "t_action_type",
    "t_concept_group",
    "t_concept_group_relation",
    "t_action_schedule",
    "t_risk_type",
    "t_metric_definition",
)


@dataclass(frozen=True)
class DBConfig:
    host: str
    port: int
    user: str
    password: str
    database: str


@dataclass(frozen=True)
class ResourceRow:
    resource_type: str
    table: str
    resource_id: str
    kn_id: str
    branch: str
    creator_id: str = ""
    creator_type: str = ""

    @property
    def normalized_branch(self) -> str:
        return self.branch if self.branch else MAIN_BRANCH

    @property
    def safe_id(self) -> str:
        if self.resource_type == KN_RESOURCE_TYPE:
            return self.resource_id
        return f"{self.kn_id}/{self.resource_id}"


@dataclass(frozen=True)
class SafeAccount:
    account_id: str
    enabled: bool
    account_type: str


@dataclass(frozen=True, order=True)
class Policy:
    accessor_id: str
    resource_type: str
    resource_id: str
    operation: str

    @property
    def object_key(self) -> str:
        return f"{self.resource_type}:{self.resource_id}"


@dataclass(frozen=True, order=True)
class ResourceParent:
    resource_type: str
    resource_id: str
    parent_type: str
    parent_id: str


@dataclass(frozen=True)
class Failure:
    code: str
    resource_type: str
    resource_id: str
    detail: str


@dataclass
class MigrationPlan:
    resources: dict[str, int]
    branch_updates: int
    policies: list[Policy] = field(default_factory=list)
    parents: list[ResourceParent] = field(default_factory=list)
    failures: list[Failure] = field(default_factory=list)
    existing_policies: int = 0
    existing_parents: int = 0

    def report(self, mode: str, status: str) -> dict[str, Any]:
        return {
            "mode": mode,
            "status": status,
            "resources": self.resources,
            "branch_updates": self.branch_updates,
            "policies": {
                "delete": self.existing_policies,
                "create": len(self.policies),
            },
            "resource_parents": {
                "delete": self.existing_parents,
                "create": len(self.parents),
            },
            "failed": len(self.failures),
            "failures": [asdict(item) for item in self.failures],
        }


class MigrationError(RuntimeError):
    """Raised when the migration cannot complete safely."""


def normalize_text(value: Any) -> str:
    """Convert a nullable database value to text without trimming it."""
    if value is None:
        return ""
    return str(value)


def is_valid_resource_id(resource_type: str, resource_id: str) -> bool:
    """Validate an ID using the runtime authorization ID contract."""
    if not resource_id or resource_id.strip() != resource_id:
        return False
    if "/" in resource_id or "*" in resource_id:
        return False
    return len(f"{resource_type}:{resource_id}".encode("utf-8")) <= 100


def is_valid_row_id(row: ResourceRow) -> bool:
    """Validate the business ID components and the resulting Safe object key."""
    if not is_valid_resource_id(KN_RESOURCE_TYPE, row.kn_id or row.resource_id):
        return False
    if row.resource_type != KN_RESOURCE_TYPE and not is_valid_resource_id(
        row.resource_type, row.resource_id
    ):
        return False
    return len(f"{row.resource_type}:{row.safe_id}".encode("utf-8")) <= 100


def creator_matches_account(creator_type: str, account: SafeAccount) -> bool:
    """Check whether a BKN creator type maps to the Safe account row."""
    if creator_type == "app":
        return account.account_type == "app"
    if creator_type == "user":
        return account.account_type != "app"
    return False


def build_plan(
    rows: Sequence[ResourceRow],
    accounts: Mapping[str, SafeAccount],
    branch_updates: int,
    existing_policies: int = 0,
    existing_parents: int = 0,
) -> MigrationPlan:
    """Build and validate the desired authorization state."""
    failures: list[Failure] = []
    grouped: dict[tuple[str, str], list[ResourceRow]] = defaultdict(list)
    valid_rows: list[ResourceRow] = []

    for row in rows:
        if not is_valid_row_id(row):
            failures.append(
                Failure(
                    "invalid_resource_id",
                    row.resource_type,
                    row.safe_id,
                    "invalid business ID component or authorization object key",
                )
            )
            continue
        grouped[(row.resource_type, row.safe_id)].append(row)

    conflicted: set[tuple[str, str]] = set()
    for key, same_resource in grouped.items():
        branches = sorted({row.normalized_branch for row in same_resource})
        if len(same_resource) > 1:
            conflicted.add(key)
            failures.append(
                Failure(
                    "branch_conflict",
                    key[0],
                    key[1],
                    f"resource resolves from multiple rows; normalized branches={branches}",
                )
            )
            continue
        valid_rows.append(same_resource[0])

    root_keys = {
        (row.resource_id, row.normalized_branch)
        for row in valid_rows
        if row.resource_type == KN_RESOURCE_TYPE
        and (row.resource_type, row.safe_id) not in conflicted
    }
    policies: set[Policy] = {
        Policy(
            NETWORK_BUILDER_ROLE_ID,
            KN_RESOURCE_TYPE,
            "*",
            "create",
        )
    }
    parents: set[ResourceParent] = set()
    spec_by_type = {spec.resource_type: spec for spec in RESOURCE_SPECS}

    for row in valid_rows:
        spec = spec_by_type[row.resource_type]
        if not spec.root:
            if (row.kn_id, row.normalized_branch) not in root_keys:
                failures.append(
                    Failure(
                        "missing_parent",
                        row.resource_type,
                        row.safe_id,
                        f"knowledge network {row.kn_id!r} does not exist in branch {row.normalized_branch!r}",
                    )
                )
                continue
            parents.add(
                ResourceParent(
                    row.resource_type,
                    row.safe_id,
                    KN_RESOURCE_TYPE,
                    row.kn_id,
                )
            )

        if not spec.creator_operations:
            continue
        creator_type = row.creator_type.strip()
        creator_id = row.creator_id.strip()
        account = accounts.get(creator_id)
        if (
            not creator_id
            or creator_id != row.creator_id
            or creator_type != row.creator_type
            or creator_type not in {"user", "app"}
        ):
            failures.append(
                Failure(
                    "invalid_creator",
                    row.resource_type,
                    row.safe_id,
                    f"creator ID or type is invalid; creator_type={creator_type!r}",
                )
            )
            continue
        if account is None:
            failures.append(
                Failure(
                    "creator_not_found",
                    row.resource_type,
                    row.safe_id,
                    f"creator {creator_id!r} does not exist in bkn-safe",
                )
            )
            continue
        if not account.enabled:
            failures.append(
                Failure(
                    "creator_disabled",
                    row.resource_type,
                    row.safe_id,
                    f"creator {creator_id!r} is disabled in bkn-safe",
                )
            )
            continue
        if not creator_matches_account(creator_type, account):
            failures.append(
                Failure(
                    "creator_type_mismatch",
                    row.resource_type,
                    row.safe_id,
                    f"creator_type={creator_type!r}, safe account_type={account.account_type!r}",
                )
            )
            continue
        for operation in spec.creator_operations:
            policies.add(
                Policy(creator_id, row.resource_type, row.safe_id, operation)
            )

    resource_counts = Counter(row.resource_type for row in rows)
    return MigrationPlan(
        resources={
            spec.resource_type: resource_counts.get(spec.resource_type, 0)
            for spec in RESOURCE_SPECS
        },
        branch_updates=branch_updates,
        policies=sorted(policies),
        parents=sorted(parents),
        failures=failures,
        existing_policies=existing_policies,
        existing_parents=existing_parents,
    )


def connect_database(config: DBConfig):
    """Open a PyMySQL connection using a dictionary cursor."""
    try:
        import pymysql
    except ImportError as exc:
        raise MigrationError(
            "PyMySQL is required; install it with 'python3 -m pip install pymysql==1.1.0'"
        ) from exc
    return pymysql.connect(
        host=config.host,
        port=config.port,
        user=config.user,
        password=config.password,
        database=config.database,
        charset="utf8mb4",
        autocommit=False,
        cursorclass=pymysql.cursors.DictCursor,
    )


def load_resources(connection) -> list[ResourceRow]:
    """Read all seven KN resource types from the BKN database."""
    rows: list[ResourceRow] = []
    with connection.cursor() as cursor:
        for spec in RESOURCE_SPECS:
            if spec.root:
                query = (
                    "SELECT f_id AS resource_id, '' AS kn_id, f_branch AS branch, "
                    "f_creator AS creator_id, f_creator_type AS creator_type "
                    f"FROM `{spec.table}` ORDER BY f_id, f_branch"
                )
            elif spec.creator_operations:
                query = (
                    "SELECT f_id AS resource_id, f_kn_id AS kn_id, f_branch AS branch, "
                    "f_creator AS creator_id, f_creator_type AS creator_type "
                    f"FROM `{spec.table}` ORDER BY f_kn_id, f_id, f_branch"
                )
            else:
                query = (
                    "SELECT f_id AS resource_id, f_kn_id AS kn_id, f_branch AS branch, "
                    "'' AS creator_id, '' AS creator_type "
                    f"FROM `{spec.table}` ORDER BY f_kn_id, f_id, f_branch"
                )
            cursor.execute(query)
            for item in cursor.fetchall():
                rows.append(
                    ResourceRow(
                        resource_type=spec.resource_type,
                        table=spec.table,
                        resource_id=normalize_text(item["resource_id"]),
                        kn_id=normalize_text(item["kn_id"]),
                        branch=normalize_text(item["branch"]),
                        creator_id=normalize_text(item["creator_id"]),
                        creator_type=normalize_text(item["creator_type"]),
                    )
                )
    return rows


def chunks(values: Sequence[str], size: int = 500) -> Iterable[Sequence[str]]:
    """Yield bounded SQL parameter batches."""
    for offset in range(0, len(values), size):
        yield values[offset : offset + size]


def load_accounts(connection, creator_ids: Sequence[str]) -> dict[str, SafeAccount]:
    """Load the Safe account rows referenced by creator policies."""
    accounts: dict[str, SafeAccount] = {}
    unique_ids = sorted({item for item in creator_ids if item})
    with connection.cursor() as cursor:
        for batch in chunks(unique_ids):
            placeholders = ",".join(["%s"] * len(batch))
            cursor.execute(
                f"SELECT id, enabled, account_type FROM users WHERE id IN ({placeholders})",
                tuple(batch),
            )
            for item in cursor.fetchall():
                account_id = normalize_text(item["id"])
                accounts[account_id] = SafeAccount(
                    account_id=account_id,
                    enabled=bool(item["enabled"]),
                    account_type=normalize_text(item["account_type"]),
                )
    return accounts


def count_branch_updates(connection) -> int:
    """Count blank branch values across BKN branch-qualified tables."""
    total = 0
    with connection.cursor() as cursor:
        for table in BRANCH_TABLES:
            cursor.execute(
                f"SELECT COUNT(*) AS count FROM `{table}` "
                "WHERE f_branch = '' OR f_branch IS NULL"
            )
            total += int(cursor.fetchone()["count"])
    return total


def typed_policy_predicate() -> tuple[str, tuple[str, ...]]:
    """Return the SQL predicate and parameters for the seven KN object types."""
    resource_types = tuple(spec.resource_type for spec in RESOURCE_SPECS)
    placeholders = ",".join(["%s"] * len(resource_types))
    return (
        f"ptype = 'p' AND LOCATE(':', v1) > 0 "
        f"AND SUBSTRING_INDEX(v1, ':', 1) IN ({placeholders})",
        resource_types,
    )


def load_existing_safe_counts(connection) -> tuple[int, int]:
    """Count Safe rows that the migration will replace."""
    predicate, parameters = typed_policy_predicate()
    resource_types = tuple(spec.resource_type for spec in RESOURCE_SPECS)
    placeholders = ",".join(["%s"] * len(resource_types))
    with connection.cursor() as cursor:
        cursor.execute(
            f"SELECT COUNT(*) AS count FROM casbin_rule WHERE {predicate}",
            parameters,
        )
        policy_count = int(cursor.fetchone()["count"])
        cursor.execute(
            "SELECT COUNT(*) AS count FROM resource_parents "
            f"WHERE resource_type_id IN ({placeholders})",
            resource_types,
        )
        parent_count = int(cursor.fetchone()["count"])
    return policy_count, parent_count


def normalize_branches(connection) -> int:
    """Normalize blank BKN branch values in one transaction."""
    updated = 0
    try:
        with connection.cursor() as cursor:
            for table in BRANCH_TABLES:
                updated += cursor.execute(
                    f"UPDATE `{table}` SET f_branch = %s "
                    "WHERE f_branch = '' OR f_branch IS NULL",
                    (MAIN_BRANCH,),
                )
        connection.commit()
        return updated
    except Exception:
        connection.rollback()
        raise


def apply_safe_plan(connection, plan: MigrationPlan) -> tuple[int, int]:
    """Replace all seven KN policy and parent sets in one Safe transaction."""
    predicate, parameters = typed_policy_predicate()
    resource_types = tuple(spec.resource_type for spec in RESOURCE_SPECS)
    placeholders = ",".join(["%s"] * len(resource_types))
    try:
        with connection.cursor() as cursor:
            deleted_policies = cursor.execute(
                f"DELETE FROM casbin_rule WHERE {predicate}", parameters
            )
            deleted_parents = cursor.execute(
                "DELETE FROM resource_parents "
                f"WHERE resource_type_id IN ({placeholders})",
                resource_types,
            )
            if plan.parents:
                cursor.executemany(
                    "INSERT INTO resource_parents "
                    "(resource_type_id, resource_id, parent_type_id, parent_id, updated_at) "
                    "VALUES (%s, %s, %s, %s, NOW())",
                    [
                        (
                            item.resource_type,
                            item.resource_id,
                            item.parent_type,
                            item.parent_id,
                        )
                        for item in plan.parents
                    ],
                )
            if plan.policies:
                cursor.executemany(
                    "INSERT INTO casbin_rule (ptype, v0, v1, v2, v3, v4, v5) "
                    "VALUES ('p', %s, %s, %s, '', '', '')",
                    [
                        (item.accessor_id, item.object_key, item.operation)
                        for item in plan.policies
                    ],
                )

            cursor.execute(
                f"SELECT COUNT(*) AS count FROM casbin_rule WHERE {predicate}",
                parameters,
            )
            if int(cursor.fetchone()["count"]) != len(plan.policies):
                raise MigrationError("policy verification failed")
            cursor.execute(
                "SELECT COUNT(*) AS count FROM resource_parents "
                f"WHERE resource_type_id IN ({placeholders})",
                resource_types,
            )
            if int(cursor.fetchone()["count"]) != len(plan.parents):
                raise MigrationError("resource-parent verification failed")
        connection.commit()
        return deleted_policies, deleted_parents
    except Exception:
        connection.rollback()
        raise


def database_config(args: argparse.Namespace, prefix: str) -> DBConfig:
    """Build one database configuration from parsed arguments."""
    key = prefix.lower()
    env_prefix = prefix.upper()
    return DBConfig(
        host=getattr(args, f"{key}_host"),
        port=getattr(args, f"{key}_port"),
        user=getattr(args, f"{key}_user"),
        password=getattr(args, f"{key}_password")
        or os.getenv(f"{env_prefix}_PASSWORD", ""),
        database=getattr(args, f"{key}_name"),
    )


def add_database_arguments(
    parser: argparse.ArgumentParser,
    prefix: str,
    default_name: str,
) -> None:
    """Add connection arguments for one database."""
    option = prefix.lower().replace("_", "-")
    destination = prefix.lower()
    env_prefix = prefix.upper()
    parser.add_argument(
        f"--{option}-host",
        dest=f"{destination}_host",
        default=os.getenv(f"{env_prefix}_HOST", "localhost"),
    )
    parser.add_argument(
        f"--{option}-port",
        dest=f"{destination}_port",
        type=int,
        default=int(os.getenv(f"{env_prefix}_PORT", "3306")),
    )
    parser.add_argument(
        f"--{option}-user",
        dest=f"{destination}_user",
        default=os.getenv(f"{env_prefix}_USER", "root"),
    )
    parser.add_argument(
        f"--{option}-password",
        dest=f"{destination}_password",
        default="",
        help=f"Password; prefer the {env_prefix}_PASSWORD environment variable",
    )
    parser.add_argument(
        f"--{option}-name",
        dest=f"{destination}_name",
        default=os.getenv(f"{env_prefix}_NAME", default_name),
    )


def parse_args(argv: Optional[Sequence[str]] = None) -> argparse.Namespace:
    """Parse command-line arguments."""
    parser = argparse.ArgumentParser(
        description="Rebuild BKN authorization policies and resource parents in bkn-safe."
    )
    mode = parser.add_mutually_exclusive_group(required=True)
    mode.add_argument("--dry-run", action="store_true", help="Validate and report only")
    mode.add_argument("--apply", action="store_true", help="Apply the validated plan")
    add_database_arguments(parser, "BKN_DB", "openbkn")
    add_database_arguments(parser, "SAFE_DB", "safe")
    parser.add_argument("--report", help="Optional path for the JSON report")
    return parser.parse_args(argv)


def write_report(report: Mapping[str, Any], report_path: Optional[str]) -> None:
    """Print the report and optionally persist it to a file."""
    content = json.dumps(report, ensure_ascii=False, indent=2, sort_keys=True)
    print(content)
    if report_path:
        Path(report_path).write_text(content + "\n", encoding="utf-8")


def run(args: argparse.Namespace) -> int:
    """Run a dry-run or apply migration."""
    bkn_connection = connect_database(database_config(args, "BKN_DB"))
    safe_connection = connect_database(database_config(args, "SAFE_DB"))
    try:
        rows = load_resources(bkn_connection)
        creator_ids = [
            row.creator_id.strip()
            for row in rows
            if row.resource_type in {KN_RESOURCE_TYPE, "action_type"}
        ]
        accounts = load_accounts(safe_connection, creator_ids)
        branch_updates = count_branch_updates(bkn_connection)
        existing_policies, existing_parents = load_existing_safe_counts(
            safe_connection
        )
        plan = build_plan(
            rows,
            accounts,
            branch_updates,
            existing_policies,
            existing_parents,
        )
        mode = "apply" if args.apply else "dry-run"
        if plan.failures:
            write_report(plan.report(mode, "validation_failed"), args.report)
            return 1
        if args.dry_run:
            write_report(plan.report(mode, "ready"), args.report)
            return 0

        actual_branch_updates = normalize_branches(bkn_connection)
        deleted_policies, deleted_parents = apply_safe_plan(safe_connection, plan)
        report = plan.report(mode, "completed")
        report["branch_updates"] = actual_branch_updates
        report["policies"]["delete"] = deleted_policies
        report["resource_parents"]["delete"] = deleted_parents
        write_report(report, args.report)
        return 0
    finally:
        bkn_connection.close()
        safe_connection.close()


def main(argv: Optional[Sequence[str]] = None) -> int:
    """CLI entry point."""
    try:
        args = parse_args(argv)
        return run(args)
    except KeyboardInterrupt:
        print(json.dumps({"status": "interrupted"}, indent=2), file=sys.stderr)
        return 130
    except Exception as exc:
        print(
            json.dumps(
                {"status": "failed", "error": str(exc)},
                ensure_ascii=False,
                indent=2,
            ),
            file=sys.stderr,
        )
        return 1


if __name__ == "__main__":
    sys.exit(main())
