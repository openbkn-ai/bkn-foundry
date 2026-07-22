import asyncio

from app import evidence, observability


def _ctx():
    return observability.TraceContext(
        trace_id="1234567890abcdef1234567890abcdef",
        request_id="req_evidence_001",
        traceparent="00-1234567890abcdef1234567890abcdef-1234567890abcdef-01",
        entry_boundary="external",
        upstream_span_id="1234567890abcdef",
    )


def test_claim_batch_uses_phase_two_contract_without_raw_answer():
    token = observability.set_context(_ctx())
    try:
        answer = "客户 A 的风险上升，因为近 7 天投诉增加。"
        cid = evidence.claim_id("answer", "thread-1", answer)
        event = evidence.claim_created(
            claim_id_value=cid,
            claim_type="answer",
            claim_hash=evidence.hash_value(answer),
            operation_name="bkn.agent.chat",
            subject_refs={"agent_id": "agent-1", "thread_id": "thread-1"},
            partial_reason=["source_refs_pending"],
        )
        batch = evidence.build_batch([event], "acct-1", "user")
    finally:
        observability.reset_context(token)

    assert batch["bkn.trace.schema.version"] == "2.0.0"
    assert batch["trace"]["bkn.request.id"] == "req_evidence_001"
    assert batch["events"][0]["event_type"] == "claim.created"
    assert batch["events"][0]["payload"]["claim_id"] == cid
    assert batch["events"][0]["payload"]["claim_hash"].startswith("sha256:")
    assert answer not in str(batch)


def test_structured_output_event_records_validation_path():
    token = observability.set_context(_ctx())
    try:
        event = evidence.structured_output_validated(
            claim_id_value="claim_1",
            schema_hash_value="sha256:schema",
            validation_path="fallback",
            valid=True,
            operation_name="bkn.agent.structured_output",
        )
    finally:
        observability.reset_context(token)

    assert event["event_type"] == "structured_output.validated"
    assert event["payload"]["validation_path"] == "fallback"
    assert event["payload"]["validation_result"] == "valid"


def test_submit_events_is_noop_when_endpoint_unset(monkeypatch):
    token = observability.set_context(_ctx())
    try:
        event = evidence.tool_budget_exhausted(
            max_tool_calls=1,
            operation_name="bkn.agent.tool.call",
            tool_name="search_schema",
        )
        monkeypatch.setattr(evidence.config, "BKN_TRACE_EVIDENCE_INGEST_URL", "")
        asyncio.run(evidence.submit_events([event], "acct-1", "user"))
    finally:
        observability.reset_context(token)
