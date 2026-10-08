"""Read-only native Core capture and strict, byte-preserving mysqldump reader.

Binary fields are lowercase hexadecimal strings under their original field names.
The metadata identifies binary columns. SQL is parsed as data, never executed.
"""

import gzip
import hashlib
import re
from decimal import Decimal
from pathlib import Path

from snapshot import identifier, literal, strict_loads

CORE_TABLES = tuple(
    ("bkn_trace", "bkn_trace_" + name)
    for name in (
        "conversations",
        "interactions",
        "operations",
        "receipts",
        "operation_call_facts",
        "evidence_event_ledger",
        "assembly_revisions",
        "idempotency_records",
        "ee_historical_provenance_projections",
        "ee_current_explanations",
        "ee_provenance_analyses",
        "projection_outbox",
        "projection_checkpoints",
    )
) + (("openbkn", "checkpoint_writes"),)
BINARY_TYPES = {"binary", "varbinary", "blob", "longblob", "mediumblob", "tinyblob"}


def _binary(value):
    if value is None:
        return None
    if (
        not isinstance(value, str)
        or len(value) % 2
        or not re.fullmatch("[0-9a-fA-F]*", value)
    ):
        raise ValueError("invalid source binary HEX")
    return value.lower()


def export_core(source):
    """Return (rows_by_qualified_table, metadata) in one read-only data snapshot."""
    rows, tables, queries, present = {}, [], [], {}
    for database, table in CORE_TABLES:
        locator = database + "." + table
        schema = source.query(
            "SELECT COLUMN_NAME,DATA_TYPE FROM information_schema.COLUMNS WHERE TABLE_SCHEMA=%s AND TABLE_NAME=%s ORDER BY ORDINAL_POSITION"
            % (literal(database), literal(table))
        )
        columns = [tuple(line.split("\t")) for line in schema]
        if not columns:
            tables.append({"locator": locator, "state": "absent", "count": 0})
            continue
        if any(len(column) != 2 for column in columns):
            raise ValueError("invalid source schema")
        binary_columns = [
            name for name, datatype in columns if datatype in BINARY_TYPES
        ]
        pairs = []
        for name, datatype in columns:
            column = identifier(name)
            value = "HEX(%s)" % column if datatype in BINARY_TYPES else column
            if datatype in {"datetime", "timestamp"}:
                value = "DATE_FORMAT(%s,'%%Y-%%m-%%d %%H:%%i:%%s.%%f')" % column
            pairs.append(literal(name) + "," + value)
        queries.append(
            "SELECT JSON_OBJECT('locator',%s,'row',JSON_OBJECT(%s)) FROM %s.%s"
            % (
                literal(locator),
                ",".join(pairs),
                identifier(database),
                identifier(table),
            )
        )
        rows[locator] = []
        entry = {
            "locator": locator,
            "state": "present",
            "columns": columns,
            "binary_columns": binary_columns,
            "count": 0,
        }
        tables.append(entry)
        present[locator] = entry
    # SQLSource.query encloses this entire batch in a consistent read-only transaction.
    for line in source.query(";".join(queries)) if queries else []:
        item = strict_loads(line)
        locator = item["locator"]
        row = item["row"]
        if locator not in present:
            raise ValueError("unexpected source table")
        entry = present[locator]
        if set(row) != {name for name, _ in entry["columns"]}:
            raise ValueError("source row columns mismatch")
        for name in entry["binary_columns"]:
            row[name] = _binary(row[name])
        rows[locator].append(row)
        entry["count"] += 1
    return rows, {"format_version": 1, "binary_encoding": "hex", "tables": tables}


def _check_directives(data):
    upper = data.upper()
    if b"NO_BACKSLASH_ESCAPES" in upper or b"ANSI_QUOTES" in upper:
        raise ValueError("unsupported dump SQL mode")
    charset = re.search(rb"SET\s+NAMES\s+['\"]?([A-Za-z0-9_]+)", data, re.I)
    if charset and charset[1].lower() not in {
        b"utf8",
        b"utf8mb3",
        b"utf8mb4",
        b"ascii",
    }:
        raise ValueError("unsupported dump text encoding")


def _statements(stream):
    """Split bytes outside quoted values/comments; reject truncated statements."""
    buffer = bytearray()
    quote = None
    block = False
    quoted_markers = {
        39: re.compile(rb"[\\']"),
        34: re.compile(rb'[\\"]'),
        96: re.compile(rb"`"),
    }
    unquoted_markers = re.compile(rb"['\"`;\/#-]")
    for line in stream:
        i = 0
        while i < len(line):
            c = line[i]
            nxt = line[i : i + 2]
            if block:
                _check_directives(line[i:])
                if nxt == b"*/":
                    block = False
                    i += 2
                else:
                    i += 1
                continue
            if quote:
                marker = quoted_markers[quote].search(line, i)
                if marker is None:
                    buffer.extend(line[i:])
                    break
                buffer.extend(line[i : marker.start()])
                i = marker.start()
                c = line[i]
                buffer.append(c)
                i += 1
                if c == 92 and quote != 96:
                    if i >= len(line):
                        raise ValueError("unsupported split SQL escape")
                    buffer.append(line[i])
                    i += 1
                elif c == quote:
                    if i < len(line) and line[i] == quote:
                        buffer.append(line[i])
                        i += 1
                    else:
                        quote = None
                continue
            marker = unquoted_markers.search(line, i)
            if marker is None:
                buffer.extend(line[i:])
                break
            buffer.extend(line[i : marker.start()])
            i = marker.start()
            c = line[i]
            nxt = line[i : i + 2]
            if nxt == b"/*":
                # mysqldump mode directives affect literal interpretation.
                ending = line.find(b"*/", i + 2)
                comment = line[i : ending + 2] if ending >= 0 else line[i:]
                _check_directives(comment)
                block = True
                i += 2
                continue
            if (
                nxt == b"--"
                and (i + 2 == len(line) or line[i + 2] in b" \t\r\n")
                or c == 35
            ):
                break
            if c in (39, 34, 96):
                quote = c
            if c == 59:
                statement = bytes(buffer).strip()
                buffer.clear()
                if statement:
                    yield statement
            else:
                buffer.append(c)
            i += 1
        if not quote:
            buffer.extend(b"\n")
    if quote or block or bytes(buffer).strip():
        raise ValueError("truncated SQL backup")


def _quoted(data, i):
    quote = data[i]
    i += 1
    value = bytearray()
    escapes = {48: 0, 110: 10, 114: 13, 116: 9, 90: 26, 98: 8}
    while i < len(data):
        c = data[i]
        i += 1
        if c == 92:
            if i >= len(data):
                break
            c = data[i]
            i += 1
            if c in (37, 95):
                value.append(92)
            value.append(escapes.get(c, c))
        elif c == quote:
            if i < len(data) and data[i] == quote:
                value.append(c)
                i += 1
            else:
                return bytes(value), i
        else:
            value.append(c)
    raise ValueError("unterminated SQL string")


def _tuples(data):
    i = 0
    while i < len(data):
        while i < len(data) and data[i] in b" \r\n\t":
            i += 1
        if i == len(data):
            break
        if data[i] != 40:
            raise ValueError("unsupported INSERT values")
        i += 1
        values = []
        while True:
            while i < len(data) and data[i] in b" \r\n\t":
                i += 1
            if i >= len(data):
                raise ValueError("truncated INSERT tuple")
            binary = False
            if data[i : i + 2].lower() == b"_b":
                if data[i : i + 7].lower() != b"_binary":
                    raise ValueError("unsupported string introducer")
                i += 7
                while i < len(data) and data[i] in b" \r\n\t":
                    i += 1
            if data[i : i + 2].lower() == b"x'":
                value, i = _quoted(data, i + 1)
                if len(value) % 2 or not re.fullmatch(rb"[0-9a-fA-F]*", value):
                    raise ValueError("invalid SQL HEX")
                try:
                    value = bytes.fromhex(value.decode("ascii"))
                except (ValueError, UnicodeDecodeError) as exc:
                    raise ValueError("invalid SQL HEX") from exc
                binary = True
            elif data[i] in (39, 34):
                value, i = _quoted(data, i)
            else:
                start = i
                while i < len(data) and data[i] not in b",)":
                    i += 1
                token = data[start:i].strip()
                if token.upper() == b"NULL":
                    value = None
                elif re.fullmatch(rb"0[xX][0-9a-fA-F]*", token):
                    try:
                        value = bytes.fromhex(token[2:].decode("ascii"))
                    except ValueError as exc:
                        raise ValueError("invalid SQL HEX") from exc
                    binary = True
                elif re.fullmatch(rb"[+-]?\d+", token):
                    value = int(token)
                elif re.fullmatch(
                    rb"[+-]?(?:\d+\.\d*|\d*\.\d+)(?:[eE][+-]?\d+)?", token
                ):
                    value = str(Decimal(token.decode("ascii")))
                else:
                    raise ValueError("unsupported SQL value expression")
            values.append((value, binary))
            while i < len(data) and data[i] in b" \r\n\t":
                i += 1
            if i >= len(data):
                raise ValueError("truncated INSERT tuple")
            delimiter = data[i]
            i += 1
            if delimiter == 41:
                break
            if delimiter != 44:
                raise ValueError("invalid INSERT delimiter")
        yield values
        while i < len(data) and data[i] in b" \r\n\t":
            i += 1
        if i < len(data):
            if data[i] != 44:
                raise ValueError("unsupported INSERT suffix")
            i += 1
            if not data[i:].strip():
                raise ValueError("trailing INSERT comma")


def read_core_backup(path):
    """Parse supported mysqldump .sql/.sql.gz without a database or SQL execution.

    Require USE or qualified names, CREATE TABLE column types, ordinary INSERT
    VALUES (optional column list), literals/NULL/numbers/HEX. Fail on unsupported
    target writes/expressions or invalid text encoding instead of dropping rows.
    """
    path = Path(path)
    opener = gzip.open if path.suffix == ".gz" else open
    rows = {}
    schemas = {}
    database = None
    sha = hashlib.sha256()
    with opener(path, "rb") as stream:
        for statement in _statements(stream):
            sha.update(statement + b";\n")
            if statement.upper().startswith(b"SET "):
                _check_directives(statement)
            use = re.fullmatch(rb"USE\s+`([A-Za-z0-9_]+)`", statement, re.I)
            if use:
                database = use[1].decode("ascii")
                continue
            head = re.match(
                rb"(CREATE TABLE|INSERT INTO)\s+(?:`([A-Za-z0-9_]+)`\.)?`([A-Za-z0-9_]+)`",
                statement,
                re.I,
            )
            if not head:
                if re.match(
                    rb"(INSERT|REPLACE|UPDATE|DELETE|TRUNCATE|LOAD)\b", statement, re.I
                ) and any(table.encode() in statement for _, table in CORE_TABLES):
                    raise ValueError("unsupported target SQL statement")
                continue
            db = head[2].decode("ascii") if head[2] else database
            table = head[3].decode("ascii")
            if db is None and any(table == name for _, name in CORE_TABLES):
                raise ValueError("target table has no database")
            if (db, table) not in CORE_TABLES:
                continue
            locator = db + "." + table
            if head[1].upper() == b"CREATE TABLE":
                columns = re.findall(
                    rb"^\s*`([A-Za-z0-9_]+)`\s+([A-Za-z]+)", statement, re.M
                )
                if not columns:
                    raise ValueError("missing source DDL columns")
                schema = [
                    (name.decode("ascii"), datatype.decode("ascii").lower())
                    for name, datatype in columns
                ]
                if len({n for n, _ in schema}) != len(schema):
                    raise ValueError("duplicate DDL columns")
                if locator in schemas and schemas[locator] != schema:
                    raise ValueError("conflicting source DDL")
                schemas[locator] = schema
                rows.setdefault(locator, [])
                continue
            if locator not in schemas:
                raise ValueError("INSERT missing source DDL")
            tail = statement[head.end() :].lstrip()
            names = [n for n, _ in schemas[locator]]
            if tail.startswith(b"("):
                end = tail.find(b")")
                if end < 0:
                    raise ValueError("invalid INSERT columns")
                names = [
                    part.strip().decode("ascii").strip("`")
                    for part in tail[1:end].split(b",")
                ]
                if len(set(names)) != len(names) or set(names) != {
                    n for n, _ in schemas[locator]
                }:
                    raise ValueError("incomplete INSERT columns")
                tail = tail[end + 1 :].lstrip()
            if not tail.upper().startswith(b"VALUES"):
                raise ValueError("unsupported target INSERT")
            types = dict(schemas[locator])
            for values in _tuples(tail[6:].lstrip()):
                if len(values) != len(names):
                    raise ValueError("INSERT column count mismatch")
                row = {}
                for name, (value, binary) in zip(names, values):
                    if value is None:
                        row[name] = None
                    elif types[name] in BINARY_TYPES:
                        if not isinstance(value, bytes):
                            raise ValueError("nonliteral binary column")
                        row[name] = value.hex()
                    elif isinstance(value, bytes):
                        try:
                            row[name] = value.decode("utf-8")
                        except UnicodeDecodeError as exc:
                            raise ValueError("invalid UTF-8 source text") from exc
                    else:
                        row[name] = value
                rows[locator].append(row)
    source_sha256 = hashlib.sha256()
    with path.open("rb") as source_file:
        for block in iter(lambda: source_file.read(1 << 20), b""):
            source_sha256.update(block)
    tables = [
        {
            "locator": loc,
            "state": "present",
            "columns": schemas[loc],
            "binary_columns": [n for n, t in schemas[loc] if t in BINARY_TYPES],
            "count": len(records),
        }
        for loc, records in rows.items()
    ]
    return rows, {
        "format_version": 1,
        "source_kind": "mysqldump",
        "source_file": path.name,
        "source_sha256": source_sha256.hexdigest(),
        "binary_encoding": "hex",
        "parsed_statements_sha256": sha.hexdigest(),
        "tables": tables,
    }
