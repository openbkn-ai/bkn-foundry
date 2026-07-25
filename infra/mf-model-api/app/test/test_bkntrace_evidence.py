import json

from app.utils.bkntrace import evidence


def test_build_model_call_events_hashes_prompt_output_and_preserves_context():
    ctx = evidence.build_request_context({
        "traceparent": "00-81230000000000000000000000000001-8123000000000001-01",
        "bkn-request-id": "req_model_call_0001",
        "x-account-id": "acct_demo",
        "x-account-type": "user",
        "x-business-domain": "domain_demo",
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
    assert len(events) == 2
    assert events[0]["trace_id"] == "81230000000000000000000000000001"
    assert events[0]["span_id"] == "8123000000000001"
    assert events[0]["bkn.request.id"] == "req_model_call_0001"
    assert events[0]["event_type"] == "claim.created"
    assert events[1]["event_type"] == "evidence.refs.created"
    assert "客户张三" not in encoded
    assert "手机号" not in encoded
    assert "不能展示手机号" not in encoded
    assert "lookup_customer" not in encoded
    assert "prompt_hash" in encoded
    assert "output_hash" in encoded
    assert "input_unit_count" in encoded
    assert "input_token_count" not in encoded
    assert "prompt_tokens" not in encoded
    assert '"ref_type": "model_ref"' not in encoded


def test_build_request_context_generates_safe_defaults_for_missing_headers():
    ctx = evidence.build_request_context({}, account_id="user1")

    assert len(ctx["trace_id"]) == 32
    assert len(ctx["span_id"]) == 16
    assert ctx["traceparent"].startswith("00-")
    assert ctx["request_id"].startswith("req_")
    assert ctx["account_id"] == "user1"
