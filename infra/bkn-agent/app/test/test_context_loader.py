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
        result={"conversation_id": "conv_real", "interaction_id": "int_real"},
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


@pytest.mark.parametrize(
    "raw",
    [
        {"conversation_id": "c", "interaction_id": "i"},
        '{"conversation_id":"c","interaction_id":"i"}',
        ['{"conversation_id":"c","interaction_id":"i"}'],
    ],
)
def test_parse_ids_accepts_dict_text_and_list(raw):
    assert context_loader._parse_ids(raw) == ("c", "i")


@pytest.mark.parametrize("raw", [None, "not json", {}, {"conversation_id": "c"}, []])
def test_parse_ids_rejects_garbage(raw):
    assert context_loader._parse_ids(raw) is None


def test_wanted_detects_ref():
    assert context_loader.wanted([{"type": "context_loader"}])
    assert not context_loader.wanted([{"type": "toolbox", "box_id": "b"}])
