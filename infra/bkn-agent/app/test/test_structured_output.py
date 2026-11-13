"""结构化输出：原生优先 + 提示词降级；run_agent_once 集成。"""
import asyncio
import contextlib
import json

import pytest
from langchain_core.messages import AIMessage, HumanMessage, SystemMessage

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


def test_conversation_result_is_reused_without_another_model_call():
    """Agent 已按合同给出 JSON 时，不应再发起一次结构化模型请求。"""

    class _MustNotRun:
        def with_structured_output(self, schema):
            raise AssertionError("structured model should not be called")

        async def ainvoke(self, messages):
            raise AssertionError("fallback model should not be called")

    messages = [
        HumanMessage(content="请分析"),
        AIMessage(content='```json\n{"greeting": "direct"}\n```'),
    ]
    out, path = asyncio.run(
        structured_extract_with_path(_MustNotRun(), messages, SCHEMA)
    )

    assert out == {"greeting": "direct"}
    assert path == "conversation"


class _CaptureMessages:
    """记录两条路径实际发给模型的消息，用于断言系统提示词到场。"""

    def __init__(self, native_ok: bool):
        self.native_ok = native_ok
        self.seen: list = []

    def with_structured_output(self, schema):
        if not self.native_ok:
            raise RuntimeError("model unsupported")
        outer = self

        class _R:
            async def ainvoke(self, messages):
                outer.seen = list(messages)
                return {"greeting": "pong"}

        return _R()

    async def ainvoke(self, messages):
        self.seen = list(messages)
        return AIMessage(content='{"greeting": "hi"}')


def test_system_prompt_reaches_native_extraction():
    """#556：抽取是独立的一次模型调用，agent 系统提示词必须在消息头。"""
    m = _CaptureMessages(native_ok=True)
    asyncio.run(structured_extract(m, [HumanMessage(content="快照")], SCHEMA, "领域约束X"))
    assert isinstance(m.seen[0], SystemMessage)
    assert m.seen[0].content == "领域约束X"
    assert isinstance(m.seen[1], HumanMessage)


def test_system_prompt_reaches_fallback_extraction():
    m = _CaptureMessages(native_ok=False)
    asyncio.run(structured_extract(m, [HumanMessage(content="快照")], SCHEMA, "领域约束X"))
    assert isinstance(m.seen[0], SystemMessage)
    assert m.seen[0].content == "领域约束X"


def test_no_system_prompt_keeps_messages_untouched():
    """不传（或空）时行为与本改动前一致，不凭空插消息。"""
    m = _CaptureMessages(native_ok=True)
    asyncio.run(structured_extract(m, [HumanMessage(content="快照")], SCHEMA))
    assert not any(isinstance(x, SystemMessage) for x in m.seen)

    m2 = _CaptureMessages(native_ok=True)
    asyncio.run(structured_extract(m2, [HumanMessage(content="快照")], SCHEMA, ""))
    assert not any(isinstance(x, SystemMessage) for x in m2.seen)


def test_existing_system_message_not_duplicated():
    """state 里已带 SystemMessage 就不重复补（langgraph 若改行为，这里自适应）。"""
    m = _CaptureMessages(native_ok=True)
    msgs = [SystemMessage(content="已有"), HumanMessage(content="快照")]
    asyncio.run(structured_extract(m, msgs, SCHEMA, "领域约束X"))
    assert [type(x).__name__ for x in m.seen] == ["SystemMessage", "HumanMessage"]
    assert m.seen[0].content == "已有"


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


class _JsonMode:
    """A provider with json_object but no json_schema, as DeepSeek: the native
    call is refused, the fallback goes through bind(response_format=...)."""

    def __init__(self, replies, refuse_json_object=False):
        self.replies = list(replies)
        self.refuse_json_object = refuse_json_object
        self.bound = []
        self.plain = 0
        self.seen = []

    def with_structured_output(self, schema):
        raise RuntimeError(
            "Error code: 400 - This response_format type is unavailable now"
        )

    def _reply(self, messages):
        self.seen.append(list(messages))
        text, finish = self.replies.pop(0)
        return AIMessage(content=text, response_metadata={"finish_reason": finish})

    def bind(self, **kwargs):
        outer = self

        class _Bound:
            async def ainvoke(self, messages):
                outer.bound.append(kwargs)
                if outer.refuse_json_object:
                    raise RuntimeError("400 - response_format json_object is not supported")
                return outer._reply(messages)

        return _Bound()

    async def ainvoke(self, messages):
        self.plain += 1
        return self._reply(messages)


def test_fallback_asks_for_json_object():
    m = _JsonMode([('{"greeting": "hi"}', "stop")])
    out, path = asyncio.run(structured_extract_with_path(m, [], SCHEMA))
    assert (out, path) == ({"greeting": "hi"}, "fallback")
    assert m.bound == [{"response_format": {"type": "json_object"}}]
    assert m.plain == 0


def test_fallback_without_json_object_asks_plainly():
    m = _JsonMode([('{"greeting": "hi"}', "stop")], refuse_json_object=True)
    out = asyncio.run(structured_extract(m, [], SCHEMA))
    assert out == {"greeting": "hi"}
    assert m.plain == 1


def test_fallback_without_json_object_in_other_words_asks_plainly():
    """A thinking-mode qwen refuses json_object without naming response_format."""

    class _Refused(Exception):
        status_code = 400

    m = _JsonMode([('{"greeting": "hi"}', "stop")])

    def bind(**kwargs):
        class _Bound:
            async def ainvoke(self, messages):
                raise _Refused("Json mode response is not supported when enable_thinking is true")

        return _Bound()

    m.bind = bind
    out = asyncio.run(structured_extract(m, [], SCHEMA))
    assert out == {"greeting": "hi"}
    assert m.plain == 1


def test_fallback_network_failure_is_not_hidden():
    class _Down(Exception):
        pass

    m = _JsonMode([('{"greeting": "hi"}', "stop")])

    def bind(**kwargs):
        class _Bound:
            async def ainvoke(self, messages):
                raise _Down("connection reset")

        return _Bound()

    m.bind = bind
    with pytest.raises(_Down):
        asyncio.run(structured_extract(m, [], SCHEMA))


def test_fallback_accepts_raw_newline_in_string():
    m = _JsonMode([('{"greeting": "a' + chr(10) + 'b"}', "stop")])
    out = asyncio.run(structured_extract(m, [], SCHEMA))
    assert out == {"greeting": "a" + chr(10) + "b"}


def test_truncated_reply_asks_for_a_shorter_one():
    m = _JsonMode([('{"greeting": "cut', "length"), ('{"greeting": "short"}', "stop")])
    out = asyncio.run(structured_extract(m, [], SCHEMA))
    assert out == {"greeting": "short"}
    retry = m.seen[1]
    # The cut-off text is not sent back; the retry asks for less.
    assert all('"cut' not in str(part) for part in retry)
    assert "cut off" in str(retry[-1])


def test_truncated_twice_says_so():
    m = _JsonMode([('{"greeting": "cut', "length"), ('{"greeting": "cut again', "length")])
    with pytest.raises(structured.StructuredOutputError, match=structured.TRUNCATED):
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
        # 非空：抽取拿到的必须是「提示词 + 技能段」拼接后的那一份。返回空串会让
        # 下面的断言退化成分不清「拼没拼技能」，抽取调用若被挪到拼接之前照样绿。
        return "\n[技能] demo"

    async def fake_tools(agent_tools, account_id, account_type, **kwargs):
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

    async def fake_extract(model, messages, schema, system_prompt=None):
        captured["extract_system_prompt"] = system_prompt
        return {"greeting": "pong"}, "native"

    monkeypatch.setattr(runner, "structured_extract_with_path", fake_extract)
    out = asyncio.run(runner.run_agent_once(
        _agent(), "hi", {}, [], None, "acc", "user", depth=1, response_format=SCHEMA,
    ))
    assert json.loads(out) == {"greeting": "pong"}
    assert False in captured["streaming"]  # 抽结构化用非流式模型
    # #556 回归：抽取是另起的一次模型调用，agent 系统提示词必须一并传下去，
    # 否则模型在零约束下填 schema（实测表现为把技术字段名原样抄进 display_name）
    assert captured["extract_system_prompt"] == "sys\n[技能] demo"


def test_run_without_response_format_text_path(monkeypatch):
    captured: dict = {}
    _wire(monkeypatch, captured)
    out = asyncio.run(runner.run_agent_once(
        _agent(), "hi", {}, [], None, "acc", "user", depth=1,
    ))
    assert out == "loop done"


def _single_call_agent(enabled=True):
    from app.models import AgentLimits
    agent = _agent()
    agent.limits = AgentLimits(max_turns=1, max_tool_calls=0, single_structured_call=enabled)
    return agent


def _wire_single(monkeypatch, captured, tools=None):
    _wire(monkeypatch, captured)

    def graph(*a, **k):
        captured["graph_built"] = True
        return _FakeGraph()

    monkeypatch.setattr(runner, "create_agent", graph)
    if tools is not None:
        async def fake_tools(agent_tools, account_id, account_type, **kwargs):
            return tools

        monkeypatch.setattr(tools_mod, "load_tools", fake_tools)

    async def fake_extract(model, messages, schema, system_prompt=None):
        captured["messages"] = messages
        captured["extract_system_prompt"] = system_prompt
        return {"greeting": "pong"}, "fallback"

    monkeypatch.setattr(runner, "structured_extract_with_path", fake_extract)


def test_single_structured_call_skips_free_form_call(monkeypatch):
    captured: dict = {}
    _wire_single(monkeypatch, captured)
    out = asyncio.run(runner.run_agent_once(
        _single_call_agent(), "the file", {}, [], None, "acc", "user", depth=1, response_format=SCHEMA,
    ))
    assert json.loads(out) == {"greeting": "pong"}
    assert "graph_built" not in captured
    # The structured call reads the task message itself, with the full system prompt.
    assert [type(m) for m in captured["messages"]] == [HumanMessage]
    assert captured["messages"][0].content == "the file"
    assert captured["extract_system_prompt"] == "sys\n[技能] demo"
    assert captured["streaming"] == [True, False]


def test_single_structured_call_off_keeps_two_calls(monkeypatch):
    captured: dict = {}
    _wire_single(monkeypatch, captured)
    asyncio.run(runner.run_agent_once(
        _single_call_agent(enabled=None), "the file", {}, [], None, "acc", "user", depth=1, response_format=SCHEMA,
    ))
    assert captured["graph_built"] is True
    assert captured["messages"][-1].content == "loop done"


def test_single_structured_call_ignored_with_tools(monkeypatch):
    captured: dict = {}
    _wire_single(monkeypatch, captured, tools=[object()])
    asyncio.run(runner.run_agent_once(
        _single_call_agent(), "the file", {}, [], None, "acc", "user", depth=1, response_format=SCHEMA,
    ))
    assert captured["graph_built"] is True


def test_single_structured_call_ignored_without_response_format(monkeypatch):
    captured: dict = {}
    _wire_single(monkeypatch, captured)
    out = asyncio.run(runner.run_agent_once(
        _single_call_agent(), "the file", {}, [], None, "acc", "user", depth=1,
    ))
    assert out == "loop done"
    assert captured["graph_built"] is True


def test_normalize_response_format():
    from app.core.llm import normalize_response_format
    assert normalize_response_format({"type": "object", "properties": {}})["title"] == "StructuredResponse"
    assert normalize_response_format({"title": "X", "type": "object"}) == {"title": "X", "type": "object"}
    assert normalize_response_format(None) is None
