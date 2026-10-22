# Copyright openbkn.ai
#
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

"""Pure conversions behind the graph explorer's query tools.

Context Loader answers in its own shapes (search_instance, query_object_instance,
query_instance_subgraph, run_cypher); these functions turn them into the nodes
and edges a canvas_result carries. They follow the EE graph explorer's rules
(studio/src/graph-explorer/graph-explorer.service.ts and cypher-pattern.ts) so
an instance the server plans has the same id as one the Web already shows.

A node is {id, object_type_id, identity, properties, label[, computed]}; an edge
is {id, relation_type_id, relation_type_name, source, target[, virtual,
evidence_count, computed]}. Labels here are only for the model to read; the Web
renders its own.
"""

import json
import math
import re
from dataclasses import dataclass, field
from typing import Any, Iterator

INSTANCE_ID = "_instance_id"
INSTANCE_IDENTITY = "_instance_identity"
DISPLAY = "_display"
# Evidence nodes drawn per derived edge; the rest are counted.
EVIDENCE_LIMIT = 20
# Properties kept on a node in the result, and characters per value.
PROPERTY_LIMIT = 40
VALUE_LIMIT = 500

_LABEL_CHAIN = (
    DISPLAY,
    "display_name",
    "name",
    "title",
    "topic",
    "description",
    "text_clean",
    "text",
    "id",
)
_NUMERIC_TYPE = re.compile(r"(int|long|float|double|decimal|number|numeric|real|bigint|short)", re.I)
_INTEGER_TYPE = re.compile(r"^(integer|int\d*|long|short|byte|bigint|smallint|tinyint)$", re.I)
_DECIMAL_TYPE = re.compile(r"^(decimal|numeric|number|float\d*|double|real)$", re.I)
_MAX_SAFE_INTEGER = 2**53 - 1


@dataclass
class ObjectTypeMeta:
    id: str
    name: str
    primary_keys: list[str]
    # Property name -> declared type.
    property_types: dict[str, str] = field(default_factory=dict)


def text(value: Any) -> str:
    """Any value as text, never a Python repr of a dict."""
    if value is None:
        return ""
    if isinstance(value, str):
        return value
    if isinstance(value, bool):
        return "true" if value else "false"
    if isinstance(value, (int, float)):
        return str(value)
    try:
        return json.dumps(value, ensure_ascii=False)
    except (TypeError, ValueError):
        return str(value)


def _non_empty(value: Any) -> bool:
    return value is not None and text(value).strip() != ""


def meta_from(raw: Any) -> ObjectTypeMeta | None:
    """One get_object_types entry."""
    if not isinstance(raw, dict) or not isinstance(raw.get("id"), str) or not raw["id"]:
        return None
    keys = [k for k in raw.get("primary_keys") or [] if isinstance(k, str) and k]
    types = {
        p["name"]: p.get("type") or ""
        for p in raw.get("data_properties") or []
        if isinstance(p, dict) and isinstance(p.get("name"), str)
    }
    return ObjectTypeMeta(
        id=raw["id"],
        name=raw.get("name") if isinstance(raw.get("name"), str) and raw.get("name") else raw["id"],
        primary_keys=keys,
        property_types=types,
    )


def instance_id(ot_id: str, primary_keys: list[str], identity: dict) -> str | None:
    """ontology-query's object id: `<ot>-<key values joined by "_">`, a missing
    value written as `__NULL__`. None without a primary key."""
    if not ot_id or not primary_keys:
        return None
    parts = [
        text(identity[pk]) if pk in identity and identity[pk] is not None else "__NULL__"
        for pk in primary_keys
    ]
    return f"{ot_id}-{'_'.join(parts)}"


def identity_key(primary_keys: list[str], identity: dict) -> str | None:
    """A stable key for an identity, or None when a key value is missing."""
    if not primary_keys:
        return None
    parts = []
    for pk in primary_keys:
        value = identity.get(pk)
        if value is None:
            return None
        parts.append(text(value))
    return "\x1f".join(parts)


def typed(meta: ObjectTypeMeta, prop: str, value: Any) -> Any:
    """A value read back as text (a Cypher column) in its declared type, so an
    identity matches the one other queries return."""
    if isinstance(value, str) and _NUMERIC_TYPE.search(meta.property_types.get(prop, "")):
        try:
            number = float(value)
        except ValueError:
            return value
        if math.isfinite(number):
            if number.is_integer() and abs(number) <= _MAX_SAFE_INTEGER and "." not in value:
                return int(number)
            return number
    return value


def key_value_for(meta: ObjectTypeMeta, key: str) -> Any:
    """A single-key id tail back as a typed value: numbers stay numbers when the
    key's declared type says so."""
    declared = meta.property_types.get(meta.primary_keys[0], "") if meta.primary_keys else ""
    if _NUMERIC_TYPE.search(declared) and key.strip():
        try:
            number = float(key)
        except ValueError:
            return key
        if math.isfinite(number):
            return int(number) if number.is_integer() and "." not in key and "e" not in key.lower() else number
    return key


def parse_ids(ids: list[str], object_type_ids: list[str]) -> tuple[list[tuple[str, str]], list[str]]:
    """Instance ids as (object type id, key tail), by the longest matching type
    prefix; ids that match no type are returned apart."""
    prefixes = sorted(object_type_ids, key=len, reverse=True)
    items: list[tuple[str, str]] = []
    unknown: list[str] = []
    seen: set[tuple[str, str]] = set()
    for raw in ids:
        token = (raw or "").strip()
        if not token:
            continue
        prefix = next(
            (p for p in prefixes if token.startswith(f"{p}-") and len(token) > len(p) + 1), None
        )
        if prefix is None:
            unknown.append(token)
            continue
        item = (prefix, token[len(prefix) + 1 :])
        if item not in seen:
            seen.add(item)
            items.append(item)
    return items, unknown


def label_of(properties: dict, fallback: str) -> str:
    by_lower: dict[str, Any] = {}
    for key, value in properties.items():
        lower = key.lower()
        if lower not in by_lower or not _non_empty(by_lower[lower]):
            by_lower[lower] = value
    for key in _LABEL_CHAIN:
        if _non_empty(by_lower.get(key)):
            return text(by_lower[key])
    # Rows fetched by column carry no _display; `confederation_name` still reads
    # better than a key.
    for key, value in properties.items():
        if not key.startswith("_") and "name" in key.lower() and _non_empty(value):
            return text(value)
    for key, value in properties.items():
        if not key.startswith("_") and _non_empty(value):
            return text(value)
    return fallback


def _bounded(properties: dict) -> dict:
    out = {}
    for key, value in properties.items():
        if key == "_score":
            continue
        if isinstance(value, str) and len(value) > VALUE_LIMIT:
            value = value[:VALUE_LIMIT]
        out[key] = value
        if len(out) >= PROPERTY_LIMIT:
            break
    return out


def _node(node_id: str, ot_id: str, identity: dict, properties: dict) -> dict:
    return {
        "id": node_id,
        "object_type_id": ot_id,
        "identity": identity,
        "properties": _bounded(properties),
        "label": label_of(properties, node_id),
    }


def node_from_row(meta: ObjectTypeMeta, row: dict) -> dict | None:
    """One query_object_instance row."""
    embedded = row.get(INSTANCE_IDENTITY)
    identity = (
        dict(embedded)
        if isinstance(embedded, dict) and embedded
        else {pk: row[pk] for pk in meta.primary_keys if pk in row}
    )
    node_id = row.get(INSTANCE_ID) if isinstance(row.get(INSTANCE_ID), str) and row.get(INSTANCE_ID) else None
    node_id = node_id or instance_id(meta.id, meta.primary_keys, identity)
    return _node(node_id, meta.id, identity, row) if node_id else None


def nodes_from_rows(meta: ObjectTypeMeta, payload: Any) -> list[dict]:
    rows = payload.get("datas") if isinstance(payload, dict) else None
    return [n for row in rows or [] if isinstance(row, dict) and (n := node_from_row(meta, row))]


def nodes_from_search(payload: Any, metas: dict[str, ObjectTypeMeta]) -> list[dict]:
    """A search_instance payload; hits that cannot be identified are dropped."""
    nodes = []
    for item in (payload.get("nodes") if isinstance(payload, dict) else None) or []:
        if not isinstance(item, dict) or not isinstance(item.get("object_type_id"), str):
            continue
        ot_id = item["object_type_id"]
        props = dict(item["properties"]) if isinstance(item.get("properties"), dict) else {}
        if not _non_empty(props.get(DISPLAY)) and _non_empty(item.get("instance_name")):
            props[DISPLAY] = item["instance_name"]
        identity = dict(item["unique_identities"]) if isinstance(item.get("unique_identities"), dict) else {}
        if not identity and isinstance(props.get(INSTANCE_IDENTITY), dict):
            identity = dict(props[INSTANCE_IDENTITY])
        embedded = props.get(INSTANCE_ID) if isinstance(props.get(INSTANCE_ID), str) and props.get(INSTANCE_ID) else None
        meta = metas.get(ot_id)
        node_id = embedded or (instance_id(ot_id, meta.primary_keys, identity) if meta else None)
        if node_id:
            nodes.append(_node(node_id, ot_id, identity, props))
    return nodes


def _relations(raw: Any) -> Iterator[dict]:
    for path in raw if isinstance(raw, list) else []:
        for relation in (path.get("relations") if isinstance(path, dict) else None) or []:
            if (
                isinstance(relation, dict)
                and isinstance(relation.get("source_object_id"), str)
                and isinstance(relation.get("target_object_id"), str)
                and isinstance(relation.get("relation_type_id"), str)
            ):
                yield relation


def edges_from_subgraph(entry: Any) -> list[dict]:
    """The edges of one query_instance_subgraph entry, deduplicated."""
    edges: dict[str, dict] = {}
    for relation in _relations(entry.get("relation_paths") if isinstance(entry, dict) else None):
        edge = relation_edge(
            relation["source_object_id"],
            relation["relation_type_id"],
            relation["target_object_id"],
            relation.get("relation_type_name") or relation["relation_type_id"],
        )
        edges.setdefault(edge["id"], edge)
    return list(edges.values())


def relation_edge(source: str, relation_type_id: str, target: str, name: str) -> dict:
    return {
        "id": f"{source}|{relation_type_id}|{target}",
        "relation_type_id": relation_type_id,
        "relation_type_name": name,
        "source": source,
        "target": target,
    }


def identity_condition(meta: ObjectTypeMeta, identities: list[dict]) -> dict:
    """A query_object_instance condition matching exactly these identities."""
    keys = meta.primary_keys
    if len(keys) == 1:
        return {"field": keys[0], "operation": "in", "value": [i[keys[0]] for i in identities]}
    each = [
        {"operation": "and", "sub_conditions": [{"field": k, "operation": "==", "value": i[k]} for k in keys]}
        for i in identities
    ]
    return each[0] if len(each) == 1 else {"operation": "or", "sub_conditions": each}


# ---- run_cypher results ----


def _is_object(value: Any) -> bool:
    return (
        isinstance(value, dict)
        and value.get("kind") == "object"
        and isinstance(value.get("object_type_id"), str)
        and isinstance(value.get("key"), dict)
    )


def _is_relation(value: Any) -> bool:
    return (
        isinstance(value, dict)
        and value.get("kind") == "relation"
        and isinstance(value.get("relation_type_id"), str)
        and _is_object(value.get("source"))
        and _is_object(value.get("target"))
    )


def _walk(value: Any, visit) -> None:
    if _is_object(value) or _is_relation(value):
        visit(value)
    elif isinstance(value, list):
        for item in value:
            _walk(item, visit)
    elif isinstance(value, dict):
        for item in value.values():
            _walk(item, visit)


def _type_holds_graph(value_type: Any) -> bool:
    if not isinstance(value_type, dict):
        return False
    if value_type.get("kind") in ("object", "relation"):
        return True
    if _type_holds_graph(value_type.get("element")):
        return True
    fields = value_type.get("fields")
    return isinstance(fields, dict) and any(_type_holds_graph(f) for f in fields.values())


def _is_scalar(value: Any) -> bool:
    found = []
    _walk(value, found.append)
    return not found


def key_value(entry: Any) -> Any:
    """A Cypher key value arrives as exact text with its declared type; numbers
    become numbers while they stay exact."""
    if not isinstance(entry, dict):
        return entry
    value, declared = entry.get("value"), entry.get("type")
    if not isinstance(value, str) or not isinstance(declared, str) or not value.strip():
        return value
    try:
        number = float(value)
    except ValueError:
        return value
    if not math.isfinite(number):
        return value
    if _INTEGER_TYPE.match(declared):
        return int(number) if number.is_integer() and abs(number) <= _MAX_SAFE_INTEGER else value
    if _DECIMAL_TYPE.match(declared):
        return number if abs(number) <= _MAX_SAFE_INTEGER else value
    return value


def assign_scalar_columns(row: dict) -> tuple[dict[str, dict], dict]:
    """Which node column each scalar column belongs to: all of them with one node
    column, else by the `<variable>_` prefix (longest wins); the rest is left over."""
    node_columns = [c for c, v in row.items() if _is_object(v)]
    by_node: dict[str, dict] = {}
    leftover: dict = {}
    for column, value in row.items():
        if column in node_columns or not _is_scalar(value):
            continue
        if len(node_columns) == 1:
            owner = node_columns[0]
        else:
            owners = sorted((n for n in node_columns if column.startswith(f"{n}_")), key=len, reverse=True)
            owner = owners[0] if owners else None
        if owner is None:
            leftover[column] = value
        else:
            by_node.setdefault(owner, {})[column] = value
    return by_node, leftover


@dataclass
class CypherGraph:
    nodes: list[dict]
    edges: list[dict]
    # Scalar columns no node or derived edge claimed; node cells hold the node id.
    table: dict | None
    holds_graph: bool


def graph_from_cypher(
    result: dict, metas: dict[str, ObjectTypeMeta], relation_names: dict[str, str]
) -> CypherGraph:
    """The nodes, relations, derived edges and leftover table of a complete query."""
    entries = [r for r in result.get("entries") or [] if isinstance(r, dict)]
    holds_graph = any(_type_holds_graph(t) for t in (result.get("value_types") or {}).values())
    nodes: dict[str, dict] = {}
    edges: dict[str, dict] = {}
    leftover_columns: list[str] = []
    leftover_rows: list[dict] = []

    def node_of(value: dict) -> dict:
        ot_id = value["object_type_id"]
        identity = {pk: key_value(entry) for pk, entry in value["key"].items()}
        meta = metas.get(ot_id)
        keys = meta.primary_keys if meta and meta.primary_keys else list(identity)
        node_id = instance_id(ot_id, keys, identity) or f"{ot_id}-{'_'.join(text(identity.get(k)) for k in keys)}"
        props = {**identity, **(value.get("properties") or {})}
        return _node(node_id, ot_id, identity, props)

    def add_node(value: dict) -> str:
        node = node_of(value)
        existing = nodes.get(node["id"])
        if existing:
            existing["properties"] = _bounded({**existing["properties"], **node["properties"]})
        else:
            nodes[node["id"]] = node
        return node["id"]

    def add_relation(value: dict) -> None:
        source, target = add_node(value["source"]), add_node(value["target"])
        rel = value["relation_type_id"]
        edge = relation_edge(source, rel, target, relation_names.get(rel, rel))
        edges.setdefault(edge["id"], edge)

    def visit(item: dict) -> None:
        if item["kind"] == "object":
            add_node(item)
        else:
            add_relation(item)

    derived: dict[str, dict] = {}
    for row in entries:
        source, target, name = row.get("source"), row.get("target"), row.get("relationType")
        if not (_is_object(source) and _is_object(target) and isinstance(name, str) and name.strip()):
            found = []
            _walk(row, found.append)
            holds_graph = holds_graph or bool(found)
            for item in found:
                visit(item)
            by_node, leftover = assign_scalar_columns(row)
            for column, values in by_node.items():
                node = nodes.get(node_of(row[column])["id"])
                if node is not None:
                    node["computed"] = {**node.get("computed", {}), **values}
            if leftover:
                for column in leftover:
                    if column not in leftover_columns:
                        leftover_columns.append(column)
                cells = {c: node_of(v)["id"] for c, v in row.items() if _is_object(v)}
                leftover_rows.append({**cells, **leftover})
            continue
        holds_graph = True
        name = name.strip()
        source_id, target_id = add_node(source), add_node(target)
        edge_id = f"virtual|{source_id}|{name}|{target_id}"
        entry = derived.setdefault(
            edge_id,
            {
                "edge": {
                    "id": edge_id,
                    "relation_type_id": f"virtual:{name}",
                    "relation_type_name": name,
                    "source": source_id,
                    "target": target_id,
                    "virtual": True,
                },
                "evidence": {},
                "listed": 0,
                "counted": 0,
                "computed": {},
            },
        )
        numbered = False
        for column, value in row.items():
            if column in ("source", "target", "relationType"):
                continue
            if not numbered and isinstance(value, (int, float)) and not isinstance(value, bool) and math.isfinite(value):
                entry["counted"] += value
                numbered = True
            if isinstance(value, list):
                entry["listed"] += len(value)
            if _is_scalar(value):
                previous = entry["computed"].get(column)
                both_numbers = all(isinstance(v, (int, float)) and not isinstance(v, bool) for v in (value, previous))
                entry["computed"][column] = previous + value if both_numbers else value

            def collect(item: dict, entry=entry) -> None:
                if item["kind"] == "relation":
                    add_relation(item)
                else:
                    entry["evidence"][node_of(item)["id"]] = item

            _walk(value, collect)

    for entry in derived.values():
        edge, evidence = entry["edge"], entry["evidence"]
        count = len(evidence) or entry["listed"] or entry["counted"]
        if count:
            edge["evidence_count"] = count
        if entry["computed"]:
            edge["computed"] = entry["computed"]
        edges[edge["id"]] = edge
        for item in list(evidence.values())[:EVIDENCE_LIMIT]:
            evidence_id = add_node(item)
            for end in (edge["source"], edge["target"]):
                if end != evidence_id:
                    line = f"evidence|{evidence_id}|{end}"
                    edges.setdefault(
                        line,
                        {
                            "id": line,
                            "relation_type_id": "evidence",
                            "relation_type_name": "evidence",
                            "source": evidence_id,
                            "target": end,
                            "virtual": True,
                        },
                    )

    table = None
    if leftover_rows:
        node_cells = [c for c in dict.fromkeys(k for r in leftover_rows for k in r) if c not in leftover_columns]
        table = {"columns": node_cells + leftover_columns, "rows": leftover_rows}
    return CypherGraph(list(nodes.values()), list(edges.values()), table, holds_graph)


# ---- controlled expansion (EE controlled-expand.ts / cypher-expand.ts) ----

SEED = "s"
PEER = "t"
SEED_BATCH = 50
MAX_FETCH_LIMIT = 10000


@dataclass(frozen=True)
class IncidentRelation:
    """One relation type a seed can be expanded through, in one direction."""

    relation_type_id: str
    relation_type_name: str
    walk: str  # "out" follows source -> target, "in" goes against it
    seed_ot: str
    peer_ot: str


@dataclass
class ExpandGroup:
    seed_id: str
    rel: IncidentRelation
    total: int

    @property
    def aggregate_id(self) -> str:
        return aggregate_id(self.seed_id, self.rel.relation_type_id, self.rel.walk)


@dataclass
class SpacePlan:
    draw: list[ExpandGroup] = field(default_factory=list)
    aggregate: list[ExpandGroup] = field(default_factory=list)
    refresh: list[ExpandGroup] = field(default_factory=list)
    skipped: list[ExpandGroup] = field(default_factory=list)


def aggregate_id(seed_id: str, relation_type_id: str, walk: str) -> str:
    return f"agg|{seed_id}|{relation_type_id}|{walk}"


def incident_relations(ot_id: str, direction: str, relation_types: dict[str, dict]) -> list[IncidentRelation]:
    """direction: out, in or both."""
    out = []
    for rel_id, rel in relation_types.items():
        name = rel.get("name") or rel_id
        if direction != "in" and rel.get("source") == ot_id:
            out.append(IncidentRelation(rel_id, name, "out", ot_id, rel["target"]))
        if direction != "out" and rel.get("target") == ot_id:
            out.append(IncidentRelation(rel_id, name, "in", ot_id, rel["source"]))
    return out


def plan_space(groups: list[ExpandGroup], threshold: int, room: int, existing: set[str]) -> SpacePlan:
    """Groups above the threshold reserve one slot each for an aggregate; the rest
    are drawn smallest first while they fit, and fall back to an aggregate when
    they do not. Totals bound the new nodes from above, so the plan never
    overruns the room."""
    plan = SpacePlan()
    room = max(0, room)
    small, large = [], []
    for group in groups:
        if group.total <= 0:
            continue
        if group.aggregate_id in existing:
            plan.refresh.append(group)
        elif group.total > threshold:
            large.append(group)
        else:
            small.append(group)
    for group in large:
        if room >= 1:
            plan.aggregate.append(group)
            room -= 1
        else:
            plan.skipped.append(group)
    for group in sorted(small, key=lambda g: g.total):
        if room >= group.total:
            plan.draw.append(group)
            room -= group.total
        elif room >= 1:
            plan.aggregate.append(group)
            room -= 1
        else:
            plan.skipped.append(group)
    return plan


def quote(identifier: str) -> str:
    if not identifier or "`" in identifier:
        raise ValueError(f"identifier {identifier!r} cannot be quoted")
    return f"`{identifier}`"


_NUMERIC_TEXT = re.compile(r"^-?\d+(\.\d+)?$")


class Parameters:
    """Bound Cypher parameters; run_cypher takes scalars only, so a list is
    written as a literal of parameters."""

    def __init__(self) -> None:
        self.values: dict[str, Any] = {}

    def add(self, value: Any, declared: str = "") -> str:
        if value is None:
            raise ValueError("a null key value cannot be compared")
        bound = value
        if _NUMERIC_TYPE.search(declared or "") or (not declared and not isinstance(value, str)):
            raw = text(value)
            if isinstance(value, bool):
                bound = value
            elif _NUMERIC_TEXT.match(raw):
                number = float(raw)
                bound = int(number) if "." not in raw else number
                if text(bound) != raw:
                    bound = raw
        else:
            bound = text(value)
        name = f"p{len(self.values)}"
        self.values[name] = bound
        return f"${name}"


def _match(rel: IncidentRelation) -> str:
    seed, peer = f"({SEED}:{quote(rel.seed_ot)})", f"({PEER}:{quote(rel.peer_ot)})"
    edge = f"[:{quote(rel.relation_type_id)}]"
    return f"MATCH {seed}-{edge}->{peer}" if rel.walk == "out" else f"MATCH {seed}<-{edge}-{peer}"


def _seed_condition(meta: ObjectTypeMeta, identities: list[dict], params: Parameters) -> str:
    keys = meta.primary_keys
    if not keys or not identities:
        raise ValueError(f"object type {meta.id} has no seed to query")
    if len(keys) == 1:
        declared = meta.property_types.get(keys[0], "")
        values = ", ".join(params.add(i[keys[0]], declared) for i in identities)
        return f"{SEED}.{quote(keys[0])} IN [{values}]"
    tuples = [
        "(" + " AND ".join(
            f"{SEED}.{quote(k)} = {params.add(i[k], meta.property_types.get(k, ''))}" for k in keys
        ) + ")"
        for i in identities
    ]
    return tuples[0] if len(tuples) == 1 else "(" + " OR ".join(tuples) + ")"


def _seed_columns(meta: ObjectTypeMeta) -> list[str]:
    return [f"{SEED}.{quote(k)} AS {quote(f'{SEED}__{k}')}" for k in meta.primary_keys]


def count_query(rel: IncidentRelation, seed: ObjectTypeMeta, peer: ObjectTypeMeta, identities: list[dict]) -> tuple[str, dict]:
    params = Parameters()
    keys = peer.primary_keys
    counted = f"count(DISTINCT {PEER}.{quote(keys[0])})" if len(keys) == 1 else "count(*)"
    query = "\n".join(
        [
            _match(rel),
            f"WHERE {_seed_condition(seed, identities, params)}",
            f"RETURN {', '.join([*_seed_columns(seed), f'{counted} AS {quote(chr(110))}'])}",
        ]
    )
    return query, params.values


def fetch_query(
    rel: IncidentRelation, seed: ObjectTypeMeta, peer: ObjectTypeMeta, identities: list[dict], limit: int
) -> tuple[str, dict]:
    params = Parameters()
    properties = list(dict.fromkeys([*peer.primary_keys, *peer.property_types]))
    peer_columns = [f"{PEER}.{quote(p)} AS {quote(f'{PEER}__{p}')}" for p in properties]
    order = [quote(f"{SEED}__{k}") for k in seed.primary_keys] + [quote(f"{PEER}__{k}") for k in peer.primary_keys]
    query = "\n".join(
        [
            _match(rel),
            f"WHERE {_seed_condition(seed, identities, params)}",
            f"RETURN DISTINCT {', '.join([*_seed_columns(seed), *peer_columns])}",
            f"ORDER BY {', '.join(order)}",
            f"LIMIT {min(MAX_FETCH_LIMIT, max(1, limit))}",
        ]
    )
    return query, params.values


def _seed_key(seed: ObjectTypeMeta, row: dict) -> str | None:
    return identity_key(seed.primary_keys, {k: row.get(f"{SEED}__{k}") for k in seed.primary_keys})


def parse_counts(seed: ObjectTypeMeta, entries: list[dict]) -> dict[str, int]:
    counts: dict[str, int] = {}
    for row in entries:
        key = _seed_key(seed, row)
        try:
            n = int(row.get("n"))
        except (TypeError, ValueError):
            continue
        if key is not None:
            counts[key] = counts.get(key, 0) + n
    return counts


def rows_to_graph(
    rel: IncidentRelation, seed: ObjectTypeMeta, peer: ObjectTypeMeta, entries: list[dict], seed_ids: dict[str, str]
) -> tuple[list[dict], list[dict]]:
    nodes: dict[str, dict] = {}
    edges: dict[str, dict] = {}
    prefix = f"{PEER}__"
    for row in entries:
        key = _seed_key(seed, row)
        seed_id = seed_ids.get(key) if key is not None else None
        if not seed_id:
            continue
        props = {c[len(prefix):]: typed(peer, c[len(prefix):], v) for c, v in row.items() if c.startswith(prefix)}
        identity = {k: props.get(k) for k in peer.primary_keys}
        node_id = instance_id(peer.id, peer.primary_keys, identity) if identity_key(peer.primary_keys, identity) else None
        if not node_id:
            continue
        nodes.setdefault(node_id, _node(node_id, peer.id, identity, props))
        source, target = (seed_id, node_id) if rel.walk == "out" else (node_id, seed_id)
        edge = relation_edge(source, rel.relation_type_id, target, rel.relation_type_name)
        edges.setdefault(edge["id"], edge)
    return list(nodes.values()), list(edges.values())
