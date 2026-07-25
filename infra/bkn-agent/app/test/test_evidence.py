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


def test_claim_batch_uses_2_1_contract_without_raw_answer():
    token = observability.set_context(_ctx())
    interaction_token = evidence.begin_interaction("question", "chat", "agent-1", "bkn.agent.chat")
    try:
        answer = "客户 A 的风险上升，因为近 7 天投诉增加。"
        cid = evidence.claim_id("answer", "thread-1", answer)
        event = evidence.claim_created(
            claim_id_value=cid,
            claim_type="answer",
            claim_hash=evidence.hash_value(answer),
            operation_name="bkn.agent.chat",
            source_event_ids=["evt-result-1"],
            operation_ids=["op-1"],
            causation_event_id="evt-result-1",
        )
        batch = evidence.build_batch([event], "acct-1", "user")
    finally:
        evidence.end_interaction(interaction_token)
        observability.reset_context(token)

    assert batch["bkn.trace.schema.version"] == "2.1.0"
    assert batch["trace"]["bkn.request.id"] == "req_evidence_001"
    assert batch["events"][0]["event_type"] == "claim.created"
    assert batch["events"][0]["claim_id"] == cid
    assert batch["events"][0]["payload"]["claim_hash"].startswith("sha256:")
    assert answer not in str(batch)


def test_private_structured_output_event_is_not_emitted_in_2_1():
    token = observability.set_context(_ctx())
    interaction_token = evidence.begin_interaction("question", "chat", "agent-1", "bkn.agent.chat")
    try:
        event = evidence.structured_output_validated(
            claim_id_value="claim_1",
            schema_hash_value="sha256:schema",
            validation_path="fallback",
            valid=True,
            operation_name="bkn.agent.structured_output",
        )
    finally:
        evidence.end_interaction(interaction_token)
        observability.reset_context(token)

    assert event is None


def test_claim_builder_rejects_short_hash_and_empty_sources():
    token = observability.set_context(_ctx())
    interaction_token = evidence.begin_interaction("question", "task", "agent-1", "bkn.agent.task")
    try:
        short_hash = evidence.claim_created(
            claim_id_value="claim-1", claim_type="answer", claim_hash="sha256:short",
            operation_name="bkn.agent.task", source_event_ids=["evt-1"], operation_ids=["op-1"],
        )
        empty_sources = evidence.claim_created(
            claim_id_value="claim-1", claim_type="answer",
            claim_hash="sha256:" + "1" * 64, operation_name="bkn.agent.task",
            source_event_ids=[], operation_ids=[],
        )
    finally:
        evidence.end_interaction(interaction_token)
        observability.reset_context(token)

    assert short_hash is None
    assert empty_sources is None


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
    interaction_token = evidence.begin_interaction("question", "chat", "agent-1", "bkn.agent.chat")
    try:
        event = evidence.claim_created(
            claim_id_value="claim_1",
            claim_type="answer",
            claim_hash="sha256:" + "a" * 64,
            operation_name="bkn.agent.chat",
            source_event_ids=["evt-result-1"],
            operation_ids=["op-1"],
            causation_event_id="evt-result-1",
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
        evidence.end_interaction(interaction_token)
        observability.reset_context(token)


def test_submit_events_preserves_causal_order_within_interaction(monkeypatch):
    started = []
    release_first = asyncio.Event()

    async def fake_send(batch):
        started.append(batch["events"][0]["event_type"])
        if len(started) == 1:
            await release_first.wait()

    trace_token = observability.set_context(_ctx())
    interaction_token = evidence.begin_interaction("question", "task", "agent-1", "bkn.agent.task")
    monkeypatch.setattr(evidence.config, "BKN_TRACE_EVIDENCE_INGEST_URL", "http://bkn-trace.local/events")
    monkeypatch.setattr(evidence, "_send_batch", fake_send)

    async def drive():
        await evidence.submit_events([evidence.interaction_started_event()], "acct", "user")
        claim = evidence.claim_created(
            claim_id_value="claim-1",
            claim_type="answer",
            claim_hash="sha256:" + "1" * 64,
            operation_name="bkn.agent.task",
            source_event_ids=["evt-result-1"],
            operation_ids=["op-1"],
            causation_event_id="evt-result-1",
        )
        await evidence.submit_events([claim], "acct", "user")
        await asyncio.sleep(0)
        assert started == ["agent.interaction.started"]
        release_first.set()
        await asyncio.gather(*list(evidence._background))

    try:
        asyncio.run(drive())
    finally:
        evidence.end_interaction(interaction_token)
        observability.reset_context(trace_token)

    assert started == ["agent.interaction.started", "claim.created"]


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
    assert "supplychain_hd0202" not in serialized
    assert "forecast_order" not in serialized
    assert "res_forecast_001" not in serialized
    assert "客户A" not in serialized
    assert "amount" not in serialized
    assert all("resolver_status" not in ref for ref in refs)


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
    interaction_token = evidence.begin_interaction("question", "task", "agent-1", "bkn.agent.task")
    try:
        evidence.record_operation_result(
            {"event_id": "evt-result-1", "operation_id": "op-1"},
            tool_name="query_object_instance",
            content={
                "kn_id": "supplychain_hd0202",
                "object_type": "forecast_order",
                "resource_id": "res_forecast_001",
            },
        )
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
        evidence.end_interaction(interaction_token)
        observability.reset_context(token)

    event_types = [event["event_type"] for event in submitted]
    assert event_types == ["claim.created", "evidence.refs.created", "business.refs.resolved"]
    business_event = submitted[2]
    assert business_event["claim_id"] == submitted[0]["claim_id"]
    assert {ref["ref_type"] for ref in business_event["payload"]["business_refs"]} >= {"object", "data"}


def test_task_evidence_reports_unresolved_business_refs(monkeypatch):
    submitted = []

    async def fake_submit(events, account_id, account_type):
        submitted.extend(events)

    agent = AgentOut(
        name="agent", mode="task", model="", tools=[], skills=[], status="published",
        agent_id="agent-1", create_user="u", update_user="u", create_time=0, update_time=0,
    )
    token = observability.set_context(_ctx())
    interaction_token = evidence.begin_interaction("question", "task", "agent-1", "bkn.agent.task")
    try:
        evidence.record_operation_result(
            {"event_id": "evt-result-1", "operation_id": "op-1"},
            tool_name="lookup",
            content={"status": "ok"},
        )
        monkeypatch.setattr(runner.evidence, "submit_events", fake_submit)
        asyncio.run(runner._emit_task_evidence(
            agent=agent, task_id="task-1", prompt_source="default", prompt_version="1",
            account_id="acct-1", account_type="user", output="supported answer",
            claim_type="answer", response_format=None, structured_validation_path=None,
            result_messages=[],
        ))
    finally:
        evidence.end_interaction(interaction_token)
        observability.reset_context(token)

    assert [event["event_type"] for event in submitted] == [
        "claim.created", "evidence.refs.created", "business.refs.resolved",
    ]
    assert submitted[-1]["payload"] == {
        "claim_id": submitted[0]["claim_id"],
        "business_refs": [],
        "resolver_status": "unresolved",
    }


def test_2_1_interaction_and_claim_expose_only_registered_causal_fields():
    token = observability.set_context(_ctx())
    interaction_token = evidence.begin_interaction(
        intent="客户 A 的风险为什么上升？",
        mode="chat",
        agent_id="agent-1",
        operation_name="bkn.agent.chat",
    )
    try:
        started = evidence.interaction_started_event()
        claim = evidence.claim_created(
            claim_id_value="claim-1",
            claim_type="answer",
            claim_hash="sha256:" + "c" * 64,
            operation_name="bkn.agent.chat",
            source_event_ids=["evt-result-1"],
            operation_ids=["op-1"],
            causation_event_id="evt-result-1",
        )
        batch = evidence.build_batch([started, claim], "acct-1", "user")
    finally:
        evidence.end_interaction(interaction_token)
        observability.reset_context(token)

    assert batch["bkn.trace.schema.version"] == "2.1.0"
    assert set(started["payload"]) == {"intent_hash", "mode", "agent_id"}
    assert started["payload"]["intent_hash"].startswith("sha256:")
    assert "客户 A" not in str(batch)
    assert claim["claim_id"] == "claim-1"
    assert claim["payload"] == {
        "claim_id": "claim-1",
        "claim_type": "answer",
        "claim_hash": "sha256:" + "c" * 64,
        "source_event_ids": ["evt-result-1"],
        "operation_ids": ["op-1"],
        "visibility": "visible",
        "version_status": "unversioned",
    }


def test_business_refs_are_hash_derived_and_never_contain_labels_or_raw_ids():
    refs = evidence.extract_business_refs_from_tool_outputs(
        [{"tool_name": "query", "content": {"kn_id": "secret-kn", "resource_id": "secret-resource"}}]
    )

    assert {ref["ref_type"] for ref in refs} == {"object", "data"}
    assert "secret-kn" not in str(refs)
    assert "secret-resource" not in str(refs)
    assert all("label" not in ref and "tool_name" not in ref and "summary_hash" not in ref for ref in refs)


def test_business_refs_resolver_status_matches_resolution_result():
    token = observability.set_context(_ctx())
    interaction_token = evidence.begin_interaction("question", "task", "agent-1", "bkn.agent.task")
    try:
        resolved = evidence.business_refs_resolved(
            claim_id_value="claim-1",
            business_refs=[{
                "ref_id": "object:" + "1" * 16,
                "ref_type": "object",
                "source_system": "bkn",
                "validity": "observed",
                "version_status": "unversioned",
                "visibility": "visible",
            }],
            operation_name="bkn.agent.task",
            operation_id="op-1",
            causation_event_id="evt-claim-1",
        )
        unresolved = evidence.business_refs_resolved(
            claim_id_value="claim-2",
            business_refs=[],
            operation_name="bkn.agent.task",
            operation_id="op-2",
            causation_event_id="evt-claim-2",
        )
    finally:
        evidence.end_interaction(interaction_token)
        observability.reset_context(token)

    assert resolved["payload"]["resolver_status"] == "resolved"
    assert unresolved["payload"]["resolver_status"] == "unresolved"


def test_task_without_adopted_source_does_not_emit_orphan_claim(monkeypatch):
    submitted = []

    async def fake_submit(events, account_id, account_type):
        submitted.extend(events)

    agent = AgentOut(
        name="agent", mode="task", model="", tools=[], skills=[], status="published",
        agent_id="agent-1", create_user="u", update_user="u", create_time=0, update_time=0,
    )
    token = observability.set_context(_ctx())
    interaction_token = evidence.begin_interaction("question", "task", "agent-1", "bkn.agent.task")
    try:
        monkeypatch.setattr(runner.evidence, "submit_events", fake_submit)
        asyncio.run(runner._emit_task_evidence(
            agent=agent, task_id="task-1", prompt_source="default", prompt_version="1",
            account_id="acct-1", account_type="user", output="unsupported answer",
            claim_type="answer", response_format=None, structured_validation_path=None,
            result_messages=[],
        ))
    finally:
        evidence.end_interaction(interaction_token)
        observability.reset_context(token)

    assert submitted == []


def test_agent_owns_action_recommendation_and_approval_request_payloads():
    token = observability.set_context(_ctx())
    interaction_token = evidence.begin_interaction("question", "task", "agent-1", "bkn.agent.task")
    try:
        recommended = evidence.action_recommended(
            claim_id_value="claim-1", operation_id="op-action-1",
            causation_event_id="evt-claim-1", action_instance_id="action-1",
            action_type="monitor", target_refs=["target:" + "1" * 24],
            reason_hash=evidence.hash_value("safe reason"),
            operation_name="bkn.agent.action.recommend",
        )
        requested = evidence.action_approval_requested(
            claim_id_value="claim-1", operation_id="op-action-1",
            causation_event_id=recommended["event_id"], action_instance_id="action-1",
            policy_ref="e2e-monitor-auto-approve",
            operation_name="bkn.agent.action.recommend",
        )
        headers = evidence.action_execution_headers(
            recommended=recommended,
            approval_requested=requested,
            action_type="monitor",
            policy_ref="e2e-monitor-auto-approve",
            reversible=True,
        )
    finally:
        evidence.end_interaction(interaction_token)
        observability.reset_context(token)

    assert recommended["producer_module"] == "bkn-agent"
    assert recommended["payload"] == {
        "action_instance_id": "action-1", "action_type": "monitor",
        "target_refs": ["target:" + "1" * 24],
        "reason_hash": evidence.hash_value("safe reason"), "status": "recommended",
    }
    assert requested["causation_event_id"] == recommended["event_id"]
    assert requested["payload"] == {
        "action_instance_id": "action-1", "policy_ref": "e2e-monitor-auto-approve",
        "status": "approval_requested",
    }
    assert headers["bkn-action-approval-requested-event-id"] == requested["event_id"]
    assert headers["bkn-claim-id"] == "claim-1"
    assert headers["bkn-action-reversible"] == "true"


def test_interaction_event_rebuild_is_content_stable_for_replay():
    token = observability.set_context(_ctx())
    first_token = evidence.begin_interaction("same question", "task", "agent-1", "bkn.agent.task")
    try:
        first = evidence.interaction_started_event()
    finally:
        evidence.end_interaction(first_token)
    replay_token = evidence.begin_interaction("same question", "task", "agent-1", "bkn.agent.task")
    try:
        replay = evidence.interaction_started_event()
    finally:
        evidence.end_interaction(replay_token)
        observability.reset_context(token)

    assert first == replay
