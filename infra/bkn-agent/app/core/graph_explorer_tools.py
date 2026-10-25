# Copyright openbkn.ai
#
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

"""The graph explorer's built-in tools (provider "graph_explorer").

The Web sends the canvas it shows as context.canvas. The tools read that
snapshot, plus whatever this run has already planned, and record canvas
changes as actions; the Web applies the actions after the run succeeds. The
run never waits for the browser.

Query tools call Context Loader as the run's user through the run's session
(the Agent also mounts context_loader with an empty allow-list, so the session
opens but none of its tools reach the model). Found instances stay candidates;
show_instances and run_cypher plan nodes and edges as merge_subgraph actions.
"""

import asyncio
import inspect
import json
import re
import uuid
from typing import Annotated, Any, Literal, Optional

from typing_extensions import Required, TypedDict

from langchain_core.tools import StructuredTool, ToolException
from pydantic import BaseModel, Field

from app.core import builtin_tools, context_loader
from app.core import graph_explorer_data as data
from app.core.graph_explorer_data import text

PROVIDER_NAME = "graph_explorer"
MAX_NODES = 5000
MAX_EDGES = 20000
PAGE_LIMIT = 100
DEFAULT_PAGE = 50
SAMPLE = 50
# The canvas size guard of the EE explorer (NODE_LIMIT).
NODE_LIMIT = 500
# Items the model reads per result, and properties / characters per item.
ITEM_LIMIT = 20
PROPERTY_CAP = 8
VALUE_CAP = 120
# Identities per query_object_instance request, paths per query_instance_subgraph.
ID_BATCH = 50
PATH_BATCH = 5
# ontology-query's largest limit (interfaces.MAX_LIMIT).
PATH_LIMIT = 10000
# get_object_types paging when get_kn_detail cannot list object types.
OBJECT_TYPE_PAGE = 200
MAX_OBJECT_TYPES = 2000
# Seeds one expansion takes, and Cypher queries it runs at once.
EXPAND_SEEDS = 50
EXPAND_CONCURRENCY = 4
EXPAND_THRESHOLD = 20
# Properties listed per object type in the schema the prompt carries.
SCHEMA_PROPERTIES = 40

# The Web's canvas choices, as explore-agent.ts offers them to the model.
Layout = Literal["force", "chain", "dagre", "tree", "radial", "circular", "grid"]
Grouping = Literal["none", "concept", "type", "manual", "relation"]
_HEX_COLOR = re.compile(r"^#[0-9a-fA-F]{6}$")

_Id = Field(min_length=1, max_length=512)


class CanvasNode(BaseModel):
    model_config = {"extra": "ignore"}
    id: str = _Id
    # An object type id, or `@value:<name>` for a value a derived edge ends at.
    object_type_id: str = Field(min_length=1, max_length=512)
    label: Optional[str] = Field(default=None, max_length=500)
    # Primary key values; needed to name an instance with a composite key.
    identity: Optional[dict[str, Any]] = None


class CanvasEdge(BaseModel):
    model_config = {"extra": "ignore"}
    id: str = _Id
    relation_type_id: str = Field(min_length=1, max_length=128)
    source: str = _Id
    target: str = _Id


class CanvasAggregate(BaseModel):
    """The neighbours one seed has through one relation type, when there are
    too many to draw: `total` exist, `on_canvas` of them are drawn as nodes."""

    model_config = {"extra": "ignore"}
    id: str = _Id
    seed_id: str = _Id
    relation_type_id: str = Field(min_length=1, max_length=128)
    total: int = Field(ge=0)
    on_canvas: int = Field(default=0, ge=0)


class ObjectTypeRef(BaseModel):
    model_config = {"extra": "ignore"}
    id: str = Field(min_length=1, max_length=128)
    name: Optional[str] = Field(default=None, max_length=200)


class CanvasLimits(BaseModel):
    """The Web's canvas settings the tools plan within."""

    model_config = {"extra": "ignore"}
    # Nodes the canvas holds, aggregates counted as one each.
    max_nodes: int = Field(default=NODE_LIMIT, ge=1, le=MAX_NODES)
    # Neighbours of one seed through one relation type drawn before they fold
    # into an aggregate.
    expand_threshold: int = Field(default=EXPAND_THRESHOLD, ge=0, le=MAX_NODES)


class CanvasContext(BaseModel):
    """context.canvas of a graph explorer execution."""

    model_config = {"extra": "forbid"}
    canvas_id: str = Field(min_length=1, max_length=100)
    revision: int = Field(ge=0)
    nodes: list[CanvasNode] = Field(default_factory=list, max_length=MAX_NODES)
    edges: list[CanvasEdge] = Field(default_factory=list, max_length=MAX_EDGES)
    aggregates: list[CanvasAggregate] = Field(default_factory=list, max_length=MAX_NODES)
    selected_ids: list[str] = Field(default_factory=list, max_length=MAX_NODES)
    # Layout, grouping, labels, analysis panel and the like, as the Web holds
    # them; the view tools read and update it.
    view: dict[str, Any] = Field(default_factory=dict)
    # The network's object types the view tools may name.
    object_types: list[ObjectTypeRef] = Field(default_factory=list, max_length=1000)
    limits: CanvasLimits = Field(default_factory=CanvasLimits)


def _canvas(run: builtin_tools.Run) -> CanvasContext:
    return run.context  # type: ignore[return-value]


def _actions(run: builtin_tools.Run) -> list[dict]:
    return run.state.setdefault("actions", [])


def _planned_nodes(run: builtin_tools.Run) -> dict[str, dict]:
    return run.state.setdefault("nodes", {})


def _planned_edges(run: builtin_tools.Run) -> dict[str, dict]:
    return run.state.setdefault("edges", {})


def _candidates(run: builtin_tools.Run) -> dict[str, dict]:
    """Instances this run found but has not planned for the canvas."""
    return run.state.setdefault("candidates", {})


def _warn(run: builtin_tools.Run, message: str) -> None:
    warnings = run.state.setdefault("warnings", [])
    if message not in warnings:
        warnings.append(message)


def _dump(value: Any) -> str:
    return json.dumps(value, ensure_ascii=False, default=str)


# ---- Context Loader ----


def _text_of(content: Any) -> str:
    if isinstance(content, str):
        return content
    if isinstance(content, list):
        return "".join(
            block.get("text", "") if isinstance(block, dict) else str(block) for block in content
        )
    return str(content)


async def _call(run: builtin_tools.Run, name: str, **arguments) -> dict:
    """Call one Context Loader tool of this run's session, as the run's user, and
    return its JSON answer. Raises ToolException with the upstream message."""
    tools = run.state.get("context_loader")
    tool = tools.get(name) if tools else None
    if tool is None:
        raise ToolException(f"Context Loader is unavailable, so {name} cannot run")
    try:
        raw = await tool.coroutine(kn_id=run.kn_id, response_format="json", **arguments)
    except ToolException:
        raise
    except Exception as exc:
        # A timeout or a dropped connection is this call's failure, not the
        # run's: the model hears about it and can work around it.
        raise ToolException(f"{name} could not reach Context Loader: {type(exc).__name__}: {exc}"[:1000]) from exc
    content = raw[0] if isinstance(raw, tuple) else raw
    body = _text_of(content).strip()
    try:
        value = json.loads(body) if body else {}
    except ValueError:
        raise ToolException(body[:1000] or f"{name} returned nothing")
    if isinstance(value, dict) and value.get("error") and len(value) <= 3:
        raise ToolException(text(value.get("error"))[:1000])
    return value if isinstance(value, dict) else {"value": value}


async def _network(run: builtin_tools.Run) -> dict:
    """Object type ids and names, and relation types with their ends, read once."""
    cached = run.state.get("network")
    if cached is None:
        detail = await _call(run, "get_kn_detail")
        listed = detail.get("object_types")
        if listed is None:
            # Past 200 object types or 600 relation types get_kn_detail leaves
            # both out; object types can still be listed page by page.
            listed = []
            offset: Any = 0
            # Follow next_offset: a page may hold fewer entries than it scanned.
            while offset is not None and len(listed) < MAX_OBJECT_TYPES:
                page = await _call(run, "get_object_types", offset=offset, limit=OBJECT_TYPE_PAGE)
                listed.extend(t for t in page.get("object_types") or [] if isinstance(t, dict))
                following = page.get("next_offset")
                offset = following if isinstance(following, int) and following > offset else None
            detail = {**detail, "object_types": listed}
        object_types = {
            t["id"]: {
                "name": t.get("name") or t["id"],
                "properties": [
                    f"{p['name']}:{p['type']}" if p.get("type") else p["name"]
                    for p in (t.get("data_properties") or t.get("properties") or [])
                    if isinstance(p, dict) and isinstance(p.get("name"), str)
                ][:SCHEMA_PROPERTIES],
            }
            for t in detail.get("object_types") or []
            if isinstance(t, dict) and isinstance(t.get("id"), str)
        }
        relation_types = {
            r["id"]: {
                "name": r.get("name") or r["id"],
                "source": r.get("source_object_type_id"),
                "target": r.get("target_object_type_id"),
            }
            for r in detail.get("relation_types") or []
            if isinstance(r, dict) and isinstance(r.get("id"), str)
        }
        cached = run.state["network"] = {
            "object_types": object_types,
            "relation_types": relation_types,
            # get_kn_detail could not list them: the network is too large.
            "relations_unlisted": detail.get("relation_types") is None,
        }
    return cached


async def _metas(run: builtin_tools.Run, ot_ids) -> dict[str, data.ObjectTypeMeta]:
    cache: dict = run.state.setdefault("metas", {})
    # A value node's type is not an object type; asking for it fails the whole lookup.
    missing = [
        i for i in dict.fromkeys(ot_ids) if i and i not in cache and not i.startswith(data.VALUE_TYPE_PREFIX)
    ]
    if missing:
        payload = await _call(run, "get_object_types", ids=missing)
        for raw in payload.get("object_types") or []:
            meta = data.meta_from(raw)
            if meta:
                cache[meta.id] = meta
    return {i: cache[i] for i in dict.fromkeys(ot_ids) if i in cache}


# ---- working state ----


def _known_node(run: builtin_tools.Run, node_id: str) -> dict | None:
    """A node this run can refer to: planned, found, or on the submitted canvas."""
    planned = _planned_nodes(run).get(node_id) or _candidates(run).get(node_id)
    if planned:
        return planned
    for node in _canvas(run).nodes:
        if node.id == node_id:
            return {"id": node.id, "object_type_id": node.object_type_id, "identity": node.identity or {}}
    return None


def _planned_aggregates(run: builtin_tools.Run) -> dict[str, dict]:
    return run.state.setdefault("aggregates", {})


def _room(run: builtin_tools.Run) -> int:
    canvas = _canvas(run)
    used = len(_present_nodes(run)) + len(_present_aggregates(run))
    return canvas.limits.max_nodes - used


def _removed(run: builtin_tools.Run) -> set[str]:
    """Nodes and aggregates this run has planned to take off the canvas."""
    return run.state.setdefault("removed", set())


def _selected(run: builtin_tools.Run) -> list[str]:
    if "selected" not in run.state:
        run.state["selected"] = list(_canvas(run).selected_ids)
    return run.state["selected"]


def _present_nodes(run: builtin_tools.Run) -> set[str]:
    """Node ids on the canvas once this run's plan is applied."""
    ids = {n.id for n in _canvas(run).nodes} | set(_planned_nodes(run))
    return ids - _removed(run)


def _present_aggregates(run: builtin_tools.Run) -> set[str]:
    ids = {a.id for a in _canvas(run).aggregates} | set(_planned_aggregates(run))
    return ids - _removed(run)


def _record(run: builtin_tools.Run, action_type: str, **fields: Any) -> dict:
    """Record a view action. What it changes in the working state can be undone
    by canvas_control undo, so the removal set and selection before it are kept."""
    action = {"action_id": f"{len(_actions(run)) + 1}", "type": action_type, **fields}
    run.state.setdefault("undo", {})[action["action_id"]] = (set(_removed(run)), list(_selected(run)))
    _actions(run).append(action)
    return action


def _check_nodes(run: builtin_tools.Run, ids: list[str]) -> list[str]:
    ids = list(dict.fromkeys(ids))
    missing = [i for i in ids if i not in _present_nodes(run)]
    if missing:
        raise ToolException(f"Not on the canvas: {', '.join(missing[:10])}. Use ids from canvas_state.")
    return ids


def _refuse_value_ends(run: builtin_tools.Run, ids: list[str]) -> None:
    """A path runs through the network between instances; a value node is not one."""
    values = [
        i for i in ids if str((_known_node(run, i) or {}).get("object_type_id") or "").startswith(data.VALUE_TYPE_PREFIX)
    ]
    if values:
        raise ToolException(
            f"Value nodes cannot start or end a path: {', '.join(values[:5])}. Use the instances linked to them."
        )


def _take_off(run: builtin_tools.Run, ids: list[str]) -> None:
    _removed(run).update(ids)
    run.state["selected"] = [i for i in _selected(run) if i not in ids]


def _merge(
    run: builtin_tools.Run,
    nodes: list[dict],
    edges: list[dict],
    anchor: str | None = None,
    aggregates: list[dict] = (),
) -> dict:
    """Plan nodes, edges and aggregates for the canvas as one merge_subgraph
    action. Nodes past the canvas limit are left out with a warning, and so are
    their edges."""
    canvas = _canvas(run)
    on_canvas = {n.id for n in canvas.nodes}
    canvas_edges = {e.id for e in canvas.edges}
    planned_nodes, planned_edges = _planned_nodes(run), _planned_edges(run)
    room = _room(run)
    removed = _removed(run)
    added_nodes, kept, cut = [], (set(on_canvas) | set(planned_nodes)) - removed, 0
    for node in nodes:
        if node["id"] in removed:
            # Shown again after this run took it off: back on, with its data, taking room like a
            # new node. The Web completes its relations to the canvas when it adds it.
            if room <= 0:
                cut += 1
                continue
            removed.discard(node["id"])
            planned_nodes.setdefault(node["id"], node)
            kept.add(node["id"])
            added_nodes.append(node["id"])
            room -= 1
            continue
        if node["id"] in kept:
            existing = planned_nodes.get(node["id"])
            if existing is not None:
                existing["properties"] = {**existing["properties"], **node["properties"]}
                if node.get("computed"):
                    existing["computed"] = {**existing.get("computed", {}), **node["computed"]}
            continue
        if room <= 0:
            cut += 1
            continue
        planned_nodes[node["id"]] = node
        _candidates(run).pop(node["id"], None)
        kept.add(node["id"])
        added_nodes.append(node["id"])
        room -= 1
    if cut:
        _warn(run, f"The canvas holds at most {canvas.limits.max_nodes} nodes; {cut} were not planned.")
    added_edges = []
    for edge in edges:
        if edge["id"] in canvas_edges or edge["source"] not in kept or edge["target"] not in kept:
            continue
        if edge["id"] not in planned_edges:
            planned_edges[edge["id"]] = edge
            added_edges.append(edge["id"])
    node_ids = [n["id"] for n in nodes if n["id"] in kept]
    edge_ids = [e["id"] for e in edges if e["id"] in planned_edges]
    for aggregate in aggregates:
        _planned_aggregates(run)[aggregate["id"]] = aggregate
    aggregate_ids = [a["id"] for a in aggregates]
    if node_ids or edge_ids or aggregate_ids:
        action = {
            "action_id": f"{len(_actions(run)) + 1}",
            "type": "merge_subgraph",
            "node_ids": list(dict.fromkeys(node_ids)),
            "edge_ids": list(dict.fromkeys(edge_ids)),
        }
        if aggregate_ids:
            action["aggregate_ids"] = aggregate_ids
        if anchor and anchor in kept:
            action["anchor_id"] = anchor
        _actions(run).append(action)
    return {
        "planned_added": {"nodes": len(added_nodes), "edges": len(added_edges)},
        **({"left_out": cut} if cut else {}),
    }


def _summary(nodes: list[dict], with_properties: bool = True) -> dict:
    """What the model reads about nodes: id, type, label, key, computed values and
    a few properties, for the first ITEM_LIMIT of them."""
    items = []
    for node in nodes[:ITEM_LIMIT]:
        item = {"id": node["id"], "type": node["object_type_id"], "label": node.get("label")}
        if node.get("identity"):
            item["key"] = node["identity"]
        if node.get("computed"):
            item["computed"] = node["computed"]
        if with_properties:
            props = {}
            for key, value in (node.get("properties") or {}).items():
                if key.startswith("_") or value in (None, ""):
                    continue
                props[key] = text(value)[:VALUE_CAP]
                if len(props) >= PROPERTY_CAP:
                    break
            item["props"] = props
        items.append(item)
    return {"count": len(nodes), "items": items}


def _edge_summary(edges: list[dict]) -> dict:
    return {
        "count": len(edges),
        "items": [f"{e['source']} -[{e['relation_type_id']}]-> {e['target']}" for e in edges[:ITEM_LIMIT]],
    }


# ---- tools ----


def _tool(fn) -> StructuredTool:
    # The docstring is what the model reads; drop the source indentation.
    return StructuredTool.from_function(
        coroutine=fn, name=fn.__name__, description=inspect.cleandoc(fn.__doc__ or "")
    )


class LabelSetting(TypedDict):
    object_type: Annotated[str, Field(min_length=1, description="Object type id.")]
    label: Optional[str]


class AnalysisConfig(TypedDict, total=False):
    kind: Literal["types", "relations", "category", "numeric", "time", "degree", "scatter"]
    otIds: Annotated[Optional[list[str]], Field(description="Types to analyse; null selects the default type. numeric takes one type.")]
    propertyByOt: Annotated[dict[str, str], Field(description="Object type id -> grouping, date or numeric property.")]
    degreeOt: Annotated[Optional[str], Field(description="Degree chart type filter; null for all types.")]
    grain: Literal["auto", "day", "week", "month", "year"]
    stacked: bool
    brushing: bool
    measure: Literal["count", "sum", "avg", "max", "min"]
    measureByOt: Annotated[dict[str, str], Field(description="Object type id -> numeric property for a non-count measure.")]
    withFolded: bool
    xByOt: dict[str, str]
    yByOt: dict[str, str]


class Filter(TypedDict, total=False):
    # A TypedDict, not a model: tool arguments must stay JSON for the trace hash.
    field: Required[Annotated[str, Field(min_length=1, description="Property name.")]]
    operation: Required[Literal["==", "!=", ">", ">=", "<", "<=", "in", "like"]]
    value: Any


def _condition(filters: list[Filter] | None) -> dict | None:
    leaves = [
        {"field": f["field"], "operation": f["operation"], "value": f.get("value")} for f in filters or []
    ]
    if not leaves:
        return None
    return leaves[0] if len(leaves) == 1 else {"operation": "and", "sub_conditions": leaves}


async def _fetch_by_identity(
    run: builtin_tools.Run, meta: data.ObjectTypeMeta, identities: list[dict]
) -> list[dict]:
    """Full instances of one object type for these identities, 50 per request; a
    composite key is grouped by its leading values so no request matches a
    combination nobody asked for."""
    keys = meta.primary_keys
    leading, last = keys[:-1], keys[-1]
    wanted: set[str] = set()
    groups: dict[str, list[dict]] = {}
    for identity in identities:
        key = data.identity_key(keys, identity)
        if key is None or key in wanted:
            continue
        wanted.add(key)
        groups.setdefault(data.identity_key(leading, identity) or "" if leading else "", []).append(identity)
    nodes = []
    for members in groups.values():
        fixed = [{"field": f, "operation": "==", "value": members[0][f]} for f in leading]
        for start in range(0, len(members), ID_BATCH):
            chunk = [i[last] for i in members[start : start + ID_BATCH]]
            within = {"field": last, "operation": "in", "value": chunk}
            condition = within if not fixed else {"operation": "and", "sub_conditions": [*fixed, within]}
            payload = await _call(
                run, "query_object_instance", ot_id=meta.id, condition=condition, limit=len(chunk)
            )
            for node in data.nodes_from_rows(meta, payload):
                if data.identity_key(keys, node["identity"]) in wanted:
                    nodes.append(node)
    return nodes


def _pairs(by_ot: dict[str, list], rel: dict) -> int:
    return len(by_ot[rel["source"]]) * len(by_ot[rel["target"]])


async def _edges_among(run: builtin_tools.Run, nodes: list[dict], metas) -> list[dict]:
    """Relations among the given nodes: one query_instance_subgraph path per
    relation type whose two ends are both in the set, five paths a request; a
    refused batch is retried path by path so one relation type costs only its own
    edges."""
    network = await _network(run)
    if network.get("relations_unlisted"):
        _warn(run, "This network is too large to list its relation types, so relations among shown instances are not drawn.")
        return []
    by_ot: dict[str, list[dict]] = {}
    for node in nodes:
        by_ot.setdefault(node["object_type_id"], []).append(node["identity"])
    ids = {n["id"] for n in nodes}
    paths = []
    for rel_id, rel in network["relation_types"].items():
        if rel["source"] in by_ot and rel["target"] in by_ot:
            source, target = metas.get(rel["source"]), metas.get(rel["target"])
            if not source or not target or not source.primary_keys or not target.primary_keys:
                continue
            paths.append(
                {
                    "object_types": [
                        {"id": rel["source"], "condition": data.identity_condition(source, by_ot[rel["source"]])},
                        {"id": rel["target"], "condition": data.identity_condition(target, by_ot[rel["target"]])},
                    ],
                    "relation_types": [
                        {
                            "relation_type_id": rel_id,
                            "source_object_type_id": rel["source"],
                            "target_object_type_id": rel["target"],
                        }
                    ],
                    # The path's limit caps the relations it returns in total,
                    # so it must cover every pair of the two ends.
                    "limit": min(PATH_LIMIT, _pairs(by_ot, rel)),
                }
            )
    edges: dict[str, dict] = {}

    async def collect(group: list[dict]) -> bool:
        try:
            payload = await _call(run, "query_instance_subgraph", relation_type_paths=group)
        except ToolException:
            return False
        returned: dict[str, int] = {}
        for entry in payload.get("entries") or []:
            for edge in data.edges_from_subgraph(entry):
                returned[edge["relation_type_id"]] = returned.get(edge["relation_type_id"], 0) + 1
                if edge["source"] in ids and edge["target"] in ids:
                    rel = network["relation_types"].get(edge["relation_type_id"])
                    if rel:
                        edge["relation_type_name"] = rel["name"]
                    edges.setdefault(edge["id"], edge)
        for path in group:
            rel_id = path["relation_types"][0]["relation_type_id"]
            # Only a limit cut below the number of pairs can drop relations;
            # one that covers every pair is reached when all of them exist.
            capped = path["limit"] < _pairs(by_ot, network["relation_types"][rel_id])
            if capped and returned.get(rel_id, 0) >= path["limit"]:
                _warn(run, f"Relations of type {rel_id} reached the {path['limit']} limit and may be incomplete.")
        return True

    for start in range(0, len(paths), PATH_BATCH):
        batch = paths[start : start + PATH_BATCH]
        if not await collect(batch):
            for single in batch:
                if not await collect([single]):
                    _warn(run, f"Relations of type {single['relation_types'][0]['relation_type_id']} could not be read.")
    return list(edges.values())


async def _enrich(run: builtin_tools.Run, nodes: list[dict], metas) -> None:
    """Fill in nodes that came back with their key only (Cypher rows), so labels
    and properties are the instance's own; computed values stay. A node whose
    instance is not found keeps what it has."""
    stubs: dict[str, list[dict]] = {}
    for node in nodes:
        meta = metas.get(node["object_type_id"])
        if not meta or not meta.primary_keys or set(node["properties"]) - set(node["identity"]):
            continue
        if data.identity_key(meta.primary_keys, node["identity"]) is not None:
            stubs.setdefault(meta.id, []).append(node)
    for ot_id, group in stubs.items():
        try:
            full = await _fetch_by_identity(run, metas[ot_id], [n["identity"] for n in group])
        except ToolException:
            continue
        by_id = {n["id"]: n for n in full}
        for node in group:
            found = by_id.get(node["id"])
            if found:
                node["properties"] = found["properties"]
                node["label"] = found["label"]


def _seed_identity(meta: data.ObjectTypeMeta, node: dict) -> dict | None:
    identity = node.get("identity") or {}
    if data.identity_key(meta.primary_keys, identity) is not None:
        return identity
    if len(meta.primary_keys) == 1:
        return {meta.primary_keys[0]: data.key_value_for(meta, node["id"][len(meta.id) + 1 :])}
    return None


async def _gather_limited(tasks, limit: int) -> None:
    gate = asyncio.Semaphore(limit)

    async def one(task):
        async with gate:
            await task()

    await asyncio.gather(*(one(t) for t in tasks))


async def _expand(run: builtin_tools.Run, ids: list[str], direction: str) -> dict:
    """The EE explorer's controlled expansion: count every incident relation type
    per seed batch, plan the canvas room, fetch the groups that fit and fold the
    rest into aggregates."""
    wanted = list(dict.fromkeys(ids))
    known = [n for i in wanted if (n := _known_node(run, i))]
    if not known:
        raise ToolException(f"Unknown instance ids: {', '.join(ids[:5])}; find them first")
    # Value nodes are not instances and have no neighbours to walk to.
    values = [n["id"] for n in known if str(n.get("object_type_id") or "").startswith(data.VALUE_TYPE_PREFIX)]
    known = [n for n in known if n["id"] not in values]
    if not known:
        raise ToolException("Value nodes are not instances and cannot be expanded; expand the instances linked to them")
    seeds = known[:EXPAND_SEEDS]
    network = await _network(run)
    if network.get("relations_unlisted"):
        # Without the relation types there is nothing to count; "no neighbours" would be false.
        raise ToolException(
            "This network is too large to list its relation types, so neighbours cannot be counted here. "
            "Use run_cypher with a named relation type instead."
        )
    rels_by_ot = {
        ot: data.incident_relations(ot, direction, network["relation_types"])
        for ot in {s["object_type_id"] for s in seeds}
    }
    ot_ids = list(rels_by_ot) + [r.peer_ot for rels in rels_by_ot.values() for r in rels]
    metas = await _metas(run, ot_ids)
    # Seeds that are only candidates go on the canvas with their neighbours.
    absent = [s for s in seeds if s["id"] in _candidates(run)]
    seed_refs: dict[str, list[tuple[dict, str, dict]]] = {}
    skipped_seeds = list(values)
    for seed in seeds:
        meta = metas.get(seed["object_type_id"])
        identity = _seed_identity(meta, seed) if meta and meta.primary_keys else None
        if identity is None:
            skipped_seeds.append(seed["id"])
            continue
        seed_refs.setdefault(meta.id, []).append((seed, data.identity_key(meta.primary_keys, identity), identity))

    failures: dict[tuple[str, str], str] = {}
    groups: list[data.ExpandGroup] = []
    attempted = 0

    async def cypher(query: str, parameters: dict) -> list[dict]:
        result = await _call(run, "run_cypher", query=query, parameters=parameters)
        return [e for e in result.get("entries") or [] if isinstance(e, dict)]

    tasks = []
    for ot_id, refs in seed_refs.items():
        for rel in rels_by_ot.get(ot_id, []):
            attempted += 1
            seed_meta, peer_meta = metas.get(rel.seed_ot), metas.get(rel.peer_ot)
            if not seed_meta or not peer_meta or not peer_meta.primary_keys:
                failures[(rel.relation_type_id, rel.walk)] = f"object type {rel.peer_ot} has no primary key"
                continue
            for start in range(0, len(refs), data.SEED_BATCH):
                batch = refs[start : start + data.SEED_BATCH]

                async def count(rel=rel, seed_meta=seed_meta, peer_meta=peer_meta, batch=batch):
                    try:
                        query, params = data.count_query(rel, seed_meta, peer_meta, [r[2] for r in batch])
                        counts = data.parse_counts(seed_meta, await cypher(query, params))
                    except (ToolException, ValueError) as exc:
                        failures.setdefault((rel.relation_type_id, rel.walk), str(exc)[:300])
                        return
                    for seed, key, _ in batch:
                        if counts.get(key, 0) > 0:
                            groups.append(data.ExpandGroup(seed["id"], rel, counts[key]))

                tasks.append(count)
    await _gather_limited(tasks, EXPAND_CONCURRENCY)
    if attempted and len(failures) == attempted:
        raise ToolException("; ".join(sorted(set(failures.values())))[:1000])

    canvas = _canvas(run)
    existing = {a.id for a in canvas.aggregates} | set(_planned_aggregates(run))
    plan = data.plan_space(groups, canvas.limits.expand_threshold, _room(run) - len(absent), existing)

    nodes: dict[str, dict] = {}
    edges: dict[str, dict] = {}
    unfetched: list[data.ExpandGroup] = []
    by_rel: dict[tuple, list[data.ExpandGroup]] = {}
    for group in plan.draw:
        by_rel.setdefault((group.rel.seed_ot, group.rel.relation_type_id, group.rel.walk), []).append(group)
    fetches = []
    for drawn in by_rel.values():
        rel = drawn[0].rel
        seed_meta, peer_meta = metas[rel.seed_ot], metas[rel.peer_ot]
        refs = {r[0]["id"]: r for r in seed_refs[rel.seed_ot]}
        for start in range(0, len(drawn), data.SEED_BATCH):
            batch = drawn[start : start + data.SEED_BATCH]

            async def fetch(rel=rel, seed_meta=seed_meta, peer_meta=peer_meta, batch=batch, refs=refs):
                chosen = [refs[g.seed_id] for g in batch]
                try:
                    query, params = data.fetch_query(
                        rel, seed_meta, peer_meta, [r[2] for r in chosen], sum(g.total for g in batch)
                    )
                    entries = await cypher(query, params)
                except (ToolException, ValueError) as exc:
                    failures.setdefault((rel.relation_type_id, rel.walk), str(exc)[:300])
                    unfetched.extend(batch)
                    return
                found_nodes, found_edges = data.rows_to_graph(
                    rel, seed_meta, peer_meta, entries, {r[1]: r[0]["id"] for r in chosen}
                )
                for node in found_nodes:
                    nodes.setdefault(node["id"], node)
                for edge in found_edges:
                    edges.setdefault(edge["id"], edge)

            fetches.append(fetch)
    await _gather_limited(fetches, EXPAND_CONCURRENCY)

    aggregates = [
        {
            "id": g.aggregate_id,
            "seed_id": g.seed_id,
            "relation_type_id": g.rel.relation_type_id,
            "relation_type_name": g.rel.relation_type_name,
            "walk": g.rel.walk,
            "peer_object_type_id": g.rel.peer_ot,
            "total": g.total,
        }
        for g in [*plan.aggregate, *plan.refresh]
    ]
    planned = _merge(
        run,
        absent + list(nodes.values()),
        list(edges.values()),
        anchor=seeds[0]["id"] if len(seeds) == 1 else None,
        aggregates=aggregates,
    )
    type_name = lambda ot: network["object_types"].get(ot, {}).get("name", ot)  # noqa: E731
    failed_rels = set(failures)
    aggregated = [
        {"seed": g.seed_id, "relation": g.rel.relation_type_name, "target_type": type_name(g.rel.peer_ot), "total": g.total}
        for g in [*plan.aggregate, *plan.refresh, *[
            s for s in [*plan.skipped, *unfetched] if (s.rel.relation_type_id, s.rel.walk) not in failed_rels
        ]]
    ]
    out: dict[str, Any] = {
        **_summary(list(nodes.values()), with_properties=False),
        "edges": _edge_summary(list(edges.values())),
        **planned,
    }
    if aggregated:
        out["aggregated"] = aggregated
    if failures:
        out["failed"] = [{"relation": r, "walk": w, "error": e} for (r, w), e in failures.items()]
    if skipped_seeds:
        out["skipped_seeds"] = skipped_seeds
    not_found = [i for i in wanted if not _known_node(run, i)]
    if not_found:
        out["not_found"] = not_found
    if len(known) > EXPAND_SEEDS:
        out["not_expanded"] = {
            "count": len(known) - EXPAND_SEEDS,
            "note": f"At most {EXPAND_SEEDS} seeds per call; expand the rest in another call.",
        }
    if not groups and not failures:
        out["note"] = "No neighbours in that direction."
    return out


_RETURN = re.compile(r"\bRETURN\b", re.I)


def _tools(run: builtin_tools.Run) -> list[StructuredTool]:
    session = context_loader.current_session()
    if session is not None:
        run.state["context_loader"] = {t.name: t for t in session.tools()}

    async def search_instances(
        query: Annotated[str, Field(min_length=1, description="What to look for, in natural language.")],
        object_types: Annotated[
            Optional[list[str]], Field(description="Restrict to these object type ids.")
        ] = None,
        limit: Annotated[int, Field(ge=1, le=50, description="Candidates per object type, default 20.")] = 20,
    ) -> str:
        """Semantic search for instances by natural-language text. Returns candidates
        (id, type, label, key, a few properties); nothing is planned for the canvas.
        Use show_instances to put chosen ids on the canvas."""
        arguments = {"query": query, "max_instances_per_type": limit}
        if object_types:
            arguments["object_types"] = object_types
        payload = await _call(run, "search_instance", **arguments)
        hits = (payload.get("nodes") if isinstance(payload, dict) else None) or []
        metas = await _metas(run, [h.get("object_type_id") for h in hits if isinstance(h, dict)])
        nodes = data.nodes_from_search(payload, metas)
        for node in nodes:
            _candidates(run)[node["id"]] = node
        return _dump(_summary(nodes))

    async def query_instances(
        object_type: Annotated[str, Field(min_length=1, description="Object type id.")],
        filters: Annotated[
            Optional[list[Filter]],
            Field(description="Filters joined with AND; empty lists the first instances."),
        ] = None,
        limit: Annotated[int, Field(ge=1, le=100, description="Default 20.")] = 20,
    ) -> str:
        """Exact filtering over one object type's properties (operations: ==, !=, >,
        >=, <, <=, in, like). Returns candidates; nothing is planned for the canvas.
        Names are usually long full names, so prefer search_instances for a short
        name."""
        meta = (await _metas(run, [object_type])).get(object_type)
        if meta is None:
            raise ToolException(f"Unknown object type: {object_type}")
        arguments: dict[str, Any] = {"ot_id": object_type, "limit": limit}
        condition = _condition(filters)
        if condition:
            arguments["condition"] = condition
        nodes = data.nodes_from_rows(meta, await _call(run, "query_object_instance", **arguments))
        for node in nodes:
            _candidates(run)[node["id"]] = node
        return _dump(_summary(nodes))

    async def show_instances(
        ids: Annotated[
            list[str],
            Field(min_length=1, description="Instance ids, each `<object type id>-<primary key value>`."),
        ],
    ) -> str:
        """Plan instances for the canvas by id, together with the relations among
        them. Returns what was planned; the Web draws it when the run finishes."""
        network = await _network(run)
        items, unknown = data.parse_ids(ids, list(network["object_types"]))
        if not items:
            raise ToolException(f"No object type matches these ids: {', '.join(unknown[:5])}")
        metas = await _metas(run, [ot for ot, _ in items])
        by_ot: dict[str, list[dict]] = {}
        for ot_id, key in items:
            meta = metas.get(ot_id)
            if meta is None or not meta.primary_keys:
                raise ToolException(f"Object type {ot_id} has no primary key, so its instances cannot be named")
            known = _known_node(run, f"{ot_id}-{key}")
            identity = known.get("identity") if known else None
            if not identity or data.identity_key(meta.primary_keys, identity) is None:
                if len(meta.primary_keys) > 1:
                    raise ToolException(
                        f"Instance {ot_id}-{key} has a composite key; find it with search_instances or query_instances first"
                    )
                identity = {meta.primary_keys[0]: data.key_value_for(meta, key)}
            by_ot.setdefault(ot_id, []).append(identity)
        nodes = []
        for ot_id, identities in by_ot.items():
            nodes.extend(await _fetch_by_identity(run, metas[ot_id], identities))
        edges = await _edges_among(run, nodes, metas)
        planned = _merge(run, nodes, edges)
        return _dump(
            {
                **_summary(nodes, with_properties=False),
                "edges": _edge_summary(edges),
                **planned,
                **({"not_found": unknown[:ITEM_LIMIT]} if unknown else {}),
            }
        )

    async def expand_neighbours(
        ids: Annotated[
            list[str],
            Field(min_length=1, description="Instance ids, each `<object type id>-<primary key value>`."),
        ],
        direction: Annotated[
            Literal["out", "in", "both"],
            Field(description="out follows source -> target, in goes against it; both by default."),
        ] = "both",
    ) -> str:
        """Plan the one-hop neighbours of the given instances for the canvas. direction
        out follows the relation type's declared source -> target, in goes against
        it, both takes both (default). Every relation type is counted first; a
        relation type with too many neighbours for one seed is not drawn but listed
        under `aggregated` with its total: those neighbours exist, they are just not
        on the canvas. To see some of them, fetch them with query_instances or
        run_cypher and a condition."""
        return _dump(await _expand(run, ids, direction))

    async def run_cypher(
        query: Annotated[
            str,
            Field(min_length=1, description="A complete read-only query: MATCH … [WHERE …] RETURN … [LIMIT n]."),
        ],
    ) -> str:
        """Run openCypher and plan the result for the canvas. Node labels are object
        type ids, every relation is directed and names exactly one relation type id,
        no variable length. Write a complete query with RETURN; it runs as written.
        LIMIT is optional: without one the platform's default row limit applies, and
        `limited` says when it cut the rows. Nodes beyond the canvas limit are not
        planned. Return node and relationship variables (RETURN p,
        r, m), or a derived edge `RETURN a AS source, 'USES' AS relationType, b AS
        target, count(x) AS n`. One end of a derived edge may be a value (`t AS
        source, 'REGION' AS relationType, t.region AS target`): it is planned as a
        value node, one per name and value, so instances group by value; a value
        node is not an instance and cannot be shown, expanded or queried by id.
        Scalar columns returned beside one node variable are
        attached to that node as computed values (RETURN t, count(g) AS goals); with
        several node variables, name a column `<variable>_<name>` (p_orders) to attach
        it to that variable. Columns no node claims, and results without nodes, come
        back as `table` rows."""
        if not _RETURN.search(query):
            raise ToolException("Write a complete query with RETURN; fragments are not run.")
        result = await _call(run, "run_cypher", query=query)
        entries = [e for e in result.get("entries") or [] if isinstance(e, dict)]
        ot_ids: list[str] = []

        def note(item):
            if item.get("kind") == "object":
                ot_ids.append(item["object_type_id"])

        for row in entries:
            data._walk(row, note)
        metas = await _metas(run, ot_ids) if ot_ids else {}
        network = await _network(run)
        names = {i: r["name"] for i, r in network["relation_types"].items()}
        graph = data.graph_from_cypher(result, metas, names)
        # Only new nodes the canvas has room for get filled in; _merge leaves the rest out.
        known = _present_nodes(run) | set(_planned_nodes(run))
        fresh = [n for n in graph.nodes if n["id"] not in known]
        await _enrich(run, fresh[: max(0, _room(run))], metas)
        # Nodes the canvas or this run already has keep the label it knows, not their key.
        labels = {n.id: n.label for n in _canvas(run).nodes if n.label}
        for node in graph.nodes:
            if node["id"] in known:
                label = (_planned_nodes(run).get(node["id"]) or {}).get("label") or labels.get(node["id"])
                if label:
                    node["label"] = label
        out: dict[str, Any] = {"rows": len(entries)}
        execution = result.get("execution") if isinstance(result.get("execution"), dict) else {}
        # Scalar rows cut at the default limit say so at the top; graph values in execution.
        if result.get("limited") or execution.get("conclusions_limited"):
            out["limited"] = "The rows stop at the platform's default limit; narrow the query or add LIMIT for a smaller, complete answer."
        if graph.holds_graph:
            out.update(_summary(graph.nodes, with_properties=False))
            out["edges"] = _edge_summary(graph.edges)
            out.update(_merge(run, graph.nodes, graph.edges))
        if graph.table:
            out["table"] = {
                "columns": graph.table["columns"],
                "rows": graph.table["rows"][:ITEM_LIMIT],
                "total": len(graph.table["rows"]),
            }
        elif not graph.holds_graph:
            columns = [c.get("name") for c in result.get("columns") or [] if isinstance(c, dict)]
            out["table"] = {"columns": columns, "rows": entries[:ITEM_LIMIT], "total": len(entries)}
        return _dump(out)

    async def canvas_state(
        object_types: Annotated[
            Optional[list[str]],
            Field(description="Object type ids to keep; omit for every type."),
        ] = None,
        offset: Annotated[int, Field(description="Nodes to skip, for paging.")] = 0,
        limit: Annotated[int, Field(description="Nodes to return, at most 100.")] = DEFAULT_PAGE,
    ) -> str:
        """What is on the canvas: nodes (id, type, label), edges and aggregates, with
        what this run has planned so far marked `planned` (the Web draws planned
        items when the run finishes). Nodes can be filtered by object_types and paged
        with offset/limit (default 50, maximum 100); nodes.count is the filtered
        total, total_nodes the whole canvas, next_offset is null at the end. Edges and
        aggregates show their totals and first 50 items. An aggregate stands for the
        neighbours one seed has through one relation type when there are too many to
        draw: `total` of them exist, `on_canvas` of those are drawn as nodes. Count an
        aggregate's neighbours when asked how many of a type the canvas holds."""
        canvas = _canvas(run)
        planned_nodes, planned_edges = _planned_nodes(run), _planned_edges(run)
        every = [n.model_dump(exclude_none=True) for n in canvas.nodes] + [
            {"id": n["id"], "object_type_id": n["object_type_id"], "label": n.get("label"), "planned": True,
             **({"computed": n["computed"]} if n.get("computed") else {})}
            for n in planned_nodes.values()
        ]
        removed = _removed(run)
        every = [n for n in every if n["id"] not in removed]
        wanted = set(object_types or ())
        nodes = [n for n in every if not wanted or n["object_type_id"] in wanted]
        offset = max(0, offset)
        limit = min(max(1, limit), PAGE_LIMIT)
        page = nodes[offset : offset + limit]
        end = offset + len(page)
        edges = [e.model_dump() for e in canvas.edges] + [
            {"id": e["id"], "relation_type_id": e["relation_type_id"], "source": e["source"],
             "target": e["target"], "planned": True}
            for e in planned_edges.values()
        ]
        edges = [e for e in edges if e["source"] not in removed and e["target"] not in removed]
        # Aggregates this run planned count too; a refreshed one carries its new total.
        planned_aggregates = _planned_aggregates(run)
        on_canvas = {a.id for a in canvas.aggregates}
        aggregates = [
            {**a.model_dump(), **({"total": planned_aggregates[a.id]["total"], "planned": True} if a.id in planned_aggregates else {})}
            for a in canvas.aggregates
            if a.id not in removed
        ] + [{**a, "planned": True} for i, a in planned_aggregates.items() if i not in on_canvas and i not in removed]
        return _dump(
            {
                "canvas_id": canvas.canvas_id,
                "revision": canvas.revision,
                "total_nodes": len(every),
                "nodes": {"count": len(nodes), "items": page, "next_offset": end if end < len(nodes) else None},
                "edges": {"total": len(edges), "items": edges[:SAMPLE]},
                "aggregates": {"total": len(aggregates), "items": aggregates[:SAMPLE]},
                "selected_ids": _selected(run)[:SAMPLE],
            }
        )

    async def view_state() -> str:
        """Read the current canvas view: layout, grouping, layout parameters,
        selection, label settings, analysis panel and the object types the view
        tools may name. Read it before controlling the canvas. If a requested node,
        group, property or type is ambiguous, ask instead of guessing."""
        canvas = _canvas(run)
        planned = [a for a in _actions(run) if a["type"] != "merge_subgraph"]
        return _dump(
            {
                "view": canvas.view,
                **({"planned_view_actions": planned} if planned else {}),
                "selected_ids": _selected(run)[:SAMPLE],
                "object_types": [t.model_dump(exclude_none=True) for t in canvas.object_types],
            }
        )


    async def remove_instances(
        ids: Annotated[list[str], Field(min_length=1, description="Exact node or aggregate ids from canvas_state.")],
    ) -> str:
        """Plan removing the given nodes or aggregates, and their edges, from the
        canvas. A view edit the user can undo; it never deletes business data. Use
        only when the user asks to remove, clean up or keep only part of the view,
        with exact ids from canvas_state, never guessed ones."""
        present = _present_nodes(run) | _present_aggregates(run)
        wanted = list(dict.fromkeys(ids))
        on_canvas = [i for i in wanted if i in present]
        out: dict[str, Any] = {"planned_removed": len(on_canvas)}
        if on_canvas:
            _record(run, "remove_nodes", node_ids=on_canvas)
            _take_off(run, on_canvas)
        missing = [i for i in wanted if i not in present]
        if missing:
            out["not_on_canvas"] = missing
        return _dump(out)

    async def configure_display(
        labels: Annotated[
            Optional[list[LabelSetting]],
            Field(description="Per object type: a property name or a template such as `{code} · {name}`; null restores the default."),
        ] = None,
        show_node_labels: Optional[bool] = None,
        show_edge_labels: Optional[bool] = None,
        node_size_by: Annotated[
            Optional[str],
            Field(description="One of view_state's display.size_choices; an empty string sizes by connections again."),
        ] = None,
    ) -> str:
        """Plan which properties node labels show for each object type, whether node
        or edge labels are shown, and which value sizes the nodes. Read view_state
        first. A label is an exact property name or a template such as
        `{code} · {name}`; `{$type}` inserts the type name; a computed Cypher column is
        `computed:<column>` and a logic property `logic:<name>` (view_state lists
        them per type). A label applies to current and future nodes of that type.
        Does not alter business data. The Web checks property names when it applies
        the plan and skips what it does not know."""
        known = {t.id for t in _canvas(run).object_types}
        patch: dict[str, Any] = {}
        if labels:
            unknown = [item["object_type"] for item in labels if known and item["object_type"] not in known]
            if unknown:
                raise ToolException(f"Unknown object types: {', '.join(unknown)}. Use ids from view_state.")
            patch["labels"] = [{"object_type": item["object_type"], "label": item.get("label")} for item in labels]
        if show_node_labels is not None:
            patch["show_node_labels"] = show_node_labels
        if show_edge_labels is not None:
            patch["show_edge_labels"] = show_edge_labels
        if node_size_by is not None:
            patch["node_size_by"] = node_size_by or None
        if not patch:
            raise ToolException("Nothing to change.")
        _record(run, "set_display", patch=patch)
        return _dump({"planned": patch})

    async def configure_analysis(
        open: Annotated[Optional[bool], Field(description="Defaults to true when configuring.")] = None,
        collapsed: Annotated[Optional[bool], Field(description="Defaults to false when configuring.")] = None,
        scope: Optional[Literal["all", "selected"]] = None,
        # Not `config`: langchain hands a parameter of that name the RunnableConfig.
        chart: Annotated[Optional[AnalysisConfig], Field(description="Chart settings; merged into the current ones.")] = None,
    ) -> str:
        """Plan opening, closing, collapsing or configuring the analysis panel; adds
        no nodes. Use exact object type ids and property names from view_state.
        scope selected uses only the visible selection and its internal edges.
        Config fields merge into the current settings. kind: types / relations
        counts; category groups by a property; numeric histogram; time timeline;
        degree scoped degree distribution; scatter numeric/time x against numeric y.
        Category and time support count/sum/avg/min/max measures. No pie charts or
        other chart styles. The Web checks the settings when it applies the plan."""
        patch = {
            key: value
            for key, value in {"open": open, "collapsed": collapsed, "scope": scope, "config": chart}.items()
            if value is not None
        }
        if not patch:
            raise ToolException("Nothing to change.")
        known = {t.id for t in _canvas(run).object_types}
        named = set((chart or {}).get("otIds") or ()) | set((chart or {}).get("propertyByOt") or {})
        unknown = sorted(named - known) if known else []
        if unknown:
            raise ToolException(f"Unknown object types: {', '.join(unknown)}. Use ids from view_state.")
        _record(run, "set_analysis", patch=patch)
        return _dump({"planned": patch})

    async def arrange_canvas(
        layout: Annotated[Optional[Layout], Field(description="Target layout; omit to rearrange with the current one.")] = None,
    ) -> str:
        """Plan changing the layout of the whole canvas when the user asks for it:
        force (force-directed), chain (path), dagre (hierarchical), tree, radial,
        circular or grid. Omit layout to rearrange with the current layout. Moves
        nodes; adds or removes none."""
        _record(run, "set_layout", **({"layout": layout} if layout else {}))
        return _dump({"planned": {"layout": layout or "current"}})

    async def canvas_control(
        action: Literal[
            "group", "layout_params", "tree_root", "select", "degree_sizing", "distance_heatmap",
            "fit", "path_start", "path_end", "find_path", "clear_path", "arrange_selection",
            "undo", "remove_selected", "clear",
        ],
        enabled: Optional[bool] = None,
        grouping: Optional[Grouping] = None,
        params: Annotated[
            Optional[dict[str, int]],
            Field(description="Integer layout settings to change, named as in view_state's canvas.layout_params."),
        ] = None,
        id: Annotated[Optional[str], Field(description="A node id; null clears tree_root, distance_heatmap or a path end.")] = None,
        ids: Optional[list[str]] = None,
        start: Optional[str] = None,
        end: Optional[str] = None,
        layout: Optional[Layout] = None,
    ) -> str:
        """Plan a canvas toolbar operation. group chooses none/type/concept/manual/
        relation; layout_params adjusts integer layout settings, such as
        forceLinkStrength (link pull, 1-100) and forceRepulsion (0-400); tree_root
        sets a node id, or null for automatic; select chooses exact node ids;
        degree_sizing sets enabled true/false to size nodes by visible degree;
        distance_heatmap colours by distance from a node id, or turns off with id
        null; fit frames the canvas; path_start / path_end set path endpoints;
        find_path searches the network for a path between start and end (or the
        current endpoints) when the plan is applied; clear_path removes endpoints and
        highlight; arrange_selection lays out the selected nodes; undo takes back the
        last view action this run planned; remove_selected removes the selected
        nodes; clear empties the canvas after the user confirms. Read canvas_state
        and view_state to resolve ids. Never guess ids."""
        if action == "undo":
            # The page may already show the action, so undo is recorded too and
            # the page undoes its own last edit.
            undone = run.state.setdefault("undone", set())
            open_actions = [a for a in _actions(run) if a["type"] != "undo" and a["action_id"] not in undone]
            if not open_actions:
                raise ToolException("This run has planned nothing to undo; it cannot undo the user's own edits.")
            last = open_actions[-1]
            if last["type"] == "merge_subgraph":
                raise ToolException("Planned nodes cannot be undone; use remove_instances to take them off.")
            removed, selected = run.state["undo"].pop(last["action_id"])
            run.state["removed"], run.state["selected"] = removed, selected
            undone.add(last["action_id"])
            _actions(run).append({"action_id": f"{len(_actions(run)) + 1}", "type": "undo", "undoes": last["action_id"]})
            return _dump({"undone": {k: v for k, v in last.items() if k != "action_id"}})
        if action == "group":
            if not grouping:
                raise ToolException("group needs grouping.")
            _record(run, "group", grouping=grouping)
        elif action == "layout_params":
            if not params:
                raise ToolException("layout_params needs params.")
            _record(run, "layout_params", params=params)
        elif action in ("tree_root", "distance_heatmap", "path_start", "path_end"):
            if id is not None:
                _check_nodes(run, [id])
                if action in ("path_start", "path_end"):
                    _refuse_value_ends(run, [id])
            _record(run, action, id=id)
        elif action == "select":
            chosen = _check_nodes(run, ids or [])
            _record(run, "select", ids=chosen)
            run.state["selected"] = chosen
        elif action == "degree_sizing":
            if enabled is None:
                raise ToolException("degree_sizing needs enabled true or false.")
            _record(run, "degree_sizing", enabled=enabled)
        elif action == "find_path":
            ends = [i for i in (start, end) if i]
            _check_nodes(run, ends)
            _refuse_value_ends(run, ends)
            _record(run, "find_path", **({"start": start} if start else {}), **({"end": end} if end else {}))
            return _dump({"planned": "find_path", "note": "The Web searches the path when the plan is applied; its result is not known yet."})
        elif action == "arrange_selection":
            if len(_selected(run)) < 2:
                raise ToolException("Select at least two nodes first.")
            _record(run, "arrange_selection", **({"layout": layout} if layout else {}))
        elif action == "remove_selected":
            chosen = [i for i in _selected(run) if i in _present_nodes(run)]
            if not chosen:
                raise ToolException("No nodes are selected.")
            _record(run, "remove_nodes", node_ids=chosen)
            _take_off(run, chosen)
            return _dump({"planned_removed": len(chosen)})
        elif action == "clear":
            everything = sorted(_present_nodes(run) | _present_aggregates(run))
            _record(run, "clear")
            _take_off(run, everything)
            return _dump({"planned": "clear", "note": "The Web asks the user to confirm; if they decline, this and later actions are skipped."})
        else:
            _record(run, action)
        return _dump({"planned": action})

    async def manual_group(
        action: Literal["create", "rename", "recolor", "collapse", "expand", "add", "exclude", "dissolve"],
        id: Annotated[Optional[str], Field(description="Group id from view_state, or one create returned.")] = None,
        name: Optional[str] = None,
        color: Annotated[Optional[str], Field(description="Hex colour such as #3366ff.")] = None,
        ids: Annotated[Optional[list[str]], Field(description="Exact node ids.")] = None,
    ) -> str:
        """Plan creating or editing a named manual canvas group. create needs a name
        and exact node ids (or the current selection), optional hex colour, and
        switches grouping to manual. Other actions use a group id from view_state or
        the id create returned; add / exclude take exact node ids. collapse / expand
        affect the group box; dissolve removes only the box, never business data."""
        groups = {g.get("id") for g in ((_canvas(run).view.get("canvas") or {}).get("manual_groups") or []) if isinstance(g, dict)}
        groups |= set(run.state.setdefault("groups", []))
        members = _check_nodes(run, ids if ids is not None else (_selected(run) if action == "create" else []))
        if color is not None and not _HEX_COLOR.match(color):
            raise ToolException("Group colour must be a hex colour such as #3366ff.")
        fields: dict[str, Any] = {"action": action}
        if action == "create":
            if not (name or "").strip() or not members:
                raise ToolException("create needs a name and node ids, or a selection.")
            # Unique across runs: an earlier run's g-ai group may already be on the canvas.
            id = f"g-ai-{uuid.uuid4().hex[:8]}"
            run.state["groups"].append(id)
            fields.update(id=id, name=name.strip(), ids=members, **({"color": color} if color else {}))
        elif action == "exclude":
            if not members:
                raise ToolException("exclude needs node ids.")
            fields["ids"] = members
        else:
            if id not in groups:
                raise ToolException(f"Unknown manual group {id!r}; read view_state for group ids.")
            fields["id"] = id
            if action == "rename":
                if not (name or "").strip():
                    raise ToolException("rename needs a name.")
                fields["name"] = name.strip()
            elif action == "recolor":
                if not color:
                    raise ToolException("recolor needs a colour.")
                fields["color"] = color
            elif action == "add":
                if not members:
                    raise ToolException("add needs node ids.")
                fields["ids"] = members
        _record(run, "manual_group", **fields)
        return _dump({"planned": fields})

    return [
        _tool(fn)
        for fn in (
            search_instances,
            query_instances,
            show_instances,
            expand_neighbours,
            run_cypher,
            remove_instances,
            canvas_state,
            view_state,
            configure_display,
            configure_analysis,
            arrange_canvas,
            canvas_control,
            manual_group,
        )
    ]


ACTION_TYPES = frozenset(
    {
        "merge_subgraph",
        "remove_nodes",
        "set_display",
        "set_analysis",
        "set_layout",
        "group",
        "layout_params",
        "tree_root",
        "select",
        "degree_sizing",
        "distance_heatmap",
        "fit",
        "path_start",
        "path_end",
        "find_path",
        "clear_path",
        "arrange_selection",
        "clear",
        "manual_group",
        "undo",
    }
)


async def _instructions(run: builtin_tools.Run) -> str:
    """The network's object and relation types, as the EE explorer's prompt lists
    them, so the model names types and relations exactly instead of guessing."""
    try:
        network = await _network(run)
    except ToolException as exc:
        return f"The network schema could not be read ({exc}); read it before naming types."
    objects = [
        f"- {ot_id} ({t['name']})" + (f"\n  properties: {', '.join(t['properties'])}" if t["properties"] else "")
        for ot_id, t in network["object_types"].items()
    ]
    relations = [
        f"- {rel_id} ({r['name']}): {r['source']} -> {r['target']}"
        for rel_id, r in network["relation_types"].items()
    ]
    if network.get("relations_unlisted"):
        relations = ["(This network is too large to list its relation types here; ask the user which relation to use.)"]
    return "\n".join(
        [
            "Object types of this network (id, name):",
            *objects,
            "",
            "Relation types (id, name): source object type -> target object type:",
            *relations,
        ]
    )


def _progress(run: builtin_tools.Run) -> dict | None:
    """The actions planned since the last call, with the graph data they name."""
    actions = _actions(run)
    start = run.state.get("emitted", 0)
    run.state["emitted"] = len(actions)
    new = actions[start:]
    if not new:
        return None
    merges = [a for a in new if a["type"] == "merge_subgraph"]

    def named(store: dict, key: str) -> list:
        return [store[i] for a in merges for i in dict.fromkeys(a.get(key, ())) if i in store]

    return {
        "nodes": named(_planned_nodes(run), "node_ids"),
        "edges": named(_planned_edges(run), "edge_ids"),
        "aggregates": named(_planned_aggregates(run), "aggregate_ids"),
        "actions": new,
    }


def _result(run: builtin_tools.Run) -> dict:
    """canvas_result: graph data held once, actions referring to it by id."""
    canvas = _canvas(run)
    nodes: dict[str, dict] = run.state.get("nodes", {})
    edges: dict[str, dict] = run.state.get("edges", {})
    aggregates: dict[str, dict] = run.state.get("aggregates", {})
    actions = _actions(run)
    known = {n.id for n in canvas.nodes} | set(nodes)
    removable = known | {a.id for a in canvas.aggregates} | set(aggregates)
    for edge in edges.values():
        if edge["source"] not in known or edge["target"] not in known:
            raise ValueError(f"edge {edge['id']} has an endpoint outside the canvas")
    for action in actions:
        if action.get("type") not in ACTION_TYPES:
            raise ValueError(f"action type {action.get('type')!r} is not allowed")
        # Removal names aggregates too, so it is checked against both.
        if action["type"] == "remove_nodes":
            for node_id in action["node_ids"]:
                if node_id not in removable:
                    raise ValueError(f"action {action['action_id']} removes unknown {node_id}")
            continue
        for node_id in action.get("node_ids", ()):
            if node_id not in known:
                raise ValueError(f"action {action.get('action_id')} names unknown node {node_id}")
        for edge_id in action.get("edge_ids", ()):
            if edge_id not in edges:
                raise ValueError(f"action {action.get('action_id')} names unknown edge {edge_id}")
        for aggregate_id in action.get("aggregate_ids", ()):
            if aggregate_id not in aggregates:
                raise ValueError(f"action {action.get('action_id')} names unknown aggregate {aggregate_id}")
    for aggregate in aggregates.values():
        if aggregate["seed_id"] not in known:
            raise ValueError(f"aggregate {aggregate['id']} has a seed outside the canvas")
    return {
        "canvas_id": canvas.canvas_id,
        "base_revision": canvas.revision,
        "nodes": list(nodes.values()),
        "edges": list(edges.values()),
        "aggregates": list(aggregates.values()),
        "actions": actions,
        "warnings": run.state.get("warnings", []),
    }


TOOL_NAMES = frozenset(
    {
        "search_instances",
        "query_instances",
        "show_instances",
        "expand_neighbours",
        "run_cypher",
        "remove_instances",
        "canvas_state",
        "view_state",
        "configure_display",
        "configure_analysis",
        "arrange_canvas",
        "canvas_control",
        "manual_group",
    }
)

builtin_tools.register(
    builtin_tools.Provider(
        name=PROVIDER_NAME,
        tool_names=TOOL_NAMES,
        context_key="canvas",
        context_model=CanvasContext,
        factory=_tools,
        result_key="canvas_result",
        result=_result,
        instructions=_instructions,
        progress=_progress,
    )
)
