#!/usr/bin/env python3
"""Safely migrate unqualified legacy atomic metrics to object-metric V1.

The command is report-only unless --apply is supplied. Metrics that contain a
business condition, fixed grouping, having/order clauses, or non-atomic logic
are deliberately left for explicit business review instead of being guessed.
"""

from __future__ import annotations

import argparse
import json
import os
import re
import sys
from dataclasses import dataclass
from datetime import UTC, datetime
from typing import Any

import pymysql


@dataclass
class Decision:
    metric_id: str
    action: str
    reason: str
    definition: dict[str, Any] | None = None


def parse_json(value: Any, default: Any) -> Any:
    if value in (None, ""):
        return default
    if isinstance(value, (dict, list)):
        return value
    return json.loads(value)


def stable_code(metric_id: str) -> str:
    return "legacy_" + re.sub(r"[^a-z0-9_]+", "_", metric_id.lower()).strip("_")


def published_at(row: dict[str, Any]) -> str:
    milliseconds = int(row.get("f_update_time") or row.get("f_create_time") or 0)
    return datetime.fromtimestamp(milliseconds / 1000, tz=UTC).isoformat().replace("+00:00", "Z")


def unit_spec(row: dict[str, Any]) -> dict[str, Any] | None:
    unit = str(row.get("f_unit") or "").strip()
    if not unit or unit == "none":
        return None
    quantity = {
        "countUnit": "count",
        "currencyUnit": "currency",
        "percentageUnit": "dimensionless",
        "percent": "dimensionless",
        "storeUnit": "data_size",
        "timeUnit": "duration",
        "transmissionRate": "rate",
        "weightUnit": "weight",
    }.get(row.get("f_unit_type"), "custom")
    system = "ISO_4217" if quantity == "currency" and unit in {"CNY", "USD", "EUR"} else "registered"
    return {"system": system, "quantity_kind": quantity, "code": unit, "scale_factor": 1}


def translate(row: dict[str, Any]) -> Decision:
    metric_id = row["f_id"]
    if row.get("f_metric_type") != "atomic":
        return Decision(metric_id, "manual_review", "only legacy atomic metrics are losslessly auto-migrated")
    if row.get("f_scope_type") != "object_type" or not row.get("f_scope_ref"):
        return Decision(metric_id, "manual_review", "metric is not owned by one object type")
    if row.get("f_unit_type") == "transmissionRate":
        return Decision(
            metric_id,
            "manual_review",
            "legacy rate unit has no explicit numerator and denominator contract",
        )
    formula = parse_json(row.get("f_calculation_formula"), {})
    if formula.get("condition") or formula.get("group_by") or formula.get("order_by") or formula.get("having"):
        return Decision(metric_id, "manual_review", "legacy business qualifiers must be separated into a derived metric")
    aggregation = formula.get("aggregation") or {}
    aggr = aggregation.get("aggr")
    prop = aggregation.get("property")
    if aggr not in {"count", "count_distinct", "sum", "avg", "min", "max"}:
        return Decision(metric_id, "manual_review", f"unsupported aggregation: {aggr!r}")
    if aggr != "count" and not prop:
        return Decision(metric_id, "manual_review", "aggregation property is missing")
    migrated_aggr = "count_rows" if aggr == "count" and not prop else aggr
    expression = None
    if migrated_aggr != "count_rows":
        expression = {
            "node_type": "property",
            "property_ref": {
                "object_type_id": row["f_scope_ref"],
                "property_name": prop,
            },
        }
    is_count = migrated_aggr in {"count_rows", "count", "count_distinct"}
    value = {
        "physical_type": "int64" if is_count else "decimal",
        "semantic_type": "count" if is_count else "measure",
    }
    if not is_count:
        value.update({"precision": 18, "scale": 2, "rounding_mode": "half_up"})
    unit = unit_spec(row)
    if unit:
        value["unit"] = unit
    analysis = parse_json(row.get("f_analysis_dimensions"), [])
    if not isinstance(analysis, list) or any(not isinstance(item, dict) for item in analysis):
        return Decision(metric_id, "manual_review", "analysis dimensions do not use the expected structured list")
    definition = {
        "id": metric_id,
        "version": 1,
        "code": stable_code(metric_id),
        "name": row["f_name"],
        "description": row.get("f_comment") or f"由历史指标 {row['f_name']} 迁移",
        "owner_object_type_id": row["f_scope_ref"],
        "metric_type": "atomic",
        "calculation_scope": "object_set",
        "result_semantics": {
            "statistical_grain": {"group_by_dimensions": []},
            "value": value,
            "no_data_policy": "zero" if is_count else "null",
            "partial_data_policy": "reject",
        },
        "specification": {
            "kind": "atomic",
            "measure": {"aggregation": migrated_aggr, **({"expression": expression} if expression else {})},
        },
        "query_capabilities": {
            "analysis_dimensions": [
                {"object_type_id": row["f_scope_ref"], "property_name": item["name"]}
                for item in analysis
                if item.get("name")
            ],
            "time_dimensions": [],
        },
        "lifecycle": {
            "status": "published",
            "published_at": published_at(row),
            "ontology_version": "migration-snapshot",
            "expression_language_version": "1.0",
            "function_registry_version": "1.0",
            "aggregation_registry_version": "1.0",
            "unit_registry_version": "1.0",
        },
    }
    return Decision(metric_id, "migrate", "lossless atomic metric", definition)


def arguments() -> argparse.Namespace:
    parser = argparse.ArgumentParser()
    parser.add_argument("--host", default=os.getenv("BKN_DB_HOST", "127.0.0.1"))
    parser.add_argument("--port", type=int, default=int(os.getenv("BKN_DB_PORT", "3306")))
    parser.add_argument("--user", default=os.getenv("BKN_DB_USER", "root"))
    parser.add_argument("--password", default=os.getenv("BKN_DB_PASSWORD", ""))
    parser.add_argument("--database", default=os.getenv("BKN_DB_NAME", "bkn_backend"))
    parser.add_argument("--kn-id")
    parser.add_argument("--branch", default="main")
    parser.add_argument("--apply", action="store_true")
    parser.add_argument("--report", default="object-metric-migration-report.json")
    return parser.parse_args()


def main() -> int:
    args = arguments()
    connection = pymysql.connect(
        host=args.host,
        port=args.port,
        user=args.user,
        password=args.password,
        database=args.database,
        charset="utf8mb4",
        cursorclass=pymysql.cursors.DictCursor,
        autocommit=False,
    )
    where = ["f_branch = %s"]
    params: list[Any] = [args.branch]
    if args.kn_id:
        where.append("f_kn_id = %s")
        params.append(args.kn_id)
    select = """
        SELECT f_id, f_kn_id, f_branch, f_name, f_comment, f_unit_type, f_unit,
               f_metric_type, f_scope_type, f_scope_ref, f_calculation_formula,
               f_analysis_dimensions, f_creator, f_creator_type, f_create_time,
               f_updater, f_updater_type, f_update_time
          FROM t_metric_definition
         WHERE """ + " AND ".join(where)
    decisions: list[Decision] = []
    try:
        with connection.cursor() as cursor:
            cursor.execute(
                """
                SELECT f_kn_id, f_branch, f_id, f_version, f_code
                  FROM t_object_metric_definition
                 WHERE f_branch = %s
                """,
                (args.branch,),
            )
            existing_rows = cursor.fetchall()
            existing_ids = {
                (item["f_kn_id"], item["f_branch"], item["f_id"], item["f_version"])
                for item in existing_rows
            }
            code_owners = {
                (item["f_kn_id"], item["f_branch"], item["f_code"], item["f_version"]): item["f_id"]
                for item in existing_rows
            }
            planned_codes: dict[tuple[str, str, str, int], str] = {}
            cursor.execute(select, params)
            rows = cursor.fetchall()
            for row in rows:
                decision = translate(row)
                target_key = (row["f_kn_id"], row["f_branch"], row["f_id"], 1)
                if target_key in existing_ids:
                    decision = Decision(row["f_id"], "already_exists", "object metric version already exists")
                elif decision.action == "migrate" and decision.definition is not None:
                    code_key = (
                        row["f_kn_id"],
                        row["f_branch"],
                        decision.definition["code"],
                        1,
                    )
                    owner = code_owners.get(code_key) or planned_codes.get(code_key)
                    if owner and owner != row["f_id"]:
                        decision = Decision(
                            row["f_id"],
                            "manual_review",
                            f"stable code conflicts with metric {owner}: {decision.definition['code']}",
                        )
                    else:
                        planned_codes[code_key] = row["f_id"]
                decisions.append(decision)
                if not args.apply or decision.action != "migrate" or decision.definition is None:
                    continue
                definition = decision.definition
                cursor.execute(
                    """
                    INSERT INTO t_object_metric_definition
                    (f_kn_id, f_branch, f_id, f_version, f_code, f_name, f_description,
                     f_owner_object_type_id, f_metric_type, f_status, f_definition,
                     f_creator, f_creator_type, f_create_time, f_updater, f_updater_type, f_update_time)
                    VALUES (%s, %s, %s, 1, %s, %s, %s, %s, 'atomic', 'published', %s,
                            %s, %s, %s, %s, %s, %s)
                    """,
                    (
                        row["f_kn_id"], row["f_branch"], row["f_id"], definition["code"],
                        definition["name"], definition["description"], definition["owner_object_type_id"],
                        json.dumps(definition, ensure_ascii=False, separators=(",", ":")),
                        row["f_creator"], row["f_creator_type"], row["f_create_time"],
                        row["f_updater"], row["f_updater_type"], row["f_update_time"],
                    ),
                )
        if args.apply:
            connection.commit()
        else:
            connection.rollback()
    except Exception:
        connection.rollback()
        raise
    finally:
        connection.close()
    report = {
        "mode": "apply" if args.apply else "dry-run",
        "summary": {
            "total": len(decisions),
            "migrate": sum(item.action == "migrate" for item in decisions),
            "manual_review": sum(item.action == "manual_review" for item in decisions),
            "already_exists": sum(item.action == "already_exists" for item in decisions),
        },
        "entries": [item.__dict__ for item in decisions],
    }
    with open(args.report, "w", encoding="utf-8") as output:
        json.dump(report, output, ensure_ascii=False, indent=2)
    print(json.dumps(report["summary"], ensure_ascii=False))
    return 0


if __name__ == "__main__":
    sys.exit(main())
