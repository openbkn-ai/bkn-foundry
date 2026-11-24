# Copyright openbkn.ai
#
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

"""Built-in tool source: registry, loading, guarding and the public execution entry."""

import asyncio
import json

import pytest
from langchain_core.tools import StructuredTool
from pydantic import BaseModel, ValidationError
from sqlalchemy import update

from app.core import builtin_tools, tools as tools_module
from app.core.agent_settings import merge
from app.core.public_chat import PublicChat
from app.models import AgentOut, AgentRow, AgentSetting, AgentSpec
from app.routers import executions
from app.test.test_public_execution import AUTH, BODY, URL, environment, parse_sse


class CanvasContext(BaseModel):
    # A provider's context model accepts {}; this one refuses what it does not know.
    model_config = {"extra": "forbid"}
    canvas_id: str = ""
    revision: int = 0


def _tool(name, run):
    async def call(note: str = "") -> str:
        run.state.setdefault("calls", []).append(name)
        return f"{name} ok"

    return StructuredTool.from_function(coroutine=call, name=name, description=name)


def _result(run):
    if run.state.get("invalid"):
        raise ValueError("unknown node referenced")
    return {"canvas_id": run.context.canvas_id, "actions": run.state.get("calls", [])}


PROVIDER = builtin_tools.Provider(
    name="test_canvas",
    tool_names=frozenset({"show_instances", "canvas_state"}),
    context_key="canvas",
    context_model=CanvasContext,
    factory=lambda run: [_tool("show_instances", run), _tool("canvas_state", run)],
    result_key="canvas_result",
    result=_result,
)


@pytest.fixture(autouse=True)
def provider():
    builtin_tools.register(PROVIDER)
    try:
        yield PROVIDER
    finally:
        builtin_tools._providers.pop(PROVIDER.name, None)
        builtin_tools.set_current(None)


def _run(state=None):
    return builtin_tools.Run(
        provider=PROVIDER,
        kn_id="network-1",
        context=CanvasContext(canvas_id="c1", revision=1),
        account_id="user-1",
        account_type="user",
        state=state or {},
    )


def _spec(tools):
    return AgentSpec(name="Graph", model="m", tools=tools)


def test_reference_names_a_registered_provider_and_its_tools():
    assert _spec([{"type": "builtin", "provider": "test_canvas"}]).tools[0]["provider"] == "test_canvas"
    with pytest.raises(ValidationError, match="unknown provider"):
        _spec([{"type": "builtin", "provider": "missing"}])
    with pytest.raises(ValidationError, match="has no tools"):
        _spec([{"type": "builtin", "provider": "test_canvas", "tools": ["drop_table"]}])


def test_load_tools_mounts_the_current_runs_tools_first():
    async def run():
        builtin_tools.set_current(_run())

        async def toolbox(refs, account_id, account_type):
            async def other(note: str = "") -> str:
                return "toolbox"

            return [StructuredTool.from_function(coroutine=other, name="canvas_state", description="x")]

        original = tools_module._toolbox_tools
        tools_module._toolbox_tools = toolbox
        try:
            loaded = await tools_module.load_tools(
                [{"type": "builtin", "provider": "test_canvas", "tools": ["canvas_state"]}],
                "user-1",
                "user",
            )
        finally:
            tools_module._toolbox_tools = original
        # The subset is honoured and the builtin tool wins the name collision.
        names = [t.name for t in loaded if t.name != "read_skill_file"]
        assert names == ["canvas_state"]
        assert builtin_tools.is_builtin(loaded[0])
        assert await loaded[0].ainvoke({"note": ""}) == "canvas_state ok"

    asyncio.run(run())


def test_load_tools_without_a_run_mounts_nothing_and_rejects_unknown_refs():
    async def run():
        builtin_tools.set_current(None)
        loaded = await tools_module.load_tools(
            [{"type": "builtin", "provider": "test_canvas"}], "user-1", "user"
        )
        assert loaded == []
        with pytest.raises(Exception) as caught:
            await tools_module.load_tools(
                [{"type": "builtin", "provider": "missing"}], "user-1", "user"
            )
        assert caught.value.detail["code"] == "BknAgent.ParamError.ToolRef"

    asyncio.run(run())


def test_guard_admits_builtin_tools_by_marker_and_layer_narrowing():
    run = _run()
    builtin_tools.set_current(run)
    mounted = builtin_tools.tools_for({"type": "builtin", "provider": "test_canvas"})
    forged = _tool("canvas_state", run)
    forged.metadata = {"bkn_builtin_trust_token": object()}

    public = PublicChat("run-1", "network-1")
    assert [t.name for t in public.guard_tools([*mounted, forged])] == [
        "show_instances",
        "canvas_state",
    ]
    public.allowed_builtin_tools = {"canvas_state"}
    assert [t.name for t in public.guard_tools(mounted)] == ["canvas_state"]


def test_layers_narrow_builtin_tools():
    agent = AgentOut(
        agent_id="graph_explorer",
        name="Graph",
        model="m",
        tools=[{"type": "builtin", "provider": "test_canvas"}],
        create_user="admin",
        update_user="admin",
        create_time=1,
        update_time=1,
    )
    assert merge(agent, None, 0, None, 0).allowed_builtin_tools is None
    narrowed = merge(agent, AgentSetting(allowed_tools=["canvas_state"]), 1, None, 0)
    assert narrowed.allowed_builtin_tools == {"canvas_state"}


CANVAS = {"canvas_id": "c1", "revision": 3}


async def _use_builtin(factory, tools):
    async with factory() as session:
        await session.execute(
            update(AgentRow)
            .where(AgentRow.f_agent_id == "studio_smart_qa")
            .values(f_tools=tools)
        )
        await session.commit()


def _chat_calling(*names, invalid=False):
    """A stand-in for stream_chat that calls the run's tools like the model would."""

    async def chat(session, agent, req, account_id, account_type, *, public):
        run = builtin_tools.current()
        mounted = builtin_tools.tools_for(agent.tools[0]) if run else []
        for tool in mounted:
            if tool.name in names:
                await tool.ainvoke({"note": ""})
        if invalid:
            run.state["invalid"] = True
        public.answer.append("Answer")
        public.prompt_version = 1

        async def events():
            yield public.event("token", {"content": "Answer"})
            yield "event: done\ndata: {}\n\n"

        return events()

    return chat


def test_execution_returns_the_providers_fragment(monkeypatch):
    async def run():
        async with environment(monkeypatch) as (client, factory, calls):
            await _use_builtin(factory, [{"type": "builtin", "provider": "test_canvas"}])
            monkeypatch.setattr(executions, "stream_chat", _chat_calling("show_instances"))
            body = {**BODY, "context": {"kn_id": "network-1", "canvas": CANVAS}}
            response = await client.post(URL, json=body, headers=AUTH)
            events = parse_sse(response.text)
            assert events[-1][1]["status"] == "succeeded", events
            result = dict(events)["result"]["content"]
            assert result["canvas_result"] == {"canvas_id": "c1", "actions": ["show_instances"]}
            run_id = events[0][1]["run_id"]
            stored = (await client.get(f"{URL}/{run_id}", headers=AUTH)).json()
            assert stored["result"]["canvas_result"]["actions"] == ["show_instances"]

    asyncio.run(run())


def test_invalid_fragment_fails_the_execution(monkeypatch):
    async def run():
        async with environment(monkeypatch) as (client, factory, calls):
            await _use_builtin(factory, [{"type": "builtin", "provider": "test_canvas"}])
            monkeypatch.setattr(executions, "stream_chat", _chat_calling(invalid=True))
            body = {**BODY, "context": {"kn_id": "network-1", "canvas": CANVAS}}
            events = parse_sse((await client.post(URL, json=body, headers=AUTH)).text)
            assert "result" not in dict(events)
            terminal = events[-1][1]
            assert terminal["status"] == "failed"
            assert terminal["error"]["code"] == "BknAgent.Execution.ResultInvalid"

    asyncio.run(run())


def test_provider_context_never_refuses_the_question(monkeypatch):
    # Each of these used to answer 400 BknAgent.Execution.ContextInvalid or ContextMissing.
    async def run():
        async with environment(monkeypatch) as (client, factory, calls):
            # An Agent without the provider ignores canvas context.
            body = {**BODY, "context": {"kn_id": "network-1", "canvas": CANVAS}}
            response = await client.post(URL, json=body, headers=AUTH)
            assert response.status_code == 200, response.text
            assert parse_sse(response.text)[-1][1]["status"] == "succeeded"
            assert len(calls) == 1

            # An Agent with it reads a missing or invalid canvas as an empty one.
            await _use_builtin(factory, [{"type": "builtin", "provider": "test_canvas"}])
            monkeypatch.setattr(executions, "stream_chat", _chat_calling("show_instances"))
            for request_id, canvas in (("request-2", None), ("request-3", {"canvas_id": "c1", "unknown": 1})):
                context = {"kn_id": "network-1", **({"canvas": canvas} if canvas else {})}
                sent = {**BODY, "client_request_id": request_id, "context": context}
                events = parse_sse((await client.post(URL, json=sent, headers=AUTH)).text)
                assert events[-1][1]["status"] == "succeeded", events
                assert dict(events)["result"]["content"]["canvas_result"] == {
                    "canvas_id": "",
                    "actions": ["show_instances"],
                }

    asyncio.run(run())


def test_an_agent_mounts_one_builtin_provider(monkeypatch):
    async def run():
        builtin_tools.register(
            builtin_tools.Provider(
                name="test_other",
                tool_names=frozenset({"x"}),
                context_key="canvas",
                context_model=CanvasContext,
                factory=lambda run: [],
                result_key="other",
                result=lambda run: None,
            )
        )
        try:
            async with environment(monkeypatch) as (client, factory, calls):
                await _use_builtin(
                    factory,
                    [
                        {"type": "builtin", "provider": "test_canvas"},
                        {"type": "builtin", "provider": "test_other"},
                    ],
                )
                body = {**BODY, "context": {"kn_id": "network-1", "canvas": CANVAS}}
                response = await client.post(URL, json=body, headers=AUTH)
                assert response.status_code == 409
        finally:
            builtin_tools._providers.pop("test_other", None)

    asyncio.run(run())


def test_graph_explorer_reads_the_canvas_and_validates_its_result():
    from app.core import graph_explorer_tools as graph

    canvas = graph.CanvasContext(
        canvas_id="c1",
        revision=4,
        nodes=[
            {"id": f"team-{i}", "object_type_id": "teams", "label": f"T{i}"} for i in range(3)
        ]
        + [{"id": "match-1", "object_type_id": "matches"}],
        edges=[
            {"id": "e1", "relation_type_id": "plays", "source": "team-0", "target": "match-1"}
        ],
        selected_ids=["team-1"],
        view={"layout": "force"},
        object_types=[{"id": "teams", "name": "球队"}],
    )
    run = builtin_tools.Run(
        provider=builtin_tools.get("graph_explorer"),
        kn_id="network-1",
        context=canvas,
        account_id="user-1",
        account_type="user",
    )
    tools = {t.name: t for t in builtin_tools.get("graph_explorer").factory(run)}

    async def read():
        state = json.loads(
            await tools["canvas_state"].ainvoke({"object_types": ["teams"], "offset": 1, "limit": 1})
        )
        assert state["total_nodes"] == 4
        assert state["nodes"]["count"] == 3
        assert [n["id"] for n in state["nodes"]["items"]] == ["team-1"]
        assert state["nodes"]["next_offset"] == 2
        assert state["edges"]["total"] == 1
        view = json.loads(await tools["view_state"].ainvoke({}))
        assert view["view"] == {"layout": "force"}
        assert view["object_types"] == [{"id": "teams", "name": "球队"}]

    asyncio.run(read())
    result = builtin_tools.get("graph_explorer").result(run)
    assert result["canvas_id"] == "c1" and result["base_revision"] == 4
    assert result["actions"] == []

    run.state["actions"] = [{"action_id": "a1", "type": "drop_everything"}]
    with pytest.raises(ValueError, match="not allowed"):
        builtin_tools.get("graph_explorer").result(run)
    run.state["actions"] = []
    run.state["edges"] = {
        "e2": {"id": "e2", "relation_type_id": "plays", "source": "team-0", "target": "ghost"}
    }
    with pytest.raises(ValueError, match="endpoint"):
        builtin_tools.get("graph_explorer").result(run)


def test_graph_explorer_canvas_takes_what_the_web_sends():
    # A long label, a field a newer Web sends, one bad item or a setting out of range used to
    # refuse the whole question with BknAgent.Execution.ContextInvalid.
    from app.core import graph_explorer_tools as graph

    model = builtin_tools.get("graph_explorer").context_model
    label = "第一行\n" + "很长的说明" * 400
    long_id = "n" * 600
    canvas = model.model_validate(
        {
            "canvas_id": "c1",
            "revision": -1,
            "sent_by_a_newer_web": {"x": 1},
            "nodes": [
                {"id": "n1", "object_type_id": "ot", "label": label},
                {"id": "", "object_type_id": "ot"},
                {"id": long_id, "object_type_id": "ot"},
                {"id": "n3"},
            ],
            "edges": [
                {"id": "e1", "relation_type_id": "r", "source": "n1", "target": long_id},
                {"id": "e2", "relation_type_id": "r", "source": "n1", "target": "n3"},
            ],
            "aggregates": [
                {"id": "a1", "seed_id": "n1", "relation_type_id": "r", "total": 9},
                {"id": "a2", "seed_id": "n3", "relation_type_id": "r", "total": 9},
            ],
            "selected_ids": ["n1", 7, "", "n3"],
            "view": None,
            "object_types": [{"id": "ot", "name": "名" * 300}, {"name": "no id"}],
            "limits": {"max_nodes": 99999, "expand_threshold": -5},
            "analysis_cards": [{"no": 1, "title": "标题" * 200, "kind": "k" * 50}, {"no": 0, "title": "bad"}],
        }
    )
    assert canvas.revision == 0
    # The invalid nodes are left out, and so is the edge to one of them; long texts stay whole.
    assert [n.id for n in canvas.nodes] == ["n1", long_id]
    assert canvas.nodes[0].label == label
    assert [e.id for e in canvas.edges] == ["e1"]
    # An aggregate's seed left out takes the aggregate with it.
    assert [a.id for a in canvas.aggregates] == ["a1"]
    assert canvas.selected_ids == ["n1"]
    assert canvas.view == {}
    assert [(t.id, t.name) for t in canvas.object_types] == [("ot", "名" * 300)]
    assert (canvas.limits.max_nodes, canvas.limits.expand_threshold) == (graph.MAX_NODES, 0)
    assert [(c.no, c.title) for c in canvas.analysis_cards] == [(1, "标题" * 200)]
    # Outside an analysis there are no cards; inside one with no valid card there still is one.
    assert model.model_validate({"canvas_id": "c1", "revision": 0}).analysis_cards is None
    endless = model.model_validate({"canvas_id": "c1", "revision": 0, "limits": {"max_nodes": float("inf")}})
    assert endless.limits.max_nodes == graph.NODE_LIMIT
    assert model.model_validate({"canvas_id": "c1", "revision": 0, "analysis_cards": [{"no": 0}]}).analysis_cards == []

    # Nothing is refused: a canvas without an id, a revision, or one that is not a map reads as empty.
    for sent, read in (
        ({}, ("", 0)),
        ({"revision": 1}, ("", 1)),
        ({"canvas_id": 123, "revision": "seven"}, ("123", 0)),
        ("c1", ("", 0)),
    ):
        canvas = model.model_validate(sent)
        assert (canvas.canvas_id, canvas.revision) == read


def test_admin_lists_tools_by_source():
    from app.routers.agent_settings import _tool_sources

    sources = _tool_sources(
        [
            {"type": "builtin", "provider": "graph_explorer"},
            {"type": "builtin", "provider": "test_canvas", "tools": ["show_instances"]},
            {"type": "context_loader", "allowed_tools": ["search_schema", "run_cypher"]},
            {"type": "context_loader"},
            {"type": "toolbox", "box_id": "box-1", "name": "Functions"},
            "not a reference",
        ]
    )
    graph = sources[0].definitions
    from app.core.graph_explorer_tools import TOOL_NAMES

    assert [d["name"] for d in graph] == sorted(TOOL_NAMES)
    canvas_state = next(d for d in graph if d["name"] == "canvas_state")
    assert set(canvas_state["parameters"]["properties"]) == {"object_types", "offset", "limit"}
    assert [d["name"] for d in sources[1].definitions] == ["show_instances"]
    assert [s.model_dump(exclude_none=True, exclude={"definitions"}) for s in sources] == [
        {"type": "builtin", "provider": "graph_explorer", "names": [d["name"] for d in graph]},
        {"type": "builtin", "provider": "test_canvas", "names": ["show_instances"]},
        {"type": "context_loader", "names": ["run_cypher", "search_schema"]},
        {"type": "context_loader"},
        {"type": "toolbox", "box_id": "box-1", "name": "Functions"},
    ]


def test_a_builtin_tool_result_carries_what_the_provider_planned_since():
    import dataclasses

    from langchain_core.messages import ToolMessage

    sent = []
    provider = dataclasses.replace(
        PROVIDER, progress=lambda run: (sent.append(1), {"actions": ["show"]})[1] if not sent else None
    )
    run = _run()
    run.provider = provider
    public = PublicChat("run-1", "network-1")
    public.builtin_run = run

    def finished(name):
        frame = next(public.updates({"tools": {"messages": [ToolMessage(content="ok", tool_call_id=name, name=name)]}}))
        return json.loads(frame.split("data: ", 1)[1])

    assert finished("show_instances")["progress"] == {"canvas_result": {"actions": ["show"]}}
    # Nothing new since, and tools from elsewhere never ask the provider.
    assert "progress" not in finished("canvas_state")
    assert "progress" not in finished("search_schema")
