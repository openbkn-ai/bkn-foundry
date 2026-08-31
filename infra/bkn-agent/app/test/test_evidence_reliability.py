import asyncio
import logging

from langchain_core.messages import AIMessage, HumanMessage, ToolMessage
from langchain_core.outputs import ChatGeneration, ChatResult
from langchain_openai import ChatOpenAI

from app import evidence, observability
from app.core.llm import build_chat_model
from app.core import llm


def _headers():
    return {
        "traceparent": "00-1234567890abcdef1234567890abcdef-abcdef1234567890-01",
        "bkn-request-id": "req_reliable_001",
        "x-account-id": "account-9",
        "x-account-type": "user",
        "x-bkn-application-principal-id": "openbkn-studio",
        "x-bkn-effective-subject-type": "user",
        "x-bkn-effective-subject-id": "account-9",
        "x-bkn-delegation-id": "delegation-1",
    }


def test_internal_request_reuses_active_otel_trace_identity(monkeypatch):
    otel_trace_id = "fedcba0987654321fedcba0987654321"
    otel_span_id = "0123456789abcdef"
    monkeypatch.setattr(
        observability,
        "_active_otel_trace_identity",
        lambda: (otel_trace_id, otel_span_id),
    )

    ctx = observability.build_context(
        {
            "bkn-request-id": "req_internal_otel_001",
        }
    )

    assert ctx.trace_id == otel_trace_id
    assert ctx.traceparent == f"00-{otel_trace_id}-{otel_span_id}-01"
    assert ctx.entry_boundary == "internal"


def test_external_traceparent_wins_over_active_otel_identity(monkeypatch):
    monkeypatch.setattr(
        observability,
        "_active_otel_trace_identity",
        lambda: (
            "fedcba0987654321fedcba0987654321",
            "0123456789abcdef",
        ),
    )
    headers = _headers()

    ctx = observability.build_context(headers)

    assert ctx.trace_id == "1234567890abcdef1234567890abcdef"
    assert (
        ctx.traceparent
        == "00-1234567890abcdef1234567890abcdef-abcdef1234567890-01"
    )
    assert ctx.entry_boundary == "external"


def test_account_identity_and_observation_time_are_propagated():
    ctx = observability.build_context(_headers())
    token = observability.set_context(ctx)
    interaction = evidence.begin_interaction("intent", "task", "agent-1", "bkn.agent.task")
    try:
        event = evidence.interaction_started_event()
        batch = evidence.build_batch([event], "account-9", "user")
    finally:
        evidence.end_interaction(interaction)
        observability.reset_context(token)

    assert batch["trace"]["bkn.account.id"] == "account-9"
    assert observability.outbound_headers(ctx)["x-account-id"] == "account-9"
    assert observability.outbound_headers(ctx)["bkn-event-observed-at"] == ctx.observed_at
    assert "bkn-trace-observed-at" not in observability.outbound_headers(ctx)


def test_missing_account_cannot_build_evidence_batch():
    headers = _headers()
    headers.pop("x-account-id")
    token = observability.set_context(observability.build_context(headers))
    interaction = evidence.begin_interaction(
        "intent", "task", "agent-1", "bkn.agent.task"
    )
    try:
        event = evidence.interaction_started_event()
        batch = evidence.build_batch([event], "account-9", "user")
    finally:
        evidence.end_interaction(interaction)
        observability.reset_context(token)

    assert batch is None


def test_authenticated_account_can_build_evidence_batch():
    headers = _headers()
    token = observability.set_context(observability.build_context(headers))
    interaction = evidence.begin_interaction(
        "intent", "task", "agent-1", "bkn.agent.task"
    )
    try:
        event = evidence.interaction_started_event()
        batch = evidence.build_batch([event], "account-9", "user")
    finally:
        evidence.end_interaction(interaction)
        observability.reset_context(token)

    assert batch is not None
    assert batch["trace"]["bkn.account.id"] == "account-9"


def test_authenticated_identity_is_propagated_to_model_evidence_producer():
    ctx = observability.build_context(_headers())

    headers = observability.outbound_headers(ctx)

    assert headers["x-account-id"] == "account-9"
    assert headers["x-account-type"] == "user"


def test_trusted_owner_identity_is_preserved_for_trace_ledger_ingest(monkeypatch):
    monkeypatch.setattr(
        evidence.config,
        "BKN_TRACE_EVIDENCE_INGEST_TOKEN",
        "producer-token",
        raising=False,
    )
    ctx = observability.build_context(_headers())

    assert ctx.application_principal_id == "openbkn-studio"
    assert ctx.effective_subject_type == "user"
    assert ctx.effective_subject_id == "account-9"
    assert ctx.delegation_id == "delegation-1"

    headers = evidence._ingest_headers(ctx)
    assert headers == {
        "X-BKN-Trace-Ingest-Token": "producer-token",
        "X-BKN-Application-Principal-ID": "openbkn-studio",
        "X-BKN-Effective-Subject-Type": "user",
        "X-BKN-Effective-Subject-ID": "account-9",
        "X-BKN-Delegation-ID": "delegation-1",
    }


def test_agent_evidence_is_converted_to_trace_3_single_events(monkeypatch):
    monkeypatch.setattr(
        evidence.config,
        "BKN_TRACE_EVIDENCE_INGEST_TOKEN",
        "producer-token",
        raising=False,
    )
    ctx = observability.build_context(_headers())
    token = observability.set_context(ctx)
    interaction = evidence.begin_interaction(
        "intent", "task", "business_provenance_optimizer", "bkn.agent.task",
        conversation_id="conv-1", interaction_id="int-1",
    )
    try:
        operation_id, cause = evidence.new_operation()
        called = evidence._event(
            "tool.called", "schema.search", {"input_hash": evidence.hash_value("x")},
            operation_id=operation_id, causation_event_id=cause,
        )
        observed = evidence._event(
            "tool.result", "schema.search", {"result_count": 1},
            operation_id=operation_id, causation_event_id=called["event_id"],
        )
        batch = evidence.build_batch(
            [evidence.interaction_started_event(), called, observed], "account-9", "user"
        )
        ledger_events = evidence.build_ledger_events(batch)
    finally:
        evidence.end_interaction(interaction)
        observability.reset_context(token)

    assert len(ledger_events) == 3
    assert all(item["bkn.trace.schema.version"] == "3.0.0" for item in ledger_events)
    assert all(item["conversation_id"] == "conv-1" for item in ledger_events)
    assert all(item["interaction_id"] == "int-1" for item in ledger_events)
    assert ledger_events[0]["envelope"]["payload"]["agent_id"] == "business_provenance_optimizer"
    assert ledger_events[0]["producer_id"] == "bkn-agent"
    assert ledger_events[1]["producer_stream_id"] != ledger_events[2]["producer_stream_id"]
    assert ledger_events[1]["payload_hash"] == evidence.canonical_payload_hash(
        ledger_events[1]["envelope"]
    )


def test_trace_3_payload_hash_matches_go_ledger_contract_fixtures():
    fixtures = [
        (
            {"b": 2, "a": 1},
            "43258cff783fe7036d8a43033f830adfc60ec037382473548ac742b888292777",
        ),
        (
            {"items": [3, 2, 1], "nested": {"z": 2, "a": 1}},
            "7f1b2afcbb4cec480f8e65ea7fb85338ce2571b5b10251c83f24bb03318d86a4",
        ),
        (
            {"number": 1.5, "message": "你好"},
            "b20ba0633e4f6cfc766715ff6e8d996f19602c268eac402951be732f26a9956a",
        ),
    ]

    for payload, expected in fixtures:
        assert evidence.canonical_payload_hash(payload) == expected


def test_restart_replay_reuses_recoverable_observed_at_envelope():
    headers = {**_headers(), "bkn-event-observed-at": "2026-07-25T10:00:00.123456Z"}

    def rebuild():
        token = observability.set_context(observability.build_context(headers))
        interaction = evidence.begin_interaction("intent", "task", "agent-1", "bkn.agent.task")
        try:
            return evidence.interaction_started_event()
        finally:
            evidence.end_interaction(interaction)
            observability.reset_context(token)

    assert rebuild() == rebuild()


def test_tool_fact_is_only_adopted_when_model_explicitly_selects_it():
    token = observability.set_context(observability.build_context(_headers()))
    interaction = evidence.begin_interaction("intent", "task", "agent-1", "bkn.agent.task")
    try:
        evidence.record_downstream_fact(
            event_id="evt_tool_fact_1",
            operation_id="op_tool_1",
            context_hash=evidence.hash_value("tool-result"),
            evidence_refs=[{
                "ref_id": "evidence:1", "ref_type": "data_snapshot",
                "source_system": "vega-data", "validity": "observed",
                "version_status": "versioned", "visibility": "visible",
            }],
            business_refs=[{
                "ref_id": "object:kn_test:1", "ref_type": "object", "source_system": "bkn",
                "validity": "observed", "version_status": "versioned", "visibility": "visible",
            }],
        )
        assert evidence.adopted_sources() == ([], [], [], [])
        evidence.model_context_headers(
            [ToolMessage(content="tool-result", tool_call_id="call-1")], "op_model_1"
        )

        evidence.record_model_fact(
            event_id="evt_model_fact_1",
            operation_id="op_model_1",
            adopted_source_event_ids=["evt_tool_fact_1"],
        )
        source_ids, operation_ids, evidence_refs, business_refs = evidence.adopted_sources()
    finally:
        evidence.end_interaction(interaction)
        observability.reset_context(token)

    assert source_ids == ["evt_tool_fact_1", "evt_model_fact_1"]
    assert operation_ids == ["op_tool_1", "op_model_1"]
    assert evidence_refs[0]["ref_id"] == "evidence:1"
    assert business_refs[0]["ref_id"] == "object:kn_test:1"


def test_tool_fact_receipt_accepts_canonical_evidence_event_header():
    token = observability.set_context(observability.build_context(_headers()))
    interaction = evidence.begin_interaction("intent", "task", "agent-1", "bkn.agent.task")
    try:
        evidence.record_fact_receipt(
            operation_id="op_tool_canonical",
            headers={"Bkn-Evidence-Event-Id": "evt_tool_canonical"},
            context_hash=evidence.tool_message_context_hash("canonical-result"),
        )
        headers = evidence.model_context_headers(
            [ToolMessage(content="canonical-result", tool_call_id="call-canonical")],
            "op_model_canonical",
        )
    finally:
        evidence.end_interaction(interaction)
        observability.reset_context(token)

    assert headers["bkn-candidate-source-event-ids"] == '["evt_tool_canonical"]'


def test_canonical_fact_receipt_overrides_expected_downstream_event_id():
    token = observability.set_context(observability.build_context(_headers()))
    interaction = evidence.begin_interaction("intent", "task", "agent-1", "bkn.agent.task")
    try:
        evidence.record_fact_receipt(
            operation_id="op_tool_canonical",
            headers={"Bkn-Evidence-Event-Id": "evt_tool_canonical"},
            context_hash=evidence.tool_message_context_hash("canonical-result"),
            expected_event_type="retrieval.completed",
        )
        headers = evidence.model_context_headers(
            [ToolMessage(content="canonical-result", tool_call_id="call-canonical")],
            "op_model_canonical",
        )
    finally:
        evidence.end_interaction(interaction)
        observability.reset_context(token)

    assert headers["bkn-candidate-source-event-ids"] == '["evt_tool_canonical"]'


def test_trusted_completed_mcp_receipt_adopts_observed_evidence_refs():
    token = observability.set_context(observability.build_context(_headers()))
    interaction = evidence.begin_interaction("intent", "task", "agent-1", "bkn.agent.task")
    try:
        result = {"bkn_receipt": {
            "receipt_status": "completed",
            "evidence_durability": "durable",
            "observed_evidence_refs": ["evt_context_retrieval_1"],
        }}
        evidence.record_fact_receipt(
            operation_id="op_context_retrieval_1",
            body=result,
            context_hash=evidence.tool_message_context_hash("context-result"),
            trust_mcp_receipt=True,
        )
        headers = evidence.model_context_headers(
            [ToolMessage(content="context-result", tool_call_id="call-context")],
            "op_model_context",
        )
    finally:
        evidence.end_interaction(interaction)
        observability.reset_context(token)

    assert headers["bkn-candidate-source-event-ids"] == '["evt_context_retrieval_1"]'


def test_trusted_mcp_receipt_without_durable_completed_evidence_is_not_adopted():
    token = observability.set_context(observability.build_context(_headers()))
    interaction = evidence.begin_interaction("intent", "task", "agent-1", "bkn.agent.task")
    try:
        evidence.record_fact_receipt(
            operation_id="op_context_retrieval_2",
            body={"bkn_receipt": {
                "receipt_status": "pending",
                "evidence_durability": "durable",
                "observed_evidence_refs": ["evt_context_retrieval_2"],
            }},
            context_hash=evidence.tool_message_context_hash("pending-result"),
            trust_mcp_receipt=True,
        )
        headers = evidence.model_context_headers(
            [ToolMessage(content="pending-result", tool_call_id="call-pending")],
            "op_model_pending",
        )
    finally:
        evidence.end_interaction(interaction)
        observability.reset_context(token)

    assert headers == {}


def test_trusted_mcp_receipt_ignores_invalid_evidence_refs():
    token = observability.set_context(observability.build_context(_headers()))
    interaction = evidence.begin_interaction("intent", "task", "agent-1", "bkn.agent.task")
    try:
        evidence.record_fact_receipt(
            operation_id="op_context_retrieval_3",
            body={"bkn_receipt": {
                "receipt_status": "completed",
                "evidence_durability": "durable",
                "observed_evidence_refs": ["", "not valid", 7],
            }},
            context_hash=evidence.tool_message_context_hash("invalid-result"),
            trust_mcp_receipt=True,
        )
        headers = evidence.model_context_headers(
            [ToolMessage(content="invalid-result", tool_call_id="call-invalid")],
            "op_model_invalid",
        )
    finally:
        evidence.end_interaction(interaction)
        observability.reset_context(token)

    assert headers == {}


def test_trusted_mcp_receipt_caps_evidence_and_normalizes_business_refs():
    token = observability.set_context(observability.build_context(_headers()))
    interaction = evidence.begin_interaction("intent", "task", "agent-1", "bkn.agent.task")
    try:
        evidence.record_fact_receipt(
            operation_id="op_context_retrieval_4",
            body={"bkn_receipt": {
                "receipt_status": "completed",
                "evidence_durability": "durable",
                "observed_evidence_refs": [f"evt_context_retrieval_{i}" for i in range(101)],
                "business_refs": [{
                    "ref_id": "object:knowledge_network:purchase_order",
                    "ref_type": "object_type",
                    "version": "v1",
                    "display_hint": "Purchase order",
                }],
            }},
            context_hash=evidence.tool_message_context_hash("capped-result"),
            trust_mcp_receipt=True,
        )
        current = evidence._interaction.get()
    finally:
        evidence.end_interaction(interaction)
        observability.reset_context(token)

    assert current is not None
    assert len(current.fact_candidates) == 100
    assert "evt_context_retrieval_100" not in current.fact_candidates
    assert current.fact_candidates["evt_context_retrieval_0"]["business_refs"] == [{
        "ref_id": "object:knowledge_network:purchase_order",
        "ref_type": "object_type",
        "source_system": "context-loader",
        "validity": "observed",
        "version_status": "v1",
        "visibility": "visible",
    }]


def test_mf_model_call_propagates_operation_and_consumes_stable_fact(monkeypatch):
    captured = {}

    async def accept(_batch):
        return None

    async def fake_generate(self, messages, stop=None, run_manager=None, **kwargs):
        captured.update(kwargs.get("extra_headers") or {})
        return ChatResult(generations=[ChatGeneration(message=AIMessage(
            content="answer",
            response_metadata={"headers": {
                "bkn-fact-event-id": "evt_model_fact_1",
                "bkn-adopted-source-event-ids": "[]",
            }},
        ))])

    monkeypatch.setattr(ChatOpenAI, "_agenerate", fake_generate)
    monkeypatch.setattr(evidence, "_send_once", accept)
    monkeypatch.setattr(evidence.config, "BKN_TRACE_EVIDENCE_INGEST_URL", "http://trace/events")
    token = observability.set_context(observability.build_context(_headers()))
    interaction = evidence.begin_interaction("intent", "task", "agent-1", "bkn.agent.task")
    try:
        assert asyncio.run(evidence.submit_events(
            [evidence.interaction_started_event()], "account-9", "user"
        ))
        model = build_chat_model("model-1", streaming=False)
        asyncio.run(model._agenerate([HumanMessage(content="secret")]))
        source_ids, operation_ids, _, _ = evidence.adopted_sources()
    finally:
        evidence.end_interaction(interaction)
        observability.reset_context(token)

    assert captured["bkn-interaction-id"].startswith("int_")
    assert captured["bkn-operation-id"].startswith("op_")
    assert captured["bkn-causation-event-id"].startswith("evt_")
    assert captured["bkn-attempt"] == "1"
    assert source_ids == ["evt_model_fact_1"]
    assert len(operation_ids) == 1


def test_final_model_request_sends_bounded_candidates_and_binds_echoed_adoption(monkeypatch):
    captured = {}

    async def accept(_batch):
        return None

    async def fake_generate(self, messages, stop=None, run_manager=None, **kwargs):
        captured.update(kwargs.get("extra_headers") or {})
        return ChatResult(generations=[ChatGeneration(message=AIMessage(
            content="answer",
            response_metadata={"headers": {
                "bkn-fact-event-id": "evt_model_fact_final",
                "bkn-adopted-source-event-ids": '["evt_tool_fact_2","evt_not_in_model_context"]',
            }},
        ))])

    monkeypatch.setattr(ChatOpenAI, "_agenerate", fake_generate)
    monkeypatch.setattr(evidence, "_send_once", accept)
    monkeypatch.setattr(evidence.config, "BKN_TRACE_EVIDENCE_INGEST_URL", "http://trace/events")
    monkeypatch.setattr(evidence.config, "BKN_TRACE_MODEL_SOURCE_LIMIT", 2)
    token = observability.set_context(observability.build_context(_headers()))
    interaction = evidence.begin_interaction("intent", "task", "agent-1", "bkn.agent.task")
    try:
        assert asyncio.run(evidence.submit_events(
            [evidence.interaction_started_event()], "account-9", "user"
        ))
        for index in range(3):
            evidence.record_downstream_fact(
                event_id=f"evt_tool_fact_{index}", operation_id=f"op_tool_{index}",
                context_hash=evidence.hash_value(f"tool-result-{index}"),
            )
        evidence.record_downstream_fact(
            event_id="evt_" + "x" * 256, operation_id="op_oversized",
            context_hash=evidence.hash_value("oversized-result"),
        )
        evidence.record_downstream_fact(
            event_id="evt_not_in_model_context", operation_id="op_not_in_context",
            context_hash=evidence.hash_value("not-in-context"),
        )
        model = build_chat_model("model-1", streaming=False)
        messages = [HumanMessage(content="tool results already in context")]
        messages.extend(
            ToolMessage(content=f"tool-result-{index}", tool_call_id=f"call-{index}")
            for index in range(3)
        )
        messages.append(ToolMessage(content="oversized-result", tool_call_id="call-oversized"))
        asyncio.run(model._agenerate(messages))
        source_ids, operation_ids, _, _ = evidence.adopted_sources()
        status = evidence.last_adoption_status()
    finally:
        evidence.end_interaction(interaction)
        observability.reset_context(token)

    assert captured["bkn-candidate-source-event-ids"] == '["evt_tool_fact_1","evt_tool_fact_2"]'
    assert source_ids == ["evt_tool_fact_2", "evt_model_fact_final"]
    assert operation_ids == ["op_tool_2", captured["bkn-operation-id"]]
    assert status == "complete"


def test_empty_or_invalid_model_adoption_is_model_only_partial(monkeypatch):
    token = observability.set_context(observability.build_context(_headers()))
    interaction = evidence.begin_interaction("intent", "task", "agent-1", "bkn.agent.task")
    try:
        evidence.record_downstream_fact(event_id="evt_tool_fact_1", operation_id="op_tool_1")
        evidence.record_model_fact(
            event_id="evt_model_fact_1",
            operation_id="op_model_1",
            adopted_source_event_ids=["evt_unknown", ""],
        )
        source_ids, operation_ids, evidence_refs, business_refs = evidence.adopted_sources()
        status = evidence.last_adoption_status()
    finally:
        evidence.end_interaction(interaction)
        observability.reset_context(token)

    assert source_ids == ["evt_model_fact_1"]
    assert operation_ids == ["op_model_1"]
    assert evidence_refs == [] and business_refs == []
    assert status == "partial"


def test_model_fact_receipt_accepts_reserved_body_compatibility_object():
    token = observability.set_context(observability.build_context(_headers()))
    interaction = evidence.begin_interaction("intent", "task", "agent-1", "bkn.agent.task")
    try:
        message = AIMessage(
            content="answer",
            additional_kwargs={"bkn_trace": {
                "source_event_id": "evt_model_body_1",
                "adopted_source_event_ids": [],
                "evidence_refs": [],
                "business_refs": [],
            }},
        )
        llm._record_model_response(message, "op_model_body_1")
        source_ids, operation_ids, _, _ = evidence.adopted_sources()
    finally:
        evidence.end_interaction(interaction)
        observability.reset_context(token)

    assert source_ids == ["evt_model_body_1"]
    assert operation_ids == ["op_model_body_1"]


def test_submission_retries_non_2xx_and_confirms_before_return(monkeypatch):
    attempts = []

    async def fake_send_once(batch):
        attempts.append(batch)
        if len(attempts) < 3:
            raise evidence.EvidenceSubmissionError("HTTP 503")

    monkeypatch.setattr(evidence, "_send_once", fake_send_once)
    monkeypatch.setattr(evidence.config, "BKN_TRACE_EVIDENCE_INGEST_URL", "http://trace/events")
    monkeypatch.setattr(evidence.config, "BKN_TRACE_EVIDENCE_MAX_ATTEMPTS", 3)
    monkeypatch.setattr(evidence.config, "BKN_TRACE_EVIDENCE_RETRY_BACKOFF_S", 0)
    token = observability.set_context(observability.build_context(_headers()))
    interaction = evidence.begin_interaction("intent", "task", "agent-1", "bkn.agent.task")
    try:
        confirmed = asyncio.run(evidence.submit_events(
            [evidence.interaction_started_event()], "account-9", "user"
        ))
    finally:
        evidence.end_interaction(interaction)
        observability.reset_context(token)

    assert confirmed is True
    assert len(attempts) == 3


def test_evidence_ingest_headers_use_dedicated_token(monkeypatch):
    monkeypatch.setattr(evidence.config, "BKN_TRACE_EVIDENCE_INGEST_TOKEN", "producer-token", raising=False)
    assert evidence._ingest_headers() == {"X-BKN-Trace-Ingest-Token": "producer-token"}


def test_evidence_ingest_headers_omit_empty_token(monkeypatch):
    monkeypatch.setattr(evidence.config, "BKN_TRACE_EVIDENCE_INGEST_TOKEN", "", raising=False)
    assert evidence._ingest_headers() == {}


def test_ingest_failure_summary_keeps_only_status_code_and_validation_paths():
    response = {
        "code": "BKN_TRACE_REQUIRED_FIELD_MISSING",
        "message": "invalid event",
        "details": [
            {
                "code": "BKN_TRACE_REQUIRED_FIELD_MISSING",
                "path": "$.events[0].causation_event_id",
                "message": "secret raw value must not be logged",
                "value": "customer@example.com",
            }
        ],
    }

    summary = evidence._safe_ingest_failure_summary(400, response)

    assert summary == (
        "HTTP 400 code=BKN_TRACE_REQUIRED_FIELD_MISSING "
        "paths=$.events[0].causation_event_id"
    )
    assert "secret raw value" not in summary
    assert "customer@example.com" not in summary


def test_successful_tool_result_includes_contract_result_count():
    token = observability.set_context(observability.build_context(_headers()))
    interaction = evidence.begin_interaction("intent", "task", "agent-1", "bkn.agent.task")
    try:
        operation_id, parent_event_id = evidence.new_operation()
        called = evidence.tool_called(
            tool_id="tool-1",
            tool_name="query",
            toolbox_id="box-1",
            args_hash=evidence.hash_value({"query": "redacted"}),
            operation_name="bkn.agent.tool.call",
            operation_id=operation_id,
            causation_event_id=parent_event_id,
        )
        result = evidence.tool_result_observed(
            tool_id="tool-1",
            tool_name="query",
            toolbox_id="box-1",
            result_hash=evidence.hash_value("result"),
            result_length=6,
            result_count=3,
            success=True,
            operation_name="bkn.agent.tool.call",
            operation_id=operation_id,
            causation_event_id=called["event_id"],
        )
    finally:
        evidence.end_interaction(interaction)
        observability.reset_context(token)

    assert result["payload"]["result_count"] == 3


def test_result_count_uses_nested_collections_totals_and_empty_results():
    assert evidence.result_count({"data": {"items": [{"id": 1}, {"id": 2}]}}) == 2
    assert evidence.result_count({"data": {"total": 615, "items": []}}) == 615
    assert evidence.result_count([]) == 0
    assert evidence.result_count({}) == 0
    assert evidence.result_count({"resource_id": "one"}) == 1


def test_failed_parent_submission_is_observable_and_blocks_child_causation(monkeypatch, caplog):
    async def reject(_batch):
        raise evidence.EvidenceSubmissionError("HTTP 503 sensitive detail")

    monkeypatch.setattr(evidence, "_send_once", reject)
    monkeypatch.setattr(evidence.config, "BKN_TRACE_EVIDENCE_INGEST_URL", "http://trace/events")
    monkeypatch.setattr(evidence.config, "BKN_TRACE_EVIDENCE_MAX_ATTEMPTS", 2)
    monkeypatch.setattr(evidence.config, "BKN_TRACE_EVIDENCE_RETRY_BACKOFF_S", 0)
    token = observability.set_context(observability.build_context(_headers()))
    interaction = evidence.begin_interaction("intent", "task", "agent-1", "bkn.agent.task")
    try:
        with caplog.at_level(logging.ERROR, logger="bkn-agent.evidence"):
            confirmed = asyncio.run(evidence.submit_events(
                [evidence.interaction_started_event()], "account-9", "user"
            ))
        _, parent_event_id = evidence.new_operation()
    finally:
        evidence.end_interaction(interaction)
        observability.reset_context(token)

    assert confirmed is False
    assert parent_event_id is None
    assert "failed after 2 attempts: EvidenceSubmissionError" in caplog.text
    assert "sensitive detail" not in caplog.text
