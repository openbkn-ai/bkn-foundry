"""Context Loader MCP 装载（ToolRef type=context_loader）。

守的是三件不能回退的事：
1. bkn_context 由运行时注入，不进模型可见的参数表（模型编不出合法 id）
2. 凭据优先调用方透传，AppKey 只是兜底；两个都没有就不挂工具，且不静默
3. 生命周期工具不暴露给模型
"""
import asyncio

import pytest

from app import auth
from app.config import config
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
        args_schema={"type": "object", "properties": {}},
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

    monkeypatch.setattr(context_loader, "_client", lambda _auth: _Client())


def test_no_credential_skips_and_warns(monkeypatch, caplog):
    monkeypatch.setattr(config, "CONTEXT_LOADER_APPKEY", "")
    token = auth.set_caller_token(None)
    try:
        with caplog.at_level("WARNING"):
            assert asyncio.run(context_loader.open_session()) is None
        assert "无可用凭据" in caplog.text  # 不静默
    finally:
        auth._caller_token.reset(token)


def test_caller_token_preferred_over_appkey(monkeypatch):
    monkeypatch.setattr(config, "CONTEXT_LOADER_APPKEY", "bak_service")
    token = auth.set_caller_token("Bearer user-token")
    try:
        assert context_loader._credential() == ("Bearer user-token", "caller")
    finally:
        auth._caller_token.reset(token)


def test_appkey_is_fallback_only(monkeypatch):
    monkeypatch.setattr(config, "CONTEXT_LOADER_APPKEY", "bak_service")
    token = auth.set_caller_token(None)
    try:
        assert context_loader._credential() == ("Bearer bak_service", "service_appkey")
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
