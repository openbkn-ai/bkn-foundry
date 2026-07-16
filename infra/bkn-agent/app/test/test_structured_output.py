"""结构化输出（可选 response_format）：透传 create_react_agent + 序列化 structured_response。"""
import asyncio
import contextlib
import json

from app import observability
from app.core import runner
from app.core import tools as tools_mod
from app.models import AgentOut


def _agent() -> AgentOut:
    return AgentOut(
        name="sa", mode="task", model="", tools=[], skills=[], status="published",
        agent_id="a1", create_user="u", update_user="u", create_time=0, update_time=0,
    )


class _FakeGraph:
    def __init__(self, structured):
        self._structured = structured

    async def ainvoke(self, inp, cfg):
        # 模拟 create_react_agent 终态：带 structured_response 时即结构化结果
        out = {"messages": []}
        if self._structured is not None:
            out["structured_response"] = self._structured
        return out


class _FakeCM:
    async def __aenter__(self):
        return object()

    async def __aexit__(self, *a):
        return False


def _wire(monkeypatch, structured, captured):
    def fake_create(model, tools, prompt=None, **kw):
        captured["kwargs"] = kw
        return _FakeGraph(structured)

    monkeypatch.setattr(runner, "create_react_agent", fake_create)
    monkeypatch.setattr(runner, "build_chat_model", lambda m: object())
    monkeypatch.setattr(runner, "SessionLocal", lambda: _FakeCM())
    monkeypatch.setattr(observability, "span", lambda *a, **k: contextlib.nullcontext())

    async def fake_prompt(session, agent, account_id, override, prompt_vars):
        return "sys", "default", 1

    async def fake_skills(ids, account_id, account_type):
        return ""

    monkeypatch.setattr(runner, "resolve_prompt", fake_prompt)
    monkeypatch.setattr(runner, "load_skills", fake_skills)

    async def fake_tools(agent_tools, account_id, account_type, depth=0, parent_thread_id=None):
        return []

    monkeypatch.setattr(tools_mod, "load_tools", fake_tools)
    monkeypatch.setattr(tools_mod, "apply_tool_call_cap", lambda t, cap: t)


SCHEMA = {"type": "object", "properties": {"greeting": {"type": "string"}}, "required": ["greeting"]}


def test_response_format_passed_and_serialized(monkeypatch):
    captured: dict = {}
    _wire(monkeypatch, {"greeting": "pong"}, captured)
    out = asyncio.run(runner.run_agent_once(
        _agent(), "hi", {}, [], None, "acc", "user", depth=1, response_format=SCHEMA,
    ))
    # response_format 透传给 create_react_agent，裸 schema 补了 title（否则 Unsupported function）
    passed = captured["kwargs"].get("response_format")
    assert passed.get("title") == "StructuredResponse"
    assert passed["properties"] == SCHEMA["properties"]
    # structured_response 被序列化成 JSON 字符串（中文不转义）
    assert json.loads(out) == {"greeting": "pong"}


def test_no_response_format_keeps_text_path(monkeypatch):
    """不传 response_format：不进结构化分支，走原文本回复，且不给 graph 传该 kwarg。"""
    from langchain_core.messages import AIMessage

    captured: dict = {}

    class _TextGraph:
        async def ainvoke(self, inp, cfg):
            return {"messages": [AIMessage(content="纯文本回复")]}

    def fake_create(model, tools, prompt=None, **kw):
        captured["kwargs"] = kw
        return _TextGraph()

    _wire(monkeypatch, None, captured)
    monkeypatch.setattr(runner, "create_react_agent", fake_create)
    out = asyncio.run(runner.run_agent_once(
        _agent(), "hi", {}, [], None, "acc", "user", depth=1,
    ))
    assert "response_format" not in captured["kwargs"]  # 可选：没传就不带
    assert out == "纯文本回复"


def test_normalize_response_format():
    from app.core.llm import normalize_response_format
    # 裸 schema 补 title
    n = normalize_response_format({"type": "object", "properties": {}})
    assert n["title"] == "StructuredResponse"
    # 已有 title 不动
    assert normalize_response_format({"title": "X", "type": "object"}) == {"title": "X", "type": "object"}
    # 已有 name 不动
    assert normalize_response_format({"name": "Y", "parameters": {}}) == {"name": "Y", "parameters": {}}
    # None 透传
    assert normalize_response_format(None) is None


def test_structured_empty_raises(monkeypatch):
    """要了结构化但模型没产出 → 明确报错，不静默返回空。"""
    captured: dict = {}
    _wire(monkeypatch, None, captured)  # graph 不返回 structured_response
    try:
        asyncio.run(runner.run_agent_once(
            _agent(), "hi", {}, [], None, "acc", "user", depth=1, response_format=SCHEMA,
        ))
        assert False, "应抛错"
    except RuntimeError as e:
        assert "结构化" in str(e)
