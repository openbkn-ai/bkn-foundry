"""Private, immutable source snapshots. No source writes or restore command."""
import hashlib
import json
import os
import re
import subprocess
from collections import Counter
from pathlib import Path

SOURCES = (
    ("openbkn", "t_operation_audit", "bkn-backend", "audit"),
    ("openbkn", "t_vega_operation_audit", "vega", "audit"),
    ("openbkn", "t_execution_factory_operation_audit", "execution-factory", "audit"),
    ("openbkn", "t_model_manager_operation_audit", "model-manager", "audit"),
    ("safe", "audit_logs", "bkn-safe-admin", "audit"),
    ("safe", "access_logs", "bkn-safe-access", "audit"),
    ("openbkn", "bkn_backend_trace_outbox", "bkn-backend", "evidence"),
    ("openbkn", "ontology_query_trace_outbox", "ontology-query", "evidence"),
)


def canonical(value):
    return json.dumps(value, ensure_ascii=False, sort_keys=True, separators=(",", ":"), allow_nan=False)


def strict_loads(text):
    def pairs(entries):
        result = {}
        for key, value in entries:
            if key in result:
                raise ValueError("duplicate JSON key")
            result[key] = value
        return result

    def reject_constant(value):
        raise ValueError("nonfinite JSON number")

    value = json.loads(text, object_pairs_hook=pairs, parse_constant=reject_constant)
    canonical(value)
    return value


def digest(data):
    return hashlib.sha256(data).hexdigest()


def identifier(value):
    if not re.fullmatch(r"[A-Za-z0-9_]+", value):
        raise ValueError("invalid SQL identifier")
    return "`" + value + "`"


def literal(value):
    if not re.fullmatch(r"[A-Za-z0-9_.-]+", value):
        raise ValueError("invalid source identifier")
    return "'" + value + "'"


def readonly(query):
    return "SET SESSION time_zone='+00:00'; SET SESSION TRANSACTION READ ONLY; START TRANSACTION WITH CONSISTENT SNAPSHOT; " + query + "; COMMIT;"


def export_query(database, table, columns, source_id, kind):
    pairs = []
    for name, datatype in columns:
        column = identifier(name)
        value = "DATE_FORMAT(%s,'%%Y-%%m-%%dT%%H:%%i:%%s.%%fZ')" % column if datatype in {"datetime", "timestamp"} else column
        if datatype in {"binary", "varbinary", "blob", "longblob", "mediumblob", "tinyblob"}:
            raise ValueError("binary column requires explicit source codec")
        pairs.append(literal(name) + "," + value)
    if not pairs:
        raise ValueError("source table has no columns")
    return "SELECT JSON_OBJECT('source_id',%s,'kind',%s,'locator',%s,'row',JSON_OBJECT(%s)) FROM %s.%s" % (
        literal(source_id), literal(kind), literal(database + "." + table), ",".join(pairs), identifier(database), identifier(table))


def private_write(path, data):
    fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(fd, "wb") as stream:
        stream.write(data)


def save_snapshot(root, records, source_deployment, metadata):
    root = Path(root)
    root.mkdir(mode=0o700, parents=True, exist_ok=False)
    records = sorted(records, key=canonical)
    payload = "".join(canonical(row) + "\n" for row in records).encode("utf-8")
    private_write(root / "records.jsonl", payload)
    manifest = {"format_version": 1, "source_deployment": source_deployment, "metadata": metadata,
                "records_file": "records.jsonl", "records_sha256": digest(payload), "record_count": len(records),
                "source_counts": dict(sorted(Counter(row.get("locator", row["source_id"]) for row in records).items()))}
    private_write(root / "snapshot.json", (canonical(manifest) + "\n").encode("utf-8"))
    return manifest


def read_snapshot(root):
    root = Path(root)
    manifest = strict_loads((root / "snapshot.json").read_text())
    if manifest.get("format_version") != 1 or manifest.get("records_file") != "records.jsonl":
        raise ValueError("unsupported snapshot format")
    payload = (root / "records.jsonl").read_bytes()
    if digest(payload) != manifest["records_sha256"]:
        raise ValueError("snapshot hash mismatch")
    records = [strict_loads(line) for line in payload.splitlines()]
    if len(records) != manifest["record_count"]:
        raise ValueError("snapshot count mismatch")
    return iter(records)


class SQLSource:
    def __init__(self, profile):
        self.profile = profile

    def query(self, sql):
        profile = self.profile
        if profile["kind"] == "kubernetes":
            prefix = ["kubectl", "--context", profile["context"], "-n", profile["namespace"], "exec", profile["pod"], "--"]
        elif profile["kind"] == "docker":
            prefix = ["docker", "exec", profile["container"]]
        else:
            raise ValueError("unsupported SQL source transport")
        # Credentials remain in the database container's environment, never argv/output.
        command = 'MYSQL_PWD="$MARIADB_ROOT_PASSWORD" mariadb -uroot --batch --raw --skip-column-names -e "$1"'
        result = subprocess.run(prefix + ["sh", "-c", command, "query", readonly(sql)], capture_output=True, text=True)
        if result.returncode:
            raise ValueError("source SQL query failed; inspect transport privately")
        return result.stdout.splitlines()

    def export(self):
        queries, tables = [], []
        for database, table, source_id, kind in SOURCES:
            rows = self.query("SELECT COLUMN_NAME,DATA_TYPE FROM information_schema.COLUMNS WHERE TABLE_SCHEMA=%s AND TABLE_NAME=%s ORDER BY ORDINAL_POSITION" % (literal(database), literal(table)))
            if not rows:
                tables.append({"locator": database + "." + table, "state": "absent"})
                continue
            columns = [tuple(row.split("\t")) for row in rows]
            queries.append(export_query(database, table, columns, source_id, kind))
            tables.append({"locator": database + "." + table, "state": "present", "columns": columns})
        lines = self.query(";".join(queries)) if queries else []
        return [strict_loads(line) for line in lines], tables
