import asyncio

from langchain_core.messages import ToolMessage

from app import evidence, observability
from app.core import runner
from app.models import AgentOut


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


def test_submit_events_schedules_background_send(monkeypatch):
    sent = []
    release = asyncio.Event()

    async def fake_send(batch):
        sent.append(batch)
        await release.wait()

    token = observability.set_context(_ctx())
    try:
        event = evidence.claim_created(
            claim_id_value="claim_1",
            claim_type="answer",
            claim_hash="sha256:answer",
            operation_name="bkn.agent.chat",
            partial_reason=["source_refs_pending"],
        )
        monkeypatch.setattr(evidence.config, "BKN_TRACE_EVIDENCE_INGEST_URL", "http://bkn-trace.local/events")
        monkeypatch.setattr(evidence, "_send_batch", fake_send)

        async def drive():
            await evidence.submit_events([event], "acct-1", "user")
            assert len(evidence._background) == 1
            await asyncio.sleep(0)
            assert sent[0]["trace"]["bkn.request.id"] == "req_evidence_001"
            release.set()
            await asyncio.sleep(0)

        asyncio.run(drive())
    finally:
        observability.reset_context(token)


def test_extract_business_refs_from_structured_tool_outputs_without_raw_rows():
    refs = evidence.extract_business_refs_from_tool_outputs(
        [
            {
                "tool_name": "query_object_instance",
                "content": {
                    "kn_id": "supplychain_hd0202",
                    "object_type": "forecast_order",
                    "rows": [{"customer_name": "客户A", "amount": 100}],
                    "resource_id": "res_forecast_001",
                },
            }
        ]
    )

    serialized = str(refs)
    assert {ref["ref_type"] for ref in refs} >= {"object", "data"}
    assert "supplychain_hd0202" in serialized
    assert "forecast_order" in serialized
    assert "res_forecast_001" in serialized
    assert "客户A" not in serialized
    assert "amount" not in serialized
    assert all(ref["summary_hash"].startswith("sha256:") for ref in refs)
    assert all(ref["resolver_status"] == "unresolved" for ref in refs)


def test_task_evidence_attaches_business_refs_to_claim(monkeypatch):
    submitted = []

    async def fake_submit(events, account_id, account_type):
        submitted.extend(events)

    agent = AgentOut(
        name="agent",
        mode="task",
        model="",
        tools=[],
        skills=[],
        status="published",
        agent_id="agent-1",
        create_user="u",
        update_user="u",
        create_time=0,
        update_time=0,
    )
    token = observability.set_context(_ctx())
    try:
        monkeypatch.setattr(runner.evidence, "submit_events", fake_submit)
        asyncio.run(
            runner._emit_task_evidence(
                agent=agent,
                task_id="task-1",
                prompt_source="default",
                prompt_version="1",
                account_id="acct-1",
                account_type="user",
                output="建议查看预测单。",
                claim_type="answer",
                response_format=None,
                structured_validation_path=None,
                result_messages=[
                    ToolMessage(
                        content='{"kn_id":"supplychain_hd0202","object_type":"forecast_order","resource_id":"res_forecast_001"}',
                        tool_call_id="call-1",
                        name="query_object_instance",
                    )
                ],
            )
        )
    finally:
        observability.reset_context(token)

    event_types = [event["event_type"] for event in submitted]
    assert event_types == ["claim.created", "evidence.refs.created", "business.refs.resolved"]
    business_event = submitted[2]
    assert business_event["payload"]["claim_id"] == submitted[0]["payload"]["claim_id"]
    assert {ref["ref_type"] for ref in business_event["payload"]["business_refs"]} >= {"object", "data"}
