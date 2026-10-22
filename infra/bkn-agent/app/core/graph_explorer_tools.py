# Copyright openbkn.ai
#
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

"""The graph explorer's built-in tools (provider "graph_explorer").

The Web sends the canvas it shows as context.canvas. The tools read that
snapshot, plus whatever this run has already planned, and record canvas
changes as actions; the Web applies the actions after the run succeeds. The
run never waits for the browser.

This first slice reads the canvas (canvas_state, view_state). The query tools
and the canvas-changing tools record their actions into the same state.
"""

import inspect
import json
from typing import Annotated, Any, Optional

from langchain_core.tools import StructuredTool
from pydantic import BaseModel, Field

from app.core import builtin_tools

PROVIDER_NAME = "graph_explorer"
MAX_NODES = 5000
MAX_EDGES = 20000
PAGE_LIMIT = 100
DEFAULT_PAGE = 50
SAMPLE = 50

_Id = Field(min_length=1, max_length=512)


class CanvasNode(BaseModel):
    model_config = {"extra": "ignore"}
    id: str = _Id
    object_type_id: str = Field(min_length=1, max_length=128)
    label: Optional[str] = Field(default=None, max_length=500)


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


def _canvas(run: builtin_tools.Run) -> CanvasContext:
    return run.context  # type: ignore[return-value]


def _actions(run: builtin_tools.Run) -> list[dict]:
    return run.state.setdefault("actions", [])


def _dump(value: Any) -> str:
    return json.dumps(value, ensure_ascii=False)


def _tool(fn) -> StructuredTool:
    # The docstring is what the model reads; drop the source indentation.
    return StructuredTool.from_function(
        coroutine=fn, name=fn.__name__, description=inspect.cleandoc(fn.__doc__ or "")
    )


def _tools(run: builtin_tools.Run) -> list[StructuredTool]:

    async def canvas_state(
        object_types: Annotated[
            Optional[list[str]],
            Field(description="Object type ids to keep; omit for every type."),
        ] = None,
        offset: Annotated[int, Field(description="Nodes to skip, for paging.")] = 0,
        limit: Annotated[
            int, Field(description="Nodes to return, at most 100.")
        ] = DEFAULT_PAGE,
    ) -> str:
        """What is on the canvas right now: nodes (id, type, label), edges and
        aggregates. Nodes can be filtered by object_types and paged with
        offset/limit (default 50, maximum 100); nodes.count is the filtered total,
        total_nodes the whole canvas, next_offset is null at the end. Edges and
        aggregates show their totals and first 50 items. An aggregate stands for
        the neighbours one seed has through one relation type when there are too
        many to draw: `total` of them exist, `on_canvas` of those are drawn as
        nodes. `planned` lists changes this run has planned but the Web has not
        applied yet."""
        canvas = _canvas(run)
        wanted = set(object_types or ())
        nodes = [n for n in canvas.nodes if not wanted or n.object_type_id in wanted]
        offset = max(0, offset)
        limit = min(max(1, limit), PAGE_LIMIT)
        page = nodes[offset : offset + limit]
        end = offset + len(page)
        return _dump(
            {
                "canvas_id": canvas.canvas_id,
                "revision": canvas.revision,
                "total_nodes": len(canvas.nodes),
                "nodes": {
                    "count": len(nodes),
                    "items": [n.model_dump(exclude_none=True) for n in page],
                    "next_offset": end if end < len(nodes) else None,
                },
                "edges": {
                    "total": len(canvas.edges),
                    "items": [e.model_dump() for e in canvas.edges[:SAMPLE]],
                },
                "aggregates": {
                    "total": len(canvas.aggregates),
                    "items": [a.model_dump() for a in canvas.aggregates[:SAMPLE]],
                },
                "selected_ids": canvas.selected_ids[:SAMPLE],
                "planned": _actions(run),
            }
        )

    async def view_state() -> str:
        """Read the current canvas view: layout, grouping, layout parameters,
        selection, label settings, analysis panel and the object types the view
        tools may name. Read it before controlling the canvas. If a requested
        node, group, property or type is ambiguous, ask instead of guessing."""
        canvas = _canvas(run)
        return _dump(
            {
                "view": canvas.view,
                "selected_ids": canvas.selected_ids[:SAMPLE],
                "object_types": [t.model_dump(exclude_none=True) for t in canvas.object_types],
                "planned": _actions(run),
            }
        )

    return [_tool(canvas_state), _tool(view_state)]


ACTION_TYPES: frozenset[str] = frozenset()


def _result(run: builtin_tools.Run) -> dict:
    """canvas_result: graph data held once, actions referring to it by id."""
    canvas = _canvas(run)
    nodes: dict[str, dict] = run.state.get("nodes", {})
    edges: dict[str, dict] = run.state.get("edges", {})
    actions = _actions(run)
    known = {n.id for n in canvas.nodes} | set(nodes)
    for edge in edges.values():
        if edge["source"] not in known or edge["target"] not in known:
            raise ValueError(f"edge {edge['id']} has an endpoint outside the canvas")
    for action in actions:
        if action.get("type") not in ACTION_TYPES:
            raise ValueError(f"action type {action.get('type')!r} is not allowed")
        for node_id in action.get("node_ids", ()):
            if node_id not in known:
                raise ValueError(f"action {action.get('action_id')} names unknown node {node_id}")
        for edge_id in action.get("edge_ids", ()):
            if edge_id not in edges:
                raise ValueError(f"action {action.get('action_id')} names unknown edge {edge_id}")
    return {
        "canvas_id": canvas.canvas_id,
        "base_revision": canvas.revision,
        "nodes": list(nodes.values()),
        "edges": list(edges.values()),
        "aggregates": run.state.get("aggregates", []),
        "actions": actions,
        "warnings": run.state.get("warnings", []),
    }


TOOL_NAMES = frozenset({"canvas_state", "view_state"})

builtin_tools.register(
    builtin_tools.Provider(
        name=PROVIDER_NAME,
        tool_names=TOOL_NAMES,
        context_key="canvas",
        context_model=CanvasContext,
        factory=_tools,
        result_key="canvas_result",
        result=_result,
    )
)
