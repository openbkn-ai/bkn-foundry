"""结构化输出：原生优先 + 提示词降级；run_agent_once 集成。"""
import asyncio
import contextlib
import json

import pytest
from langchain_core.messages import AIMessage

from app import observability
from app.core import runner
from app.core import structured
from app.core import tools as tools_mod
from app.core.structured import structured_extract, structured_extract_with_path
from app.models import AgentOut

SCHEMA = {"type": "object", "properties": {"greeting": {"type": "string"}}, "required": ["greeting"]}


# ---------- structured_extract 单元 ----------

class _NativeOK:
    def __init__(self, obj):
        self.obj = obj

    def with_structured_output(self, schema):
        obj = self.obj

        class _R:
            async def ainvoke(self, messages):
                return obj

        return _R()


class _Fallback:
    """with_structured_output 抛错逼降级；ainvoke 按序列返回文本。"""

    def __init__(self, texts):
        self.texts = list(texts)
        self.i = 0

    def with_structured_output(self, schema):
        raise RuntimeError("model unsupported")

    async def ainvoke(self, messages):
        t = self.texts[min(self.i, len(self.texts) - 1)]
        self.i += 1
        return AIMessage(content=t)


def test_native_path():
    out = asyncio.run(structured_extract(_NativeOK({"greeting": "pong"}), [], SCHEMA))
    assert out == {"greeting": "pong"}


def test_native_path_reports_trace_path():
    out, path = asyncio.run(structured_extract_with_path(_NativeOK({"greeting": "pong"}), [], SCHEMA))
    assert out == {"greeting": "pong"}
    assert path == "native"


def test_fallback_valid():
    out = asyncio.run(structured_extract(_Fallback(['{"greeting": "hi"}']), [], SCHEMA))
    assert out == {"greeting": "hi"}


def test_fallback_reports_trace_path():
    out, path = asyncio.run(structured_extract_with_path(_Fallback(['{"greeting": "hi"}']), [], SCHEMA))
    assert out == {"greeting": "hi"}
    assert path == "fallback"


def test_fallback_strips_fence_and_retries():
    m = _Fallback(["不是 JSON", "```json\n{\"greeting\": \"ok\"}\n```"])
    out = asyncio.run(structured_extract(m, [], SCHEMA))
    assert out == {"greeting": "ok"}


def test_fallback_all_invalid_raises():
    m = _Fallback(["坏的", '{"wrong": 1}'])  # 第二次解析成功但缺 required greeting
    with pytest.raises(RuntimeError):
        asyncio.run(structured_extract(m, [], SCHEMA))


# ---------- run_agent_once 集成 ----------

class _FakeGraph:
    async def ainvoke(self, inp, cfg):
        return {"messages": [AIMessage(content="loop done")]}


class _FakeCM:
    async def __aenter__(self):
        return object()

    async def __aexit__(self, *a):
        return False


def _wire(monkeypatch, captured):
    monkeypatch.setattr(runner, "create_agent", lambda *a, **k: _FakeGraph())

    def fake_model(m, streaming=True, max_output_tokens=None):
        captured.setdefault("streaming", []).append(streaming)
        return object()

    monkeypatch.setattr(runner, "build_chat_model", fake_model)
    monkeypatch.setattr(runner, "SessionLocal", lambda: _FakeCM())
    monkeypatch.setattr(observability, "span", lambda *a, **k: contextlib.nullcontext())

    async def fake_prompt(session, agent, account_id, override, prompt_vars):
        return "sys", "default", 1

    async def fake_skills(ids, account_id, account_type):
        return ""

    async def fake_tools(agent_tools, account_id, account_type, depth=0, parent_thread_id=None):
        return []

    monkeypatch.setattr(runner, "resolve_prompt", fake_prompt)
    monkeypatch.setattr(runner, "load_skills", fake_skills)
    monkeypatch.setattr(tools_mod, "load_tools", fake_tools)
    monkeypatch.setattr(tools_mod, "apply_tool_call_cap", lambda t, cap, *a: t)
    monkeypatch.setattr(runner.evidence, "submit_events", lambda *a, **k: _noop())


async def _noop():
    return None


def _agent():
    return AgentOut(
        name="sa", mode="task", model="", tools=[], skills=[], status="published",
        agent_id="a1", create_user="u", update_user="u", create_time=0, update_time=0,
    )


def test_run_with_response_format_serializes(monkeypatch):
    captured: dict = {}
    _wire(monkeypatch, captured)

    async def fake_extract(model, messages, schema):
        return {"greeting": "pong"}, "native"

    monkeypatch.setattr(runner, "structured_extract_with_path", fake_extract)
    out = asyncio.run(runner.run_agent_once(
        _agent(), "hi", {}, [], None, "acc", "user", depth=1, response_format=SCHEMA,
    ))
    assert json.loads(out) == {"greeting": "pong"}
    assert False in captured["streaming"]  # 抽结构化用非流式模型


def test_run_without_response_format_text_path(monkeypatch):
    captured: dict = {}
    _wire(monkeypatch, captured)
    out = asyncio.run(runner.run_agent_once(
        _agent(), "hi", {}, [], None, "acc", "user", depth=1,
    ))
    assert out == "loop done"


def test_normalize_response_format():
    from app.core.llm import normalize_response_format
    assert normalize_response_format({"type": "object", "properties": {}})["title"] == "StructuredResponse"
    assert normalize_response_format({"title": "X", "type": "object"}) == {"title": "X", "type": "object"}
    assert normalize_response_format(None) is None
