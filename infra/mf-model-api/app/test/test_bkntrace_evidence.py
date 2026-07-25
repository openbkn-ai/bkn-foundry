import json
import importlib
import sys
from unittest.mock import Mock, patch

from app.utils.bkntrace import evidence


def test_build_model_call_events_hashes_prompt_output_and_preserves_context():
    ctx = evidence.build_request_context({
        "traceparent": "00-81230000000000000000000000000001-8123000000000001-01",
        "bkn-request-id": "req_model_call_0001",
        "x-account-id": "acct_demo",
        "x-account-type": "user",
        "x-business-domain": "domain_demo",
        "bkn-interaction-id": "interaction_model_001",
        "bkn-operation-id": "operation_model_001",
        "bkn-causation-event-id": "event_tool_called_001",
        "bkn-attempt": "1",
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
        "x-business-domain": "domain_demo",
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
        "x-business-domain": "domain_demo",
        "bkn-interaction-id": "interaction_model_003",
        "bkn-operation-id": "operation_model_003",
        "bkn-causation-event-id": "event_tool_called_003",
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


def test_model_event_id_is_stable_for_same_operation_attempt():
    ctx = evidence.build_request_context({
        "traceparent": "00-81230000000000000000000000000001-8123000000000001-01",
        "bkn-request-id": "req_model_call_0004",
        "bkn-interaction-id": "interaction_model_004",
        "bkn-operation-id": "operation_model_004",
        "bkn-causation-event-id": "event_tool_called_004",
        "bkn-attempt": "2",
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


def test_build_request_context_generates_safe_defaults_for_missing_headers():
    ctx = evidence.build_request_context({}, account_id="user1")

    assert len(ctx["trace_id"]) == 32
    assert len(ctx["span_id"]) == 16
    assert ctx["traceparent"].startswith("00-")
    assert ctx["request_id"].startswith("req_")
    assert ctx["account_id"] == "user1"


def test_emit_model_call_events_keeps_background_task_reference(monkeypatch):
    monkeypatch.setenv(evidence.EVIDENCE_INGEST_URL_ENV, "http://bkn-trace.local/evidence")
    evidence._background_tasks.clear()
    task = Mock()
    task.add_done_callback = Mock(side_effect=lambda callback: callback(task))

    with patch("asyncio.get_running_loop") as get_loop, patch.object(evidence, "_post_batch", Mock(return_value=object())):
        get_loop.return_value.create_task.return_value = task
        evidence.emit_model_call_events({"trace_id": "t", "request_id": "req_1"}, [{"event_type": "claim.created"}])

    get_loop.return_value.create_task.assert_called_once()
    task.add_done_callback.assert_called_once()
    assert task not in evidence._background_tasks


def test_emit_bkn_trace_evidence_is_fail_open(monkeypatch):
    monkeypatch.setenv(evidence.EVIDENCE_INGEST_URL_ENV, "http://bkn-trace.local/evidence")
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
        llm_utils = importlib.import_module("app.utils.llm_utils")

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
