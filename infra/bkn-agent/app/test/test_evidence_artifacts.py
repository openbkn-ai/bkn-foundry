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


def test_question_artifact_contains_business_content_and_event_only_references_it():
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


def test_result_artifact_is_referenced_by_claim_without_copying_answer_into_event():
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
    assert event["payload"]["result_artifact_ref"] == f"artifact:{artifact['artifact_id']}"
    assert answer not in str(batch)
    assert artifact["artifact_type"] == "result"
    assert artifact["content"] == answer


def test_submit_interaction_started_writes_artifact_before_referencing_event(monkeypatch):
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
    monkeypatch.setattr(evidence, "_send_artifact_once", fake_artifact_send, raising=False)
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


def test_submit_interaction_started_skips_invalid_2_2_event_when_artifact_fails(monkeypatch):
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
    monkeypatch.setattr(evidence, "_send_artifact_once", reject_artifact, raising=False)
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
