"""执行工厂 toolbox 工具装载（工具面收敛，替代 agent-retrieval 专用 MCP 通道）。"""
import asyncio

import pytest

from app import evidence, observability
from app.core import toolbox
from app.core.tools import _derive_agent_tool_name, _mcp_connections, _toolbox_tools
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


_QUERY_TOOL_INFO = {
    # contextloader 真实形态：必填 kn_id/ot_id 只声明在 parameters(in:query)，body 里没有。
    # 老实现只读 request_body → LLM 无处填 kn_id → 下游必 400（P0 回归）。
    "tool_id": "t-2",
    "name": "query_object_instance",
    "description": "查对象实例",
    "status": "enabled",
    "metadata_type": "openapi",
    "metadata": {
        "path": "/api/agent-retrieval/in/v1/kn/query_object_instance",
        "method": "POST",
        "api_spec": {
            "parameters": [
                {"name": "kn_id", "in": "query", "required": True, "schema": {"type": "string"}},
                {"name": "ot_id", "in": "query", "required": True, "schema": {"type": "string"}},
                {"name": "x-account-id", "in": "header", "required": False, "schema": {"type": "string"}},
            ],
            "request_body": {
                "content": {
                    "application/json": {"schema": {"$ref": "#/components/schemas/QueryReq"}}
                }
            },
            "components": {
                "schemas": {
                    "QueryReq": {
                        "type": "object",
                        "properties": {"limit": {"type": "integer", "description": "条数"}},
                    }
                }
            },
        },
    },
}


def test_safe_name_sanitize_and_fallback():
    assert toolbox._safe_name("get_kn_detail", "x") == "get_kn_detail"
    assert toolbox._safe_name("含 空格-中文", "abcdef1234") == "tool_abcdef12"
    assert len(toolbox._safe_name("a" * 200, "x")) == 64


def test_args_model_required_and_optional():
    model, params = toolbox._args_model("t", _TOOL_INFO["metadata"])
    fields = model.model_fields
    assert fields["query"].is_required()
    assert not fields["limit"].is_required()
    with pytest.raises(Exception):
        model()  # 缺必填
    m = model(query="q")
    assert m.limit is None
    assert {p.wire: p.location for p in params} == {"query": "body", "limit": "body"}


def test_args_model_includes_query_params_and_resolves_ref():
    """P0 回归：必填 query 参数（kn_id/ot_id）必须进 args model，身份 header 必须排除。"""
    model, params = toolbox._args_model("query_object_instance", _QUERY_TOOL_INFO["metadata"])
    fields = model.model_fields
    assert fields["kn_id"].is_required() and fields["ot_id"].is_required()
    assert "limit" in fields and not fields["limit"].is_required()  # $ref body schema 解析
    assert "x-account-id" not in fields and "x_account_id" not in fields  # 身份不给 LLM
    loc = {p.wire: p.location for p in params}
    assert loc == {"kn_id": "query", "ot_id": "query", "limit": "body"}


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

    async def fake_execute(box_id, tool_id, tool_name, method, args, params, account_id, account_type):
        captured.update(box=box_id, tool=tool_id, tool_name=tool_name, method=method, args=args, aid=account_id)
        return "ok"

    monkeypatch.setattr(toolbox, "_execute", fake_execute)
    tool = toolbox._build_tool("b-1", _TOOL_INFO, "u-9", "user")
    out = asyncio.run(tool.coroutine(query="q", limit=None))
    assert out == "ok"
    assert captured["box"] == "b-1" and captured["tool"] == "t-1"
    assert captured["tool_name"] == "list_knowledge_networks"
    assert captured["aid"] == "u-9"


def test_execute_splits_query_and_body_by_declared_location(monkeypatch):
    """P0 回归：POST 工具的 query 参数必须落 payload['query']，不能全塞 body。"""
    sent = {}

    class _Resp:
        status = 200

        async def text(self):
            return '{"status_code": 200, "body": {"ok": true}}'

        async def __aenter__(self):
            return self

        async def __aexit__(self, *a):
            return False

    class _Session:
        def __init__(self, *a, **k):
            pass

        def post(self, url, json=None, headers=None):
            sent.update(url=url, payload=json, headers=headers)
            return _Resp()

        async def __aenter__(self):
            return self

        async def __aexit__(self, *a):
            return False

    monkeypatch.setattr(toolbox.aiohttp, "ClientSession", _Session)
    _, params = toolbox._args_model("query_object_instance", _QUERY_TOOL_INFO["metadata"])
    out = asyncio.run(
        toolbox._execute("b-1", "t-2", "query_object_instance", "POST", {"kn_id": "kn1", "ot_id": "ot1", "limit": 5}, params, "u-9", "user")
    )
    assert '"ok": true' in out
    assert sent["payload"]["query"] == {"kn_id": "kn1", "ot_id": "ot1"}
    assert sent["payload"]["body"] == {"limit": 5}
    assert sent["payload"]["header"]["x-account-id"] == "u-9"


def test_execute_propagates_trace_context_to_proxy_and_downstream_payload(monkeypatch):
    """E2E-1 回归：bkn-agent -> toolbox proxy -> 下游 OpenBKN 必须保留 trace/request id。"""
    sent = {}
    traceparent = "00-1234567890abcdef1234567890abcdef-abcdef1234567890-01"

    class _Resp:
        status = 200

        async def text(self):
            return '{"status_code": 200, "body": {"ok": true}}'

        async def __aenter__(self):
            return self

        async def __aexit__(self, *a):
            return False

    class _Session:
        def __init__(self, *a, **k):
            pass

        def post(self, url, json=None, headers=None):
            sent.update(url=url, payload=json, headers=headers)
            return _Resp()

        async def __aenter__(self):
            return self

        async def __aexit__(self, *a):
            return False

    ctx = observability.TraceContext(
        trace_id="1234567890abcdef1234567890abcdef",
        request_id="req_toolbox_trace_001",
        traceparent=traceparent,
        entry_boundary="external",
        upstream_span_id="abcdef1234567890",
    )
    token = observability.set_context(ctx)
    try:
        monkeypatch.setattr(toolbox.aiohttp, "ClientSession", _Session)
        _, params = toolbox._args_model("query_object_instance", _QUERY_TOOL_INFO["metadata"])
        out = asyncio.run(
            toolbox._execute(
                "b-1",
                "t-2",
                "query_object_instance",
                "POST",
                {"kn_id": "kn1", "ot_id": "ot1", "limit": 5},
                params,
                "u-9",
                "user",
            )
        )
    finally:
        observability.reset_context(token)

    assert '"ok": true' in out
    expected = {
        "traceparent": traceparent,
        "bkn-request-id": "req_toolbox_trace_001",
        "x-request-id": "req_toolbox_trace_001",
        "x-trace-id": "1234567890abcdef1234567890abcdef",
    }
    for key, value in expected.items():
        assert sent["headers"][key] == value
        assert sent["payload"]["header"][key] == value


def test_execute_emits_hash_only_tool_evidence(monkeypatch):
    """E2E-1 回归：工具调用要形成证据事件，但不能泄露参数值或结果正文。"""
    submitted = []
    traceparent = "00-1234567890abcdef1234567890abcdef-abcdef1234567890-01"

    class _Resp:
        status = 200

        async def text(self):
            return '{"status_code": 200, "body": {"answer": "客户A风险升高"}}'

        async def __aenter__(self):
            return self

        async def __aexit__(self, *a):
            return False

    class _Session:
        def __init__(self, *a, **k):
            pass

        def post(self, url, json=None, headers=None):
            return _Resp()

        async def __aenter__(self):
            return self

        async def __aexit__(self, *a):
            return False

    async def fake_submit(events, account_id, account_type):
        submitted.extend(events)

    ctx = observability.TraceContext(
        trace_id="1234567890abcdef1234567890abcdef",
        request_id="req_toolbox_evidence_001",
        traceparent=traceparent,
        entry_boundary="external",
        upstream_span_id="abcdef1234567890",
    )
    token = observability.set_context(ctx)
    try:
        monkeypatch.setattr(toolbox.aiohttp, "ClientSession", _Session)
        monkeypatch.setattr(evidence, "submit_events", fake_submit)
        _, params = toolbox._args_model("query_object_instance", _QUERY_TOOL_INFO["metadata"])
        out = asyncio.run(
            toolbox._execute(
                "b-1",
                "t-2",
                "query_object_instance",
                "POST",
                {"kn_id": "kn1", "ot_id": "ot1", "limit": 5},
                params,
                "u-9",
                "user",
            )
        )
    finally:
        observability.reset_context(token)

    assert "客户A风险升高" in out
    assert [event["event_type"] for event in submitted] == ["tool.called", "tool.result.observed"]
    serialized = str(submitted)
    assert "kn1" not in serialized
    assert "ot1" not in serialized
    assert "客户A风险升高" not in serialized
    assert submitted[0]["payload"]["args_hash"].startswith("sha256:")
    assert submitted[1]["payload"]["result_hash"].startswith("sha256:")


def test_build_tool_survives_bad_args_schema(monkeypatch):
    """单个工具元数据坏不应连累整箱：跳过并告警，不抛。"""

    def boom(name, metadata):
        raise ValueError("bad schema")

    monkeypatch.setattr(toolbox, "_args_model", boom)
    assert toolbox._build_tool("b-1", _TOOL_INFO, "u", "user") is None


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


def test_mcp_connections_propagate_trace_context():
    """E2E-1 回归：外部 MCP 工具连接也必须保留 bkn-agent 当前 trace context。"""
    traceparent = "00-fedcba0987654321fedcba0987654321-0123456789abcdef-01"
    ctx = observability.TraceContext(
        trace_id="fedcba0987654321fedcba0987654321",
        request_id="req_mcp_trace_001",
        traceparent=traceparent,
        entry_boundary="external",
        upstream_span_id="0123456789abcdef",
    )
    token = observability.set_context(ctx)
    try:
        conns = _mcp_connections(
            [{"type": "mcp", "name": "openbkn-mcp", "url": "http://mcp.example/sse"}],
            "u-9",
            "user",
        )
    finally:
        observability.reset_context(token)

    headers = conns["openbkn-mcp"]["headers"]
    assert headers["x-account-id"] == "u-9"
    assert headers["traceparent"] == traceparent
    assert headers["bkn-request-id"] == "req_mcp_trace_001"
    assert headers["x-request-id"] == "req_mcp_trace_001"
    assert headers["x-trace-id"] == "fedcba0987654321fedcba0987654321"


def test_explicit_box_error_is_classified(monkeypatch):
    """P1 回归：坏 box_id（工厂 4xx）= 调用方配置错 → 400 封套；
    工厂 5xx/网络 → 502。老实现一律裸 RuntimeError → 无封套 500。"""
    from fastapi import HTTPException

    class _Resp:
        def __init__(self, status, body):
            self.status = status
            self._body = body

        async def text(self):
            return self._body

        async def __aenter__(self):
            return self

        async def __aexit__(self, *a):
            return False

    class _Session:
        status = 400
        body = '{"code":"...ToolBoxNotFound"}'

        def __init__(self, *a, **k):
            pass

        def get(self, url, params=None, headers=None):
            return _Resp(type(self).status, type(self).body)

        async def __aenter__(self):
            return self

        async def __aexit__(self, *a):
            return False

    monkeypatch.setattr(toolbox.aiohttp, "ClientSession", _Session)
    with pytest.raises(HTTPException) as e:
        asyncio.run(toolbox._list_tools("bad-box", "u", "user"))
    assert e.value.status_code == 400 and "BoxUnavailable" in e.value.detail["code"]

    _Session.status = 503
    _Session.body = "upstream down"
    with pytest.raises(HTTPException) as e2:
        asyncio.run(toolbox._list_tools("b-1", "u", "user"))
    assert e2.value.status_code == 502 and "Upstream" in e2.value.detail["code"]


_OPENAI_NAME_RE = __import__("re").compile(r"^[a-zA-Z0-9_-]{1,64}$")


def test_agent_as_tool_name_is_openai_legal():
    # Chinese agent name: must not leak non-ASCII into the tool name (the bug —
    # `agent_语义理解` 400s the model every call). The derived form keeps the
    # ASCII `agent_` prefix, so it stays legal without hitting the id fallback.
    n = _derive_agent_tool_name({}, "语义理解", "abcdef123456")
    assert _OPENAI_NAME_RE.match(n), n
    assert "语" not in n

    # Over-long name is truncated to 64.
    assert len(_derive_agent_tool_name({}, "a" * 200, "x")) == 64

    # An ASCII agent name stays readable.
    assert _derive_agent_tool_name({}, "translator", "x") == "agent_translator"

    # An explicit AgentToolRef.name wins but is still sanitized/bounded; an
    # all-non-ASCII explicit name (no `agent_` prefix) hits the id fallback.
    assert _derive_agent_tool_name({"name": "我的工具"}, "translator", "abcdef1234") == "tool_abcdef12"
    assert _derive_agent_tool_name({"name": "my_tool"}, "translator", "x") == "my_tool"
    assert _OPENAI_NAME_RE.match(_derive_agent_tool_name({"name": "工具" * 40}, "x", "abcdef1234"))
