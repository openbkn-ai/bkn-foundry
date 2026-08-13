import asyncio
import hashlib
import json
import logging
import re
import uuid
from contextvars import ContextVar
from dataclasses import dataclass, field
from datetime import datetime, timedelta, timezone
from typing import Any

import aiohttp

from app import observability
from app.config import config

logger = logging.getLogger("bkn-agent.evidence")

CONTRACT_VERSION = "2.2.0"
LEDGER_CONTRACT_VERSION = "3.0.0"
_HASH_RE = re.compile(r"^sha256:[0-9a-f]{64}$")
_ID_RE = re.compile(r"^[0-9A-Za-z_.:-]{1,128}$")
_background: set[asyncio.Task] = set()
_REF_FIELDS = {
    "ref_id",
    "ref_type",
    "source_system",
    "validity",
    "version_status",
    "visibility",
    "summary_hash",
}
_DERIVABLE_DOWNSTREAM_EVENTS = {"retrieval.completed"}


class EvidenceSubmissionError(RuntimeError):
    def __init__(self, message: str, *, safe_summary: str = ""):
        super().__init__(message)
        self.safe_summary = safe_summary


@dataclass
class InteractionEvidence:
    interaction_id: str
    conversation_id: str | None
    started_event: dict[str, Any]
    observed_at: str
    question: Any
    agent_id: str
    operation_sequence: int = 0
    fact_candidates: dict[str, dict[str, Any]] = field(default_factory=dict)
    model_candidate_sets: dict[str, set[str]] = field(default_factory=dict)
    adopted_source_event_ids: list[str] = field(default_factory=list)
    adopted_operation_ids: list[str] = field(default_factory=list)
    adopted_evidence_refs: list[dict[str, Any]] = field(default_factory=list)
    adopted_business_refs: list[dict[str, Any]] = field(default_factory=list)
    adoption_status: str = "partial"
    confirmed_event_ids: set[str] = field(default_factory=set)
    submission_tail: asyncio.Task[bool] | None = None


_interaction: ContextVar[InteractionEvidence | None] = ContextVar(
    "bkn_evidence_interaction", default=None
)


def hash_value(value: Any) -> str:
    if isinstance(value, str):
        raw = value
    else:
        raw = json.dumps(
            value, ensure_ascii=False, sort_keys=True, separators=(",", ":")
        )
    return "sha256:" + hashlib.sha256(raw.encode("utf-8")).hexdigest()


def artifact_content_hash(value: Any) -> str:
    raw = json.dumps(value, ensure_ascii=False, sort_keys=True, separators=(",", ":"))
    return "sha256:" + hashlib.sha256(raw.encode("utf-8")).hexdigest()


def _canonical_json(value: Any) -> str:
    raw = json.dumps(
        value, ensure_ascii=False, sort_keys=True, separators=(",", ":")
    )
    return (
        raw.replace("&", "\\u0026")
        .replace("<", "\\u003c")
        .replace(">", "\\u003e")
        .replace("\u2028", "\\u2028")
        .replace("\u2029", "\\u2029")
    )


def canonical_payload_hash(value: Any) -> str:
    return hashlib.sha256(_canonical_json(value).encode("utf-8")).hexdigest()


def tool_message_context_hash(value: Any) -> str:
    if isinstance(value, str) or (
        isinstance(value, list)
        and all(
            isinstance(item, dict) and isinstance(item.get("type"), str)
            for item in value
        )
    ):
        content = value
    else:
        try:
            content = json.dumps(value, ensure_ascii=False)
        except (TypeError, ValueError):
            content = str(value)
    return hash_value(content)


def result_count(value: Any) -> int:
    if value in (None, "", [], {}):
        return 0
    if isinstance(value, dict):
        for key in ("result_count", "total", "count"):
            count = value.get(key)
            if isinstance(count, int) and not isinstance(count, bool) and count >= 0:
                return count
        for key in ("items", "results", "records", "rows", "data"):
            if key in value:
                return result_count(value[key])
        return 1
    if isinstance(value, (list, tuple, set)):
        return len(value)
    if isinstance(value, str):
        try:
            parsed = json.loads(value)
        except (TypeError, ValueError):
            return 1
        return result_count(parsed)
    return 1


def _stable_id(*parts: str) -> str:
    value = "\x1f".join(parts).encode("utf-8")
    return hashlib.sha256(value).hexdigest()[:32]


def _valid_hash(value: Any) -> bool:
    return isinstance(value, str) and bool(_HASH_RE.fullmatch(value))


def _valid_id(value: Any) -> bool:
    return isinstance(value, str) and bool(_ID_RE.fullmatch(value))


def schema_hash(schema: dict[str, Any] | None) -> str | None:
    if not schema:
        return None
    return hash_value(schema)


def _now() -> str:
    return (
        datetime.now(timezone.utc)
        .isoformat(timespec="microseconds")
        .replace("+00:00", "Z")
    )


def _event_observed_at(base: str, event_type: str) -> str:
    offsets = {"action.recommended": 1, "action.approval_requested": 2}
    offset = offsets.get(event_type, 0)
    if not offset:
        return base
    parsed = datetime.fromisoformat(base.replace("Z", "+00:00"))
    return (
        (parsed + timedelta(microseconds=offset))
        .isoformat(timespec="microseconds")
        .replace("+00:00", "Z")
    )


def _span_id(ctx: observability.TraceContext) -> str:
    _, span_id = observability.parse_traceparent(ctx.traceparent)
    return span_id or uuid.uuid4().hex[:16]


def claim_id(kind: str, subject_id: str, value: Any) -> str:
    digest = hashlib.sha256(
        json.dumps(
            {"kind": kind, "subject_id": subject_id, "hash": hash_value(value)},
            ensure_ascii=False,
            sort_keys=True,
            separators=(",", ":"),
        ).encode("utf-8")
    ).hexdigest()[:24]
    return f"claim_{digest}"


def _event(
    event_type: str,
    operation_name: str,
    payload: dict[str, Any],
    *,
    event_id: str | None = None,
    interaction_id: str | None = None,
    operation_id: str | None = None,
    causation_event_id: str | None = None,
    claim_id_value: str | None = None,
    attempt: int = 1,
) -> dict[str, Any] | None:
    ctx = observability.current_context()
    if not ctx:
        return None
    current = _interaction.get()
    resolved_interaction_id = interaction_id or (
        current.interaction_id if current else None
    )
    ts = _event_observed_at(
        current.observed_at if current else ctx.observed_at, event_type
    )
    stable_event_id = _stable_id(
        "event",
        ctx.trace_id,
        ctx.request_id,
        resolved_interaction_id or "",
        operation_id or "",
        event_type,
        claim_id_value or "",
        str(attempt),
    )
    event = {
        "event_id": event_id or f"evt_{stable_event_id}",
        "event_type": event_type,
        "bkn.trace.schema.version": CONTRACT_VERSION,
        "observed_at": ts,
        "emitted_at": ts,
        "producer_module": observability.MODULE_NAME,
        "trace_id": ctx.trace_id,
        "span_id": _span_id(ctx),
        "bkn.request.id": ctx.request_id,
        "bkn.operation.name": operation_name,
        "payload": {k: v for k, v in payload.items() if v is not None},
    }
    causal = {
        "interaction_id": resolved_interaction_id,
        "operation_id": operation_id,
        "causation_event_id": causation_event_id,
        "claim_id": claim_id_value,
        "attempt": attempt if operation_id else None,
    }
    event.update({key: value for key, value in causal.items() if value is not None})
    return event


def begin_interaction(
    intent: Any,
    mode: str,
    agent_id: str,
    operation_name: str,
    *,
    conversation_id: str | None = None,
    interaction_id: str | None = None,
):
    """开一轮交互。

    interaction_id 传入时用传入的，否则本地铸一个。传入的来源是 Context Loader
    的 bkn_start_interaction —— 那条路上的 id 必须是生命周期服务发的（本地铸的会被
    MCP 面按 owner tuple 拒掉），同时也让证据链与 Context Loader 落在同一轮交互上，
    不至于一次对话在 trace 里裂成两条。

    没挂 Context Loader 工具的 agent 仍走本地铸：为了拿一个服务端 id 就让每次执行
    都依赖生命周期服务，会把一个可选能力变成硬依赖。
    """
    ctx = observability.current_context()
    if not ctx:
        return _interaction.set(None)
    interaction_id = interaction_id or "int_" + _stable_id(
        "interaction", ctx.trace_id, ctx.request_id, mode, agent_id, operation_name
    )
    started = _event(
        "agent.interaction.started",
        operation_name,
        {"intent_hash": hash_value(intent), "mode": mode, "agent_id": agent_id},
        interaction_id=interaction_id,
    )
    if started is None:
        return _interaction.set(None)
    return _interaction.set(
        InteractionEvidence(
            interaction_id=interaction_id,
            conversation_id=conversation_id or ctx.conversation_id,
            started_event=started,
            observed_at=ctx.observed_at,
            question=intent,
            agent_id=agent_id,
        )
    )


def end_interaction(token) -> None:
    _interaction.reset(token)


def interaction_started_event(
    *, question_artifact_ref: str | None = None
) -> dict[str, Any] | None:
    current = _interaction.get()
    if not current:
        return None
    event = {
        **current.started_event,
        "payload": dict(current.started_event["payload"]),
    }
    if question_artifact_ref:
        event["payload"]["question_artifact_ref"] = question_artifact_ref
    return event


def artifact_ref(artifact: dict[str, Any] | None) -> str | None:
    artifact_id = artifact.get("artifact_id") if artifact else None
    return f"artifact:{artifact_id}" if artifact_id else None


def _artifact(
    artifact_type: str,
    content: Any,
    *,
    account_id: str,
    account_type: str,
    claim_id_value: str | None = None,
    business_refs: list[dict[str, Any]] | None = None,
) -> dict[str, Any] | None:
    ctx = observability.current_context()
    current = _interaction.get()
    if not ctx or not current or not account_id or not account_type:
        return None
    business_ref_ids = [
        ref["ref_id"]
        for ref in _safe_refs(business_refs)
        if isinstance(ref.get("ref_id"), str)
    ]
    artifact_id = "art_" + _stable_id(
        "artifact",
        ctx.trace_id,
        ctx.request_id,
        current.interaction_id,
        artifact_type,
        claim_id_value or "",
    )
    return {
        "artifact_id": artifact_id,
        "artifact_type": artifact_type,
        "bkn.request.id": ctx.request_id,
        "trace_id": ctx.trace_id,
        "interaction_id": current.interaction_id,
        "claim_id": claim_id_value,
        "business_refs": business_ref_ids,
        "content_type": "application/json",
        "schema_version": CONTRACT_VERSION,
        "observed_at": current.observed_at,
        "content_hash": artifact_content_hash(content),
        "content": content,
        "bkn.tenant.id": ctx.tenant_id,
        "business_domain": ctx.business_domain,
        "bkn.account.id": account_id,
        "bkn.account.type": account_type,
        "agent_or_app": current.agent_id,
    }


def question_artifact(account_id: str, account_type: str) -> dict[str, Any] | None:
    current = _interaction.get()
    if not current:
        return None
    return _artifact(
        "question",
        current.question,
        account_id=account_id,
        account_type=account_type,
    )


def result_artifact(
    content: Any,
    *,
    claim_id_value: str,
    business_refs: list[dict[str, Any]] | None,
    account_id: str,
    account_type: str,
) -> dict[str, Any] | None:
    return _artifact(
        "result",
        content,
        account_id=account_id,
        account_type=account_type,
        claim_id_value=claim_id_value,
        business_refs=business_refs,
    )


def has_interaction() -> bool:
    return _interaction.get() is not None


def new_operation() -> tuple[str, str | None]:
    current = _interaction.get()
    if not current:
        return "", None
    current.operation_sequence += 1
    operation_id = "op_" + _stable_id(
        "operation", current.interaction_id, str(current.operation_sequence)
    )
    if current.adopted_source_event_ids:
        parent = current.adopted_source_event_ids[-1]
    elif current.started_event["event_id"] in current.confirmed_event_ids:
        parent = current.started_event["event_id"]
    else:
        parent = None
    return operation_id, parent


def operation_headers(
    operation_id: str, causation_event_id: str, attempt: int = 1
) -> dict[str, str]:
    current = _interaction.get()
    if not current:
        return {}
    return {
        **(
            {"bkn-conversation-id": current.conversation_id}
            if current.conversation_id
            else {}
        ),
        "bkn-interaction-id": current.interaction_id,
        "bkn-operation-id": operation_id,
        "bkn-causation-event-id": causation_event_id,
        "bkn-attempt": str(max(attempt, 1)),
    }


def _safe_refs(value: Any) -> list[dict[str, Any]]:
    if not isinstance(value, list):
        return []
    refs: list[dict[str, Any]] = []
    for item in value[:100]:
        if (
            not isinstance(item, dict)
            or not item
            or not set(item).issubset(_REF_FIELDS)
        ):
            continue
        required = {
            "ref_id",
            "ref_type",
            "source_system",
            "validity",
            "version_status",
            "visibility",
        }
        if not required.issubset(item) or not all(
            isinstance(item[key], str) and item[key] for key in required
        ):
            continue
        if "summary_hash" in item and not _valid_hash(item["summary_hash"]):
            continue
        refs.append(dict(item))
    return refs


def record_downstream_fact(
    *,
    event_id: str,
    operation_id: str,
    evidence_refs: list[dict[str, Any]] | None = None,
    business_refs: list[dict[str, Any]] | None = None,
    context_hash: str | None = None,
) -> None:
    current = _interaction.get()
    if not current or not _valid_id(event_id) or not _valid_id(operation_id):
        return
    current.fact_candidates[event_id] = {
        "operation_id": operation_id,
        "evidence_refs": _safe_refs(evidence_refs),
        "business_refs": _safe_refs(business_refs),
        "context_hash": context_hash if _valid_hash(context_hash) else None,
    }
    current.confirmed_event_ids.add(event_id)


def record_fact_receipt(
    *,
    operation_id: str,
    headers: dict[str, Any] | None = None,
    body: Any = None,
    context_hash: str | None = None,
    expected_event_type: str | None = None,
) -> None:
    normalized_headers = {
        str(key).lower(): value for key, value in (headers or {}).items()
    }
    body_receipt = body.get("bkn_trace") if isinstance(body, dict) else None
    body_receipt = body_receipt if isinstance(body_receipt, dict) else {}

    def structured(name: str, header: str) -> Any:
        if name in body_receipt:
            return body_receipt[name]
        raw = normalized_headers.get(header)
        if not isinstance(raw, str):
            return None
        try:
            return json.loads(raw)
        except ValueError:
            return None

    event_id = (
        normalized_headers.get("bkn-evidence-event-id")
        or normalized_headers.get("bkn-fact-event-id")
        or body_receipt.get("source_event_id")
    )
    if not isinstance(event_id, str):
        event_id = _expected_downstream_event_id(expected_event_type, operation_id)
    if not isinstance(event_id, str):
        return
    record_downstream_fact(
        event_id=event_id,
        operation_id=operation_id,
        evidence_refs=structured("evidence_refs", "bkn-fact-evidence-refs"),
        business_refs=structured("business_refs", "bkn-fact-business-refs"),
        context_hash=context_hash,
    )


def _expected_downstream_event_id(
    event_type: str | None, operation_id: str, attempt: int = 1
) -> str | None:
    ctx = observability.current_context()
    if (
        not ctx
        or event_type not in _DERIVABLE_DOWNSTREAM_EVENTS
        or not _valid_id(operation_id)
        or attempt < 1
    ):
        return None
    digest = hashlib.sha256(
        f"{ctx.trace_id}|{operation_id}|{event_type}|{attempt}".encode("utf-8")
    ).hexdigest()
    return f"evt_{digest}"


def record_model_fact(
    *,
    event_id: str,
    operation_id: str,
    adopted_source_event_ids: list[str],
    evidence_refs: list[dict[str, Any]] | None = None,
    business_refs: list[dict[str, Any]] | None = None,
) -> None:
    current = _interaction.get()
    if not current or not _valid_id(event_id) or not _valid_id(operation_id):
        return
    offered = current.model_candidate_sets.get(operation_id, set())
    selected = [
        source
        for source in adopted_source_event_ids
        if source in offered and source in current.fact_candidates
    ]
    source_ids = [*selected, event_id]
    operation_ids = [
        current.fact_candidates[source]["operation_id"] for source in selected
    ]
    operation_ids.append(operation_id)
    current.adopted_source_event_ids = list(dict.fromkeys(source_ids))
    current.adopted_operation_ids = list(dict.fromkeys(operation_ids))
    current.adopted_evidence_refs = _safe_refs(evidence_refs)
    current.adopted_business_refs = _safe_refs(business_refs)
    for source in selected:
        current.adopted_evidence_refs.extend(
            current.fact_candidates[source]["evidence_refs"]
        )
        current.adopted_business_refs.extend(
            current.fact_candidates[source]["business_refs"]
        )
    current.confirmed_event_ids.add(event_id)
    current.adoption_status = (
        "complete" if not current.fact_candidates or bool(selected) else "partial"
    )


def model_context_headers(
    messages: list[Any] | tuple[Any, ...] | None = None, operation_id: str = ""
) -> dict[str, str]:
    current = _interaction.get()
    if not current or not current.fact_candidates:
        return {}
    context_hashes = {
        tool_message_context_hash(getattr(message, "content", ""))
        for message in (messages or [])
        if getattr(message, "type", "") == "tool"
    }
    limit = min(max(config.BKN_TRACE_MODEL_SOURCE_LIMIT, 0), 100)
    candidates = [
        event_id
        for event_id, fact in current.fact_candidates.items()
        if fact.get("context_hash") in context_hashes
    ]
    candidates = candidates[-limit:] if limit else []
    if operation_id:
        current.model_candidate_sets[operation_id] = set(candidates)
    return (
        {
            "bkn-candidate-source-event-ids": json.dumps(
                candidates, ensure_ascii=True, separators=(",", ":")
            )
        }
        if candidates
        else {}
    )


def last_adoption_status() -> str:
    current = _interaction.get()
    return current.adoption_status if current else "partial"


def adopted_sources() -> tuple[
    list[str], list[str], list[dict[str, Any]], list[dict[str, Any]]
]:
    current = _interaction.get()
    if not current:
        return [], [], [], []
    return (
        list(current.adopted_source_event_ids),
        list(current.adopted_operation_ids),
        list(current.adopted_evidence_refs),
        list(current.adopted_business_refs),
    )


def build_batch(
    events: list[dict[str, Any]], account_id: str, account_type: str
) -> dict[str, Any] | None:
    ctx = observability.current_context()
    if not ctx or (not ctx.tenant_id and not ctx.business_domain) or not events:
        return None
    current = _interaction.get()
    conversation_id = current.conversation_id if current else ctx.conversation_id
    trace = {
        "trace_id": ctx.trace_id,
        "traceparent": ctx.traceparent,
        "bkn.request.id": ctx.request_id,
        "bkn.tenant.id": ctx.tenant_id,
        "business_domain": ctx.business_domain,
        "bkn.account.id": account_id,
        "bkn.account.type": account_type,
        "bkn.application.principal.id": (
            ctx.application_principal_id or account_id
        ),
        "bkn.effective.subject.type": (
            ctx.effective_subject_type
            or ("service" if account_type in {"app", "service"} else "user")
        ),
        "bkn.effective.subject.id": ctx.effective_subject_id or account_id,
        "bkn.delegation.id": ctx.delegation_id,
    }
    if conversation_id:
        trace["bkn.conversation.id"] = conversation_id
    return {
        "bkn.trace.schema.version": CONTRACT_VERSION,
        "trace": trace,
        "events": events,
    }


def _ledger_ref_type(value: Any) -> str | None:
    aliases = {"object": "object_type", "relation": "relation_type", "action": "action_type"}
    value = aliases.get(value, value)
    allowed = {
        "knowledge_network", "object_type", "object_instance", "property",
        "relation_type", "data_resource", "metric", "logic", "function",
        "action_type", "action_instance",
    }
    return value if value in allowed else None


def _ledger_business_refs(event: dict[str, Any], business_domain: str) -> list[dict[str, Any]]:
    payload = event.get("payload") if isinstance(event.get("payload"), dict) else {}
    candidates: list[Any] = []
    for field in ("source_refs", "resource_refs", "field_refs", "business_refs"):
        value = payload.get(field)
        if isinstance(value, list):
            candidates.extend(value)
    refs: list[dict[str, Any]] = []
    seen: set[tuple[str, str]] = set()
    for candidate in candidates:
        if not isinstance(candidate, dict):
            continue
        ref_id = str(candidate.get("ref_id") or "").strip()
        ref_type = _ledger_ref_type(candidate.get("ref_type"))
        if not ref_id or not ref_type or not business_domain:
            continue
        key = (ref_type, ref_id)
        if key in seen:
            continue
        seen.add(key)
        ref = {
            "ref_type": ref_type,
            "ref_id": ref_id,
            "business_domain_id": business_domain,
            "version": str(
                candidate.get("version")
                or candidate.get("version_status")
                or "unversioned"
            ).strip(),
        }
        display_hint = str(candidate.get("display_hint") or "").strip()
        if display_hint:
            ref["display_hint"] = display_hint
        refs.append(ref)
    return refs


def _ledger_artifact_refs(event: dict[str, Any]) -> list[str]:
    payload = event.get("payload") if isinstance(event.get("payload"), dict) else {}
    refs: list[str] = []
    for key, value in payload.items():
        if key.endswith("artifact_ref") and isinstance(value, str) and value.strip():
            refs.append(value.strip())
        elif key.endswith("artifact_refs") and isinstance(value, list):
            refs.extend(str(item).strip() for item in value if str(item).strip())
    return list(dict.fromkeys(refs))


def build_ledger_events(batch: dict[str, Any]) -> list[dict[str, Any]]:
    trace = batch.get("trace") if isinstance(batch, dict) else None
    events = batch.get("events") if isinstance(batch, dict) else None
    if not isinstance(trace, dict) or not isinstance(events, list):
        return []
    business_domain = str(trace.get("business_domain") or "").strip()
    conversation_id = str(trace.get("bkn.conversation.id") or "").strip()
    result: list[dict[str, Any]] = []
    for event in events:
        if not isinstance(event, dict):
            continue
        event_id = str(event.get("event_id") or "").strip()
        event_type = str(event.get("event_type") or "").strip()
        interaction_id = str(event.get("interaction_id") or "").strip()
        if not event_id or not event_type or not conversation_id or not interaction_id:
            continue
        operation_id = str(event.get("operation_id") or "").strip()
        attempt = max(int(event.get("attempt") or 1), 1)
        observed_at = str(event.get("observed_at") or "").strip()
        emitted_at = str(event.get("emitted_at") or observed_at).strip()
        stream_key = operation_id or f"interaction:{interaction_id}"
        refs = _ledger_business_refs(event, business_domain)
        ledger = {
            "bkn.trace.schema.version": LEDGER_CONTRACT_VERSION,
            "event_id": event_id,
            "event_type": event_type,
            "payload_hash": canonical_payload_hash(event),
            "conversation_id": conversation_id,
            "interaction_id": interaction_id,
            "attempt": attempt,
            "request_id": str(trace.get("bkn.request.id") or "").strip(),
            "trace_id": str(trace.get("trace_id") or "").strip(),
            "span_id": str(event.get("span_id") or "").strip(),
            "producer_id": observability.MODULE_NAME,
            "producer_stream_id": f"{observability.MODULE_NAME}:{stream_key}:{event_type}",
            "producer_epoch": 1,
            "producer_sequence": attempt,
            "started_at": observed_at,
            "observed_at": observed_at,
            "emitted_at": emitted_at,
            "envelope": event,
        }
        if operation_id:
            ledger["operation_id"] = operation_id
        causation_event_id = str(event.get("causation_event_id") or "").strip()
        if causation_event_id:
            ledger["causation_event_ids"] = [causation_event_id]
        artifact_refs = _ledger_artifact_refs(event)
        if artifact_refs:
            ledger["artifact_refs"] = artifact_refs
        if refs:
            ledger["business_refs"] = refs
            if operation_id:
                ledger["operation_business_edges"] = [
                    {
                        "operation_id": operation_id,
                        "business_ref": ref,
                        "role": "read",
                        "observed_at": observed_at,
                    }
                    for ref in refs
                ]
        result.append(ledger)
    return result


async def submit_events(
    events: list[dict[str, Any]], account_id: str, account_type: str
) -> bool:
    if not account_id or not account_type:
        return False
    batch = build_batch(events, account_id, account_type)
    if not batch or not config.BKN_TRACE_EVIDENCE_INGEST_URL:
        return False
    current = _interaction.get()
    previous = current.submission_tail if current else None
    task = asyncio.create_task(_send_after(previous, batch))
    if current:
        current.submission_tail = task
    _background.add(task)
    task.add_done_callback(_background.discard)
    try:
        confirmed = await asyncio.shield(task)
    except asyncio.CancelledError:
        raise
    if confirmed and current:
        current.confirmed_event_ids.update(
            event["event_id"] for event in events if event
        )
    return confirmed


async def submit_artifact(artifact: dict[str, Any] | None) -> bool:
    if not artifact or not config.BKN_TRACE_ARTIFACT_INGEST_URL:
        return False
    return await _send_artifact(artifact)


async def submit_interaction_started(account_id: str, account_type: str) -> bool:
    artifact = question_artifact(account_id, account_type)
    artifact_confirmed = await submit_artifact(artifact)
    if not artifact_confirmed:
        return False
    event = interaction_started_event(question_artifact_ref=artifact_ref(artifact))
    return await submit_events([event] if event else [], account_id, account_type)


async def _send_after(
    previous: asyncio.Task[bool] | None, batch: dict[str, Any]
) -> bool:
    if previous is not None:
        try:
            if not await previous:
                return False
        except Exception:
            return False
    return await _send_batch(batch)


async def _send_once(batch: dict[str, Any]) -> None:
    ledger_events = build_ledger_events(batch)
    if not ledger_events:
        raise EvidenceSubmissionError("Trace 3.0 event conversion produced no events")
    async with aiohttp.ClientSession(
        timeout=aiohttp.ClientTimeout(total=config.BKN_TRACE_EVIDENCE_TIMEOUT_S)
    ) as session:
        for ledger_event in ledger_events:
            async with session.post(
                config.BKN_TRACE_EVIDENCE_INGEST_URL,
                json=ledger_event,
                headers=_ingest_headers(batch.get("trace")),
            ) as resp:
                if not 200 <= resp.status < 300:
                    try:
                        response = await resp.json(content_type=None)
                    except (aiohttp.ContentTypeError, json.JSONDecodeError, ValueError):
                        response = {}
                    raise EvidenceSubmissionError(
                        f"HTTP {resp.status}",
                        safe_summary=_safe_ingest_failure_summary(resp.status, response),
                    )


async def _send_artifact_once(artifact: dict[str, Any]) -> None:
    async with aiohttp.ClientSession(
        timeout=aiohttp.ClientTimeout(total=config.BKN_TRACE_EVIDENCE_TIMEOUT_S)
    ) as session:
        async with session.post(
            config.BKN_TRACE_ARTIFACT_INGEST_URL,
            json=artifact,
            headers=_ingest_headers(observability.current_context()),
        ) as resp:
            if not 200 <= resp.status < 300:
                try:
                    response = await resp.json(content_type=None)
                except (aiohttp.ContentTypeError, json.JSONDecodeError, ValueError):
                    response = {}
                raise EvidenceSubmissionError(
                    f"HTTP {resp.status}",
                    safe_summary=_safe_ingest_failure_summary(resp.status, response),
                )


def _safe_ingest_failure_summary(status: int, response: Any) -> str:
    parts = [f"HTTP {status}"]
    if not isinstance(response, dict):
        return parts[0]
    code = response.get("code")
    if isinstance(code, str) and re.fullmatch(r"[0-9A-Za-z_.-]{1,128}", code):
        parts.append(f"code={code}")
    paths: list[str] = []
    for detail in response.get("details") or []:
        path = detail.get("path") if isinstance(detail, dict) else None
        if (
            isinstance(path, str)
            and len(path) <= 256
            and re.fullmatch(r"\$(?:\.[0-9A-Za-z_.-]+|\[[0-9]+\])+", path)
        ):
            paths.append(path)
        if len(paths) == 3:
            break
    if paths:
        parts.append(f"paths={','.join(paths)}")
    return " ".join(parts)


def _ingest_headers(identity: Any = None) -> dict[str, str]:
    token = str(getattr(config, "BKN_TRACE_EVIDENCE_INGEST_TOKEN", "") or "").strip()
    headers = {"X-BKN-Trace-Ingest-Token": token} if token else {}
    if isinstance(identity, observability.TraceContext):
        values = {
            "tenant": identity.tenant_id,
            "business_domain": identity.business_domain,
            "application": identity.application_principal_id or identity.account_id,
            "subject_type": identity.effective_subject_type
            or ("service" if identity.account_type in {"app", "service"} else "user"),
            "subject": identity.effective_subject_id or identity.account_id,
            "delegation": identity.delegation_id,
        }
    elif isinstance(identity, dict):
        values = {
            "tenant": identity.get("bkn.tenant.id"),
            "business_domain": identity.get("business_domain"),
            "application": identity.get("bkn.application.principal.id"),
            "subject_type": identity.get("bkn.effective.subject.type"),
            "subject": identity.get("bkn.effective.subject.id") or identity.get("bkn.account.id"),
            "delegation": identity.get("bkn.delegation.id"),
        }
    else:
        return headers
    mapping = {
        "X-BKN-Tenant-ID": values["tenant"],
        "X-Business-Domain-ID": values["business_domain"],
        "X-BKN-Application-Principal-ID": values["application"],
        "X-BKN-Effective-Subject-Type": values["subject_type"],
        "X-BKN-Effective-Subject-ID": values["subject"],
        "X-BKN-Delegation-ID": values["delegation"],
    }
    headers.update({key: str(value).strip() for key, value in mapping.items() if value})
    return headers


async def _send_batch(batch: dict[str, Any]) -> bool:
    attempts = max(config.BKN_TRACE_EVIDENCE_MAX_ATTEMPTS, 1)
    for attempt in range(1, attempts + 1):
        try:
            await _send_once(batch)
            return True
        except Exception as exc:
            if attempt == attempts:
                failure = getattr(exc, "safe_summary", "") or type(exc).__name__
                logger.error(
                    "BKN Trace evidence ingestion failed after %s attempts: %s",
                    attempts,
                    failure,
                )
                return False
            await asyncio.sleep(config.BKN_TRACE_EVIDENCE_RETRY_BACKOFF_S * attempt)
    return False


async def _send_artifact(artifact: dict[str, Any]) -> bool:
    attempts = max(config.BKN_TRACE_EVIDENCE_MAX_ATTEMPTS, 1)
    for attempt in range(1, attempts + 1):
        try:
            await _send_artifact_once(artifact)
            return True
        except Exception as exc:
            if attempt == attempts:
                failure = getattr(exc, "safe_summary", "") or type(exc).__name__
                logger.error(
                    "BKN Trace artifact ingestion failed after %s attempts: %s",
                    attempts,
                    failure,
                )
                return False
            await asyncio.sleep(config.BKN_TRACE_EVIDENCE_RETRY_BACKOFF_S * attempt)
    return False


async def drain_pending() -> bool:
    pending = list(_background)
    if not pending:
        return True
    try:
        async with asyncio.timeout(config.BKN_TRACE_EVIDENCE_DRAIN_TIMEOUT_S):
            results = await asyncio.gather(*pending, return_exceptions=True)
        return all(result is True for result in results)
    except TimeoutError:
        logger.error(
            "BKN Trace evidence drain timed out with %s pending batches", len(pending)
        )
        return False


def claim_created(
    *,
    claim_id_value: str,
    claim_type: str,
    claim_hash: str,
    operation_name: str,
    source_event_ids: list[str] | None = None,
    operation_ids: list[str] | None = None,
    causation_event_id: str | None = None,
    result_artifact_ref: str | None = None,
    **_legacy: Any,
) -> dict[str, Any] | None:
    if (
        not claim_id_value
        or not _valid_hash(claim_hash)
        or not source_event_ids
        or not operation_ids
    ):
        return None
    payload = {
        "claim_id": claim_id_value,
        "claim_type": claim_type,
        "claim_hash": claim_hash,
        "source_event_ids": source_event_ids or [],
        "operation_ids": operation_ids or [],
        "result_artifact_ref": result_artifact_ref,
        "visibility": "visible",
        "version_status": "unversioned",
    }
    return _event(
        "claim.created",
        operation_name,
        payload,
        claim_id_value=claim_id_value,
        causation_event_id=causation_event_id,
    )


def evidence_refs_created(
    *,
    claim_id_value: str,
    evidence_refs: list[dict[str, Any]],
    operation_name: str,
    operation_id: str | None = None,
    causation_event_id: str | None = None,
    **_legacy: Any,
) -> dict[str, Any] | None:
    if not claim_id_value or not evidence_refs:
        return None
    return _event(
        "evidence.refs.created",
        operation_name,
        {"claim_id": claim_id_value, "evidence_refs": evidence_refs},
        claim_id_value=claim_id_value,
        operation_id=operation_id,
        causation_event_id=causation_event_id,
    )


def business_refs_resolved(
    *,
    claim_id_value: str,
    business_refs: list[dict[str, Any]],
    operation_name: str,
    operation_id: str | None = None,
    causation_event_id: str | None = None,
    **_legacy: Any,
) -> dict[str, Any] | None:
    return _event(
        "business.refs.resolved",
        operation_name,
        {
            "claim_id": claim_id_value,
            "business_refs": business_refs,
            "resolver_status": "resolved" if business_refs else "unresolved",
        },
        claim_id_value=claim_id_value,
        operation_id=operation_id,
        causation_event_id=causation_event_id,
    )


def action_recommended(
    *,
    claim_id_value: str,
    operation_id: str,
    causation_event_id: str,
    action_instance_id: str,
    action_type: str,
    target_refs: list[str],
    reason_hash: str,
    operation_name: str,
    attempt: int = 1,
) -> dict[str, Any] | None:
    current = _interaction.get()
    if not target_refs or not all(
        (
            claim_id_value,
            operation_id,
            causation_event_id,
            action_instance_id,
            action_type,
        )
    ):
        return None
    if not current or causation_event_id not in current.confirmed_event_ids:
        return None
    if not _valid_hash(reason_hash):
        return None
    return _event(
        "action.recommended",
        operation_name,
        {
            "action_instance_id": action_instance_id,
            "action_type": action_type,
            "target_refs": target_refs,
            "reason_hash": reason_hash,
            "status": "recommended",
        },
        operation_id=operation_id,
        causation_event_id=causation_event_id,
        claim_id_value=claim_id_value,
        attempt=attempt,
    )


def action_approval_requested(
    *,
    claim_id_value: str,
    operation_id: str,
    causation_event_id: str,
    action_instance_id: str,
    policy_ref: str,
    operation_name: str,
    attempt: int = 1,
) -> dict[str, Any] | None:
    current = _interaction.get()
    if not all(
        (
            claim_id_value,
            operation_id,
            causation_event_id,
            action_instance_id,
            policy_ref,
        )
    ):
        return None
    if not current or causation_event_id not in current.confirmed_event_ids:
        return None
    return _event(
        "action.approval_requested",
        operation_name,
        {
            "action_instance_id": action_instance_id,
            "policy_ref": policy_ref,
            "status": "approval_requested",
        },
        operation_id=operation_id,
        causation_event_id=causation_event_id,
        claim_id_value=claim_id_value,
        attempt=attempt,
    )


def action_execution_headers(
    *,
    recommended: dict[str, Any],
    approval_requested: dict[str, Any],
    action_type: str,
    policy_ref: str,
    reversible: bool,
) -> dict[str, str]:
    payload = recommended.get("payload") or {}
    requested_payload = approval_requested.get("payload") or {}
    current = _interaction.get()
    if (
        not current
        or recommended.get("event_type") != "action.recommended"
        or approval_requested.get("event_type") != "action.approval_requested"
        or approval_requested.get("causation_event_id") != recommended.get("event_id")
        or recommended.get("event_id") not in current.confirmed_event_ids
        or approval_requested.get("event_id") not in current.confirmed_event_ids
        or recommended.get("claim_id") != approval_requested.get("claim_id")
        or recommended.get("operation_id") != approval_requested.get("operation_id")
        or payload.get("action_instance_id")
        != requested_payload.get("action_instance_id")
        or payload.get("action_type") != action_type
        or requested_payload.get("policy_ref") != policy_ref
    ):
        return {}
    return {
        **operation_headers(
            str(recommended.get("operation_id") or ""),
            str(recommended.get("causation_event_id") or ""),
            int(recommended.get("attempt") or 1),
        ),
        "bkn-claim-id": str(recommended.get("claim_id") or ""),
        "bkn-action-instance-id": str(payload.get("action_instance_id") or ""),
        "bkn-action-type": action_type,
        "bkn-action-reversible": "true" if reversible else "false",
        "bkn-action-policy-ref": policy_ref,
        "bkn-action-observed-at": str(approval_requested.get("observed_at") or ""),
        "bkn-action-approval-requested-event-id": str(
            approval_requested.get("event_id") or ""
        ),
    }


def structured_output_validated(
    *,
    claim_id_value: str,
    schema_hash_value: str | None,
    validation_path: str,
    valid: bool,
    operation_name: str,
) -> None:
    return None


def tool_budget_exhausted(
    *,
    max_tool_calls: int,
    operation_name: str,
    tool_name: str | None = None,
) -> None:
    return None


def tool_called(
    *,
    tool_id: str,
    tool_name: str,
    toolbox_id: str | None,
    args_hash: str,
    operation_name: str,
    operation_id: str,
    causation_event_id: str | None,
) -> dict[str, Any] | None:
    if not _valid_hash(args_hash):
        return None
    return _event(
        "tool.called",
        operation_name,
        {
            "tool_id": tool_id,
            "tool_name": tool_name,
            "args_hash": args_hash,
            "visibility": "visible",
            "version_status": "unversioned",
        },
        operation_id=operation_id,
        causation_event_id=causation_event_id,
    )


def tool_result_observed(
    *,
    tool_id: str,
    tool_name: str,
    toolbox_id: str | None,
    result_hash: str | None,
    result_length: int | None,
    result_count: int | None,
    success: bool,
    operation_name: str,
    status_code: int | None = None,
    error_hash: str | None = None,
    operation_id: str,
    causation_event_id: str,
    partial_reason: list[str] | None = None,
) -> dict[str, Any] | None:
    if success and not _valid_hash(result_hash):
        return None
    if not success and not _valid_hash(error_hash):
        return None
    payload: dict[str, Any] = {
        "tool_id": tool_id,
        "tool_name": tool_name,
        "result_hash": result_hash,
        "result_length": result_length,
        "result_count": result_count,
        "status": "success" if success else "error",
        "error_hash": error_hash,
        "error_category": "tool_execution" if not success else None,
        "visibility": "visible",
        "version_status": "unversioned",
    }
    return _event(
        "tool.result.observed",
        operation_name,
        payload,
        operation_id=operation_id,
        causation_event_id=causation_event_id,
    )


def agent_as_tool_invoked(
    *,
    parent_thread_id: str | None,
    child_task_id: str,
    child_agent_id: str,
    depth: int,
    message_hash: str,
    operation_name: str,
) -> None:
    return None
