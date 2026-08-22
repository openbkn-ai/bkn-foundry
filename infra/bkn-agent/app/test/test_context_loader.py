"""Context Loader MCP 装载（ToolRef type=context_loader）。

守的是三件不能回退的事：
1. bkn_context 由运行时注入，不进模型可见的参数表（模型编不出合法 id）
2. 凭据只认调用方透传的令牌；没有就不挂工具，且不静默（无服务凭据兜底）
3. 生命周期工具不暴露给模型
"""
import asyncio

import pytest
from langchain_core.tools import StructuredTool

from app import auth
from app.core import context_loader


class _FakeTool:
    def __init__(self, name, args_schema=None, result=None):
        self.name = name
        self.description = f"desc {name}"
        self.args_schema = args_schema if args_schema is not None else {
            "type": "object",
            "properties": {"kn_id": {"type": "string"}, "bkn_context": {"type": "object"}},
            "required": ["kn_id", "bkn_context"],
        }
        self.metadata = {}
        self._result = result
        self.calls = []

    async def coroutine(self, **kwargs):
        self.calls.append(kwargs)
        return self._result


def _start_tool():
    return _FakeTool(
        "bkn_start_interaction",
        args_schema={"type": "object", "properties": {"conversation_mode": {"type": "string"}}},
        # VM 实测形状：(content_blocks, {"structured_content": {...}})
        result=(
            [{"type": "text",
              "text": '{"conversation_id":"conv_real","execution_status":"active",'
                      '"interaction_id":"int_real"}',
              "id": "lc_1"}],
            {"structured_content": {
                "conversation_id": "conv_real",
                "execution_status": "active",
                "interaction_id": "int_real"}},
        ),
    )


def _install(monkeypatch, tools):
    class _Client:
        async def get_tools(self):
            return tools

    monkeypatch.setattr(context_loader, "_client", lambda *_a, **_k: _Client())


def test_no_credential_skips_and_warns(monkeypatch, caplog):
    token = auth.set_caller_token(None)
    try:
        with caplog.at_level("WARNING"):
            assert asyncio.run(context_loader.open_session()) is None
        assert "no Authorization header" in caplog.text  # never silent
    finally:
        auth._caller_token.reset(token)


def test_session_injects_bkn_context_and_hides_it(monkeypatch):
    business = _FakeTool("search_schema", result="ok")
    _install(monkeypatch, [_start_tool(), _FakeTool("bkn_finish_interaction"), business])
    token = auth.set_caller_token("Bearer t")
    try:
        session = asyncio.run(context_loader.open_session())
    finally:
        auth._caller_token.reset(token)

    assert session is not None
    assert session.conversation_id == "conv_real"
    assert session.interaction_id == "int_real"

    # 生命周期工具不给模型
    names = [t.name for t in session.tools()]
    assert names == ["search_schema"]

    # bkn_context 已从模型可见的入参表里摘掉
    assert "bkn_context" not in business.args_schema["properties"]
    assert "bkn_context" not in business.args_schema["required"]

    # 但调用时会被补上
    asyncio.run(session.tools()[0].coroutine(kn_id="kn-1"))
    assert business.calls == [
        {"kn_id": "kn-1", "bkn_context": {"conversation_id": "conv_real", "interaction_id": "int_real"}}
    ]


def test_start_handshake_declares_new_mode_without_a_local_conversation_id(monkeypatch):
    start = _start_tool()
    _install(monkeypatch, [start, _FakeTool("bkn_finish_interaction"), _FakeTool("search_schema")])
    token = auth.set_caller_token("Bearer t")
    try:
        session = asyncio.run(context_loader.open_session(
            "查询库存", agent_name="supply-chain-analyst", host_conversation_key="thread-1"
        ))
    finally:
        auth._caller_token.reset(token)

    assert session is not None
    assert start.calls == [{
        "question": "查询库存",
        "agent_name": "supply-chain-analyst",
        "conversation_mode": "new",
    }]


def test_start_handshake_omits_mode_when_the_server_has_not_declared_it(monkeypatch):
    start = _start_tool()
    start.args_schema = {"type": "object", "properties": {}}
    _install(monkeypatch, [start, _FakeTool("bkn_finish_interaction"), _FakeTool("search_schema")])
    token = auth.set_caller_token("Bearer t")
    try:
        session = asyncio.run(context_loader.open_session(
            "查询库存", agent_name="supply-chain-analyst", host_conversation_key="thread-1"
        ))
    finally:
        auth._caller_token.reset(token)

    assert session is not None
    assert start.calls == [{
        "question": "查询库存",
        "agent_name": "supply-chain-analyst",
    }]


def test_bound_context_loader_tool_preserves_mcp_artifact_response_format():
    async def raw_call(**_kwargs):
        return ([{"type": "text", "text": "result"}], {"structured_content": {}})

    raw = StructuredTool.from_function(
        coroutine=raw_call,
        name="search_schema",
        description="search",
        response_format="content_and_artifact",
    )

    bound = context_loader._bind_context(
        raw, context_loader.ContextLoaderSession("conv-1", "int-1")
    )

    assert bound.response_format == "content_and_artifact"


def test_session_tools_can_be_limited_to_an_explicit_read_only_allow_list(monkeypatch):
    schema = _FakeTool("search_schema")
    detail = _FakeTool("get_kn_detail")
    unsafe = _FakeTool("run_sql")
    _install(monkeypatch, [_start_tool(), _FakeTool("bkn_finish_interaction"), schema, detail, unsafe])
    token = auth.set_caller_token("Bearer t")
    try:
        session = asyncio.run(context_loader.open_session())
    finally:
        auth._caller_token.reset(token)

    assert session is not None
    assert [tool.name for tool in session.tools({"search_schema", "get_kn_detail"})] == [
        "search_schema", "get_kn_detail"
    ]
    assert [tool.name for tool in session.tools({"search_schema"})] == ["search_schema"]


def test_start_interaction_failure_skips_tools(monkeypatch):
    class _Boom(_FakeTool):
        async def coroutine(self, **kwargs):
            raise RuntimeError("resource_not_disclosed")

    _install(monkeypatch, [_Boom("bkn_start_interaction"), _FakeTool("search_schema")])
    token = auth.set_caller_token("Bearer t")
    try:
        assert asyncio.run(context_loader.open_session()) is None
    finally:
        auth._caller_token.reset(token)


@pytest.mark.parametrize("raw", [
    # 裸 dict
    {"conversation_id": "c", "interaction_id": "i"},
    # JSON 串
    '{"conversation_id":"c","interaction_id":"i"}',
    # VM 实测：(content_blocks, artifact)，id 在 structured_content 里
    ([{"type": "text", "text": '{"conversation_id":"c","interaction_id":"i"}'}],
     {"structured_content": {"conversation_id": "c", "interaction_id": "i"}}),
    # 同上但只有文本块，没有 structured_content —— 退回解析 text
    ([{"type": "text", "text": '{"conversation_id":"c","interaction_id":"i"}'}], {}),
    # camelCase 的 structuredContent
    ([], {"structuredContent": {"conversation_id": "c", "interaction_id": "i"}}),
])
def test_parse_ids_accepts_real_and_simple_shapes(raw):
    assert context_loader._parse_ids(raw) == ("c", "i")


@pytest.mark.parametrize("raw", [
    None, "", "not json", 123, [], {},
    {"conversation_id": "c"},                      # 缺一半
    {"conversation_id": "", "interaction_id": "i"},  # 空串不算
    ([{"type": "text", "text": "plain text"}], {}),
])
def test_parse_ids_rejects_garbage(raw):
    assert context_loader._parse_ids(raw) is None


def test_wanted_detects_ref():
    assert context_loader.wanted([{"type": "context_loader"}])
    assert not context_loader.wanted([{"type": "toolbox", "box_id": "b"}])


def _probe_app():
    """装同一条中间件的一次性 app。

    不往 app.main 的真 app 上挂路由——那会漏进 openapi()，把契约冻结测试撞红
    （已经撞过一次）。
    """
    from fastapi import FastAPI

    from app.main import bkn_trace_context_middleware

    probe = FastAPI()
    probe.middleware("http")(bkn_trace_context_middleware)
    return probe


def test_caller_token_visible_inside_request_handler():
    """回归：令牌必须在请求协程里读得到。

    原先在 get_account（同步依赖）里 set，FastAPI 把同步依赖丢进线程池执行，
    ContextVar 传不回请求协程，caller_token() 恒为 None —— VM 实测的表现是
    CL 工具静默不挂、模型改口编了个工具调用当答案。改由中间件 set。
    """
    from fastapi.testclient import TestClient

    seen = {}
    probe = _probe_app()

    @probe.get("/t")
    def _t():
        seen["token"] = auth.caller_token()
        return {"ok": True}

    TestClient(probe).get("/t", headers={"Authorization": "Bearer probe-token"})
    assert seen["token"] == "Bearer probe-token"


def test_caller_token_absent_when_header_missing():
    from fastapi.testclient import TestClient

    seen = {}
    probe = _probe_app()

    @probe.get("/t")
    def _t():
        seen["token"] = auth.caller_token()
        return {"ok": True}

    TestClient(probe).get("/t")
    assert seen["token"] is None


class _FinishTool:
    def __init__(self):
        self.calls = []

    async def coroutine(self, **kwargs):
        self.calls.append(kwargs)
        return {"execution_status": "completed"}


def _session_with_finish():
    s = context_loader.ContextLoaderSession("conv_x", "int_x")
    fin = _FinishTool()
    s._finish = fin
    return s, fin


def test_close_sends_interaction_id_not_bkn_context():
    """服务端要平铺的 interaction_id；包进 bkn_context 会被 resource_not_disclosed 拒。"""
    s, fin = _session_with_finish()
    asyncio.run(context_loader.close_session(s, outcome="completed", answer="a"))
    assert fin.calls[0]["interaction_id"] == "int_x"
    assert "bkn_context" not in fin.calls[0]


def test_close_completed_always_carries_answer():
    """outcome=completed 不带 answer 会被 closure_manifest_invalid 拒。"""
    s, fin = _session_with_finish()
    asyncio.run(context_loader.close_session(s, outcome="completed", answer="hello"))
    assert fin.calls[0]["outcome"] == "completed"
    assert fin.calls[0]["answer"] == "hello"


@pytest.mark.parametrize("bad", ["succeeded", "ok", "", None, "COMPLETED"])
def test_close_clamps_outcome_to_enum(bad):
    """枚举只有 completed/failed/cancelled/handed_off，别的一律夹回 completed。"""
    s, fin = _session_with_finish()
    asyncio.run(context_loader.close_session(s, outcome=bad, answer="a"))
    assert fin.calls[0]["outcome"] in context_loader._OUTCOMES


def test_close_failed_carries_reason_and_no_answer():
    s, fin = _session_with_finish()
    asyncio.run(context_loader.close_session(s, outcome="failed", reason="没产出"))
    assert fin.calls[0]["outcome"] == "failed"
    assert fin.calls[0]["reason"] == "没产出"
    assert "answer" not in fin.calls[0]


def test_close_failure_is_warned_not_raised():
    """收尾失败不该把已经跑完的一轮翻成失败。"""
    s, fin = _session_with_finish()

    async def boom(**_):
        raise RuntimeError("resource_not_disclosed")

    fin.coroutine = boom
    asyncio.run(context_loader.close_session(s, outcome="completed", answer="a"))


def test_close_on_none_session_is_noop():
    asyncio.run(context_loader.close_session(None, outcome="completed", answer="a"))


def test_setup_failure_still_finishes_interaction(monkeypatch):
    """setup 阶段抛异常时，已握手的交互必须被收尾。

    _events() 在 stream_chat 抛异常时根本没被构造，它的 finally 永远不跑。
    握手若已成功，那次交互会永久停在 active，且每次重试再泄一个。
    """
    from app.core import graph

    closed = []

    async def fake_close(session, **kw):
        closed.append((session, kw))

    fake_session = context_loader.ContextLoaderSession("conv_s", "int_s")

    async def fake_open(*_a, **_k):
        return fake_session

    monkeypatch.setattr(graph.context_loader, "open_session", fake_open)
    monkeypatch.setattr(graph.context_loader, "close_session", fake_close)
    monkeypatch.setattr(graph.context_loader, "wanted", lambda _refs: True)

    async def boom(*_a, **_k):
        raise RuntimeError("toolbox 解析不了")

    monkeypatch.setattr(graph, "load_tools", boom)
    monkeypatch.setattr(graph, "load_skills", lambda *_a, **_k: _async_str())
    monkeypatch.setattr(graph, "resolve_prompt", lambda *_a, **_k: _async_prompt())

    class _Dao:
        async def get_thread_row(self, *_a, **_k):
            return None

        async def touch_thread(self, *_a, **_k):
            return None

    monkeypatch.setattr(graph, "dao", _Dao())

    class _Req:
        thread_id = "thread-setup-fail"
        message = "hi"
        skills: list = []
        prompt_override = None
        prompt_vars: dict = {}
        response_format = None

    class _Agent:
        agent_id = "a"
        name = "a"
        tools = [{"type": "context_loader"}]
        skills: list = []
        limits = None
        model = ""

    with pytest.raises(RuntimeError):
        asyncio.run(graph.stream_chat(None, _Agent(), _Req(), "acct", "user"))

    assert closed, "setup 失败后必须调 close_session，否则交互永久 active"
    assert closed[0][1]["outcome"] == "failed"
    assert "thread-setup-fail" not in graph._busy_threads


async def _async_str():
    return ""


async def _async_prompt():
    return ("prompt", "src", "v1")


def test_load_tools_never_opens_its_own_session(monkeypatch):
    """load_tools 绝不自己握手。

    原先是 `current_session() or await open_session()`。主路握手失败时它会再开
    一个交互，而 graph 的 finally 只关自己持有的 cl_session（那时是 None），
    兜底开的那个永远关不掉。服务端一个 conversation 只允许一个 active 交互，
    于是每轮泄一个，下一轮必被 interaction_in_progress 挡掉。
    """
    from app.core import tools as tools_mod

    opened = []

    async def spy_open(*_a, **_k):
        opened.append(1)
        return context_loader.ContextLoaderSession("c", "i")

    monkeypatch.setattr(tools_mod.context_loader, "open_session", spy_open)
    monkeypatch.setattr(tools_mod.context_loader, "current_session", lambda: None)
    monkeypatch.setattr(tools_mod.context_loader, "wanted", lambda _refs: True)

    async def no_toolbox(*_a, **_k):
        return []

    monkeypatch.setattr(tools_mod, "_toolbox_tools", no_toolbox)

    result = asyncio.run(
        tools_mod.load_tools([{"type": "context_loader"}], "acct", "user")
    )

    assert opened == [], "load_tools 自己开了会话——那个交互没人关，会挡住下一轮"
    assert result == []


def test_close_session_is_idempotent():
    """正常路径提前关一次、finally 再兜一次，第二次必须是 no-op。

    不幂等的话第二次会打到服务端，拿一个已经 completed 的交互再 finish，
    白落一条告警。
    """
    s, fin = _session_with_finish()
    asyncio.run(context_loader.close_session(s, outcome="completed", answer="a"))
    asyncio.run(context_loader.close_session(s, outcome="completed", answer="a"))
    assert len(fin.calls) == 1
    assert s.closed is True


def test_contextvar_token_reset_in_same_context():
    """ContextVar 的 set/reset 必须成对出现在同一个 context。

    早先是在 stream_chat 里 set、在 _events() 这个异步生成器的 finally 里 reset。
    生成器跑在复制出来的独立 context，跨 context 复位直接抛
    ValueError: Token was created in a different Context，而且它抛在 finally
    开头，把后面的兜底收尾一并废掉——VM 上每个 /chat 请求都在刷这条 ASGI 异常。
    """
    import contextvars

    sess = context_loader.ContextLoaderSession("c", "i")
    token = context_loader.set_current(sess)
    assert context_loader.current_session() is sess

    def _in_copied_context():
        # 生成器/任务拿到的是 context 的副本：读得到，但复位会炸
        assert context_loader.current_session() is sess
        try:
            context_loader.reset_current(token)
        except ValueError as e:
            return str(e)
        return None

    err = contextvars.copy_context().run(_in_copied_context)
    assert err is not None and "different Context" in err

    # 同一 context 内复位则正常
    context_loader.reset_current(token)
    assert context_loader.current_session() is None


def test_session_stays_visible_after_load_tools():
    """会话必须活过整轮，不能只活到 load_tools 结束。

    agent-as-tool 的子 agent 在执行期才调 load_tools，靠 current_session() 拿
    父会话。上一版把 ContextVar 收窄到装载那一段，子 agent 于是带着零个 CL 工具
    作答——不报错、不告警。
    """
    sess = context_loader.ContextLoaderSession("c", "i")
    context_loader.set_current(sess)
    try:
        # 装载早已结束；执行期（子 agent）仍要看得见
        assert context_loader.current_session() is sess
    finally:
        context_loader.set_current(None)


def test_busy_release_is_first_in_sse_finally():
    """放位必须是 SSE finally 的第一条语句，前面不许有会抛的调用。

    这条守的是模式而不是某一行：排在放位前面的每一条都可能把 thread 永久留在
    409——close_session 挡不住断连的 CancelledError，evidence.end_interaction 是
    ContextVar 复位、由 aclose 任务驱动时跨 context 必抛。两者都真的踩过。

    源码级断言是刻意的：这个失效只在客户端中途断连时发生，单测跑不出来。
    """
    import inspect
    import re

    from app.core import graph

    src = inspect.getsource(graph.stream_chat)
    # 取最后一个 finally 块（_events 的那个）
    tail = src[src.rindex("finally:"):]
    body = [ln.strip() for ln in tail.splitlines()[1:] if ln.strip() and not ln.strip().startswith("#")]
    assert body, "没解析到 finally 块"
    assert re.match(r"_busy_threads\.discard", body[0]), (
        f"finally 的第一条不是放位，而是：{body[0]}\n"
        "排在放位前面的任何抛出都会让该 thread 永久停在 409"
    )


def test_sub_agent_opens_and_closes_its_own_session(monkeypatch):
    """agent-as-tool：父轮已开交互时，子 agent 也要拿得到 CL 会话。

    run_agent_once 在 has_interaction() 为真时直接短路进 core，从不调
    open_session。父 agent 没声明 context_loader 时 current_session() 是 None，
    子 agent 于是带着零个 CL 工具作答——而且 open_session 没被调用过，
    连 warning 都没有，日志里什么都看不到。

    继承得到就不重复握手、也不去关别人开的会话；继承不到才自己开、自己关。
    """
    from app.core import runner as runner_mod

    opened, closed = [], []
    sess = context_loader.ContextLoaderSession("c-sub", "i-sub")

    async def fake_open(*_a, **_k):
        opened.append(1)
        return sess

    async def fake_close(s, **kw):
        closed.append((s, kw))

    async def fake_core(*_a, **_k):
        assert context_loader.current_session() is sess, "子 agent 执行期看不到会话"
        return "子 agent 的回答"

    monkeypatch.setattr(runner_mod.evidence, "has_interaction", lambda: True)
    monkeypatch.setattr(runner_mod.context_loader, "open_session", fake_open)
    monkeypatch.setattr(runner_mod.context_loader, "close_session", fake_close)
    monkeypatch.setattr(runner_mod, "_run_agent_once_core", fake_core)

    class _Sub:
        agent_id = "b"
        name = "b"
        tools = [{"type": "context_loader"}]
        skills: list = []
        limits = None
        model = ""

    out = asyncio.run(
        runner_mod.run_agent_once(_Sub(), "问题", {}, [], None, "acct", "user", 1)
    )

    assert out == "子 agent 的回答"
    assert opened == [1], "子 agent 没拿到会话——它会静默少掉全部检索工具"
    assert closed and closed[0][0] is sess, "自己开的会话必须自己关"
    assert closed[0][1]["outcome"] == "completed"
    assert context_loader.current_session() is None, "ContextVar 没复位"


def test_sub_agent_does_not_close_inherited_session(monkeypatch):
    """继承来的会话归开它的人关，子 agent 不许代关。"""
    from app.core import runner as runner_mod

    parent = context_loader.ContextLoaderSession("c-p", "i-p")
    opened, closed = [], []

    async def fake_open(*_a, **_k):
        opened.append(1)
        return context_loader.ContextLoaderSession("c-x", "i-x")

    async def fake_close(s, **kw):
        closed.append(s)

    async def fake_core(*_a, **_k):
        return "ok"

    monkeypatch.setattr(runner_mod.evidence, "has_interaction", lambda: True)
    monkeypatch.setattr(runner_mod.context_loader, "open_session", fake_open)
    monkeypatch.setattr(runner_mod.context_loader, "close_session", fake_close)
    monkeypatch.setattr(runner_mod, "_run_agent_once_core", fake_core)

    class _Sub:
        agent_id = "b"
        name = "b"
        tools = [{"type": "context_loader"}]
        skills: list = []
        limits = None
        model = ""

    token = context_loader.set_current(parent)
    try:
        asyncio.run(
            runner_mod.run_agent_once(_Sub(), "问题", {}, [], None, "acct", "user", 1)
        )
    finally:
        context_loader.reset_current(token)

    assert opened == [], "继承得到会话还去重复握手"
    assert all(s is not parent for s in closed), "把继承来的会话关掉了"
