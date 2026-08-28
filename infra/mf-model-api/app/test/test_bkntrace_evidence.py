import asyncio
import hashlib
import json
import importlib
import sys
from pathlib import Path
from unittest.mock import Mock, patch

from app.utils.bkntrace import evidence


def load_llm_utils_with_stubs():
    sys.modules.pop("app.utils.llm_utils", None)
    fake_logics = Mock()
    fake_logics.AddModelUsedAudit = Mock()
    fake_audit = Mock()
    fake_audit.add_llm_model_call_log = Mock()
    fake_dao = Mock()
    fake_dao.llm_model_dao = Mock()
    with patch.dict(sys.modules, {
        "app.interfaces": Mock(logics=fake_logics),
        "app.interfaces.logics": fake_logics,
        "app.controller.model_audit_controller": fake_audit,
        "app.dao.llm_model_dao": fake_dao,
    }):
        return importlib.import_module("app.utils.llm_utils")


def test_build_model_call_events_hashes_prompt_output_and_preserves_context():
    ctx = evidence.build_request_context({
        "traceparent": "00-81230000000000000000000000000001-8123000000000001-01",
        "bkn-request-id": "req_model_call_0001",
        "x-account-id": "acct_demo",
        "x-account-type": "user",
        "x-tenant-id": "tenant_demo",
        "bkn-interaction-id": "interaction_model_001",
        "bkn-operation-id": "operation_model_001",
        "bkn-causation-event-id": "event_tool_called_001",
        "bkn-attempt": "1",
        "bkn-event-observed-at": "2026-07-25T08:00:00Z",
    })

    events = evidence.build_model_call_events(
        ctx,
        model_id="model_123",
        model_name="gpt-demo",
        model_provider="openai",
        operation="model.chat.completions",
        messages=[{"role": "user", "content": "客户张三的手机号是多少？"}],
        params={"temperature": 0.1, "max_tokens": 256, "tools": [{"function": {"name": "lookup_customer"}}]},
        status="success",
        input_token_count=12,
        output_token_count=8,
        output={"choices": [{"message": {"content": "不能展示手机号"}}]},
    )

    encoded = json.dumps(events, ensure_ascii=False)
    assert len(events) == 1
    assert events[0]["trace_id"] == "81230000000000000000000000000001"
    assert events[0]["span_id"] == "8123000000000001"
    assert events[0]["bkn.request.id"] == "req_model_call_0001"
    assert ctx["tenant_id"] == "tenant_demo"
    assert events[0]["event_type"] == "model.call.observed"
    assert events[0]["interaction_id"] == "interaction_model_001"
    assert events[0]["operation_id"] == "operation_model_001"
    assert events[0]["causation_event_id"] == "event_tool_called_001"
    assert events[0]["attempt"] == 1
    assert "客户张三" not in encoded
    assert "手机号" not in encoded
    assert "不能展示手机号" not in encoded
    assert "lookup_customer" not in encoded
    assert "prompt_hash" in encoded
    assert "output_hash" in encoded
    assert "input_token_count" in encoded
    assert "output_token_count" in encoded
    assert "input_unit_count" not in encoded
    assert "prompt_tokens" not in encoded
    assert '"ref_type": "model_ref"' not in encoded
    assert "claim.created" not in encoded
    assert "evidence.refs.created" not in encoded
    assert set(events[0]["payload"]) == {
        "model_name",
        "model_provider",
        "status",
        "input_token_count",
        "output_token_count",
        "prompt_hash",
        "output_hash",
    }


def test_model_event_requires_business_causality_context():
    ctx = evidence.build_request_context({
        "traceparent": "00-81230000000000000000000000000001-8123000000000001-01",
        "bkn-request-id": "req_model_call_0002",
        "x-account-id": "acct_demo",
    })

    events = evidence.build_model_call_events(
        ctx,
        model_id="model_123",
        model_name="gpt-demo",
        model_provider="openai",
        operation="model.chat.completions",
        messages=[],
        params={},
        status="success",
    )

    assert events == []


def test_failed_model_event_contains_only_safe_error_hash_and_category():
    ctx = evidence.build_request_context({
        "traceparent": "00-81230000000000000000000000000001-8123000000000001-01",
        "bkn-request-id": "req_model_call_0003",
        "x-account-id": "acct_demo",
        "bkn-interaction-id": "interaction_model_003",
        "bkn-operation-id": "operation_model_003",
        "bkn-causation-event-id": "event_tool_called_003",
        "bkn-event-observed-at": "2026-07-25T08:00:00Z",
    })

    events = evidence.build_model_call_events(
        ctx,
        model_id="model_123",
        model_name="gpt-demo",
        model_provider="openai",
        operation="model.chat.completions",
        messages=[{"role": "user", "content": "private prompt"}],
        params={},
        status="failed",
        error_category="dependency_error",
    )

    payload = events[0]["payload"]
    assert payload["status"] == "error"
    assert payload["error_category"] == "dependency_error"
    assert payload["error_hash"].startswith("sha256:")
    assert "private prompt" not in json.dumps(events)


def test_model_receipt_uses_canonical_event_header_and_bounded_candidates():
    ctx = evidence.build_request_context({
        "traceparent": "00-81230000000000000000000000000001-8123000000000001-01",
        "bkn-request-id": "req_model_call_0004",
        "x-account-id": "acct_demo",
        "x-account-type": "user",
        "bkn-interaction-id": "interaction_model_004",
        "bkn-operation-id": "operation_model_004",
        "bkn-causation-event-id": "event_tool_called_004",
        "bkn-event-observed-at": "2026-07-25T08:00:00Z",
        "bkn-candidate-source-event-ids": json.dumps([
            "evt_tool_1",
            "not valid",
            "evt_tool_2",
            "evt_tool_1",
        ]),
    })

    headers = evidence.model_receipt_headers(ctx)

    assert headers == {
        "bkn-evidence-event-id": (
            "evt_" + hashlib.sha256(
                b"81230000000000000000000000000001|operation_model_004|"
                b"model.call.observed|1"
            ).hexdigest()
        ),
        "bkn-adopted-source-event-ids": '["evt_tool_1","evt_tool_2"]',
    }
    assert "bkn-fact-event-id" not in headers


def test_private_llm_route_forwards_trace_headers():
    source = (
        Path(__file__).parents[1] / "routers" / "private_route.py"
    ).read_text(encoding="utf-8")

    assert (
        "used_model_openai(request.dict(), userId, language, func_module, dict(headers))"
        in source
    )


def test_model_event_id_is_stable_for_same_operation_attempt():
    ctx = evidence.build_request_context({
        "traceparent": "00-81230000000000000000000000000001-8123000000000001-01",
        "bkn-request-id": "req_model_call_0004",
        "bkn-interaction-id": "interaction_model_004",
        "bkn-operation-id": "operation_model_004",
        "bkn-causation-event-id": "event_tool_called_004",
        "bkn-attempt": "2",
        "bkn-event-observed-at": "2026-07-25T08:00:00Z",
    })

    first = evidence.build_model_call_events(
        ctx,
        model_id="model_123",
        model_name="gpt-demo",
        model_provider="openai",
        operation="model.chat.completions",
        messages=[{"role": "user", "content": "first delivery"}],
        params={},
        status="success",
        output="first response",
    )
    replay = evidence.build_model_call_events(
        ctx,
        model_id="model_123",
        model_name="gpt-demo",
        model_provider="openai",
        operation="model.chat.completions",
        messages=[{"role": "user", "content": "first delivery"}],
        params={},
        status="success",
        output="first response",
    )

    assert first == replay
    assert first[0]["attempt"] == 2
    identity = "|".join([
        "81230000000000000000000000000001",
        "operation_model_004",
        "model.call.observed",
        "2",
    ])
    assert first[0]["event_id"] == "evt_" + hashlib.sha256(identity.encode("utf-8")).hexdigest()


def test_build_request_context_generates_safe_defaults_for_missing_headers():
    ctx = evidence.build_request_context({}, account_id="user1")

    assert len(ctx["trace_id"]) == 32
    assert len(ctx["span_id"]) == 16
    assert ctx["traceparent"].startswith("00-")
    assert ctx["request_id"].startswith("req_")
    assert ctx["account_id"] == "user1"
    assert ctx["observed_at"] == ""


def test_model_event_requires_propagated_observed_time():
    ctx = evidence.build_request_context({
        "traceparent": "00-81230000000000000000000000000001-8123000000000001-01",
        "bkn-request-id": "req_model_call_0005",
        "bkn-interaction-id": "interaction_model_005",
        "bkn-operation-id": "operation_model_005",
        "bkn-causation-event-id": "event_tool_called_005",
    })
    assert evidence.build_model_call_events(
        ctx, model_id="model_123", model_name="gpt-demo", model_provider="openai",
        operation="model.chat.completions", messages=[], params={}, status="success",
    ) == []


def test_post_batch_retries_non_2xx(monkeypatch):
    calls = []

    async def post_once(_url, _payload, _timeout):
        calls.append(1)
        if len(calls) < 3:
            raise evidence.EvidenceIngestError("HTTP 503")

    monkeypatch.setattr(evidence, "_post_once", post_once)
    asyncio.run(evidence._post_batch("http://trace.local", {"events": []}))
    assert len(calls) == 3


def test_evidence_ingest_headers_use_dedicated_token(monkeypatch):
    monkeypatch.setenv(evidence.EVIDENCE_INGEST_TOKEN_ENV, "producer-token")
    assert evidence._ingest_headers() == {
        "X-BKN-Trace-Ingest-Token": "producer-token",
        "Accept-Language": "zh-CN",
    }


def test_evidence_ingest_headers_omit_empty_token(monkeypatch):
    monkeypatch.delenv(evidence.EVIDENCE_INGEST_TOKEN_ENV, raising=False)
    assert evidence._ingest_headers() == {"Accept-Language": "zh-CN"}


def test_emit_model_call_events_keeps_background_task_reference(monkeypatch):
    monkeypatch.setenv(evidence.EVIDENCE_INGEST_URL_ENV, "http://bkn-trace.local/evidence")
    evidence._background_tasks.clear()
    task = Mock()
    task.exception.return_value = None
    task.add_done_callback = Mock(side_effect=lambda callback: callback(task))

    with patch("asyncio.get_running_loop") as get_loop, patch.object(evidence, "_post_batch", Mock(return_value=object())):
        get_loop.return_value.create_task.return_value = task
        evidence.emit_model_call_events({"trace_id": "t", "request_id": "req_1"}, [{"event_type": "claim.created"}])

    get_loop.return_value.create_task.assert_called_once()
    task.add_done_callback.assert_called_once()
    assert task not in evidence._background_tasks


def test_emit_bkn_trace_evidence_is_fail_open(monkeypatch):
    monkeypatch.setenv(evidence.EVIDENCE_INGEST_URL_ENV, "http://bkn-trace.local/evidence")
    llm_utils = load_llm_utils_with_stubs()

    client = llm_utils.OpenAIClientRequest(
        api_url="http://example.com/",
        api_model="gpt-demo",
        api_key="secret",
        model_id="model_123",
        temperature=0.1,
        top_p=0.9,
        frequency_penalty=0,
        presence_penalty=0,
        max_tokens=128,
    )
    client.trace_context = evidence.build_request_context({})

    with patch.object(llm_utils.bkntrace_evidence, "build_model_call_events", side_effect=RuntimeError("boom")):
        client._emit_bkn_trace_evidence(
            messages=[{"role": "user", "content": "private"}],
            params={"temperature": 0.1},
            status="success",
        )


def test_all_actual_model_providers_share_trace_emitter():
    llm_utils = load_llm_utils_with_stubs()
    for provider in (
        llm_utils.OpenAIClientRequest,
        llm_utils.BaiduClient,
        llm_utils.BaiduTianchenClient,
        llm_utils.OtherClient,
        llm_utils.ClaudeClient,
    ):
        assert issubclass(provider, llm_utils.BKNTraceModelMixin)


def test_trace_model_stream_emits_success_and_preserves_chunks():
    llm_utils = load_llm_utils_with_stubs()
    client = Mock()

    async def stream():
        yield "first"
        yield "second"

    async def consume():
        return [chunk async for chunk in llm_utils.trace_model_stream(client, stream(), [], {})]

    assert asyncio.run(consume()) == ["first", "second"]
    client._emit_bkn_trace_evidence.assert_called_once()
    assert client._emit_bkn_trace_evidence.call_args.kwargs["status"] == "success"


def test_trace_model_stream_emits_before_terminal_chunk_is_consumed():
    llm_utils = load_llm_utils_with_stubs()
    client = Mock()

    async def stream():
        yield "first"
        yield "[DONE]"

    async def consume_until_terminal_then_close():
        wrapped = llm_utils.trace_model_stream(client, stream(), [], {})
        assert await anext(wrapped) == "first"
        terminal = await anext(wrapped)
        client._emit_bkn_trace_evidence.assert_called_once()
        await wrapped.aclose()
        return terminal

    assert asyncio.run(consume_until_terminal_then_close()) == "[DONE]"


def test_stream_terminal_emits_model_fact_before_client_can_close():
    llm_utils = load_llm_utils_with_stubs()
    client = Mock()

    async def consume_terminal_then_close():
        stream = llm_utils.emit_model_fact_before_terminal(
            client,
            messages=[{"role": "user", "content": "private"}],
            params={"stream": True},
            input_token_count=8,
            output_token_count=5,
            output_hash_source="answer",
        )
        terminal = await anext(stream)
        client._emit_bkn_trace_evidence.assert_called_once()
        await stream.aclose()
        return terminal

    assert asyncio.run(consume_terminal_then_close()) == "[DONE]"
    assert client._emit_bkn_trace_evidence.call_args.kwargs["status"] == "success"
