"""执行工厂 toolbox 工具装载（工具面收敛，替代 agent-retrieval 专用 MCP 通道）。"""

import asyncio
import hashlib

import pytest
from langchain_core.messages import ToolMessage
from langchain_mcp_adapters.interceptors import MCPToolCallRequest

from app import evidence, observability
from app.core import toolbox
from app.core.tools import (
    _derive_agent_tool_name,
    _mcp_connections,
    _toolbox_tools,
    _trace_mcp_call,
    load_tools,
)
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
                {
                    "name": "kn_id",
                    "in": "query",
                    "required": True,
                    "schema": {"type": "string"},
                },
                {
                    "name": "ot_id",
                    "in": "query",
                    "required": True,
                    "schema": {"type": "string"},
                },
                {
                    "name": "x-account-id",
                    "in": "header",
                    "required": False,
                    "schema": {"type": "string"},
                },
            ],
            "request_body": {
                "content": {
                    "application/json": {
                        "schema": {"$ref": "#/components/schemas/QueryReq"}
                    }
                }
            },
            "components": {
                "schemas": {
                    "QueryReq": {
                        "type": "object",
                        "properties": {
                            "limit": {"type": "integer", "description": "条数"}
                        },
                    }
                }
            },
        },
    },
}


def test_mcp_interceptor_injects_operation_headers_at_call_time(monkeypatch):
    monkeypatch.setattr(
        observability,
        "outbound_headers",
        lambda: {
            "bkn-interaction-id": "interaction-1",
            "bkn-operation-id": "operation-1",
            "bkn-causation-event-id": "event-1",
        },
    )
    request = MCPToolCallRequest(
        name="search",
        args={"query": "redacted"},
        server_name="test",
        headers={"x-account-id": "account-1"},
        runtime=None,
    )

    async def handler(actual):
        return actual

    actual = asyncio.run(_trace_mcp_call(request, handler))

    assert actual.headers == {
        "x-account-id": "account-1",
        "bkn-interaction-id": "interaction-1",
        "bkn-operation-id": "operation-1",
        "bkn-causation-event-id": "event-1",
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
    model, params = toolbox._args_model(
        "query_object_instance", _QUERY_TOOL_INFO["metadata"]
    )
    fields = model.model_fields
    assert fields["kn_id"].is_required() and fields["ot_id"].is_required()
    assert (
        "limit" in fields and not fields["limit"].is_required()
    )  # $ref body schema 解析
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

    async def fake_execute(
        box_id,
        tool_id,
        tool_name,
        method,
        args,
        params,
        account_id,
        account_type,
        expected_fact_event_type=None,
    ):
        captured.update(
            box=box_id,
            tool=tool_id,
            tool_name=tool_name,
            method=method,
            args=args,
            aid=account_id,
        )
        return "ok"

    monkeypatch.setattr(toolbox, "_execute", fake_execute)
    tool = toolbox._build_tool("b-1", _TOOL_INFO, "u-9", "user")
    out = asyncio.run(tool.coroutine(query="q", limit=None))
    assert out == "ok"
    assert captured["box"] == "b-1" and captured["tool"] == "t-1"
    assert captured["tool_name"] == "list_knowledge_networks"
    assert captured["aid"] == "u-9"


def test_build_context_loader_retrieval_tool_declares_expected_fact_event(monkeypatch):
    captured = {}
    search_schema_info = {
        **_TOOL_INFO,
        "tool_id": "tool-search-schema",
        "name": "search_schema",
        "metadata": {
            **_TOOL_INFO["metadata"],
            "path": "/api/agent-retrieval/in/v1/kn/search_schema",
        },
    }

    async def fake_execute(
        box_id,
        tool_id,
        tool_name,
        method,
        args,
        params,
        account_id,
        account_type,
        expected_fact_event_type=None,
    ):
        captured["expected_fact_event_type"] = expected_fact_event_type
        return "ok"

    monkeypatch.setattr(toolbox, "_execute", fake_execute)

    tool = toolbox._build_tool(
        "box-context-loader", search_schema_info, "account-1", "user"
    )
    result = asyncio.run(tool.coroutine(query="采购履约风险"))

    assert result == "ok"
    assert captured["expected_fact_event_type"] == "retrieval.completed"


def test_external_tool_with_context_loader_path_does_not_declare_fact_event():
    metadata = {
        "server_url": "https://tools.example.com",
        "path": "/api/agent-retrieval/in/v1/kn/search_schema",
    }

    assert toolbox._expected_fact_event_type("external-box", metadata) is None


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
    _, params = toolbox._args_model(
        "query_object_instance", _QUERY_TOOL_INFO["metadata"]
    )
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
        _, params = toolbox._args_model(
            "query_object_instance", _QUERY_TOOL_INFO["metadata"]
        )
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
        _, params = toolbox._args_model(
            "query_object_instance", _QUERY_TOOL_INFO["metadata"]
        )
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
    assert [event["event_type"] for event in submitted] == [
        "tool.called",
        "tool.result.observed",
    ]
    serialized = str(submitted)
    assert "kn1" not in serialized
    assert "ot1" not in serialized
    assert "客户A风险升高" not in serialized
    assert submitted[0]["payload"]["args_hash"].startswith("sha256:")
    assert submitted[1]["payload"]["result_hash"].startswith("sha256:")


def test_execute_assigns_operation_and_propagates_causality_headers(monkeypatch):
    submitted = []
    sent = {}

    class _Resp:
        status = 200

        async def text(self):
            return '{"status_code":200,"body":{"resource_id":"res-1"}}'

        async def __aenter__(self):
            return self

        async def __aexit__(self, *args):
            return False

    class _Session:
        def __init__(self, *args, **kwargs):
            pass

        async def __aenter__(self):
            return self

        async def __aexit__(self, *args):
            return False

        def post(self, url, json=None, headers=None):
            sent.update(headers=headers, payload=json)
            return _Resp()

    async def fake_submit(events, account_id, account_type):
        submitted.extend(events)

    token = observability.set_context(
        observability.TraceContext(
            trace_id="1234567890abcdef1234567890abcdef",
            request_id="req_toolbox_causality_001",
            traceparent="00-1234567890abcdef1234567890abcdef-abcdef1234567890-01",
            entry_boundary="external",
            upstream_span_id="abcdef1234567890",
        )
    )
    interaction_token = evidence.begin_interaction(
        "hello", "task", "agent-1", "bkn.agent.task"
    )
    try:
        monkeypatch.setattr(toolbox.aiohttp, "ClientSession", _Session)
        monkeypatch.setattr(evidence, "submit_events", fake_submit)
        asyncio.run(
            toolbox._execute(
                "box-1", "tool-1", "monitor", "POST", {}, [], "acct", "user"
            )
        )
    finally:
        evidence.end_interaction(interaction_token)
        observability.reset_context(token)

    called, result = submitted
    assert called["event_type"] == "tool.called"
    assert result["event_type"] == "tool.result.observed"
    assert called["operation_id"] == result["operation_id"]
    assert result["causation_event_id"] == called["event_id"]
    assert sent["headers"]["bkn-interaction-id"] == called["interaction_id"]
    assert sent["headers"]["bkn-operation-id"] == called["operation_id"]
    assert sent["headers"]["bkn-causation-event-id"] == called["event_id"]
    assert "bkn-claim-id" not in sent["headers"]
    assert not any(key.startswith("bkn-action-") for key in sent["headers"])


def test_context_loader_search_without_receipt_links_real_retrieval_event_to_claim_sources(
    monkeypatch,
):
    submitted = []

    class _Resp:
        status = 200

        async def text(self):
            return (
                '{"status_code":200,"headers":{},'
                '"body":{"object_types":[{"concept_id":"purchase_order"}],'
                '"relation_types":[{"concept_id":"contains_material"}]}}'
            )

        async def __aenter__(self):
            return self

        async def __aexit__(self, *args):
            return False

    class _Session:
        def __init__(self, *args, **kwargs):
            pass

        async def __aenter__(self):
            return self

        async def __aexit__(self, *args):
            return False

        def post(self, url, json=None, headers=None):
            return _Resp()

    async def fake_submit(events, account_id, account_type):
        submitted.extend(events)
        return True

    ctx = observability.TraceContext(
        trace_id="ce31e2d297bd4ee0b1313ca3bdcd1acf",
        request_id="req_real_retrieval_001",
        traceparent=("00-ce31e2d297bd4ee0b1313ca3bdcd1acf-abcdef1234567890-01"),
        entry_boundary="external",
        upstream_span_id="abcdef1234567890",
        tenant_id="tenant-demo",
        business_domain="bd-public",
    )
    token = observability.set_context(ctx)
    interaction_token = evidence.begin_interaction(
        "分析采购履约风险", "task", "agent-1", "bkn.agent.task"
    )
    try:
        monkeypatch.setattr(toolbox.aiohttp, "ClientSession", _Session)
        monkeypatch.setattr(evidence, "submit_events", fake_submit)
        result = asyncio.run(
            toolbox._execute(
                "box-context-loader",
                "tool-search-schema",
                "search_schema",
                "POST",
                {"kn_id": "supply-chain", "query": "采购履约风险"},
                [],
                "account-1",
                "user",
                expected_fact_event_type="retrieval.completed",
            )
        )
        operation_id = submitted[0]["operation_id"]
        expected_event_id = (
            "evt_"
            + hashlib.sha256(
                (f"{ctx.trace_id}|{operation_id}|retrieval.completed|1").encode("utf-8")
            ).hexdigest()
        )
        model_headers = evidence.model_context_headers(
            [ToolMessage(content=result, tool_call_id="call-search-schema")],
            "op-model-final",
        )
        evidence.record_model_fact(
            event_id="evt-model-final",
            operation_id="op-model-final",
            adopted_source_event_ids=[expected_event_id],
        )
        source_ids, operation_ids, evidence_refs, business_refs = (
            evidence.adopted_sources()
        )
        claim = evidence.claim_created(
            claim_id_value="claim-real-retrieval",
            claim_type="answer",
            claim_hash=evidence.hash_value("存在采购履约风险"),
            operation_name="bkn.agent.task",
            source_event_ids=source_ids,
            operation_ids=operation_ids,
            causation_event_id=source_ids[-1],
        )
    finally:
        evidence.end_interaction(interaction_token)
        observability.reset_context(token)

    assert model_headers["bkn-candidate-source-event-ids"] == (
        f'["{expected_event_id}"]'
    )
    assert source_ids == [expected_event_id, "evt-model-final"]
    assert operation_ids == [operation_id, "op-model-final"]
    assert claim["payload"]["source_event_ids"] == [
        expected_event_id,
        "evt-model-final",
    ]
    assert evidence_refs == []
    assert business_refs == []
    assert "purchase_order" not in str(evidence_refs)
    assert "contains_material" not in str(business_refs)


def test_build_tool_survives_bad_args_schema(monkeypatch):
    """单个工具元数据坏不应连累整箱：跳过并告警，不抛。"""

    def boom(name, metadata):
        raise ValueError("bad schema")

    monkeypatch.setattr(toolbox, "_args_model", boom)
    assert toolbox._build_tool("b-1", _TOOL_INFO, "u", "user") is None


def test_args_model_accepts_wire_parameters_with_leading_underscores():
    metadata = {
        "api_spec": {
            "request_body": {
                "content": {
                    "application/json": {
                        "schema": {
                            "type": "object",
                            "required": ["_action_type"],
                            "properties": {
                                "_action_type": {"type": "string"},
                                "__logic_property": {"type": "string"},
                            },
                        }
                    }
                }
            }
        }
    }

    model, params = toolbox._args_model("execute_action", metadata)
    values = model.model_validate(
        {"p__action_type": "monitor", "p___logic_property": "risk_level"}
    )

    assert values.p__action_type == "monitor"
    assert values.p___logic_property == "risk_level"
    assert [(item.field, item.wire) for item in params] == [
        ("p__action_type", "_action_type"),
        ("p___logic_property", "__logic_property"),
    ]


def test_explicit_toolbox_ref_failure_is_not_swallowed(monkeypatch):
    """显式 type=toolbox 拉不到 → 抛错。工具是定义方点名要的，静默降级等于
    让 agent 带着残缺工具面跑，比直接失败更难查。"""

    async def boom(box_id, account_id, account_type):
        raise RuntimeError("factory down")

    monkeypatch.setattr("app.core.tools.load_toolbox_tools", boom)

    with pytest.raises(RuntimeError):
        asyncio.run(
            _toolbox_tools([{"type": "toolbox", "box_id": "box-x"}], "u", "user")
        )


def test_no_declared_tools_yields_no_tools(monkeypatch):
    """零声明 = 零工具，且一次装载请求都不发。图因此不长 tools 节点，模型没有
    可空转的对象（#447）。read_skill_file 也不挂——此时它没有读取对象。

    这条锁的是产品语义：agent.tools 是工具全集，没有任何隐式挂载可言。"""

    async def boom(box_id, account_id, account_type):
        raise AssertionError("零声明时不应发起任何 toolbox 装载")

    monkeypatch.setattr("app.core.tools.load_toolbox_tools", boom)

    assert asyncio.run(load_tools([], "u", "user")) == []
    assert asyncio.run(_toolbox_tools([], "u", "user")) == []


def test_skill_reader_rides_along_but_never_alone(monkeypatch):
    """read_skill_file 三态：有技能则挂；无技能但已有其他工具则搭车挂；
    两者都无则不挂（不为它一个撑出 tools 节点）。"""

    async def one_tool(box_id, account_id, account_type):
        return [toolbox._build_tool(box_id, _TOOL_INFO, account_id, account_type)]

    monkeypatch.setattr("app.core.tools.load_toolbox_tools", one_tool)

    def names(ts):
        return [getattr(t, "name", None) for t in ts]

    with_skill = asyncio.run(load_tools([], "u", "user", skill_ids=["sk-1"]))
    assert names(with_skill) == ["read_skill_file"]

    with_box = asyncio.run(
        load_tools([{"type": "toolbox", "box_id": "box-x"}], "u", "user")
    )
    assert names(with_box) == ["list_knowledge_networks", "read_skill_file"]

    assert asyncio.run(load_tools([], "u", "user", skill_ids=[])) == []


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
    name = _derive_agent_tool_name({}, "语义理解", "abcdef123456")
    assert _OPENAI_NAME_RE.match(name), name
    assert "语" not in name
    assert len(_derive_agent_tool_name({}, "a" * 200, "x")) == 64
    assert _derive_agent_tool_name({}, "translator", "x") == "agent_translator"
    assert (
        _derive_agent_tool_name({"name": "我的工具"}, "translator", "abcdef1234")
        == "tool_abcdef12"
    )
    assert _derive_agent_tool_name({"name": "my_tool"}, "translator", "x") == "my_tool"
    assert _OPENAI_NAME_RE.match(
        _derive_agent_tool_name({"name": "工具" * 40}, "x", "abcdef1234")
    )
