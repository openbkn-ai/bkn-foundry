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


def test_claim_batch_uses_2_2_contract_and_keeps_raw_answer_in_artifact_only():
    token = observability.set_context(_ctx())
    interaction_token = evidence.begin_interaction(
        "question", "chat", "agent-1", "bkn.agent.chat"
    )
    try:
        answer = "客户 A 的风险上升，因为近 7 天投诉增加。"
        cid = evidence.claim_id("answer", "thread-1", answer)
        artifact = evidence.result_artifact(
            answer,
            claim_id_value=cid,
            business_refs=[],
            account_id="acct-1",
            account_type="user",
        )
        event = evidence.claim_created(
            claim_id_value=cid,
            claim_type="answer",
            claim_hash=evidence.hash_value(answer),
            operation_name="bkn.agent.chat",
            source_event_ids=["evt-result-1"],
            operation_ids=["op-1"],
            causation_event_id="evt-result-1",
            result_artifact_ref=evidence.artifact_ref(artifact),
        )
        batch = evidence.build_batch([event], "acct-1", "user")
    finally:
        evidence.end_interaction(interaction_token)
        observability.reset_context(token)

    assert batch["bkn.trace.schema.version"] == "2.2.0"
    assert batch["trace"]["bkn.request.id"] == "req_evidence_001"
    assert batch["events"][0]["event_type"] == "claim.created"
    assert batch["events"][0]["claim_id"] == cid
    assert batch["events"][0]["payload"]["claim_hash"].startswith("sha256:")
    assert batch["events"][0]["payload"]["result_artifact_ref"] == (
        f"artifact:{artifact['artifact_id']}"
    )
    assert answer not in str(batch)
    assert artifact["artifact_type"] == "result"
    assert artifact["content"] == answer
    assert artifact["content_hash"] != evidence.hash_value(answer)


def test_interaction_question_artifact_contains_business_content_and_event_only_references_it():
    token = observability.set_context(_ctx())
    interaction_token = evidence.begin_interaction(
        "客户 A 的风险为什么上升？", "chat", "agent-1", "bkn.agent.chat"
    )
    try:
        artifact = evidence.question_artifact("acct-1", "user")
        event = evidence.interaction_started_event(
            question_artifact_ref=evidence.artifact_ref(artifact)
        )
    finally:
        evidence.end_interaction(interaction_token)
        observability.reset_context(token)

    assert artifact["artifact_type"] == "question"
    assert artifact["content"] == "客户 A 的风险为什么上升？"
    assert artifact["bkn.request.id"] == "req_evidence_001"
    assert artifact["trace_id"] == "1234567890abcdef1234567890abcdef"
    assert artifact["interaction_id"].startswith("int_")
    assert artifact["schema_version"] == "2.2.0"
    assert artifact["bkn.account.id"] == "acct-1"
    assert artifact["bkn.account.type"] == "user"
    assert artifact["agent_or_app"] == "agent-1"
    assert event["payload"]["question_artifact_ref"] == (
        f"artifact:{artifact['artifact_id']}"
    )
    assert artifact["content"] not in str(event)


def test_chat_thread_becomes_conversation_identity_and_propagates_downstream():
    token = observability.set_context(_ctx())
    interaction_token = evidence.begin_interaction(
        "question",
        "chat",
        "agent-1",
        "bkn.agent.chat",
        conversation_id="thread_supply_chain",
    )
    try:
        event = evidence.interaction_started_event()
        batch = evidence.build_batch([event], "acct-1", "user")
        operation_id, causation_id = evidence.new_operation()
        headers = evidence.operation_headers(operation_id, causation_id or "")
    finally:
        evidence.end_interaction(interaction_token)
        observability.reset_context(token)

    assert batch["trace"]["bkn.conversation.id"] == "thread_supply_chain"
    assert headers["bkn-conversation-id"] == "thread_supply_chain"


def test_submit_interaction_started_writes_artifact_before_referencing_event(
    monkeypatch,
):
    submitted = []

    async def fake_artifact_send(artifact):
        submitted.append(("artifact", artifact))

    async def fake_event_send(batch):
        submitted.append(("event", batch))

    monkeypatch.setattr(
        evidence.config,
        "BKN_TRACE_ARTIFACT_INGEST_URL",
        "http://bkn-trace.local/evidence/artifacts",
        raising=False,
    )
    monkeypatch.setattr(
        evidence.config,
        "BKN_TRACE_EVIDENCE_INGEST_URL",
        "http://bkn-trace.local/evidence/events",
    )
    monkeypatch.setattr(evidence, "_send_artifact_once", fake_artifact_send)
    monkeypatch.setattr(evidence, "_send_once", fake_event_send)
    token = observability.set_context(_ctx())
    interaction_token = evidence.begin_interaction(
        "需要保留的业务问题", "task", "agent-1", "bkn.agent.task"
    )
    try:
        assert asyncio.run(evidence.submit_interaction_started("acct-1", "user"))
    finally:
        evidence.end_interaction(interaction_token)
        observability.reset_context(token)

    assert [kind for kind, _ in submitted] == ["artifact", "event"]
    artifact = submitted[0][1]
    event = submitted[1][1]["events"][0]
    assert event["payload"]["question_artifact_ref"] == (
        f"artifact:{artifact['artifact_id']}"
    )


def test_submit_interaction_started_skips_invalid_2_2_event_when_artifact_fails(
    monkeypatch,
):
    submitted_events = []

    async def reject_artifact(_artifact):
        raise evidence.EvidenceSubmissionError("HTTP 503")

    async def accept_event(batch):
        submitted_events.append(batch)

    monkeypatch.setattr(
        evidence.config,
        "BKN_TRACE_ARTIFACT_INGEST_URL",
        "http://bkn-trace.local/evidence/artifacts",
        raising=False,
    )
    monkeypatch.setattr(
        evidence.config,
        "BKN_TRACE_EVIDENCE_INGEST_URL",
        "http://bkn-trace.local/evidence/events",
    )
    monkeypatch.setattr(evidence.config, "BKN_TRACE_EVIDENCE_MAX_ATTEMPTS", 1)
    monkeypatch.setattr(evidence, "_send_artifact_once", reject_artifact)
    monkeypatch.setattr(evidence, "_send_once", accept_event)
    token = observability.set_context(_ctx())
    interaction_token = evidence.begin_interaction(
        "问题仍可通过哈希诊断", "task", "agent-1", "bkn.agent.task"
    )
    try:
        assert not asyncio.run(evidence.submit_interaction_started("acct-1", "user"))
    finally:
        evidence.end_interaction(interaction_token)
        observability.reset_context(token)

    assert submitted_events == []


def test_private_structured_output_event_is_not_emitted_in_2_1():
    token = observability.set_context(_ctx())
    interaction_token = evidence.begin_interaction(
        "question", "chat", "agent-1", "bkn.agent.chat"
    )
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
    interaction_token = evidence.begin_interaction(
        "question", "task", "agent-1", "bkn.agent.task"
    )
    try:
        short_hash = evidence.claim_created(
            claim_id_value="claim-1",
            claim_type="answer",
            claim_hash="sha256:short",
            operation_name="bkn.agent.task",
            source_event_ids=["evt-1"],
            operation_ids=["op-1"],
        )
        empty_sources = evidence.claim_created(
            claim_id_value="claim-1",
            claim_type="answer",
            claim_hash="sha256:" + "1" * 64,
            operation_name="bkn.agent.task",
            source_event_ids=[],
            operation_ids=[],
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
        return True

    token = observability.set_context(_ctx())
    interaction_token = evidence.begin_interaction(
        "question", "chat", "agent-1", "bkn.agent.chat"
    )
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
        monkeypatch.setattr(
            evidence.config,
            "BKN_TRACE_EVIDENCE_INGEST_URL",
            "http://bkn-trace.local/events",
        )
        monkeypatch.setattr(evidence, "_send_batch", fake_send)

        async def drive():
            task = asyncio.create_task(
                evidence.submit_events([event], "acct-1", "user")
            )
            while not sent:
                await asyncio.sleep(0)
            assert len(evidence._background) == 1
            assert sent[0]["trace"]["bkn.request.id"] == "req_evidence_001"
            release.set()
            assert await task is True

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
        return True

    trace_token = observability.set_context(_ctx())
    interaction_token = evidence.begin_interaction(
        "question", "task", "agent-1", "bkn.agent.task"
    )
    monkeypatch.setattr(
        evidence.config,
        "BKN_TRACE_EVIDENCE_INGEST_URL",
        "http://bkn-trace.local/events",
    )
    monkeypatch.setattr(evidence, "_send_batch", fake_send)

    async def drive():
        first = asyncio.create_task(
            evidence.submit_events(
                [evidence.interaction_started_event()], "acct", "user"
            )
        )
        await asyncio.sleep(0)
        claim = evidence.claim_created(
            claim_id_value="claim-1",
            claim_type="answer",
            claim_hash="sha256:" + "1" * 64,
            operation_name="bkn.agent.task",
            source_event_ids=["evt-result-1"],
            operation_ids=["op-1"],
            causation_event_id="evt-result-1",
        )
        second = asyncio.create_task(evidence.submit_events([claim], "acct", "user"))
        await asyncio.sleep(0)
        assert started == ["agent.interaction.started"]
        release_first.set()
        assert await asyncio.gather(first, second) == [True, True]

    try:
        asyncio.run(drive())
    finally:
        evidence.end_interaction(interaction_token)
        observability.reset_context(trace_token)

    assert started == ["agent.interaction.started", "claim.created"]


def test_task_evidence_attaches_business_refs_to_claim(monkeypatch):
    submitted = []

    async def fake_submit_artifact(artifact):
        return artifact is not None

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
    interaction_token = evidence.begin_interaction(
        "question", "task", "agent-1", "bkn.agent.task"
    )
    try:
        qualified_business_ref = (
            "bkn://domain/domain-supply-chain/knowledge-network/kn-orders/"
            "object-type/purchase-order/object/po-2026-0001"
        )
        evidence.record_downstream_fact(
            event_id="evt-result-1",
            operation_id="op-1",
            context_hash=evidence.hash_value("qualified tool result"),
            evidence_refs=[
                {
                    "ref_id": "snapshot:1",
                    "ref_type": "data_snapshot",
                    "source_system": "vega-data",
                    "validity": "observed",
                    "version_status": "versioned",
                    "visibility": "visible",
                }
            ],
            business_refs=[
                {
                    "ref_id": qualified_business_ref,
                    "ref_type": "object",
                    "source_system": "bkn",
                    "validity": "observed",
                    "version_status": "versioned",
                    "visibility": "visible",
                }
            ],
        )
        evidence.model_context_headers(
            [
                ToolMessage(
                    content="qualified tool result", tool_call_id="call-qualified"
                )
            ],
            "op-model-1",
        )
        evidence.record_model_fact(
            event_id="evt-model-1",
            operation_id="op-model-1",
            adopted_source_event_ids=["evt-result-1"],
        )
        monkeypatch.setattr(runner.evidence, "submit_artifact", fake_submit_artifact)
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
                result_messages=[],
            )
        )
    finally:
        evidence.end_interaction(interaction_token)
        observability.reset_context(token)

    event_types = [event["event_type"] for event in submitted]
    assert event_types == [
        "claim.created",
        "evidence.refs.created",
        "business.refs.resolved",
    ]
    business_event = submitted[2]
    assert business_event["claim_id"] == submitted[0]["claim_id"]
    assert {ref["ref_type"] for ref in business_event["payload"]["business_refs"]} == {
        "object"
    }
    assert (
        business_event["payload"]["business_refs"][0]["ref_id"]
        == qualified_business_ref
    )


def test_task_evidence_reports_unresolved_business_refs(monkeypatch):
    submitted = []

    async def fake_submit_artifact(artifact):
        return artifact is not None

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
    interaction_token = evidence.begin_interaction(
        "question", "task", "agent-1", "bkn.agent.task"
    )
    try:
        evidence.record_model_fact(
            event_id="evt-model-1",
            operation_id="op-model-1",
            adopted_source_event_ids=[],
        )
        monkeypatch.setattr(runner.evidence, "submit_artifact", fake_submit_artifact)
        monkeypatch.setattr(runner.evidence, "submit_events", fake_submit)
        asyncio.run(
            runner._emit_task_evidence(
                agent=agent,
                task_id="task-1",
                prompt_source="default",
                prompt_version="1",
                account_id="acct-1",
                account_type="user",
                output="supported answer",
                claim_type="answer",
                response_format=None,
                structured_validation_path=None,
                result_messages=[],
            )
        )
    finally:
        evidence.end_interaction(interaction_token)
        observability.reset_context(token)

    assert [event["event_type"] for event in submitted] == [
        "claim.created",
        "business.refs.resolved",
    ]
    assert submitted[-1]["payload"] == {
        "claim_id": submitted[0]["claim_id"],
        "business_refs": [],
        "resolver_status": "unresolved",
    }


def test_2_2_interaction_and_claim_expose_only_registered_causal_fields():
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

    assert batch["bkn.trace.schema.version"] == "2.2.0"
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


def test_business_refs_resolver_status_matches_resolution_result():
    token = observability.set_context(_ctx())
    interaction_token = evidence.begin_interaction(
        "question", "task", "agent-1", "bkn.agent.task"
    )
    try:
        resolved = evidence.business_refs_resolved(
            claim_id_value="claim-1",
            business_refs=[
                {
                    "ref_id": "object:" + "1" * 16,
                    "ref_type": "object",
                    "source_system": "bkn",
                    "validity": "observed",
                    "version_status": "unversioned",
                    "visibility": "visible",
                }
            ],
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
    interaction_token = evidence.begin_interaction(
        "question", "task", "agent-1", "bkn.agent.task"
    )
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
                output="unsupported answer",
                claim_type="answer",
                response_format=None,
                structured_validation_path=None,
                result_messages=[],
            )
        )
    finally:
        evidence.end_interaction(interaction_token)
        observability.reset_context(token)

    assert submitted == []


def test_action_helper_requires_confirmed_parents_and_distinct_stage_times(monkeypatch):
    async def accept(_batch):
        return None

    monkeypatch.setattr(evidence, "_send_once", accept)
    monkeypatch.setattr(
        evidence.config, "BKN_TRACE_EVIDENCE_INGEST_URL", "http://trace/events"
    )
    token = observability.set_context(_ctx())
    interaction_token = evidence.begin_interaction(
        "question", "task", "agent-1", "bkn.agent.task"
    )
    try:
        claim = evidence.claim_created(
            claim_id_value="claim-1",
            claim_type="answer",
            claim_hash=evidence.hash_value("answer"),
            operation_name="bkn.agent.task",
            source_event_ids=["evt-model-1"],
            operation_ids=["op-model-1"],
            causation_event_id="evt-model-1",
        )
        blocked_recommended = evidence.action_recommended(
            claim_id_value="claim-1",
            operation_id="op-action-1",
            causation_event_id=claim["event_id"],
            action_instance_id="action-1",
            action_type="monitor",
            target_refs=["target:" + "1" * 24],
            reason_hash=evidence.hash_value("safe reason"),
            operation_name="bkn.agent.action.recommend",
        )
        assert asyncio.run(evidence.submit_events([claim], "acct-1", "user"))
        recommended = evidence.action_recommended(
            claim_id_value="claim-1",
            operation_id="op-action-1",
            causation_event_id=claim["event_id"],
            action_instance_id="action-1",
            action_type="monitor",
            target_refs=["target:" + "1" * 24],
            reason_hash=evidence.hash_value("safe reason"),
            operation_name="bkn.agent.action.recommend",
        )
        blocked_requested = evidence.action_approval_requested(
            claim_id_value="claim-1",
            operation_id="op-action-1",
            causation_event_id=recommended["event_id"],
            action_instance_id="action-1",
            policy_ref="e2e-monitor-auto-approve",
            operation_name="bkn.agent.action.recommend",
        )
        assert asyncio.run(evidence.submit_events([recommended], "acct-1", "user"))
        requested = evidence.action_approval_requested(
            claim_id_value="claim-1",
            operation_id="op-action-1",
            causation_event_id=recommended["event_id"],
            action_instance_id="action-1",
            policy_ref="e2e-monitor-auto-approve",
            operation_name="bkn.agent.action.recommend",
        )
        unconfirmed_headers = evidence.action_execution_headers(
            recommended=recommended,
            approval_requested=requested,
            action_type="monitor",
            policy_ref="e2e-monitor-auto-approve",
            reversible=True,
        )
        assert asyncio.run(evidence.submit_events([requested], "acct-1", "user"))
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
    assert blocked_recommended is None
    assert blocked_requested is None
    assert recommended["payload"] == {
        "action_instance_id": "action-1",
        "action_type": "monitor",
        "target_refs": ["target:" + "1" * 24],
        "reason_hash": evidence.hash_value("safe reason"),
        "status": "recommended",
    }
    assert requested["causation_event_id"] == recommended["event_id"]
    assert requested["payload"] == {
        "action_instance_id": "action-1",
        "policy_ref": "e2e-monitor-auto-approve",
        "status": "approval_requested",
    }
    assert recommended["observed_at"] != requested["observed_at"]
    assert unconfirmed_headers == {}
    assert headers["bkn-action-approval-requested-event-id"] == requested["event_id"]
    assert headers["bkn-action-observed-at"] == requested["observed_at"]
    assert headers["bkn-claim-id"] == "claim-1"
    assert headers["bkn-action-reversible"] == "true"


def test_interaction_event_rebuild_is_content_stable_for_replay():
    token = observability.set_context(_ctx())
    first_token = evidence.begin_interaction(
        "same question", "task", "agent-1", "bkn.agent.task"
    )
    try:
        first = evidence.interaction_started_event()
    finally:
        evidence.end_interaction(first_token)
    replay_token = evidence.begin_interaction(
        "same question", "task", "agent-1", "bkn.agent.task"
    )
    try:
        replay = evidence.interaction_started_event()
    finally:
        evidence.end_interaction(replay_token)
        observability.reset_context(token)

    assert first == replay
