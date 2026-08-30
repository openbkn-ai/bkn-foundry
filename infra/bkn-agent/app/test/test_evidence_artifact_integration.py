import asyncio

from langchain_core.messages import ToolMessage

from app import evidence, observability
from app.core import graph, runner
from app.models import AgentOut


def _ctx():
    return observability.TraceContext(
        trace_id="1234567890abcdef1234567890abcdef",
        request_id="req_evidence_artifact_integration",
        traceparent="00-1234567890abcdef1234567890abcdef-1234567890abcdef-01",
        entry_boundary="external",
        upstream_span_id="1234567890abcdef",
        tenant_id="tenant-test",
    )


def _agent():
    return AgentOut(
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


def _seed_adopted_source():
    evidence.record_downstream_fact(
        event_id="evt-result-1",
        operation_id="op-1",
        context_hash=evidence.hash_value("tool result"),
        business_refs=[
            {
                "ref_id": "bkn://domain/domain-test/knowledge-network/kn-1/object/o-1",
                "ref_type": "object",
                "source_system": "bkn",
                "validity": "observed",
                "version_status": "versioned",
                "visibility": "visible",
            }
        ],
    )
    evidence.model_context_headers(
        [ToolMessage(content="tool result", tool_call_id="call-1")],
        "op-model-1",
    )
    evidence.record_model_fact(
        event_id="evt-model-1",
        operation_id="op-model-1",
        adopted_source_event_ids=["evt-result-1"],
    )


def test_task_result_artifact_is_persisted_before_claim_event(monkeypatch):
    submissions = []

    async def submit_artifact(artifact):
        submissions.append(("artifact", artifact))
        return True

    async def submit_events(events, account_id, account_type):
        submissions.append(("events", events))
        return True

    monkeypatch.setattr(runner.evidence, "submit_artifact", submit_artifact)
    monkeypatch.setattr(runner.evidence, "submit_events", submit_events)
    token = observability.set_context(_ctx())
    interaction_token = evidence.begin_interaction(
        "业务问题", "task", "agent-1", "bkn.agent.task"
    )
    try:
        _seed_adopted_source()
        asyncio.run(
            runner._emit_task_evidence(
                agent=_agent(),
                task_id="task-1",
                prompt_source="default",
                prompt_version="1",
                account_id="acct-1",
                account_type="user",
                output="业务结论",
                claim_type="answer",
                response_format=None,
                structured_validation_path=None,
                result_messages=[],
            )
        )
    finally:
        evidence.end_interaction(interaction_token)
        observability.reset_context(token)

    assert [kind for kind, _ in submissions] == ["artifact", "events"]
    artifact = submissions[0][1]
    claim = submissions[1][1][0]
    assert artifact["content"] == "业务结论"
    assert artifact["business_refs"] == [
        "bkn://domain/domain-test/knowledge-network/kn-1/object/o-1"
    ]
    assert claim["payload"]["result_artifact_ref"] == (
        f"artifact:{artifact['artifact_id']}"
    )


def test_chat_skips_invalid_2_2_claim_when_result_artifact_persistence_fails(monkeypatch):
    submissions = []

    async def reject_artifact(_artifact):
        submissions.append(("artifact", None))
        return False

    async def submit_events(events, account_id, account_type):
        submissions.append(("events", events))
        return True

    monkeypatch.setattr(graph.evidence, "submit_artifact", reject_artifact)
    monkeypatch.setattr(graph.evidence, "submit_events", submit_events)
    token = observability.set_context(_ctx())
    interaction_token = evidence.begin_interaction(
        "业务问题", "chat", "agent-1", "bkn.agent.chat"
    )
    try:
        _seed_adopted_source()
        asyncio.run(
            graph._emit_chat_evidence(
                agent=_agent(),
                thread_id="thread-1",
                prompt_source="default",
                prompt_version="1",
                account_id="acct-1",
                account_type="user",
                output="业务结论",
                claim_type="answer",
                response_format=None,
                structured_validation_path=None,
                tool_names=[],
            )
        )
    finally:
        evidence.end_interaction(interaction_token)
        observability.reset_context(token)

    assert [kind for kind, _ in submissions] == ["artifact"]
