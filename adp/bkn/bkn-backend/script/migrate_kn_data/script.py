#!/usr/bin/env python3
# Copyright openbkn.ai
#
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

"""Migrate OpenBKN 0.1.4 knowledge-network data offline for OpenBKN 0.1.5."""

from __future__ import annotations

import hashlib
import json
import os
import secrets
import sys
import traceback
from collections import Counter, defaultdict
from dataclasses import dataclass, field
from datetime import datetime, timezone
from typing import Any, Iterable, Optional


MAIN_BRANCH = "main"
KN_RESOURCE_TYPE = "knowledge_network"
NETWORK_BUILDER_ROLE_ID = "1572fb82-526f-11f0-bde6-e674ec8dde71"
MIGRATION_GRANTOR_ID = "266c6a42-6131-4d62-8f39-853e7093701c"
PUBLIC_ACCESSOR_ID = "00000000-0000-0000-0000-000000000000"
PROXY_SOURCE_TYPE = "kn_proxy_binding"
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


@dataclass(frozen=True, order=True)
class ProxySource:
    resource_type: str
    resource_id: str
    operation: str
    source_id: str
    kn_id: str
    binding_type: str
    binding_id: str


@dataclass
class ProxyNetworkPlan:
    kn_id: str
    kn_name: str
    proxy_account_id: str
    model_version: str
    sources: list[ProxySource]
    create_account: bool


@dataclass
class ProxyMigrationPlan:
    networks: list[ProxyNetworkPlan] = field(default_factory=list)
    failures: list[Failure] = field(default_factory=list)


class MigrationError(RuntimeError):
    """Raised when the migration cannot complete safely."""


def normalize_text(value: Any) -> str:
    """Convert a nullable database value to text without trimming it."""
    if value is None:
        return ""
    return str(value)


def utc_now() -> datetime:
    """Return a timezone-naive UTC timestamp accepted by MySQL DATETIME."""
    return datetime.now(timezone.utc).replace(tzinfo=None)


def decode_json(value: Any, default: Any, description: str) -> Any:
    """Decode one JSON database column and report the owning field on failure."""
    if value is None or value == "" or value == b"":
        return default
    if isinstance(value, (dict, list)):
        return value
    try:
        return json.loads(value)
    except (TypeError, ValueError) as exc:
        raise MigrationError(f"invalid JSON in {description}: {exc}") from exc


def stable_proxy_source_id(kn_id: str, binding_type: str, binding_id: str) -> str:
    """Match BKN Backend's stable source identity calculation."""
    raw = "\x00".join((kn_id, binding_type, binding_id)).encode("utf-8")
    return hashlib.sha256(raw).hexdigest()


def derive_proxy_sources(
    kn_id: str,
    object_rows: Sequence[Mapping[str, Any]],
    relation_rows: Sequence[Mapping[str, Any]],
    metric_rows: Sequence[Mapping[str, Any]],
    action_rows: Sequence[Mapping[str, Any]],
) -> tuple[list[ProxySource], str]:
    """Derive the same persisted-model proxy grants as BKN Backend."""
    object_resources: dict[str, str] = {}
    object_ids: set[str] = set()
    sources: list[ProxySource] = []
    bindings: list[dict[str, str]] = []
    seen: set[tuple[str, str, str, str]] = set()

    def add(
        binding_type: str,
        binding_id: str,
        resource_type: str,
        resource_id: str,
        operation: str,
        detail: str = "",
    ) -> None:
        binding_id = normalize_text(binding_id).strip()
        resource_id = normalize_text(resource_id).strip()
        if not binding_id or not resource_id:
            raise MigrationError(
                f"{binding_type} binding and target IDs are required for {kn_id}"
            )
        source_id = stable_proxy_source_id(kn_id, binding_type, binding_id)
        source = ProxySource(
            resource_type=resource_type,
            resource_id=resource_id,
            operation=operation,
            source_id=source_id,
            kn_id=kn_id,
            binding_type=binding_type,
            binding_id=binding_id,
        )
        key = (resource_type, resource_id, operation, source_id)
        if key not in seen:
            seen.add(key)
            sources.append(source)
        binding = {
            "type": binding_type,
            "id": binding_id,
            "target_type": resource_type,
            "target_id": resource_id,
        }
        if detail:
            binding["detail"] = detail
        bindings.append(binding)

    for row in object_rows:
        object_id = normalize_text(row["f_id"]).strip()
        if object_id:
            object_ids.add(object_id)
        data_source = decode_json(
            row.get("f_data_source"), {}, f"object type {object_id} data source"
        )
        if data_source and normalize_text(data_source.get("id")).strip():
            if normalize_text(data_source.get("type")).strip() != "resource":
                raise MigrationError(
                    f"object type {object_id} has unsupported data source type"
                )
            resource_id = normalize_text(data_source["id"]).strip()
            object_resources[object_id] = resource_id
            add("object_type", object_id, "resource", resource_id, "view_detail", "schema")
            add("object_type", object_id, "resource", resource_id, "query_data", "data")

        logic_properties = decode_json(
            row.get("f_logic_properties"), [], f"object type {object_id} logic properties"
        )
        for prop in logic_properties or []:
            data_source = prop.get("data_source") or {}
            if normalize_text(data_source.get("type")).strip() != "tool":
                continue
            box_id = normalize_text(data_source.get("box_id")).strip()
            tool_id = normalize_text(data_source.get("tool_id")).strip()
            if not box_id or not tool_id:
                continue
            property_name = normalize_text(prop.get("name"))
            property_binding = stable_proxy_source_id(
                kn_id, "logic_property", "\x00".join((object_id, property_name))
            )
            add(
                "logic_property",
                property_binding,
                "tool_box",
                box_id,
                "execute",
                ":".join((object_id, property_name, tool_id)),
            )

    for row in relation_rows:
        relation_id = normalize_text(row["f_id"]).strip()
        rules = decode_json(
            row.get("f_mapping_rules"), {}, f"relation type {relation_id} mapping rules"
        )
        if isinstance(rules, dict) and "backing_data_source" in rules:
            backing = rules.get("backing_data_source") or {}
            backing_id = normalize_text(backing.get("id")).strip()
            if backing_id:
                if normalize_text(backing.get("type")).strip() != "resource":
                    raise MigrationError(
                        f"relation type {relation_id} has unsupported backing data source type"
                    )
                add(
                    "relation_type",
                    relation_id,
                    "resource",
                    backing_id,
                    "query_data",
                    "backing_data_source",
                )
        for object_id in (
            normalize_text(row.get("f_source_object_type_id")).strip(),
            normalize_text(row.get("f_target_object_type_id")).strip(),
        ):
            if not object_id or object_id not in object_ids:
                continue
            resource_id = object_resources.get(object_id, "")
            if resource_id:
                add(
                    "relation_type",
                    relation_id,
                    "resource",
                    resource_id,
                    "query_data",
                    object_id,
                )

    for row in metric_rows:
        metric_id = normalize_text(row["f_id"]).strip()
        scope_type = normalize_text(row.get("f_scope_type")).strip()
        scope_ref = normalize_text(row.get("f_scope_ref")).strip()
        if not scope_type and not scope_ref:
            continue
        if scope_ref not in object_ids:
            continue
        resource_id = object_resources.get(scope_ref, "")
        if resource_id:
            add(
                "metric",
                metric_id,
                "resource",
                resource_id,
                "query_data",
                f"{scope_type}:{scope_ref}",
            )

    for row in action_rows:
        action_id = normalize_text(row["f_id"]).strip()
        action_source = decode_json(
            row.get("f_action_source"), {}, f"action type {action_id} action source"
        )
        source_type = normalize_text(action_source.get("type")).strip()
        if not source_type:
            continue
        if source_type == "tool":
            box_id = normalize_text(action_source.get("box_id")).strip()
            tool_id = normalize_text(action_source.get("tool_id")).strip()
            if box_id and tool_id:
                add(
                    "action_type",
                    action_id,
                    "tool_box",
                    box_id,
                    "execute",
                    f"tool:{tool_id}",
                )
        elif source_type == "mcp":
            mcp_id = normalize_text(action_source.get("mcp_id")).strip()
            tool_name = normalize_text(action_source.get("tool_name")).strip()
            if mcp_id and tool_name:
                add(
                    "action_type",
                    action_id,
                    "mcp",
                    mcp_id,
                    "execute",
                    f"tool:{tool_name}",
                )
        else:
            raise MigrationError(
                f"action type {action_id} has unsupported action source type"
            )

    sources.sort(
        key=lambda item: (
            item.source_id,
            item.resource_type,
            item.resource_id,
            item.operation,
        )
    )
    bindings.sort(
        key=lambda item: (
            item["type"],
            item["id"],
            item["target_type"],
            item["target_id"],
            item.get("detail", ""),
        )
    )
    canonical_text = json.dumps(
        bindings, ensure_ascii=False, separators=(",", ":")
    )
    # encoding/json escapes these characters even when it otherwise emits UTF-8.
    # Keep the digest identical to BKN Backend for every valid persisted ID.
    for literal, escaped in (
        ("&", "\\u0026"),
        ("<", "\\u003c"),
        (">", "\\u003e"),
        ("\u2028", "\\u2028"),
        ("\u2029", "\\u2029"),
    ):
        canonical_text = canonical_text.replace(literal, escaped)
    canonical = canonical_text.encode("utf-8")
    return sources, "sha256:" + hashlib.sha256(canonical).hexdigest()


def key_match(value: str, pattern: str) -> bool:
    """Implement the Casbin keyMatch form used by bkn-safe."""
    wildcard = pattern.find("*")
    if wildcard < 0:
        return value == pattern
    return value[:wildcard] == pattern[:wildcard]


class GrantIndex:
    """Evaluate a non-managed grantor from persisted bkn-safe authorization data."""

    def __init__(
        self,
        accessor_id: str,
        rules: Sequence[Mapping[str, Any]],
        parents: Mapping[tuple[str, str], tuple[str, str]],
        parent_operations: Mapping[tuple[str, str], str],
        registered_operations: set[tuple[str, str]],
    ) -> None:
        subjects = {accessor_id, PUBLIC_ACCESSOR_ID}
        grouping = defaultdict(set)
        for row in rules:
            if row["ptype"] == "g":
                grouping[normalize_text(row["v0"])].add(normalize_text(row["v1"]))
        pending = [accessor_id]
        while pending:
            subject = pending.pop()
            for role in grouping.get(subject, set()):
                if role not in subjects:
                    subjects.add(role)
                    pending.append(role)
        self.policies = [
            (normalize_text(row["v1"]), normalize_text(row["v2"]))
            for row in rules
            if row["ptype"] == "p" and normalize_text(row["v0"]) in subjects
        ]
        self.parents = parents
        self.parent_operations = parent_operations
        self.registered_operations = registered_operations

    def _direct(self, resource_type: str, resource_id: str, operation: str) -> bool:
        target = f"{resource_type}:{resource_id}"
        return any(
            key_match(target, pattern) and (allowed == "*" or allowed == operation)
            for pattern, allowed in self.policies
        )

    def allows(self, resource_type: str, resource_id: str, operation: str) -> bool:
        if (resource_type, operation) not in self.registered_operations:
            return False
        if self._direct(resource_type, resource_id, operation):
            return True
        node = (resource_type, resource_id)
        current_operation = operation
        visited = {node}
        for _ in range(64):
            parent_operation = self.parent_operations.get((node[0], current_operation), "")
            parent = self.parents.get(node)
            if not parent_operation or parent is None or parent in visited:
                return False
            if self._direct(parent[0], parent[1], parent_operation):
                return True
            visited.add(parent)
            node = parent
            current_operation = parent_operation
        return False


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


def table_exists(connection, table: str) -> bool:
    """Return whether a MySQL table exists in the selected database."""
    with connection.cursor() as cursor:
        cursor.execute("SHOW TABLES LIKE %s", (table,))
        return cursor.fetchone() is not None


def require_safe_proxy_schema(connection) -> None:
    """Require the 0.1.5 bkn-safe schema before offline data migration."""
    required = (
        "users",
        "managed_proxy_accounts",
        "proxy_grant_source",
        "proxy_grant_policy",
        "casbin_rule",
        "resource_parents",
        "operations",
    )
    missing = [table for table in required if not table_exists(connection, table)]
    if missing:
        raise MigrationError(
            "bkn-safe 0.1.5 schema is unavailable; missing tables: "
            + ", ".join(missing)
        )


def load_grant_index(connection, grantor_id: str) -> GrantIndex:
    """Load and validate the human/app authority backing migrated proxy sources."""
    with connection.cursor() as cursor:
        cursor.execute(
            "SELECT id, enabled FROM users WHERE id = %s", (grantor_id,)
        )
        grantor = cursor.fetchone()
        if grantor is None or not bool(grantor["enabled"]):
            raise MigrationError(f"grantor {grantor_id!r} is missing or disabled")
        cursor.execute(
            "SELECT COUNT(*) AS count FROM managed_proxy_accounts "
            "WHERE proxy_account_id = %s",
            (grantor_id,),
        )
        if int(cursor.fetchone()["count"]) > 0:
            raise MigrationError("grantor must not be a managed proxy account")
        cursor.execute("SELECT ptype, v0, v1, v2 FROM casbin_rule")
        rules = list(cursor.fetchall())
        cursor.execute(
            "SELECT resource_type_id, resource_id, parent_type_id, parent_id "
            "FROM resource_parents"
        )
        parents = {
            (normalize_text(row["resource_type_id"]), normalize_text(row["resource_id"])): (
                normalize_text(row["parent_type_id"]),
                normalize_text(row["parent_id"]),
            )
            for row in cursor.fetchall()
        }
        cursor.execute(
            "SELECT resource_type_id, id, parent_operation_id FROM operations"
        )
        operations = list(cursor.fetchall())
    parent_operations = {
        (normalize_text(row["resource_type_id"]), normalize_text(row["id"])): normalize_text(
            row["parent_operation_id"]
        )
        for row in operations
        if normalize_text(row["parent_operation_id"])
    }
    registered_operations = {
        (normalize_text(row["resource_type_id"]), normalize_text(row["id"]))
        for row in operations
    }
    return GrantIndex(
        grantor_id,
        rules,
        parents,
        parent_operations,
        registered_operations,
    )


def load_proxy_plan(
    bkn_connection, safe_connection, grantor_id: str
) -> ProxyMigrationPlan:
    """Build a complete, side-effect-free managed-proxy backfill plan."""
    require_safe_proxy_schema(safe_connection)
    if not table_exists(bkn_connection, "t_kn_proxy_account"):
        raise MigrationError(
            "BKN 0.1.5 schema is unavailable; missing table: t_kn_proxy_account"
        )
    authority = load_grant_index(safe_connection, grantor_id)
    with bkn_connection.cursor() as cursor:
        cursor.execute(
            "SELECT f_id, f_name FROM t_knowledge_network "
            "WHERE COALESCE(NULLIF(f_branch, ''), %s) = %s ORDER BY f_id",
            (MAIN_BRANCH, MAIN_BRANCH),
        )
        networks = list(cursor.fetchall())
        cursor.execute(
            "SELECT f_kn_id, f_id, f_data_source, f_logic_properties "
            "FROM t_object_type WHERE COALESCE(NULLIF(f_branch, ''), %s) = %s "
            "ORDER BY f_kn_id, f_id",
            (MAIN_BRANCH, MAIN_BRANCH),
        )
        object_rows = list(cursor.fetchall())
        cursor.execute(
            "SELECT f_kn_id, f_id, f_source_object_type_id, "
            "f_target_object_type_id, f_mapping_rules FROM t_relation_type "
            "WHERE COALESCE(NULLIF(f_branch, ''), %s) = %s ORDER BY f_kn_id, f_id",
            (MAIN_BRANCH, MAIN_BRANCH),
        )
        relation_rows = list(cursor.fetchall())
        cursor.execute(
            "SELECT f_kn_id, f_id, f_scope_type, f_scope_ref "
            "FROM t_metric_definition WHERE COALESCE(NULLIF(f_branch, ''), %s) = %s "
            "ORDER BY f_kn_id, f_id",
            (MAIN_BRANCH, MAIN_BRANCH),
        )
        metric_rows = list(cursor.fetchall())
        cursor.execute(
            "SELECT f_kn_id, f_id, f_action_source FROM t_action_type "
            "WHERE COALESCE(NULLIF(f_branch, ''), %s) = %s ORDER BY f_kn_id, f_id",
            (MAIN_BRANCH, MAIN_BRANCH),
        )
        action_rows = list(cursor.fetchall())
        cursor.execute(
            "SELECT f_kn_id, f_proxy_account_id FROM t_kn_proxy_account"
        )
        bkn_mappings = {
            normalize_text(row["f_kn_id"]): row for row in cursor.fetchall()
        }

    by_kn_objects: dict[str, list[Mapping[str, Any]]] = defaultdict(list)
    by_kn_relations: dict[str, list[Mapping[str, Any]]] = defaultdict(list)
    by_kn_metrics: dict[str, list[Mapping[str, Any]]] = defaultdict(list)
    by_kn_actions: dict[str, list[Mapping[str, Any]]] = defaultdict(list)
    for rows, destination in (
        (object_rows, by_kn_objects),
        (relation_rows, by_kn_relations),
        (metric_rows, by_kn_metrics),
        (action_rows, by_kn_actions),
    ):
        for row in rows:
            destination[normalize_text(row["f_kn_id"])].append(row)

    with safe_connection.cursor() as cursor:
        cursor.execute(
            "SELECT proxy_account_id, managed_resource_id, lifecycle_status "
            "FROM managed_proxy_accounts WHERE managed_by = 'bkn' "
            "AND managed_resource_type = 'knowledge_network'"
        )
        managed_by_kn = {
            normalize_text(row["managed_resource_id"]): row
            for row in cursor.fetchall()
        }
        proxy_ids = [normalize_text(row["proxy_account_id"]) for row in managed_by_kn.values()]
        safe_users: dict[str, Mapping[str, Any]] = {}
        if proxy_ids:
            placeholders = ",".join(["%s"] * len(proxy_ids))
            cursor.execute(
                "SELECT id, enabled, account_type, password_hash FROM users "
                f"WHERE id IN ({placeholders})",
                tuple(proxy_ids),
            )
            safe_users = {normalize_text(row["id"]): row for row in cursor.fetchall()}

    plan = ProxyMigrationPlan()
    known_networks = {normalize_text(row["f_id"]) for row in networks}
    for orphan_kn in sorted(set(managed_by_kn) - known_networks):
        plan.failures.append(
            Failure(
                "orphan_managed_proxy",
                KN_RESOURCE_TYPE,
                orphan_kn,
                "bkn-safe proxy has no main-branch knowledge network",
            )
        )

    proxy_owners: dict[str, str] = {}
    for kn_id, mapping in bkn_mappings.items():
        proxy_id = normalize_text(mapping["f_proxy_account_id"])
        previous = proxy_owners.get(proxy_id)
        if previous and previous != kn_id:
            plan.failures.append(
                Failure(
                    "duplicate_proxy_mapping",
                    KN_RESOURCE_TYPE,
                    kn_id,
                    f"proxy {proxy_id!r} is also mapped to {previous!r}",
                )
            )
        proxy_owners[proxy_id] = kn_id

    for network in networks:
        kn_id = normalize_text(network["f_id"])
        kn_name = normalize_text(network["f_name"])
        try:
            sources, model_version = derive_proxy_sources(
                kn_id,
                by_kn_objects[kn_id],
                by_kn_relations[kn_id],
                by_kn_metrics[kn_id],
                by_kn_actions[kn_id],
            )
            for source in sources:
                if not authority.allows(
                    source.resource_type, source.resource_id, source.operation
                ):
                    raise MigrationError(
                        f"grantor {grantor_id!r} lacks {source.operation} on "
                        f"{source.resource_type}:{source.resource_id}"
                    )
        except MigrationError as exc:
            plan.failures.append(
                Failure("invalid_proxy_plan", KN_RESOURCE_TYPE, kn_id, str(exc))
            )
            continue

        bkn_mapping = bkn_mappings.get(kn_id)
        managed = managed_by_kn.get(kn_id)
        if bkn_mapping and not managed:
            plan.failures.append(
                Failure(
                    "missing_managed_proxy",
                    KN_RESOURCE_TYPE,
                    kn_id,
                    "BKN mapping exists but bkn-safe ownership mapping is missing",
                )
            )
            continue
        if managed:
            proxy_id = normalize_text(managed["proxy_account_id"])
            if bkn_mapping and normalize_text(bkn_mapping["f_proxy_account_id"]) != proxy_id:
                plan.failures.append(
                    Failure(
                        "proxy_mapping_conflict",
                        KN_RESOURCE_TYPE,
                        kn_id,
                        "BKN and bkn-safe reference different proxy accounts",
                    )
                )
                continue
            user = safe_users.get(proxy_id)
            if (
                user is None
                or normalize_text(user["account_type"]) != "app"
                or normalize_text(user["password_hash"]) != ""
                or not bool(user["enabled"])
                or normalize_text(managed["lifecycle_status"]) != "active"
            ):
                plan.failures.append(
                    Failure(
                        "inconsistent_managed_proxy",
                        KN_RESOURCE_TYPE,
                        kn_id,
                        f"managed proxy {proxy_id!r} is not an active credential-free app",
                    )
                )
                continue
            create_account = False
        else:
            proxy_id = secrets.token_hex(16)
            create_account = True
        plan.networks.append(
            ProxyNetworkPlan(
                kn_id=kn_id,
                kn_name=kn_name,
                proxy_account_id=proxy_id,
                model_version=model_version,
                sources=sources,
                create_account=create_account,
            )
        )
    return plan


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


def normalize_branches(connection, commit: bool = True) -> int:
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
        if commit:
            connection.commit()
        return updated
    except Exception:
        connection.rollback()
        raise


def apply_safe_plan(
    connection, plan: MigrationPlan, commit: bool = True
) -> tuple[int, int]:
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
        if commit:
            connection.commit()
        return deleted_policies, deleted_parents
    except Exception:
        connection.rollback()
        raise


def casbin_policy_exists(
    cursor, proxy_id: str, resource_type: str, resource_id: str, operation: str
) -> bool:
    cursor.execute(
        "SELECT COUNT(*) AS count FROM casbin_rule WHERE ptype = 'p' "
        "AND v0 = %s AND v1 = %s AND v2 = %s",
        (proxy_id, f"{resource_type}:{resource_id}", operation),
    )
    return int(cursor.fetchone()["count"]) > 0


def sync_proxy_sources(
    cursor,
    network: ProxyNetworkPlan,
    grantor_id: str,
    timestamp: datetime,
) -> dict[str, int]:
    """Synchronize one proxy's source ledger and materialized Casbin policies."""
    cursor.execute(
        "SELECT id, resource_type, resource_id, operation, source_id, kn_id, "
        "binding_type, binding_id, lifecycle_status FROM proxy_grant_source "
        "WHERE proxy_account_id = %s AND source_type = %s",
        (network.proxy_account_id, PROXY_SOURCE_TYPE),
    )
    current_rows = list(cursor.fetchall())
    current = {
        (
            normalize_text(row["resource_type"]),
            normalize_text(row["resource_id"]),
            normalize_text(row["operation"]),
            normalize_text(row["source_id"]),
        ): row
        for row in current_rows
    }
    desired = {
        (source.resource_type, source.resource_id, source.operation, source.source_id): source
        for source in network.sources
    }
    added = 0
    reactivated = 0
    revoked = 0
    for key, source in desired.items():
        row = current.get(key)
        if row is None:
            cursor.execute(
                "INSERT INTO proxy_grant_source "
                "(id, proxy_account_id, resource_type, resource_id, operation, "
                "source_type, source_id, kn_id, binding_type, binding_id, granted_by, "
                "lifecycle_status, created_at, updated_at, revoked_at) "
                "VALUES (%s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, "
                "'active', %s, %s, NULL)",
                (
                    secrets.token_hex(16),
                    network.proxy_account_id,
                    source.resource_type,
                    source.resource_id,
                    source.operation,
                    PROXY_SOURCE_TYPE,
                    source.source_id,
                    source.kn_id,
                    source.binding_type,
                    source.binding_id,
                    grantor_id,
                    timestamp,
                    timestamp,
                ),
            )
            added += 1
            continue
        if (
            normalize_text(row["kn_id"]) != source.kn_id
            or normalize_text(row["binding_type"]) != source.binding_type
            or normalize_text(row["binding_id"]) != source.binding_id
        ):
            raise MigrationError(
                f"source identity conflict for knowledge network {network.kn_id}"
            )
        if normalize_text(row["lifecycle_status"]) != "active":
            reactivated += 1
        cursor.execute(
            "UPDATE proxy_grant_source SET granted_by = %s, lifecycle_status = 'active', "
            "revoked_at = NULL, updated_at = %s WHERE id = %s",
            (grantor_id, timestamp, row["id"]),
        )

    for key, row in current.items():
        if key in desired or normalize_text(row["lifecycle_status"]) != "active":
            continue
        cursor.execute(
            "UPDATE proxy_grant_source SET lifecycle_status = 'revoked', "
            "revoked_at = %s, updated_at = %s WHERE id = %s",
            (timestamp, timestamp, row["id"]),
        )
        revoked += 1

    permission_keys = {
        (source.resource_type, source.resource_id, source.operation)
        for source in network.sources
    }
    permission_keys.update(
        (
            normalize_text(row["resource_type"]),
            normalize_text(row["resource_id"]),
            normalize_text(row["operation"]),
        )
        for row in current_rows
    )
    policies_created = 0
    policies_removed = 0
    for resource_type, resource_id, operation in sorted(permission_keys):
        cursor.execute(
            "SELECT COUNT(*) AS count FROM proxy_grant_source "
            "WHERE proxy_account_id = %s AND resource_type = %s AND resource_id = %s "
            "AND operation = %s AND lifecycle_status = 'active'",
            (network.proxy_account_id, resource_type, resource_id, operation),
        )
        active_sources = int(cursor.fetchone()["count"])
        cursor.execute(
            "SELECT policy_owned FROM proxy_grant_policy WHERE proxy_account_id = %s "
            "AND resource_type = %s AND resource_id = %s AND operation = %s",
            (network.proxy_account_id, resource_type, resource_id, operation),
        )
        marker = cursor.fetchone()
        policy_exists = casbin_policy_exists(
            cursor,
            network.proxy_account_id,
            resource_type,
            resource_id,
            operation,
        )
        if active_sources:
            if marker is None:
                cursor.execute(
                    "INSERT INTO proxy_grant_policy "
                    "(proxy_account_id, resource_type, resource_id, operation, "
                    "policy_owned, created_at, updated_at) VALUES (%s, %s, %s, %s, %s, %s, %s)",
                    (
                        network.proxy_account_id,
                        resource_type,
                        resource_id,
                        operation,
                        not policy_exists,
                        timestamp,
                        timestamp,
                    ),
                )
            if not policy_exists:
                cursor.execute(
                    "INSERT INTO casbin_rule (ptype, v0, v1, v2, v3, v4, v5) "
                    "VALUES ('p', %s, %s, %s, '', '', '')",
                    (
                        network.proxy_account_id,
                        f"{resource_type}:{resource_id}",
                        operation,
                    ),
                )
                if marker is not None and not bool(marker["policy_owned"]):
                    cursor.execute(
                        "UPDATE proxy_grant_policy SET policy_owned = 1, updated_at = %s "
                        "WHERE proxy_account_id = %s AND resource_type = %s "
                        "AND resource_id = %s AND operation = %s",
                        (
                            timestamp,
                            network.proxy_account_id,
                            resource_type,
                            resource_id,
                            operation,
                        ),
                    )
                policies_created += 1
            continue
        if marker is None:
            continue
        if bool(marker["policy_owned"]) and policy_exists:
            cursor.execute(
                "DELETE FROM casbin_rule WHERE ptype = 'p' AND v0 = %s "
                "AND v1 = %s AND v2 = %s",
                (
                    network.proxy_account_id,
                    f"{resource_type}:{resource_id}",
                    operation,
                ),
            )
            policies_removed += 1
        cursor.execute(
            "DELETE FROM proxy_grant_policy WHERE proxy_account_id = %s "
            "AND resource_type = %s AND resource_id = %s AND operation = %s",
            (network.proxy_account_id, resource_type, resource_id, operation),
        )
    return {
        "sources_added": added,
        "sources_reactivated": reactivated,
        "sources_revoked": revoked,
        "policies_created": policies_created,
        "policies_removed": policies_removed,
    }


def apply_proxy_plan(
    bkn_connection,
    safe_connection,
    plan: ProxyMigrationPlan,
    grantor_id: str,
) -> dict[str, int]:
    """Apply all proxy accounts, sources, policies, and BKN mappings offline."""
    timestamp = utc_now()
    epoch_ms = int(timestamp.replace(tzinfo=timezone.utc).timestamp() * 1000)
    totals = Counter()
    with safe_connection.cursor() as safe_cursor, bkn_connection.cursor() as bkn_cursor:
        for network in plan.networks:
            if network.create_account:
                safe_cursor.execute(
                    "INSERT INTO managed_proxy_accounts "
                    "(proxy_account_id, managed_by, managed_resource_type, "
                    "managed_resource_id, lifecycle_status, version, created_at, updated_at) "
                    "VALUES (%s, 'bkn', 'knowledge_network', %s, 'active', 1, %s, %s)",
                    (
                        network.proxy_account_id,
                        network.kn_id,
                        timestamp,
                        timestamp,
                    ),
                )
                account_name = f"BKN proxy: {network.kn_name}"[:255]
                safe_cursor.execute(
                    "INSERT INTO users "
                    "(id, account, name, email, telephone, enabled, source, account_type, "
                    "password_hash, must_change_password, created_at, updated_at) "
                    "VALUES (%s, %s, %s, '', '', 1, 'local', 'app', '', 0, %s, %s)",
                    (
                        network.proxy_account_id,
                        f"bkn-proxy-{network.proxy_account_id}",
                        account_name,
                        timestamp,
                        timestamp,
                    ),
                )
                totals["accounts_created"] += 1
            else:
                totals["accounts_reused"] += 1

            totals.update(
                sync_proxy_sources(safe_cursor, network, grantor_id, timestamp)
            )
            bkn_cursor.execute(
                "INSERT INTO t_kn_proxy_account "
                "(f_kn_id, f_proxy_account_id, f_proxy_account_type, "
                "f_lifecycle_status, f_version, f_sync_status, "
                "f_published_model_version, f_synced_model_version, "
                "f_last_sync_error, f_last_grantor_id, f_lock_owner, "
                "f_lock_until, f_created_at, f_updated_at) "
                "VALUES (%s, %s, 'app', 'active', 1, 'ready', %s, %s, '', %s, '', 0, %s, %s) "
                "ON DUPLICATE KEY UPDATE "
                "f_proxy_account_id = VALUES(f_proxy_account_id), "
                "f_proxy_account_type = 'app', f_lifecycle_status = 'active', "
                "f_sync_status = 'ready', "
                "f_published_model_version = VALUES(f_published_model_version), "
                "f_synced_model_version = VALUES(f_synced_model_version), "
                "f_last_sync_error = '', f_last_grantor_id = VALUES(f_last_grantor_id), "
                "f_lock_owner = '', f_lock_until = 0, f_updated_at = VALUES(f_updated_at)",
                (
                    network.kn_id,
                    network.proxy_account_id,
                    network.model_version,
                    network.model_version,
                    grantor_id,
                    epoch_ms,
                    epoch_ms,
                ),
            )
            totals["mappings_ready"] += 1
    return dict(totals)


def verify_proxy_plan(
    bkn_connection, safe_connection, plan: ProxyMigrationPlan
) -> None:
    """Verify the complete proxy state before committing either database."""
    with safe_connection.cursor() as safe_cursor, bkn_connection.cursor() as bkn_cursor:
        for network in plan.networks:
            safe_cursor.execute(
                "SELECT u.enabled, u.account_type, u.password_hash, "
                "m.lifecycle_status FROM users u JOIN managed_proxy_accounts m "
                "ON m.proxy_account_id = u.id WHERE u.id = %s "
                "AND m.managed_by = 'bkn' AND m.managed_resource_type = 'knowledge_network' "
                "AND m.managed_resource_id = %s",
                (network.proxy_account_id, network.kn_id),
            )
            account = safe_cursor.fetchone()
            if (
                account is None
                or not bool(account["enabled"])
                or normalize_text(account["account_type"]) != "app"
                or normalize_text(account["password_hash"]) != ""
                or normalize_text(account["lifecycle_status"]) != "active"
            ):
                raise MigrationError(f"proxy account verification failed for {network.kn_id}")
            safe_cursor.execute(
                "SELECT COUNT(*) AS count FROM proxy_grant_source WHERE proxy_account_id = %s "
                "AND source_type = %s AND lifecycle_status = 'active'",
                (network.proxy_account_id, PROXY_SOURCE_TYPE),
            )
            if int(safe_cursor.fetchone()["count"]) != len(network.sources):
                raise MigrationError(f"proxy source verification failed for {network.kn_id}")
            for source in network.sources:
                if not casbin_policy_exists(
                    safe_cursor,
                    network.proxy_account_id,
                    source.resource_type,
                    source.resource_id,
                    source.operation,
                ):
                    raise MigrationError(
                        f"proxy policy verification failed for {network.kn_id}"
                    )
            bkn_cursor.execute(
                "SELECT f_proxy_account_id, f_lifecycle_status, f_sync_status, "
                "f_published_model_version, f_synced_model_version "
                "FROM t_kn_proxy_account WHERE f_kn_id = %s",
                (network.kn_id,),
            )
            mapping = bkn_cursor.fetchone()
            if (
                mapping is None
                or normalize_text(mapping["f_proxy_account_id"])
                != network.proxy_account_id
                or normalize_text(mapping["f_lifecycle_status"]) != "active"
                or normalize_text(mapping["f_sync_status"]) != "ready"
                or normalize_text(mapping["f_published_model_version"])
                != network.model_version
                or normalize_text(mapping["f_synced_model_version"])
                != network.model_version
            ):
                raise MigrationError(f"proxy mapping verification failed for {network.kn_id}")


def database_config(prefix: str, default_name: str) -> DBConfig:
    """Build one database configuration from environment variables."""
    fallback_prefix = "BKN_DB" if prefix == "SAFE_DB" else prefix

    def setting(name: str, default: str) -> str:
        return os.getenv(
            f"{prefix}_{name}",
            os.getenv(f"{fallback_prefix}_{name}", default),
        )

    return DBConfig(
        host=setting("HOST", "localhost"),
        port=int(setting("PORT", "3306")),
        user=setting("USER", "root"),
        password=setting("PASSWORD", ""),
        database=os.getenv(f"{prefix}_NAME", default_name),
    )


def format_failures(failures: Iterable[Failure]) -> str:
    """Format every validation failure for terminal error output."""
    return "\n".join(
        f"- [{failure.code}] {failure.resource_type}/{failure.resource_id}: "
        f"{failure.detail}"
        for failure in failures
    )


def run() -> int:
    """Plan, apply, and verify the complete offline migration in one execution."""
    bkn_connection = None
    safe_connection = None
    try:
        bkn_connection = connect_database(database_config("BKN_DB", "openbkn"))
        safe_connection = connect_database(database_config("SAFE_DB", "safe"))
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
        proxy_plan = load_proxy_plan(
            bkn_connection, safe_connection, MIGRATION_GRANTOR_ID
        )
        failures = [*plan.failures, *proxy_plan.failures]
        if failures:
            raise MigrationError(
                "migration validation failed:\n" + format_failures(failures)
            )

        try:
            normalize_branches(bkn_connection, commit=False)
            apply_safe_plan(safe_connection, plan, commit=False)
            apply_proxy_plan(
                bkn_connection,
                safe_connection,
                proxy_plan,
                MIGRATION_GRANTOR_ID,
            )
            verify_proxy_plan(bkn_connection, safe_connection, proxy_plan)
            safe_connection.commit()
            bkn_connection.commit()
        except Exception:
            safe_connection.rollback()
            bkn_connection.rollback()
            raise

        print("Knowledge-network data migration completed successfully.")
        return 0
    finally:
        if bkn_connection is not None:
            bkn_connection.close()
        if safe_connection is not None:
            safe_connection.close()


def main() -> int:
    """Run the fixed one-shot migration command."""
    try:
        return run()
    except KeyboardInterrupt:
        print("Migration interrupted.", file=sys.stderr)
        return 130
    except Exception:
        traceback.print_exc()
        return 1


if __name__ == "__main__":
    sys.exit(main())
