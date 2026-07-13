"""执行工厂 toolbox 工具装载（工具面收敛，替代 agent-retrieval 专用 MCP 通道）。"""
import asyncio

import pytest

from app.core import toolbox
from app.core.tools import _toolbox_tools
from app.errors import bad_request  # noqa: F401  (确认导出仍存在)

_TOOL_INFO = {
    "tool_id": "t-1",
    "name": "list_knowledge_networks",
    "description": "列出知识网络",
    "status": "enabled",
    "metadata_type": "openapi",
    "metadata": {
        "server_url": "http://agent-retrieval:30779",
        "path": "/api/agent-retrieval/in/v1/kn/list_knowledge_networks",
        "method": "POST",
        # impex 扁平形态：request_body + responses 数组（非 OpenAPI paths 树）
        "api_spec": {
            "request_body": {
                "required": True,
                "content": {
                    "application/json": {
                        "schema": {
                            "type": "object",
                            "properties": {
                                "query": {"type": "string", "description": "过滤"},
                                "limit": {"type": "integer"},
                            },
                            "required": ["query"],
                        }
                    }
                },
            },
            "responses": [{"status_code": "200", "description": "ok", "content": {}}],
        },
    },
}


def test_safe_name_sanitize_and_fallback():
    assert toolbox._safe_name("get_kn_detail", "x") == "get_kn_detail"
    assert toolbox._safe_name("含 空格-中文", "abcdef1234") == "tool_abcdef12"
    assert len(toolbox._safe_name("a" * 200, "x")) == 64


def test_args_model_required_and_optional():
    model = toolbox._args_model("t", _TOOL_INFO["metadata"])
    fields = model.model_fields
    assert fields["query"].is_required()
    assert not fields["limit"].is_required()
    with pytest.raises(Exception):
        model()  # 缺必填
    m = model(query="q")
    assert m.limit is None


def test_build_tool_skips_disabled_and_non_openapi():
    disabled = {**_TOOL_INFO, "status": "disabled"}
    func = {**_TOOL_INFO, "metadata_type": "function"}
    assert toolbox._build_tool("b-1", disabled, "u", "user") is None
    assert toolbox._build_tool("b-1", func, "u", "user") is None
    tool = toolbox._build_tool("b-1", _TOOL_INFO, "u", "user")
    assert tool is not None
    assert tool.name == "list_knowledge_networks"


def test_execute_payload_routing(monkeypatch):
    """POST 参数进 body、GET 进 query；身份 header 双份（外层请求 + 转发 header）。"""
    captured = {}

    async def fake_execute(box_id, tool_id, method, args, account_id, account_type):
        captured.update(box=box_id, tool=tool_id, method=method, args=args, aid=account_id)
        return "ok"

    monkeypatch.setattr(toolbox, "_execute", fake_execute)
    tool = toolbox._build_tool("b-1", _TOOL_INFO, "u-9", "user")
    out = asyncio.run(tool.coroutine(query="q", limit=None))
    assert out == "ok"
    assert captured["box"] == "b-1" and captured["tool"] == "t-1"
    assert captured["aid"] == "u-9"


def test_default_toolbox_degrades_but_explicit_ref_fails(monkeypatch):
    """默认 box 拉不到 → 降级空工具；显式 type=toolbox 拉不到 → 抛错。"""

    async def boom(box_id, account_id, account_type):
        raise RuntimeError("factory down")

    monkeypatch.setattr("app.core.tools.load_toolbox_tools", boom)
    monkeypatch.setattr("app.core.tools.config.DEFAULT_TOOLBOXES", "box-default")

    tools = asyncio.run(_toolbox_tools([], "u", "user"))
    assert tools == []

    with pytest.raises(RuntimeError):
        asyncio.run(_toolbox_tools([{"type": "toolbox", "box_id": "box-x"}], "u", "user"))
